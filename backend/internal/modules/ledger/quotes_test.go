package ledger

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var stockNow = time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)

type stockTransport func(*http.Request) (*http.Response, error)

func (f stockTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stockRow(i Instrument, change func([]string)) string {
	f := make([]string, 83)
	f[1], f[2], f[3], f[5], f[6], f[30], f[82] = "\xff\xfe", i.Code, "12.345678", "12", "100", "20260906150000", string(i.Currency)
	if i.Market == "HK" {
		f[30], f[75] = "2026/09/06 15:00:00", "HKD"
	}
	if change != nil {
		change(f)
	}
	return "v_" + strings.ToLower(i.Market) + i.Code + "=\"" + strings.Join(f, "~") + "\";"
}

func TestStockMappingAndRows(t *testing.T) {
	for _, tc := range []struct {
		market, code string
		currency     Currency
	}{
		{"SH", "600000", CNY}, {"SH", "601000", CNY}, {"SH", "603000", CNY}, {"SH", "605000", CNY}, {"SH", "688000", CNY}, {"SH", "689000", CNY}, {"SH", "900901", USD},
		{"SZ", "000001", CNY}, {"SZ", "001001", CNY}, {"SZ", "002001", CNY}, {"SZ", "003001", CNY}, {"SZ", "300001", CNY}, {"SZ", "301001", CNY}, {"SZ", "200002", HKD}, {"HK", "00700", HKD},
	} {
		i := Instrument{ID: "i", Market: tc.market, Code: tc.code, Currency: tc.currency}
		symbol, code := quoteSymbol(i)
		require.Empty(t, code)
		rows := parseStockQuotes([]byte(stockRow(i, nil)), []string{symbol}, map[string][]Instrument{symbol: {i}}, stockNow)
		require.Empty(t, rows[symbol].ErrorCode)
		require.Equal(t, Price(12_345_678), rows[symbol].Quote.Price)
		require.Equal(t, tc.currency, rows[symbol].Quote.Currency)
	}
	for _, i := range []Instrument{
		{Market: "US", Code: "123456", Currency: USD}, {Market: "TEST", Code: "600000", Currency: CNY}, {Market: "SH", Code: "510300", Currency: CNY},
		{Market: "SZ", Code: "159001", Currency: CNY}, {Market: "HK", Code: "700", Currency: HKD}, {Market: "SH", Code: "60000x", Currency: CNY},
	} {
		_, code := quoteSymbol(i)
		require.Equal(t, "unsupported_instrument", code)
	}
	i := Instrument{ID: "i", Market: "SH", Code: "600000", Currency: CNY}
	wrong := i
	wrong.Currency = USD
	_, code := quoteSymbol(wrong)
	require.Equal(t, "currency_mismatch", code)
	for _, tc := range []struct {
		name, want string
		mutate     func([]string)
	}{
		{"code", "quote_unavailable", func(f []string) { f[2] = "600001" }},
		{"currency", "currency_mismatch", func(f []string) { f[82] = "USD" }},
		{"zero", "quote_unavailable", func(f []string) { f[3] = "0" }},
		{"negative", "quote_unavailable", func(f []string) { f[3] = "-1" }},
		{"date", "quote_unavailable", func(f []string) { f[30] = "20260230150000" }},
		{"future", "quote_unavailable", func(f []string) { f[30] = "20260907150000" }},
		{"delisted", "quote_inactive", func(f []string) { f[40] = "D" }},
		{"volume", "quote_inactive", func(f []string) { f[6] = "0" }},
		{"open", "quote_inactive", func(f []string) { f[5] = "0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := parseStockQuotes([]byte(stockRow(i, tc.mutate)), []string{"sh600000"}, map[string][]Instrument{"sh600000": {i}}, stockNow)
			require.Equal(t, tc.want, rows["sh600000"].ErrorCode)
		})
	}
	body := stockRow(i, nil) + stockRow(i, nil) + "v_sh600001=\"bad\";"
	rows := parseStockQuotes([]byte(body), []string{"sh600000", "sh600002"}, map[string][]Instrument{"sh600000": {i}}, stockNow)
	require.Equal(t, "quote_unavailable", rows["sh600000"].ErrorCode)
	require.NotContains(t, rows, "sh600002")
}

func TestStockBatchHTTPBounds(t *testing.T) {
	var sizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		symbols := strings.Split(r.URL.Query().Get("q"), ",")
		sizes = append(sizes, len(symbols))
		for _, symbol := range symbols {
			if symbol == "sh600050" {
				continue
			}
			_, _ = fmt.Fprint(w, stockRow(Instrument{Market: "SH", Code: symbol[2:], Currency: CNY}, nil))
		}
	}))
	defer server.Close()
	p := newTencentQuotes(stockTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "https", r.URL.Scheme)
		require.Equal(t, "qt.gtimg.cn", r.URL.Host)
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return server.Client().Transport.RoundTrip(r)
	}), func() time.Time { return stockNow })
	var instruments []Instrument
	for n := 0; n < 101; n++ {
		instruments = append(instruments, Instrument{ID: fmt.Sprint(n), Market: "SH", Code: fmt.Sprintf("600%03d", n), Currency: CNY})
	}
	rows := p.Fetch(t.Context(), instruments)
	require.Equal(t, []int{50, 50, 1}, sizes)
	require.Len(t, rows, 101)
	require.Equal(t, "quote_unavailable", rows["50"].ErrorCode)
	require.NotNil(t, rows["100"].Quote)
	for _, status := range []int{200, 302, 500} {
		p.client.Transport = stockTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: http.NoBody, Header: make(http.Header)}, nil
		})
		require.Equal(t, "quote_unavailable", p.Fetch(t.Context(), instruments[:1])["0"].ErrorCode)
	}
	p.client.Transport = stockTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", (1<<20)+1))), Header: make(http.Header)}, nil
	})
	require.Equal(t, "quote_unavailable", p.Fetch(t.Context(), instruments[:1])["0"].ErrorCode)
}

func TestStockGlobalConcurrencyAndCancellation(t *testing.T) {
	var active, peak atomic.Int32
	transport := stockTransport(func(r *http.Request) (*http.Response, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			p := newTencentQuotes(transport, time.Now)
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			rows := p.Fetch(ctx, []Instrument{{ID: "i", Market: "SH", Code: "600000", Currency: CNY}})
			require.Equal(t, "quote_timeout", rows["i"].ErrorCode)
		})
	}
	wg.Wait()
	require.Equal(t, int32(4), peak.Load())
	require.Zero(t, active.Load())
	require.Empty(t, stockQuoteSlots)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p := newTencentQuotes(stockTransport(func(*http.Request) (*http.Response, error) {
		t.Error("canceled request fetched")
		return nil, context.Canceled
	}), time.Now)
	p.Fetch(ctx, []Instrument{{ID: "i", Market: "SH", Code: "600000", Currency: CNY}})
}
