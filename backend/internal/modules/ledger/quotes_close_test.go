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

func closingQuoteFixture(i Instrument) map[string]any {
	symbol, _ := quoteSymbol(i)
	identity := make([]string, 83)
	identity[2], identity[75], identity[82] = i.Code, string(i.Currency), string(i.Currency)
	return map[string]any{"code": 0, "data": map[string]any{symbol: map[string]any{
		"day": [][]string{
			{"2026-03-18", "2.740", "2.680", "2.740", "2.660", "6000"},
			{"2026-03-19", "2.670", "2.640", "2.690", "2.620", "10000"},
		},
		"qt": map[string][]string{symbol: identity},
	}}}
}

func closingQuoteJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	return b
}

func inactiveStockRow(i Instrument, change func([]string)) string {
	return stockRow(i, func(f []string) {
		f[3], f[4], f[5], f[6], f[40] = "2.640", "2.640", "0.000", "0.0", "S"
		if change != nil {
			change(f)
		}
	})
}

func TestClosingQuoteUsesActualUnadjustedTradingDate(t *testing.T) {
	i := Instrument{ID: "halted", Market: "HK", Code: "01448", Currency: HKD}
	body := closingQuoteFixture(i)
	data := body["data"].(map[string]any)["hk01448"].(map[string]any)
	data["day"] = append(data["day"].([][]string), []string{"2026-09-06", "0", "2.640", "0", "0", "0"})
	q, err := parseClosingQuote(closingQuoteJSON(t, body), i, 2_640_000, stockNow)
	require.NoError(t, err)
	require.Equal(t, Price(2_640_000), q.Price)
	require.Equal(t, "2026-03-19", q.Date)
	require.Equal(t, "2026-03-19T00:00:00+08:00", q.QuotedAt)
	require.Equal(t, stockNow.Format(time.RFC3339Nano), q.FetchedAt)
	require.Equal(t, "Tencent", q.Source)
	require.Equal(t, HKD, q.Currency)
}

func TestClosingQuoteRejectsUnverifiedHistory(t *testing.T) {
	i := Instrument{ID: "halted", Market: "HK", Code: "01448", Currency: HKD}
	for _, tc := range []struct {
		name   string
		change func(map[string]any, map[string]any)
	}{
		{"missing status", func(root, _ map[string]any) { delete(root, "code") }},
		{"upstream failure", func(root, _ map[string]any) { root["code"] = 1 }},
		{"wrong symbol", func(root, data map[string]any) { root["data"] = map[string]any{"hk00700": data} }},
		{"wrong identity", func(_, data map[string]any) { data["qt"].(map[string][]string)["hk01448"][2] = "00700" }},
		{"wrong currency", func(_, data map[string]any) { data["qt"].(map[string][]string)["hk01448"][75] = "USD" }},
		{"missing identity", func(_, data map[string]any) { delete(data, "qt") }},
		{"adjusted only", func(_, data map[string]any) { data["qfqday"] = data["day"]; delete(data, "day") }},
		{"empty history", func(_, data map[string]any) { data["day"] = [][]string{} }},
		{"invalid date", func(_, data map[string]any) { data["day"].([][]string)[1][0] = "2026-02-30" }},
		{"future date", func(_, data map[string]any) { data["day"].([][]string)[1][0] = "2026-09-07" }},
		{"duplicate date", func(_, data map[string]any) { data["day"].([][]string)[1][0] = "2026-03-18" }},
		{"reversed history", func(_, data map[string]any) {
			rows := data["day"].([][]string)
			rows[0], rows[1] = rows[1], rows[0]
		}},
		{"bad volume", func(_, data map[string]any) { data["day"].([][]string)[1][5] = "NaN" }},
		{"negative volume", func(_, data map[string]any) { data["day"].([][]string)[1][5] = "-1" }},
		{"zero price", func(_, data map[string]any) { data["day"].([][]string)[1][2] = "0" }},
		{"invalid range", func(_, data map[string]any) { data["day"].([][]string)[1][3] = "2.5" }},
		{"close disagrees", func(_, data map[string]any) { data["day"].([][]string)[1][2] = "2.65" }},
		{"only current day", func(_, data map[string]any) {
			data["day"] = [][]string{{"2026-09-06", "2.64", "2.64", "2.64", "2.64", "100"}}
		}},
		{"zero volume only", func(_, data map[string]any) {
			for _, row := range data["day"].([][]string) {
				row[5] = "0"
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := closingQuoteFixture(i)
			data := body["data"].(map[string]any)["hk01448"].(map[string]any)
			tc.change(body, data)
			q, err := parseClosingQuote(closingQuoteJSON(t, body), i, 2_640_000, stockNow)
			require.ErrorIs(t, err, errClosingQuote)
			require.Nil(t, q)
		})
	}
}

func TestClosingQuoteRestoresFullAccountWeights(t *testing.T) {
	instruments := []Instrument{
		{ID: "a", Market: "SH", Code: "600000", Name: "Synthetic active", Currency: CNY},
		{ID: "b", Market: "HK", Code: "01448", Name: "Synthetic suspended", Currency: HKD},
	}
	s := valuationFixture(t, []CurrentPosition{{InstrumentID: "a", Quantity: 100_000_000}, {InstrumentID: "b", Quantity: 500_000_000}}, instruments)
	historyCalls, failHistory := 0, true
	provider := newTencentQuotes(stockTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "https", r.URL.Scheme)
		status, body := 200, ""
		switch r.URL.Host {
		case "qt.gtimg.cn":
			body = stockRow(instruments[0], func(f []string) { f[3] = "10" }) + inactiveStockRow(instruments[1], nil)
		case "web.ifzq.gtimg.cn":
			historyCalls++
			require.Equal(t, "/appstock/app/fqkline/get", r.URL.Path)
			require.Equal(t, "hk01448,day,,,30,", r.URL.Query().Get("param"))
			if failHistory {
				status = 503
			} else {
				body = string(closingQuoteJSON(t, closingQuoteFixture(instruments[1])))
			}
		default:
			t.Fatalf("unexpected quote host %s", r.URL.Host)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	}), func() time.Time { return stockNow })
	h := Handler{Store: s, Now: func() time.Time { return stockNow }, Quotes: provider,
		FX: valuationFX(func(context.Context, FXRequest) (FXQuote, error) {
			return FXQuote{Base: HKD, Quote: CNY, Mode: "latest", Rate: 50_000_000, Date: "2026-09-06", Source: "Tencent/spot/HKDCNY", QuotedAt: "2026-09-06T15:00:00+08:00", FetchedAt: stockNow.Format(time.RFC3339)}, nil
		}),
	}
	w := StockMarketWorker{store: s, quotes: provider, fx: h.FX, marketOpen: func(context.Context, string, time.Time) (bool, error) { return true, nil }}
	// No verified close available yet: persist the successful leg only.
	require.Error(t, w.refreshSlot(t.Context(), stockNow.Add(-40*time.Minute), []string{"SH", "HK"}))
	b, err := h.stockBookView(t.Context(), "a")
	require.NoError(t, err)
	require.False(t, b.Complete)
	require.Nil(t, b.TotalAssets)
	require.Equal(t, Money(100000), *b.PositionsValue)
	for _, item := range b.Items {
		require.Nil(t, item.Weight)
	}
	require.Nil(t, b.Items[1].Quote)
	require.Equal(t, 1, historyCalls)
	failHistory = false
	require.NoError(t, w.refreshSlot(t.Context(), stockNow.Add(-20*time.Minute), []string{"SH", "HK"}))
	b, err = h.stockBookView(t.Context(), "a")
	require.NoError(t, err)
	require.True(t, b.Complete)
	require.Equal(t, 2, historyCalls)
	require.Equal(t, Money(176000), *b.TotalAssets) // 100 cash + 1000 CNY + 1320 HKD × 0.5.
	require.Equal(t, Money(166000), *b.PositionsValue)
	require.Equal(t, "56.82", *b.Items[0].Weight)
	require.Equal(t, "37.50", *b.Items[1].Weight)
	require.Equal(t, Money(132000), *b.Items[1].MarketValue)
	require.Equal(t, "prior_date", b.Items[1].QuoteStatus)
	require.Equal(t, "2026-03-19", b.Items[1].Quote.Date)

	// A verified old close also remains valid frozen valuation evidence.
	v, held, err := s.valuationInputs(t.Context(), "a")
	require.NoError(t, err)
	require.NoError(t, h.valuePositions(t.Context(), &v, held))
	proof := valuationSnapshot{SchemaVersion: 1, AccountName: "Synthetic", Valuation: v, Instruments: []instrumentJSON{}}
	for _, i := range held {
		proof.Instruments = append(proof.Instruments, instrumentJSON{i.ID, i.Market, i.Code, i.Name, i.Currency})
	}
	require.NoError(t, proof.validate())

	// A later upstream failure can't destroy a previously verified price.
	failHistory = true
	require.Error(t, w.refreshSlot(t.Context(), stockNow, []string{"SH", "HK"}))
	after, err := h.stockBookView(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, b, after)
}

func TestClosingQuoteDoesNotRescueInvalidOrDelistedSpot(t *testing.T) {
	i := Instrument{ID: "i", Market: "HK", Code: "01448", Currency: HKD}
	for _, tc := range []struct {
		name   string
		change func([]string)
	}{
		{"delisted", func(f []string) { f[40] = "D" }},
		{"wrong currency", func(f []string) { f[75] = "USD" }},
		{"no previous close", func(f []string) { f[4] = "" }},
		{"disagreeing prices", func(f []string) { f[3] = "2.65" }},
		{"bad volume", func(f []string) { f[6] = "bad" }},
		{"bad open", func(f []string) { f[5] = "bad" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newTencentQuotes(stockTransport(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "qt.gtimg.cn", r.URL.Host, "invalid rows must never fetch fallback history")
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(inactiveStockRow(i, tc.change))), Header: make(http.Header)}, nil
			}), func() time.Time { return stockNow })
			row := p.Fetch(t.Context(), []Instrument{i})[i.ID]
			require.Nil(t, row.Quote)
			require.NotEmpty(t, row.ErrorCode)
		})
	}
}
