package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
	"golang.org/x/text/encoding/simplifiedchinese"
)

const instrumentSearchTimeout = 12 * time.Second

var (
	ErrInstrumentSearchUnavailable = errors.New("instrument search unavailable")
	ErrInstrumentSearchTimeout     = errors.New("instrument search timed out")
)

type InstrumentSearchItem struct {
	Name     string   `json:"name"`
	Market   string   `json:"market"`
	Code     string   `json:"code"`
	Currency Currency `json:"currency"`
}

// Separate from QuotesProvider so valuation-only providers need not implement search.
// Implementations must honor cancellation and return only validated exact identities.
type InstrumentSearchProvider interface {
	Search(context.Context, string) ([]InstrumentSearchItem, error)
}

func instrumentSearchCode(input string) (market, code string, err error) {
	code = input
	if len(input) >= 2 {
		switch input[:2] {
		case "sh", "sz", "hk":
			market, code = strings.ToUpper(input[:2]), input[2:]
		}
	}
	if len(code) != 5 && len(code) != 6 || market == "HK" && len(code) != 5 || (market == "SH" || market == "SZ") && len(code) != 6 {
		return "", "", ErrQuery
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return "", "", ErrQuery
		}
	}
	return market, code, nil
}

func instrumentSearchError(err error) error {
	var timeout net.Error
	switch {
	case err == nil, errors.Is(err, ErrQuery), errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, ErrInstrumentSearchTimeout), errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return ErrInstrumentSearchTimeout
	default:
		return ErrInstrumentSearchUnavailable
	}
}

func (h Handler) searchInstruments(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	values, err := query(r, "code")
	if err == nil {
		_, _, err = instrumentSearchCode(values.Get("code"))
	}
	items := []InstrumentSearchItem{}
	if err == nil {
		if h.InstrumentSearch == nil {
			err = ErrInstrumentSearchUnavailable
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), instrumentSearchTimeout)
			defer cancel()
			items, err = h.InstrumentSearch.Search(ctx, values.Get("code"))
			if err == nil {
				err = ctx.Err()
			}
		}
	}
	if err != nil {
		switch instrumentSearchError(err) {
		case ErrInstrumentSearchTimeout:
			httpapi.Fail(w, 504, "instrument_search_timeout", "instrument search timed out")
		case ErrInstrumentSearchUnavailable:
			httpapi.Fail(w, 502, "instrument_search_unavailable", "instrument search is temporarily unavailable")
		default:
			h.fail(w, r, err)
		}
		return
	}
	if items == nil {
		items = []InstrumentSearchItem{}
	}
	httpapi.Write(w, 200, struct {
		Items []InstrumentSearchItem `json:"items"`
	}{items})
}

func validInstrumentSearchName(name string) bool {
	return validText(name) && strings.IndexFunc(name, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError || strings.ContainsRune("<>\"\\;=~", r)
	}) == -1
}

func stockSearchType(market, kind string) bool {
	switch market {
	case "SH":
		return kind == "GP-A" || kind == "GP-A-KCB" || kind == "GP-B"
	case "SZ":
		return kind == "GP-A" || kind == "GP-A-CYB" || kind == "GP-B"
	case "HK":
		return kind == "GP"
	}
	return false
}

func (p *TencentQuotes) Search(ctx context.Context, input string) (items []InstrumentSearchItem, err error) {
	defer func() { err = instrumentSearchError(err) }()
	market, code, err := instrumentSearchCode(input)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, instrumentSearchTimeout)
	defer cancel()
	data, err := p.get(ctx, "https://proxy.finance.qq.com/cgi/cgi-bin/smartbox/search?stockFlag=1&fundFlag=1&app=official_website&c=1&query="+url.QueryEscape(code), 256<<10)
	if err != nil {
		return nil, err
	}
	// Reject malformed/duplicate JSON, including error objects masquerading as no matches.
	decoder := json.NewDecoder(bytes.NewReader(data))
	if !utf8.Valid(data) || uniqueFXJSON(decoder, 0) != nil {
		return nil, ErrInstrumentSearchUnavailable
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrInstrumentSearchUnavailable
	}
	var result struct {
		Stock []struct {
			Code string `json:"code"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"stock"`
		Fund []json.RawMessage `json:"fund"`
	}
	if json.Unmarshal(data, &result) != nil || result.Stock == nil || result.Fund == nil {
		return nil, ErrInstrumentSearchUnavailable
	}
	symbols := []string{}
	kinds := make(map[string]string)
	for _, row := range result.Stock {
		if row.Code == "" || row.Type == "" || !validInstrumentSearchName(row.Name) {
			return nil, ErrInstrumentSearchUnavailable
		}
		m, c, parseErr := instrumentSearchCode(row.Code)
		if (strings.HasPrefix(row.Code, "sh") || strings.HasPrefix(row.Code, "sz") || strings.HasPrefix(row.Code, "hk")) && parseErr != nil {
			return nil, ErrInstrumentSearchUnavailable
		}
		if parseErr != nil || m == "" || c != code || market != "" && market != m || !stockSearchType(m, row.Type) {
			continue
		}
		if kind, seen := kinds[row.Code]; seen {
			if kind != row.Type {
				return nil, ErrInstrumentSearchUnavailable
			}
			continue
		}
		kinds[row.Code] = row.Type
		symbols = append(symbols, row.Code)
	}
	items = []InstrumentSearchItem{}
	if len(symbols) == 0 {
		return items, ctx.Err()
	}
	data, err = p.batch(ctx, symbols)
	if err != nil {
		return nil, err
	}
	// GBK trail bytes may equal '~'; decode before splitting protocol fields.
	text, decodeErr := simplifiedchinese.GBK.NewDecoder().String(string(data))
	text = strings.TrimSpace(text)
	if decodeErr != nil || strings.ContainsRune(text, utf8.RuneError) || !strings.HasSuffix(text, ";") {
		return nil, ErrInstrumentSearchUnavailable
	}
	identities := make(map[string]InstrumentSearchItem)
	for _, record := range strings.Split(text, ";") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		key, payload, ok := strings.Cut(record, "=")
		symbol := strings.TrimPrefix(key, "v_")
		if !ok || key != "v_"+symbol || kinds[symbol] == "" || len(payload) < 2 || payload[0] != '"' || payload[len(payload)-1] != '"' {
			return nil, ErrInstrumentSearchUnavailable
		}
		if _, seen := identities[symbol]; seen {
			return nil, ErrInstrumentSearchUnavailable
		}
		payload = payload[1 : len(payload)-1]
		if strings.ContainsAny(payload, "\"\r\n") {
			return nil, ErrInstrumentSearchUnavailable
		}
		fields := strings.Split(payload, "~")
		typeIndex, currencyIndex := 61, 82
		m := strings.ToUpper(symbol[:2])
		if m == "HK" {
			typeIndex, currencyIndex = 63, 75
		}
		if len(fields) <= currencyIndex || fields[2] != code || fields[typeIndex] != kinds[symbol] {
			return nil, ErrInstrumentSearchUnavailable
		}
		currency := Currency(fields[currencyIndex])
		if !currency.valid() {
			return nil, ErrInstrumentSearchUnavailable
		}
		if m != "HK" {
			expected := CNY
			if kinds[symbol] == "GP-B" {
				expected = USD
				if m == "SZ" {
					expected = HKD
				}
			}
			if currency != expected {
				return nil, ErrInstrumentSearchUnavailable
			}
		}
		name := fields[1]
		if !validInstrumentSearchName(name) {
			return nil, ErrInstrumentSearchUnavailable
		}
		identities[symbol] = InstrumentSearchItem{Name: name, Market: m, Code: code, Currency: currency}
	}
	for _, symbol := range symbols {
		item, ok := identities[symbol]
		if !ok {
			return nil, ErrInstrumentSearchUnavailable
		}
		items = append(items, item)
	}
	return items, ctx.Err()
}
