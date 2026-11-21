package ledger

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func stockMarketTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04", s, fxBeijing)
	require.NoError(t, err)
	return at
}

func TestStockMarketScheduleAcrossLunchCloseAndWeekend(t *testing.T) {
	// Friday, Beijing. An off-hours start never catches up a missed session.
	for _, tc := range []struct{ at, slot, markets, next string }{
		{"20 08:00", "", "", "20 09:50"},
		{"20 09:50", "20 09:50", "SH,SZ,HK", "20 10:10"},
		{"20 10:27", "20 10:10", "SH,SZ,HK", "20 10:30"},
		{"20 11:30", "20 11:30", "SH,SZ,HK", "20 11:50"},
		{"20 11:45", "20 11:30", "HK", "20 11:50"},
		{"20 11:50", "20 11:50", "HK", "20 12:00"},
		{"20 12:00", "20 12:00", "HK", "20 13:20"},
		{"20 12:20", "", "", "20 13:20"},
		{"20 13:20", "20 13:20", "SH,SZ,HK", "20 13:40"},
		{"20 15:00", "20 15:00", "SH,SZ,HK", "20 15:20"},
		{"20 15:55", "20 15:40", "HK", "20 16:00"},
		{"20 16:00", "20 16:00", "HK", "23 09:50"},
		{"20 16:01", "", "", "23 09:50"},
		{"21 10:10", "", "", "23 09:50"},
	} {
		t.Run(tc.at, func(t *testing.T) {
			at := stockMarketTime(t, "2026-11-"+tc.at)
			slot, markets, next := stockQuoteSchedule(at.UTC().Add(15 * time.Second))
			if tc.slot == "" {
				require.True(t, slot.IsZero())
			} else {
				require.Equal(t, tc.slot, slot.Format("02 15:04"))
			}
			require.Equal(t, tc.markets, strings.Join(markets, ","))
			require.Equal(t, tc.next, next.Format("02 15:04"))
		})
	}
}

func TestStockMarketOpenRequiresCurrentExchangeSessionEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, market, symbol, index, date, bar, now string
		want, bad                                   bool
	}{
		{"Shanghai", "SH", "sh000001", "000001", "20261120", "1009 3000.0 1000 2000", "10:10", true, false},
		{"Shenzhen", "SZ", "sz399001", "399001", "20261120", "1459 10000 1000 2000", "15:00", true, false},
		{"delayed HK", "HK", "hkHSI", "HSI", "20261120", "1305 20000 1000 2000", "13:20", true, false},
		{"exchange holiday", "HK", "hkHSI", "HSI", "20261119", "1600 20000 1000 2000", "10:10", false, false},
		{"half day", "HK", "hkHSI", "HSI", "20261120", "1208 20000 1000 2000", "13:20", false, false},
		{"copied lunch close", "HK", "hkHSI", "HSI", "20261120", "1300 20000 1000 2000", "13:20", false, false},
		{"stale today", "SH", "sh000001", "000001", "20261120", "1009 3000.0 1000 2000", "10:30", false, false},
		{"future bar", "SZ", "sz399001", "399001", "20261120", "1011 10000 1000 2000", "10:10", false, false},
		{"wrong index", "HK", "hkHSI", "00700", "20261120", "1009 20 1000 2000", "10:10", false, true},
		{"malformed volume", "SH", "sh000001", "000001", "20261120", "1009 3000.0 -1 2000", "10:10", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := stockMarketTime(t, "2026-11-20 "+tc.now)
			body, err := json.Marshal(map[string]any{"code": 0, "data": map[string]any{tc.symbol: map[string]any{
				"qt":   map[string]any{tc.symbol: []string{"", "index", tc.index}, "market": map[string]string{"status": "unused"}},
				"data": map[string]any{"date": tc.date, "data": []string{tc.bar}},
			}}})
			require.NoError(t, err)
			provider := newTencentQuotes(stockTransport(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "https://web.ifzq.gtimg.cn/appstock/app/minute/query?code="+tc.symbol, r.URL.String())
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			}), func() time.Time { return now })
			open, err := provider.stockMarketOpen(context.Background(), tc.market, now.UTC())
			if tc.bad {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, open)
		})
	}
}
