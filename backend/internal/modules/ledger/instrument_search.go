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
const maxInstrumentSearchResults = 8

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

// Free-text queries accept a security name or a code the caller has not fully
// typed yet. Reject control/format characters and delimiters before any network I/O.
func validInstrumentSearchQuery(input string) bool {
	return input != "" && len(input) <= 96 && strings.TrimSpace(input) == input && validInstrumentSearchName(input)
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
	values, err := query(r, "code", "q")
	code, text := values.Get("code"), values.Get("q")
	if err == nil {
		switch {
		case code != "" && text != "":
			err = ErrQuery
		case code != "":
			_, _, err = instrumentSearchCode(code)
		case text != "":
			if !validInstrumentSearchQuery(text) {
				err = ErrQuery
			}
		default:
			err = ErrQuery
		}
	}
	items := []InstrumentSearchItem{}
	if err == nil {
		if h.InstrumentSearch == nil {
			err = ErrInstrumentSearchUnavailable
		} else {
			input := code
			if text != "" {
				input = text
			}
			ctx, cancel := context.WithTimeout(r.Context(), instrumentSearchTimeout)
			defer cancel()
			items, err = h.InstrumentSearch.Search(ctx, input)
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
	if market, code, codeErr := instrumentSearchCode(input); codeErr == nil {
		return p.searchCode(ctx, market, code)
	}
	return p.searchQuery(ctx, input)
}

func smartboxURL(query string) string {
	return "https://proxy.finance.qq.com/cgi/cgi-bin/smartbox/search?stockFlag=1&fundFlag=1&app=official_website&c=1&query=" + url.QueryEscape(query)
}

// searchCode resolves one exact code to its authoritative identity.
func (p *TencentQuotes) searchCode(ctx context.Context, market, code string) ([]InstrumentSearchItem, error) {
	ctx, cancel := context.WithTimeout(ctx, instrumentSearchTimeout)
	defer cancel()
	data, err := p.get(ctx, smartboxURL(code), 256<<10)
	if err != nil {
		return nil, err
	}
	result, err := decodeSmartbox(data)
	if err != nil {
		return nil, err
	}
	symbols := []string{}
	candidates := make(map[string]searchCandidate)
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
		if kind, seen := candidates[row.Code]; seen {
			if kind.kind != row.Type {
				return nil, ErrInstrumentSearchUnavailable
			}
			continue
		}
		candidates[row.Code] = searchCandidate{code: c, kind: row.Type}
		symbols = append(symbols, row.Code)
	}
	return p.identities(ctx, symbols, candidates)
}

// searchQuery resolves a name or partial code to up to maxInstrumentSearchResults
// authoritative identities, in the provider's relevance order.
func (p *TencentQuotes) searchQuery(ctx context.Context, input string) ([]InstrumentSearchItem, error) {
	if !validInstrumentSearchQuery(input) {
		return nil, ErrQuery
	}
	ctx, cancel := context.WithTimeout(ctx, instrumentSearchTimeout)
	defer cancel()
	data, err := p.get(ctx, smartboxURL(input), 256<<10)
	if err != nil {
		return nil, err
	}
	result, err := decodeSmartbox(data)
	if err != nil {
		return nil, err
	}
	symbols := []string{}
	candidates := make(map[string]searchCandidate)
	for _, row := range result.Stock {
		raw := strings.ToLower(strings.TrimSpace(row.Code))
		if raw == "" || row.Type == "" || !validInstrumentSearchName(row.Name) {
			return nil, ErrInstrumentSearchUnavailable
		}
		m, c, parseErr := instrumentSearchCode(raw)
		if (strings.HasPrefix(raw, "sh") || strings.HasPrefix(raw, "sz") || strings.HasPrefix(raw, "hk")) && parseErr != nil {
			return nil, ErrInstrumentSearchUnavailable
		}
		if parseErr != nil || !stockSearchType(m, row.Type) {
			continue
		}
		if _, seen := candidates[raw]; seen {
			continue
		}
		candidates[raw] = searchCandidate{code: c, kind: row.Type}
		symbols = append(symbols, raw)
		if len(symbols) == maxInstrumentSearchResults {
			break
		}
	}
	return p.identities(ctx, symbols, candidates)
}

type smartboxStock struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type smartboxResult struct {
	Stock []smartboxStock   `json:"stock"`
	Fund  []json.RawMessage `json:"fund"`
}

func decodeSmartbox(data []byte) (smartboxResult, error) {
	var result smartboxResult
	// Reject malformed/duplicate JSON, including error objects masquerading as no matches.
	decoder := json.NewDecoder(bytes.NewReader(data))
	if !utf8.Valid(data) || uniqueFXJSON(decoder, 0) != nil {
		return result, ErrInstrumentSearchUnavailable
	}
	if _, err := decoder.Token(); err != io.EOF {
		return result, ErrInstrumentSearchUnavailable
	}
	if json.Unmarshal(data, &result) != nil || result.Stock == nil || result.Fund == nil {
		return result, ErrInstrumentSearchUnavailable
	}
	return result, nil
}

type searchCandidate struct {
	code string
	kind string
}

func (p *TencentQuotes) identities(ctx context.Context, symbols []string, candidates map[string]searchCandidate) ([]InstrumentSearchItem, error) {
	items := []InstrumentSearchItem{}
	if len(symbols) == 0 {
		return items, ctx.Err()
	}
	data, err := p.batch(ctx, symbols)
	if err != nil {
		return nil, err
	}
	identities, ok := parseSearchIdentities(data, candidates)
	if !ok {
		return nil, ErrInstrumentSearchUnavailable
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

// GBK trail bytes may equal '~'; decode before splitting protocol fields. Every
// requested symbol must resolve exactly once, and its code/type/currency must
// match the provider's own record, never the caller's query text.
func parseSearchIdentities(data []byte, candidates map[string]searchCandidate) (map[string]InstrumentSearchItem, bool) {
	text, decodeErr := simplifiedchinese.GBK.NewDecoder().String(string(data))
	text = strings.TrimSpace(text)
	if decodeErr != nil || strings.ContainsRune(text, utf8.RuneError) || !strings.HasSuffix(text, ";") {
		return nil, false
	}
	identities := make(map[string]InstrumentSearchItem)
	for _, record := range strings.Split(text, ";") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		key, payload, ok := strings.Cut(record, "=")
		symbol := strings.TrimPrefix(key, "v_")
		candidate, wanted := candidates[symbol]
		if !ok || key != "v_"+symbol || !wanted || len(payload) < 2 || payload[0] != '"' || payload[len(payload)-1] != '"' {
			return nil, false
		}
		if _, seen := identities[symbol]; seen {
			return nil, false
		}
		payload = payload[1 : len(payload)-1]
		if strings.ContainsAny(payload, "\"\r\n") {
			return nil, false
		}
		fields := strings.Split(payload, "~")
		typeIndex, currencyIndex := 61, 82
		m := strings.ToUpper(symbol[:2])
		if m == "HK" {
			typeIndex, currencyIndex = 63, 75
		}
		if len(fields) <= currencyIndex || fields[2] != candidate.code || fields[typeIndex] != candidate.kind {
			return nil, false
		}
		currency := Currency(fields[currencyIndex])
		if !currency.valid() {
			return nil, false
		}
		if m != "HK" {
			expected := CNY
			if candidate.kind == "GP-B" {
				expected = USD
				if m == "SZ" {
					expected = HKD
				}
			}
			if currency != expected {
				return nil, false
			}
		}
		name := fields[1]
		if !validInstrumentSearchName(name) {
			return nil, false
		}
		identities[symbol] = InstrumentSearchItem{Name: name, Market: m, Code: candidate.code, Currency: currency}
	}
	return identities, true
}
