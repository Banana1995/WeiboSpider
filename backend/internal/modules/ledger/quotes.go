package ledger

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

const valuationTimeout = 12 * time.Second
const quoteNetworkTimeout = 6 * time.Second

type Quote struct {
	Symbol    string   `json:"symbol"`
	Price     Price    `json:"price"`
	Currency  Currency `json:"currency"`
	Source    string   `json:"source"`
	Date      string   `json:"date"`
	QuotedAt  string   `json:"quoted_at"`
	FetchedAt string   `json:"fetched_at"`
}

type QuoteResult struct {
	Quote     *Quote
	ErrorCode string
}

// Results are keyed by instrument ID. Missing results are unavailable, never zero.
// Implementations must honor context cancellation; no goroutine is spawned per holding.
type QuotesProvider interface {
	Fetch(context.Context, []Instrument) map[string]QuoteResult
}

// Stock-only whitelist: SH 600/601/603/605/688/689 CNY, 900 USD;
// SZ 000/001/002/003/300/301 CNY, 200 HKD; HK five digits HKD.
// Funds and other markets are deliberately not inferred from a numeric code.
func quoteSymbol(i Instrument) (string, string) {
	for _, c := range i.Code {
		if c < '0' || c > '9' {
			return "", "unsupported_instrument"
		}
	}
	var currency Currency
	switch {
	case i.Market == "HK" && len(i.Code) == 5:
		currency = HKD
	case (i.Market == "SH" || i.Market == "SZ") && len(i.Code) == 6:
		prefix := i.Code[:3]
		if i.Market == "SH" {
			switch prefix {
			case "600", "601", "603", "605", "688", "689":
				currency = CNY
			case "900":
				currency = USD
			}
		} else {
			switch prefix {
			case "000", "001", "002", "003", "300", "301":
				currency = CNY
			case "200":
				currency = HKD
			}
		}
	}
	if currency == "" {
		return "", "unsupported_instrument"
	}
	if currency != i.Currency {
		return "", "currency_mismatch"
	}
	return strings.ToLower(i.Market) + i.Code, ""
}

type TencentQuotes struct {
	client *http.Client
	now    func() time.Time
}

// Shared across provider instances and requests, including response reads.
var stockQuoteSlots = make(chan struct{}, 4)

// No startup I/O and no user-configurable URL or redirect destination.
func NewTencentQuotes() *TencentQuotes { return newTencentQuotes(http.DefaultTransport, time.Now) }

func newTencentQuotes(transport http.RoundTripper, now func() time.Time) *TencentQuotes {
	return &TencentQuotes{client: &http.Client{Transport: transport, Timeout: quoteNetworkTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, now: now}
}

func (p *TencentQuotes) Fetch(ctx context.Context, instruments []Instrument) map[string]QuoteResult {
	ctx, cancel := context.WithTimeout(ctx, valuationTimeout)
	defer cancel()
	result := make(map[string]QuoteResult, len(instruments))
	bySymbol := make(map[string][]Instrument)
	symbols := make([]string, 0, len(instruments))
	for _, i := range instruments {
		symbol, code := quoteSymbol(i)
		if code != "" {
			result[i.ID] = QuoteResult{ErrorCode: code}
			continue
		}
		if _, exists := bySymbol[symbol]; !exists {
			symbols = append(symbols, symbol)
		}
		bySymbol[symbol] = append(bySymbol[symbol], i)
	}
	// Sequential batches bound work without a holdings limit or goroutine fan-out.
	for start := 0; start < len(symbols); start += 50 {
		batch := symbols[start:min(start+50, len(symbols))]
		body, err := p.batch(ctx, batch)
		rows := map[string]QuoteResult{}
		if err == nil {
			rows = parseStockQuotes(body, batch, bySymbol, p.now())
		}
		for _, symbol := range batch {
			row, exists := rows[symbol]
			if !exists {
				row.ErrorCode = "quote_unavailable"
				if fxFailure(err) == ErrFXTimeout {
					row.ErrorCode = "quote_timeout"
				}
			}
			for _, i := range bySymbol[symbol] {
				result[i.ID] = row
			}
		}
	}
	return result
}

func (p *TencentQuotes) batch(ctx context.Context, symbols []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, quoteNetworkTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case stockQuoteSlots <- struct{}{}:
		defer func() { <-stockQuoteSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://qt.gtimg.cn/?q="+strings.Join(symbols, ","), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrFXUnavailable
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, ErrFXUnavailable
	}
	return body, ctx.Err()
}

func parseStockQuotes(body []byte, symbols []string, instruments map[string][]Instrument, now time.Time) map[string]QuoteResult {
	result := make(map[string]QuoteResult, len(symbols))
	wanted := make(map[string]bool, len(symbols))
	for _, symbol := range symbols {
		wanted[symbol] = true
	}
	seen := make(map[string]bool)
	for _, record := range strings.Split(string(body), ";") {
		key, payload, ok := strings.Cut(strings.TrimSpace(record), "=")
		symbol := strings.TrimPrefix(key, "v_")
		if !ok || key != "v_"+symbol || !wanted[symbol] {
			continue
		}
		if seen[symbol] {
			result[symbol] = QuoteResult{ErrorCode: "quote_unavailable"}
			continue
		}
		seen[symbol] = true
		result[symbol] = QuoteResult{ErrorCode: "quote_unavailable"}
		if len(payload) < 2 || payload[0] != '"' || payload[len(payload)-1] != '"' {
			continue
		}
		payload = payload[1 : len(payload)-1]
		if strings.ContainsAny(payload, "\"\r\n") {
			continue
		}
		// The GBK name field is unused. All validated protocol fields are ASCII.
		fields := strings.Split(payload, "~")
		i := instruments[symbol][0]
		currencyIndex, layout := 82, "20060102150405"
		if i.Market == "HK" {
			currencyIndex, layout = 75, "2006/01/02 15:04:05"
		}
		if len(fields) <= currencyIndex || fields[2] != i.Code {
			continue
		}
		if Currency(fields[currencyIndex]) != i.Currency {
			result[symbol] = QuoteResult{ErrorCode: "currency_mismatch"}
			continue
		}
		price, priceErr := ParsePrice(fields[3])
		stamp, stampErr := time.ParseInLocation(layout, fields[30], fxBeijing)
		if priceErr != nil || price <= 0 || stampErr != nil || stamp.Year() < 1 || stamp.Format(layout) != fields[30] || stamp.After(now) {
			continue
		}
		// Delisted securities and today's zero-volume/open placeholders are not
		// tradable marks, even when Tencent gives them a fresh timestamp.
		volume, volumeErr := ParseQuantity(fields[6])
		open, openErr := ParsePrice(fields[5])
		if i.Market != "HK" && fields[40] == "D" ||
			stamp.Format(time.DateOnly) == now.In(fxBeijing).Format(time.DateOnly) && (volumeErr != nil || volume <= 0 || openErr != nil || open <= 0) {
			result[symbol] = QuoteResult{ErrorCode: "quote_inactive"}
			continue
		}
		result[symbol] = QuoteResult{Quote: &Quote{Symbol: symbol, Price: price, Currency: i.Currency, Source: "Tencent",
			Date: stamp.Format(time.DateOnly), QuotedAt: stamp.Format(time.RFC3339), FetchedAt: now.UTC().Format(time.RFC3339Nano)}}
	}
	return result
}
