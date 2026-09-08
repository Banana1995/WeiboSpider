package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

var (
	ErrFXUnavailable       = errors.New("FX unavailable")
	ErrFXTimeout           = errors.New("FX timeout")
	ErrUnsupportedCurrency = errors.New("unsupported currency")
)

var fxBeijing = time.FixedZone("Beijing", 8*60*60)

const fxTimeout = 8 * time.Second

type FXRequest struct {
	Base  Currency
	Quote Currency
	Mode  string
	Date  string
}

// FXQuote is flat on the wire; only the four existing snapshot fields are
// copied into a write. Fetching a quote never reads or mutates the ledger.
type FXQuote struct {
	Base          Currency `json:"base"`
	Quote         Currency `json:"quote"`
	Mode          string   `json:"mode"`
	RequestedDate string   `json:"requested_date"`
	Rate          Rate     `json:"rate"`
	Date          string   `json:"date"`
	Source        string   `json:"source"`
	FetchedAt     string   `json:"fetched_at"`
	QuotedAt      string   `json:"quoted_at,omitempty"`
}

type FXProvider interface {
	Fetch(context.Context, FXRequest) (FXQuote, error)
}

func validateFX(req FXRequest, now time.Time) error {
	if req.Base == "" || req.Quote == "" || (req.Mode != "latest" && req.Mode != "historical") {
		return ErrQuery
	}
	if req.Mode == "latest" && req.Date != "" || req.Mode == "historical" && (!validDate(req.Date) || req.Date > now.In(fxBeijing).Format(time.DateOnly)) {
		return ErrQuery
	}
	if !req.Base.valid() || !req.Quote.valid() {
		return ErrUnsupportedCurrency
	}
	return nil
}

func (h Handler) fx(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	values, err := query(r, "base", "quote", "mode", "date")
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	req := FXRequest{Base: Currency(values.Get("base")), Quote: Currency(values.Get("quote")), Mode: values.Get("mode"), Date: values.Get("date")}
	if err == nil {
		err = validateFX(req, now)
	}
	var result FXQuote
	if err == nil {
		if h.FX == nil {
			err = ErrFXUnavailable
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), fxTimeout)
			defer cancel()
			result, err = h.FX.Fetch(ctx, req)
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrQuery):
			httpapi.Fail(w, 400, "invalid_query", "汇率查询参数无效")
		case errors.Is(err, ErrUnsupportedCurrency):
			httpapi.Fail(w, 400, "unsupported_currency", "仅支持 CNY、HKD、USD")
		case errors.Is(err, ErrFXTimeout), errors.Is(err, context.DeadlineExceeded):
			httpapi.Fail(w, 504, "fx_timeout", "腾讯汇率查询超时，请稍后重试")
		default:
			httpapi.Fail(w, 502, "fx_unavailable", "腾讯汇率暂不可用，请稍后重试")
		}
		return
	}
	httpapi.Write(w, 200, result)
}

type TencentFX struct {
	client   *http.Client
	spotURL  string
	closeURL string
	now      func() time.Time
	slots    chan struct{}
}

// NewTencentFX has no startup I/O and accepts no configurable upstream URLs.
func NewTencentFX() *TencentFX {
	return newTencentFX(http.DefaultTransport, "https://qt.gtimg.cn/", "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get", time.Now)
}

// Constructor injection is package-private and used only by synthetic tests.
func newTencentFX(transport http.RoundTripper, spotURL, closeURL string, now func() time.Time) *TencentFX {
	return &TencentFX{client: &http.Client{Transport: transport, Timeout: fxTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		spotURL: spotURL, closeURL: closeURL, now: now, slots: make(chan struct{}, 4)}
}

func (p *TencentFX) Fetch(ctx context.Context, req FXRequest) (FXQuote, error) {
	now := p.now()
	if err := validateFX(req, now); err != nil {
		return FXQuote{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, fxTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return FXQuote{}, fxFailure(err)
	}
	date := req.Date
	if req.Mode == "latest" {
		date = now.In(fxBeijing).Format(time.DateOnly)
	}
	result := FXQuote{Base: req.Base, Quote: req.Quote, Mode: req.Mode, RequestedDate: date}
	if req.Base == req.Quote {
		result.Rate, result.Date, result.Source = 100_000_000, date, "identity"
		result.FetchedAt = now.UTC().Format(time.RFC3339Nano)
		return result, nil
	}
	pair := string(req.Base) + string(req.Quote)
	inverse := pair != "USDCNY" && pair != "USDHKD" && pair != "HKDCNY"
	if inverse {
		pair = string(req.Quote) + string(req.Base)
	}
	u, _ := url.Parse(p.spotURL)
	params := url.Values{"q": {"wh" + pair}}
	start := ""
	if req.Mode == "historical" {
		u, _ = url.Parse(p.closeURL)
		end, _ := time.Parse(time.DateOnly, date)
		start = end.AddDate(0, 0, -30).Format(time.DateOnly)
		params = url.Values{"param": {"wh" + pair + ",day," + start + "," + date + ",40,qfq"}}
	}
	u.RawQuery = params.Encode()
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	case <-ctx.Done():
		return FXQuote{}, fxFailure(ctx.Err())
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return FXQuote{}, ErrFXUnavailable
	}
	response, err := p.client.Do(r)
	if err != nil {
		return FXQuote{}, fxFailure(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return FXQuote{}, ErrFXUnavailable
	}
	const maxBody = 256 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return FXQuote{}, fxFailure(err)
	}
	if len(body) > maxBody {
		return FXQuote{}, ErrFXUnavailable
	}
	if req.Mode == "latest" {
		result.Rate, result.Date, result.QuotedAt, err = parseTencentSpot(body, pair, p.now())
		result.Source = "Tencent/spot/" + pair
	} else {
		result.Rate, result.Date, err = parseTencentClose(body, pair, start, date)
		result.Source = "Tencent/close/" + pair
	}
	if err != nil {
		return FXQuote{}, ErrFXUnavailable
	}
	if inverse {
		result.Rate, err = reciprocalRate(result.Rate)
		if err != nil {
			return FXQuote{}, ErrFXUnavailable
		}
		result.Source += "/inverse"
	}
	if err := ctx.Err(); err != nil {
		return FXQuote{}, fxFailure(err)
	}
	result.FetchedAt = p.now().UTC().Format(time.RFC3339Nano)
	return result, nil
}

func fxFailure(err error) error {
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return ErrFXTimeout
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return ErrFXUnavailable
}

func reciprocalRate(rate Rate) (Rate, error) {
	if rate <= 0 {
		return 0, ErrPrecision
	}
	// Exact rational 1e16/rate, rounded half-away by the shared math/big helper.
	v, err := roundedProduct(100_000_000, 100_000_000, int64(rate))
	if err != nil || v <= 0 {
		return 0, ErrPrecision
	}
	return Rate(v), nil
}

func parseTencentSpot(body []byte, pair string, now time.Time) (Rate, string, string, error) {
	s := strings.TrimSpace(string(body))
	prefix := "v_wh" + pair + "=\""
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, "\";") {
		return 0, "", "", ErrFXUnavailable
	}
	payload := s[len(prefix) : len(s)-2]
	if strings.ContainsAny(payload, "\"\r\n") {
		return 0, "", "", ErrFXUnavailable
	}
	fields := strings.Split(payload, "~")
	if len(fields) < 22 || fields[2] != pair {
		return 0, "", "", ErrFXUnavailable
	}
	rate, err := ParseRate(fields[3])
	stamp, stampErr := time.ParseInLocation("20060102150405", fields[5], fxBeijing)
	date := fields[21]
	// Overnight FX can have a timestamp on the day AFTER its business date.
	if err != nil || rate <= 0 || stampErr != nil || len(fields[5]) != 14 || stamp.After(now) || !validDate(date) || date > stamp.Format(time.DateOnly) {
		return 0, "", "", ErrFXUnavailable
	}
	return rate, date, stamp.Format(time.RFC3339), nil
}

func parseTencentClose(body []byte, pair, start, end string) (Rate, string, error) {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if uniqueFXJSON(decoder, 0) != nil {
		return 0, "", ErrFXUnavailable
	}
	var envelope struct {
		Code *int `json:"code"`
		Data map[string]struct {
			Day [][]string `json:"day"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 || len(envelope.Data) != 1 {
		return 0, "", ErrFXUnavailable
	}
	rows := envelope.Data["wh"+pair].Day
	seen := make(map[string]bool)
	var rate Rate
	date := ""
	for _, row := range rows {
		if len(row) < 6 || !validDate(row[0]) || row[0] > end || row[0] < start || seen[row[0]] {
			return 0, "", ErrFXUnavailable
		}
		seen[row[0]] = true
		closeRate, err := ParseRate(row[2])
		if err != nil || closeRate <= 0 {
			return 0, "", ErrFXUnavailable
		}
		if row[0] > date {
			rate, date = closeRate, row[0]
		}
	}
	if date == "" {
		return 0, "", ErrFXUnavailable
	}
	return rate, date, nil
}

// Tencent's symbol keys contain uppercase letters, unlike ledger write DTOs.
func uniqueFXJSON(decoder *json.Decoder, depth int) error {
	if depth > 16 {
		return ErrFXUnavailable
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return ErrFXUnavailable
	}
	keys := make(map[string]bool)
	for decoder.More() {
		if delim == '{' {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || keys[key] {
				return ErrFXUnavailable
			}
			keys[key] = true
		}
		if err := uniqueFXJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
