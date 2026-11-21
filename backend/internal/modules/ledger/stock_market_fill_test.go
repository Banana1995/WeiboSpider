package ledger

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStockBookSaturdayReadThroughAndOffHoursWarmup(t *testing.T) {
	ctx := t.Context()
	hk := Instrument{ID: "foreign", Market: "HK", Code: "00700", Name: "Foreign", Currency: HKD}
	cn := Instrument{ID: "local", Market: "SH", Code: "600000", Name: "Local", Currency: CNY}
	other := Instrument{ID: "another", Market: "SZ", Code: "000001", Name: "Other account", Currency: CNY}
	s := valuationFixture(t, []CurrentPosition{{hk.ID, 1_000_000}, {cn.ID, 1_000_000}}, []Instrument{hk, cn, other})
	manualSourceAccount(t, s, "b")
	putSource(t, s, "b", "b-open", "0", 0, CurrentPosition{other.ID, 1_000_000})
	saturday := stockMarketTime(t, "2026-11-21 12:00")
	s.now = func() time.Time { return saturday }
	friday := stockMarketTime(t, "2026-11-20 15:00")
	stamp := friday.Format(time.RFC3339)
	local := &Quote{Symbol: "sh600000", Price: 10_000_000, Currency: CNY, Source: "Tencent", Date: "2026-11-20", QuotedAt: stamp, FetchedAt: stamp}
	require.NoError(t, s.saveStockPrices(ctx, savedStockQuotes{local.Symbol: local}, nil))
	var requests []string
	fxCalls := 0
	provider := newTencentQuotes(stockTransport(func(r *http.Request) (*http.Response, error) {
		key := r.URL.Query().Get("q")
		requests = append(requests, key)
		var row string
		switch key {
		case "hk00700":
			row = stockRow(hk, func(f []string) { f[30] = "2026/11/20 16:08:00"; f[3] = "20.00" })
		case "sz000001":
			row = stockRow(other, func(f []string) { f[30] = "20261120150000" })
		default:
			t.Fatalf("unexpected request %s", key)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(row))}, nil
	}), s.now)
	h := Handler{Store: s, Quotes: provider, FX: valuationFX(func(_ context.Context, r FXRequest) (FXQuote, error) {
		fxCalls++
		require.Equal(t, FXRequest{Base: HKD, Quote: CNY, Mode: "latest"}, r)
		return FXQuote{Base: HKD, Quote: CNY, Mode: "latest", Rate: 50_000_000, Date: "2026-11-20", QuotedAt: stamp, FetchedAt: saturday.Format(time.RFC3339), Source: "Tencent/spot/HKDCNY"}, nil
	})}
	mux := http.NewServeMux()
	h.Register(mux)
	count := auditCount(t, s)
	var book StockBook
	require.NoError(t, json.Unmarshal(historyRequest(t, mux, "GET", "a/stock-book", 200), &book))
	require.True(t, book.Complete)
	require.Equal(t, Money(12000), *book.TotalAssets)
	require.Equal(t, "2026-11-20", book.Items[0].Quote.Date)
	require.Equal(t, []string{"hk00700"}, requests)
	require.Equal(t, 1, fxCalls)
	// The result is durable, not just in the HTTP response. Repeated GET/HEAD
	// do not hit the network, and the other account was not requested.
	require.True(t, readBook(t, s).Complete)
	historyRequest(t, mux, "HEAD", "a/stock-book", 200)
	require.Equal(t, []string{"hk00700"}, requests)
	require.Equal(t, 1, fxCalls)
	// Offline worker discovers the other account even without a page or open exchange.
	w := StockMarketWorker{store: s, quotes: provider, fx: h.FX, marketOpen: func(context.Context, string, time.Time) (bool, error) { panic("no trading session on Saturday") }}
	slot, markets, _ := stockQuoteSchedule(saturday)
	require.True(t, slot.IsZero())
	require.NoError(t, w.refreshSlot(ctx, slot, markets))
	require.NoError(t, w.refreshSlot(ctx, slot, markets))
	require.Equal(t, []string{"hk00700", "sz000001"}, requests)
	require.Equal(t, 1, fxCalls)
	var clock int
	require.NoError(t, s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stock_market_clock`).Scan(&clock))
	require.Zero(t, clock)
	require.Equal(t, count, auditCount(t, s))
	historyCount(t, s, 0)
}

func TestStockBookFailedMissingLegIsRetriedWithoutRefetchingGoodLeg(t *testing.T) {
	for _, bad := range []string{"quote", "fx"} {
		t.Run(bad, func(t *testing.T) {
			i := Instrument{ID: "one", Market: "HK", Code: "00700", Name: "Synthetic", Currency: HKD}
			s := valuationFixture(t, []CurrentPosition{{i.ID, 1_000_000}}, []Instrument{i})
			now := stockNow
			s.now = func() time.Time { return now }
			quoteCalls, fxCalls := 0, 0
			fail := true
			h := Handler{Store: s, Quotes: valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
				quoteCalls++
				if fail && bad == "quote" {
					return nil
				}
				return validValuationQuotes(ctx, is)
			}), FX: valuationFX(func(context.Context, FXRequest) (FXQuote, error) {
				fxCalls++
				if fail && bad == "fx" {
					return FXQuote{}, ErrFXUnavailable
				}
				return FXQuote{Base: HKD, Quote: CNY, Mode: "latest", Rate: 50_000_000, Date: "2026-09-06", QuotedAt: stockNow.Format(time.RFC3339), FetchedAt: now.Format(time.RFC3339), Source: "Tencent/spot/HKDCNY"}, nil
			})}
			// Readers race on a cold book with one unavailable leg. The source is
			// attempted once, not once per tab, and the valid leg is persisted.
			var group sync.WaitGroup
			books := make([]StockBook, 8)
			errs := make([]error, len(books))
			for n := range books {
				group.Go(func() { books[n], errs[n] = h.stockBookView(t.Context(), "a") })
			}
			group.Wait()
			for n, book := range books {
				require.NoError(t, errs[n])
				require.False(t, book.Complete)
				require.Nil(t, book.PositionsValue)
				require.Nil(t, book.TotalAssets)
				if bad == "fx" {
					require.NotNil(t, book.Items[0].MarketValue)
				}
			}
			require.Equal(t, 1, quoteCalls)
			require.Equal(t, 1, fxCalls)
			fail = false
			now = now.Add(time.Minute)
			book, err := h.stockBookView(t.Context(), "a")
			require.NoError(t, err)
			require.True(t, book.Complete)
			require.NotNil(t, book.Items[0].Weight)
			if bad == "fx" {
				require.Equal(t, 1, quoteCalls)
				require.Equal(t, 2, fxCalls)
			} else {
				require.Equal(t, 2, quoteCalls)
				require.Equal(t, 1, fxCalls)
			}
		})
	}
}
