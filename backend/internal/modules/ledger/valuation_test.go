package ledger

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type valuationQuotes func(context.Context, []Instrument) map[string]QuoteResult

func (f valuationQuotes) Fetch(c context.Context, i []Instrument) map[string]QuoteResult {
	return f(c, i)
}

type valuationFX func(context.Context, FXRequest) (FXQuote, error)

func (f valuationFX) Fetch(c context.Context, r FXRequest) (FXQuote, error) { return f(c, r) }

func valuationFixture(t *testing.T, positions []OpeningPosition, instruments []Instrument) *Store {
	t.Helper()
	db, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	s := NewStore(db, func() time.Time { return stockNow })
	for _, i := range instruments {
		require.NoError(t, s.AddInstrument(t.Context(), i))
	}
	require.NoError(t, s.InitializeAccount(t.Context(), "Synthetic", Opening{AccountID: "a", Currency: CNY, Date: "2026-01-01", Cash: 10000, Positions: positions}))
	return s
}

func validValuationQuotes(_ context.Context, instruments []Instrument) map[string]QuoteResult {
	rows := map[string]QuoteResult{}
	for _, i := range instruments {
		symbol, _ := quoteSymbol(i)
		rows[i.ID] = QuoteResult{Quote: &Quote{Symbol: symbol, Price: 12_345_678, Currency: i.Currency, Source: "Tencent", Date: "2026-09-06", QuotedAt: "2026-09-06T15:00:00+08:00", FetchedAt: stockNow.Format(time.RFC3339)}}
	}
	return rows
}

func TestValuationExactForeignAndUnknownCost(t *testing.T) {
	instruments := []Instrument{{ID: "a", Market: "SH", Code: "600000", Name: "A", Currency: CNY}, {ID: "b", Market: "HK", Code: "00700", Name: "B", Currency: HKD}, {ID: "c", Market: "SZ", Code: "200001", Name: "C", Currency: HKD}}
	negative := Money(-500)
	s := valuationFixture(t, []OpeningPosition{{InstrumentID: "a", Quantity: 1_234_567, DilutedBasis: &negative}, {InstrumentID: "b", Quantity: 1_234_567}, {InstrumentID: "c", Quantity: 1_234_567}}, instruments)
	result, held, err := s.valuationInputs(t.Context(), "a")
	require.NoError(t, err)
	fxCalls := 0
	h := Handler{Now: func() time.Time { return stockNow }, Quotes: valuationQuotes(validValuationQuotes), FX: valuationFX(func(ctx context.Context, r FXRequest) (FXQuote, error) {
		fxCalls++
		require.Equal(t, HKD, r.Base)
		require.Equal(t, CNY, r.Quote)
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 6*time.Second)
		return FXQuote{Base: HKD, Quote: CNY, Mode: "latest", RequestedDate: "2026-09-06", Rate: 91_234_567, Date: "2026-09-05", Source: "Tencent/spot/HKDCNY", QuotedAt: "2026-09-05T15:00:00+08:00", FetchedAt: stockNow.Format(time.RFC3339)}, nil
	})}
	require.NoError(t, h.valuePositions(t.Context(), &result, held))
	require.Equal(t, 1, fxCalls)
	require.True(t, result.Complete)
	// 1.234567 * 12.345678 -> 15.24; 15.24 * .91234567 -> 13.90.
	require.Equal(t, Money(4304), *result.PositionsValue)
	require.Equal(t, Money(14304), *result.TotalAssets)
	require.Nil(t, result.Items[0].FX)
	require.Equal(t, "prior_date", result.Items[1].Status)
	require.Equal(t, "current", result.Items[0].Status)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"cash":"100.00"`)
}

func TestValuationIncompleteAndOverflow(t *testing.T) {
	i := Instrument{ID: "i", Market: "HK", Code: "00700", Currency: HKD}
	for _, code := range []string{"quote_unavailable", "quote_timeout", "quote_inactive", "fx_unavailable", "fx_timeout", "prior_date", "overflow", "total_overflow"} {
		t.Run(code, func(t *testing.T) {
			v := Valuation{Currency: CNY, AsOf: "2026-09-06", Cash: 10000, Complete: true, Items: []ValuationItem{{InstrumentID: "i", Quantity: 1_000_000, Status: "unavailable"}}}
			h := Handler{Now: func() time.Time { return stockNow }, Quotes: valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
				if code == "quote_unavailable" {
					return nil
				}
				if code == "quote_timeout" || code == "quote_inactive" {
					return map[string]QuoteResult{"i": {ErrorCode: code}}
				}
				rows := validValuationQuotes(ctx, is)
				if code == "prior_date" {
					rows["i"].Quote.Date = "2026-09-05"
					rows["i"].Quote.QuotedAt = "2026-09-05T15:00:00+08:00"
				}
				if code == "overflow" {
					rows["i"].Quote.Price = Price(math.MaxInt64)
					v.Items[0].Quantity = Quantity(math.MaxInt64)
				}
				return rows
			}), FX: valuationFX(func(context.Context, FXRequest) (FXQuote, error) {
				if code == "fx_timeout" {
					return FXQuote{}, ErrFXTimeout
				}
				if code == "fx_unavailable" {
					return FXQuote{}, ErrFXUnavailable
				}
				return FXQuote{Base: HKD, Quote: CNY, Mode: "latest", Rate: 100_000_000, Date: "2026-09-06", Source: "Tencent", QuotedAt: "2026-09-06T15:00:00+08:00", FetchedAt: stockNow.Format(time.RFC3339)}, nil
			})}
			if code == "total_overflow" {
				v.Cash = Money(math.MaxInt64)
			}
			err := h.valuePositions(t.Context(), &v, []Instrument{i})
			if code == "overflow" || code == "total_overflow" {
				require.ErrorIs(t, err, ErrPrecision)
				return
			}
			require.NoError(t, err)
			if code == "prior_date" {
				require.True(t, v.Complete)
				require.Equal(t, code, v.Items[0].Status)
				return
			}
			require.False(t, v.Complete)
			require.Nil(t, v.TotalAssets)
			require.Nil(t, v.PositionsValue)
			require.Zero(t, v.KnownPositionsValue)
			require.Nil(t, v.Items[0].MarketValue)
			require.Equal(t, code, v.Items[0].ErrorCode)
		})
	}
}

func TestValuationEndpointCashClosedAndErrors(t *testing.T) {
	s := valuationFixture(t, nil, nil)
	h := Handler{Store: s, Now: func() time.Time { return stockNow }, Quotes: valuationQuotes(func(context.Context, []Instrument) map[string]QuoteResult { t.Fatal("unexpected upstream"); return nil }), FX: valuationFX(func(context.Context, FXRequest) (FXQuote, error) { t.Fatal("unexpected FX"); return FXQuote{}, nil })}
	mux := http.NewServeMux()
	h.Register(mux)
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "a/valuation", 200}, {"HEAD", "a/valuation", 200}, {"GET", "missing/valuation", 404}, {"GET", "a/valuation?date=2026-01-01", 400}, {"GET", "a/valuation?", 400}, {"POST", "a/valuation", 400}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, ledgerPrefix+"/accounts/"+tc.path, nil))
		require.Equal(t, tc.status, w.Code)
		if tc.method == "HEAD" {
			require.Empty(t, w.Body.String())
		} else if tc.status == 200 {
			var v Valuation
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
			require.True(t, v.Complete)
			require.Equal(t, Money(10000), *v.TotalAssets)
			require.Empty(t, v.HistoryID)
			require.Len(t, v.LedgerRevision, 64)
			require.Empty(t, v.Items)
			require.NotNil(t, v.Items)
		}
	}
	v := Valuation{Currency: CNY, AsOf: "2026-09-06", Cash: 100, Complete: true, Items: []ValuationItem{{InstrumentID: "closed", Quantity: 0}}}
	require.NoError(t, h.valuePositions(t.Context(), &v, []Instrument{{ID: "closed", Market: "TEST"}}))
	require.Equal(t, "closed", v.Items[0].Status)
	require.Zero(t, *v.Items[0].MarketValue)
	require.Equal(t, Money(100), *v.TotalAssets)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", ledgerPrefix+"/accounts/a/valuation", nil).WithContext(ctx))
	require.Equal(t, 408, w.Code)
	historyCount(t, s, 0)
}

func TestValuationSnapshotAndNetworkOutsideTransaction(t *testing.T) {
	i := Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Synthetic", Currency: CNY}
	s := valuationFixture(t, []OpeningPosition{{InstrumentID: "i", Quantity: 1_000_000}}, []Instrument{i})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	written := make(chan error, 1)
	writer := NewStore(s.db, func() time.Time { return stockNow })
	s.now = func() time.Time {
		waiting := s.db.Stats().WaitCount
		go func() {
			_, err := writer.Write(ctx, Command{Action: CreateOperation, Key: "concurrent-deposit", Reason: "Synthetic",
				Operation: Operation{ID: "deposit", AccountID: "a", Date: "2026-01-02", Sequence: 1, Kind: Deposit, Amount: 10_000}})
			written <- err
		}()
		require.Eventually(t, func() bool { return s.db.Stats().WaitCount > waiting }, time.Second, time.Millisecond)
		return stockNow
	}
	v, held, err := s.valuationInputs(ctx, "a")
	require.NoError(t, err)
	require.NoError(t, <-written)
	require.Equal(t, Money(10000), v.Cash)
	h := Handler{Now: func() time.Time { return stockNow }, Quotes: valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
		_, err := writer.Write(ctx, Command{Action: CreateOperation, Key: "concurrent-buy", Reason: "Synthetic",
			Operation: Operation{ID: "buy", AccountID: "a", InstrumentID: "i", Date: "2026-01-02", Sequence: 2, Kind: Buy, Quantity: 1_000_000, Price: 1_000_000}})
		require.NoError(t, err)
		return validValuationQuotes(ctx, is)
	})}
	require.NoError(t, h.valuePositions(ctx, &v, held))
	require.Equal(t, Quantity(1_000_000), v.Items[0].Quantity)
	require.Equal(t, Money(11235), *v.TotalAssets)
}

func TestValuationPartialEndpointAndPrecisionFailures(t *testing.T) {
	instruments := []Instrument{
		{ID: "a", Market: "SH", Code: "600000", Name: "Synthetic A", Currency: CNY},
		{ID: "b", Market: "SZ", Code: "000001", Name: "Synthetic B", Currency: CNY},
	}
	s := valuationFixture(t, []OpeningPosition{{InstrumentID: "a", Quantity: 1_000_000}, {InstrumentID: "b", Quantity: 1_000_000}}, instruments)
	for _, scenario := range []string{"missing", "timeout", "bad_symbol", "bad_date", "currency", "sum_overflow", "conversion_overflow"} {
		t.Run(scenario, func(t *testing.T) {
			h := Handler{Store: s, Now: func() time.Time { return stockNow }, Quotes: valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, time.Until(deadline), 12*time.Second)
				rows := validValuationQuotes(ctx, is)
				switch scenario {
				case "missing":
					delete(rows, "b")
				case "timeout":
					rows["b"] = QuoteResult{ErrorCode: "quote_timeout"}
				case "bad_symbol":
					rows["b"].Quote.Symbol = "sh600000"
				case "bad_date":
					rows["b"].Quote.Date = "2026-09-07"
				case "currency":
					rows["b"].Quote.Currency = USD
				case "sum_overflow", "conversion_overflow":
					for _, row := range rows {
						row.Quote.Price = Price(math.MaxInt64)
					}
				}
				return rows
			})}
			if scenario == "sum_overflow" || scenario == "conversion_overflow" {
				v := Valuation{Currency: CNY, AsOf: "2026-09-06", Complete: true, Items: []ValuationItem{{InstrumentID: "a", Quantity: 6_000_000_000}, {InstrumentID: "b", Quantity: 6_000_000_000}}}
				is := append([]Instrument(nil), instruments...)
				if scenario == "conversion_overflow" {
					is[0].Market, is[0].Code, is[0].Currency = "HK", "00700", HKD
					h.FX = valuationFX(func(context.Context, FXRequest) (FXQuote, error) {
						return FXQuote{Base: HKD, Quote: CNY, Mode: "latest", Rate: 200_000_000, Date: "2026-09-06", Source: "Tencent", QuotedAt: "2026-09-06T15:00:00+08:00", FetchedAt: stockNow.Format(time.RFC3339)}, nil
					})
				}
				ctx, cancel := context.WithTimeout(t.Context(), valuationTimeout)
				defer cancel()
				require.ErrorIs(t, h.valuePositions(ctx, &v, is), ErrPrecision)
				require.Nil(t, v.TotalAssets)
				return
			}
			mux := http.NewServeMux()
			h.Register(mux)
			for _, method := range []string{"GET", "HEAD"} {
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(method, ledgerPrefix+"/accounts/a/valuation", nil))
				require.Equal(t, 200, w.Code)
				if method == "HEAD" {
					require.Empty(t, w.Body.String())
					continue
				}
				var v Valuation
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
				require.False(t, v.Complete)
				require.Empty(t, v.HistoryID)
				require.Len(t, v.LedgerRevision, 64)
				require.Nil(t, v.TotalAssets)
				require.Nil(t, v.PositionsValue)
				require.Equal(t, Money(1235), v.KnownPositionsValue)
				require.Equal(t, "unavailable", v.Items[1].Status)
			}
			historyCount(t, s, 0)
		})
	}
}
