package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/stretchr/testify/require"
)

func manualTradeFixture(t *testing.T, positions ...CurrentPosition) *Store {
	t.Helper()
	s := importStoreFixture(t)
	manualSourceAccount(t, s, "a")
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Synthetic", Currency: CNY}))
	putSource(t, s, "a", "baseline", "0", 100000, positions...)
	return s
}

func tradeInput(version string, kind Kind, q, p int64, fee Money) ManualTradeInput {
	quantity, price := Quantity(q*1_000_000), Price(p*1_000_000)
	return ManualTradeInput{ExpectedVersion: version, Kind: kind, Date: "2026-09-06", Quantity: &quantity, Price: &price, Fee: &fee, Note: "Synthetic", Reason: "Synthetic"}
}

func appendTrade(t *testing.T, s *Store, input ManualTradeInput) ManualTradeResult {
	t.Helper()
	out, err := s.AddManualTrade(t.Context(), "a", "i", "trade-"+input.ExpectedVersion, input)
	require.NoError(t, err)
	return out
}

func holdingView(t *testing.T, s *Store) HoldingsView {
	t.Helper()
	v, _, err := s.holdingsInputs(t.Context(), "a")
	require.NoError(t, err)
	return v
}

func TestHoldingCostsManualAndReplayOriginalCurrencyCycles(t *testing.T) {
	for _, mode := range []string{"manual", "replay"} {
		t.Run(mode, func(t *testing.T) {
			s := importStoreFixture(t)
			require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Synthetic", Currency: CNY}))
			if mode == "manual" {
				manualSourceAccount(t, s, "a")
				putSource(t, s, "a", "baseline", "0", 100000)
			} else {
				require.NoError(t, s.InitializeAccount(t.Context(), "Synthetic", Opening{AccountID: "a", Currency: CNY, Date: "2026-01-01", Cash: 100000}))
			}
			inputs := []ManualTradeInput{tradeInput("1", Buy, 10, 10, 100), tradeInput("2", Sell, 4, 20, 200), tradeInput("3", Buy, 4, 30, 300)}
			firstCycle := ""
			write := func(input ManualTradeInput) {
				if mode == "manual" {
					r := appendTrade(t, s, input)
					if firstCycle == "" {
						firstCycle = r.Transaction.CycleID
					}
					return
				}
				seq, _ := strconv.ParseInt(input.ExpectedVersion, 10, 64)
				o := Operation{ID: "op-" + input.ExpectedVersion, AccountID: "a", InstrumentID: "i", Date: input.Date, Sequence: seq, Kind: input.Kind, Fee: input.Fee}
				if input.Quantity != nil {
					o.Quantity = *input.Quantity
				}
				if input.Price != nil {
					o.Price = *input.Price
				}
				if input.Amount != nil {
					o.Amount = *input.Amount
					o.CycleID = firstCycle
				}
				if firstCycle == "" {
					firstCycle = o.ID
				}
				_, err := s.Write(t.Context(), Command{Action: CreateOperation, Key: o.ID, Operation: o, Reason: "Synthetic", Note: input.Note})
				require.NoError(t, err)
			}
			for _, input := range inputs {
				write(input)
			}
			v := holdingView(t, s)
			require.Equal(t, "16.000000", v.Items[0].HoldingCost.String()) // (101+123)/14, NOT remaining-cost average.
			require.Equal(t, "14.600000", v.Items[0].DilutedCost.String())
			require.Equal(t, "known", v.Items[0].CostStatus)
			if mode == "replay" {
				book, err := s.State(t.Context())
				require.NoError(t, err)
				p, err := book.Accounts["a"].Cycles[firstCycle].MovingAverage()
				require.NoError(t, err)
				require.Equal(t, "18.360000", p.String())
			}
			dividend := ManualTradeInput{ExpectedVersion: "4", Kind: Dividend, Date: "2026-09-06", Amount: replayMoney(20000), Reason: "Synthetic"}
			write(dividend)
			v = holdingView(t, s)
			require.Equal(t, "-5.400000", v.Items[0].DilutedCost.String())
			write(tradeInput("5", Sell, 10, 8, 0))
			v = holdingView(t, s)
			require.Equal(t, "closed", v.Items[0].CostStatus)
			require.Nil(t, v.Items[0].HoldingCost)
			require.Nil(t, v.Items[0].DilutedCost)
			write(tradeInput("6", Buy, 2, 7, 0))
			v = holdingView(t, s)
			require.NotEqual(t, firstCycle, v.Items[0].CycleID)
			require.Equal(t, "7.000000", v.Items[0].HoldingCost.String())
			require.Equal(t, "7.000000", v.Items[0].DilutedCost.String())
			require.Equal(t, "1120.00", v.Cash.String())
			rows, err := s.HoldingTransactions(t.Context(), "a", "i", 100, "")
			require.NoError(t, err)
			require.Len(t, rows.Items, 6)
			require.Equal(t, v.Items[0].CycleID, rows.Items[0].CycleID)
			require.Equal(t, firstCycle, rows.Items[1].CycleID)
			require.Equal(t, Dividend, rows.Items[2].Kind)
			require.Nil(t, rows.Items[2].Quantity)
			require.Equal(t, "101.00", rows.Items[5].Amount.String())
			require.Equal(t, "1.00", rows.Items[5].Fee.String())
		})
	}
}

func TestManualUnknownBaselineEditsDeletionAndReopen(t *testing.T) {
	s := manualTradeFixture(t, CurrentPosition{"i", 5_000_000})
	before := holdingView(t, s)
	require.Equal(t, "unknown", before.Items[0].CostStatus)
	appendTrade(t, s, tradeInput("1", Buy, 1, 10, 0))
	v := holdingView(t, s)
	require.Nil(t, v.Items[0].HoldingCost)
	require.Nil(t, v.Items[0].DilutedCost)
	require.Equal(t, before.Items[0].CycleID, v.Items[0].CycleID)
	appendTrade(t, s, tradeInput("2", Sell, 6, 10, 0))
	appendTrade(t, s, tradeInput("3", Buy, 2, 20, 0))
	v = holdingView(t, s)
	require.Equal(t, "known", v.Items[0].CostStatus)
	knownCycle := v.Items[0].CycleID
	putSource(t, s, "a", "cash-only", "4", 90000, CurrentPosition{"i", 2_000_000})
	v = holdingView(t, s)
	require.Equal(t, knownCycle, v.Items[0].CycleID)
	require.Equal(t, "20.000000", v.Items[0].HoldingCost.String())
	putSource(t, s, "a", "quantity-edit", "5", 90000, CurrentPosition{"i", 3_000_000})
	v = holdingView(t, s)
	require.Equal(t, "unknown", v.Items[0].CostStatus)
	require.Equal(t, knownCycle, v.Items[0].CycleID, "a positive quantity correction does not invent a liquidation")
	putSource(t, s, "a", "delete-position", "6", 90000)
	require.Empty(t, holdingView(t, s).Items)
	rows, err := s.HoldingTransactions(t.Context(), "a", "i", 20, "")
	require.NoError(t, err)
	require.Len(t, rows.Items, 3)
	appendTrade(t, s, tradeInput("7", Buy, 1, 30, 0))
	v = holdingView(t, s)
	require.Equal(t, "30.000000", v.Items[0].HoldingCost.String())
}

func TestManualChronologicalBackfillAndCashBaselineFence(t *testing.T) {
	s := importStoreFixture(t)
	manualSourceAccount(t, s, "a")
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Synthetic", Currency: CNY}))
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "other", Market: "SH", Code: "600001", Name: "Other", Currency: CNY}))
	clock := s.now
	s.now = func() time.Time { return time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC) }
	putSource(t, s, "a", "baseline", "0", 100000)
	s.now = clock
	input := tradeInput("1", Buy, 2, 10, 0)
	input.Date = "2026-08-31"
	_, err := s.AddManualTrade(t.Context(), "a", "i", "early", input)
	require.ErrorIs(t, err, ErrUnsafeTradeDate)
	input.Date = "2026-09-02"
	first := appendTrade(t, s, input)
	input = tradeInput("2", Sell, 2, 10, 0)
	input.Date = "2026-09-04"
	appendTrade(t, s, input)
	input = tradeInput("3", Buy, 1, 20, 0)
	input.Date = "2026-09-04"
	reopened := appendTrade(t, s, input)
	require.NotEqual(t, first.Transaction.CycleID, reopened.Transaction.CycleID)
	input = tradeInput("4", Buy, 1, 5, 0)
	input.Date = "2026-09-03"
	for _, id := range []string{"i", "other"} {
		_, err = s.AddManualTrade(t.Context(), "a", id, "out-of-order", input)
		require.ErrorIs(t, err, ErrUnsafeTradeDate)
	}
	putSource(t, s, "a", "new-cash-baseline", "4", 10000, CurrentPosition{"i", 1_000_000})
	input.ExpectedVersion = "5"
	input.Date = "2026-09-05"
	_, err = s.AddManualTrade(t.Context(), "a", "i", "before-cash", input)
	require.ErrorIs(t, err, ErrUnsafeTradeDate)
	input.Date = "2026-09-07"
	_, err = s.AddManualTrade(t.Context(), "a", "i", "future", input)
	require.ErrorIs(t, err, ErrOperation)
}

func TestHoldingsOriginalCurrencyFXMissingMarksAndReadFence(t *testing.T) {
	s := manualTradeFixture(t)
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "hk", Market: "HK", Code: "00700", Name: "Synthetic HK", Currency: HKD}))
	input := tradeInput("1", Buy, 10, 10, 100)
	input.FX = &fxJSON{Rate: 80_000_000, Date: "2026-09-06", Source: "Synthetic", FetchedAt: "2026-09-06T00:00:00Z"}
	_, err := s.AddManualTrade(t.Context(), "a", "hk", "foreign", input)
	require.NoError(t, err)
	v, is, err := s.holdingsInputs(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, "10.100000", v.Items[0].HoldingCost.String())
	require.Equal(t, "919.20", v.Cash.String())
	count := auditCount(t, s)
	h := Handler{Store: s, Now: s.now, Quotes: valuationQuotes(func(ctx context.Context, instruments []Instrument) map[string]QuoteResult {
		// A concurrent edit during quote I/O must not mix cost/quantity/cash bases.
		putSource(t, s, "a", "during-quote", "2", 10000, CurrentPosition{"hk", 20_000_000})
		return validValuationQuotes(ctx, instruments)
	}), FX: valuationFX(func(ctx context.Context, r FXRequest) (FXQuote, error) {
		_, ok := ctx.Deadline()
		require.True(t, ok)
		return FXQuote{Base: HKD, Quote: CNY, Mode: "latest", Rate: 90_000_000, Date: "2026-09-05", Source: "Synthetic", QuotedAt: "2026-09-05T00:00:00Z", FetchedAt: "2026-09-06T00:00:00Z"}, nil
	})}
	require.NoError(t, h.valueHoldings(t.Context(), &v, is))
	require.Equal(t, "2", *v.ManualVersion)
	require.Equal(t, "10.000000", v.Items[0].Quantity.String())
	require.Equal(t, "123.46", v.Items[0].MarketValue.String())
	require.Equal(t, "111.11", v.Items[0].AccountMarketValue.String())
	require.Equal(t, "1030.31", v.TotalAssets.String())
	require.Equal(t, "10.78", *v.Items[0].Weight)
	require.Equal(t, "prior_date", v.Items[0].QuoteStatus)
	require.Equal(t, count+1, auditCount(t, s), "only the injected PUT writes an audit")
	h.Quotes = valuationQuotes(validValuationQuotes)
	h.FX = nil
	v, is, err = s.holdingsInputs(t.Context(), "a")
	require.NoError(t, err)
	require.NoError(t, h.valueHoldings(t.Context(), &v, is))
	require.False(t, v.Complete)
	require.Nil(t, v.TotalAssets)
	require.Nil(t, v.Items[0].Weight)
	require.Nil(t, v.Items[0].AccountMarketValue)
	require.NotNil(t, v.Items[0].MarketValue)
	putSource(t, s, "a", "two-marks", "3", 10000, CurrentPosition{"hk", 1_000_000}, CurrentPosition{"i", 1_000_000})
	v, is, err = s.holdingsInputs(t.Context(), "a")
	require.NoError(t, err)
	h.Quotes = valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
		r := validValuationQuotes(ctx, is)
		delete(r, "hk")
		return r
	})
	require.NoError(t, h.valueHoldings(t.Context(), &v, is))
	require.Nil(t, v.TotalAssets)
	require.Nil(t, v.Items[0].Weight)
	require.Nil(t, v.Items[1].Weight)
	require.NotNil(t, v.Items[1].AccountMarketValue)
}

func TestManualTradeReceiptsCASAtomicityAndHistoryUntouched(t *testing.T) {
	s := manualTradeFixture(t)
	input := tradeInput("1", Buy, 1, 10, 100)
	first := appendTrade(t, s, input)
	appendTrade(t, s, tradeInput("2", Sell, 1, 20, 0))
	count := auditCount(t, s)
	retried, err := s.AddManualTrade(t.Context(), "a", "i", "trade-1", input)
	require.NoError(t, err)
	require.Equal(t, first, retried)
	require.Equal(t, count, auditCount(t, s))
	_, err = s.AddManualTrade(t.Context(), "a", "i", "stale", input)
	require.ErrorIs(t, err, ErrVersion)
	input.ExpectedVersion = "3"
	_, err = s.AddManualTrade(t.Context(), "a", "i", "trade-1", input)
	require.ErrorIs(t, err, ErrIdempotency)
	_, err = s.db.ExecContext(t.Context(), `CREATE TRIGGER test_manual_failure BEFORE INSERT ON idempotency_receipts WHEN NEW.key='rollback' BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
	require.NoError(t, err)
	before := holdingView(t, s)
	_, err = s.AddManualTrade(t.Context(), "a", "i", "rollback", input)
	require.Error(t, err)
	require.Equal(t, before, holdingView(t, s))
	require.Equal(t, count, auditCount(t, s))
	var records, ops, trades int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&records))
	require.Zero(t, records)
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM operations`).Scan(&ops))
	require.Zero(t, ops)
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM manual_trades`).Scan(&trades))
	require.Equal(t, 2, trades)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for n := range 2 {
		wg.Go(func() {
			_, err := s.AddManualTrade(t.Context(), "a", "i", fmt.Sprintf("parallel-%d", n), input)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrVersion)
			conflicts++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflicts)
	for _, q := range []string{`DELETE FROM manual_trades`, `UPDATE manual_trades SET business_date='2020-01-01'`, `UPDATE current_holdings SET payload='{}'`} {
		_, err := s.db.ExecContext(t.Context(), q)
		require.Error(t, err)
	}
}

func TestHoldingTransactionsPaginationIsolationAndReplayAttribution(t *testing.T) {
	s := manualTradeFixture(t)
	manualSourceAccount(t, s, "b")
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "other", Market: "SH", Code: "600001", Name: "Synthetic", Currency: CNY}))
	for n := 1; n <= 4; n++ {
		appendTrade(t, s, tradeInput(strconv.Itoa(n), Buy, 1, int64(n), 0))
	}
	page, err := s.HoldingTransactions(t.Context(), "a", "i", 2, "")
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.NotEmpty(t, page.NextCursor)
	next, err := s.HoldingTransactions(t.Context(), "a", "i", 2, page.NextCursor)
	require.NoError(t, err)
	require.Len(t, next.Items, 2)
	require.Empty(t, next.NextCursor)
	require.Equal(t, "2.00", next.Items[0].Amount.String())
	require.Equal(t, "1.00", next.Items[1].Amount.String())
	for _, pair := range [][2]string{{"b", "i"}, {"a", "other"}} {
		_, err := s.HoldingTransactions(t.Context(), pair[0], pair[1], 2, page.NextCursor)
		require.ErrorIs(t, err, ErrQuery)
	}
	_, err = s.HoldingTransactions(t.Context(), "missing", "i", 20, "")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.HoldingTransactions(t.Context(), "a", "missing", 20, "")
	require.ErrorIs(t, err, ErrNotFound)
	// Replay accepts out-of-submission-order dates and explicit old-cycle dividends.
	require.NoError(t, s.InitializeAccount(t.Context(), "Replay", Opening{AccountID: "r", Currency: CNY, Date: "2026-01-01", Cash: 100000}))
	write := func(o Operation) {
		o.AccountID = "r"
		o.InstrumentID = "i"
		_, err := s.Write(t.Context(), Command{Action: CreateOperation, Key: o.ID, Operation: o, Reason: "Synthetic"})
		require.NoError(t, err)
	}
	write(Operation{ID: "old", Kind: DepositBuy, Date: "2026-09-01", Sequence: 1, Quantity: 2_000_000, Price: 10_000_000, Amount: 3000})
	write(Operation{ID: "close", Kind: SellWithdraw, Date: "2026-09-02", Sequence: 1, Quantity: 2_000_000, Price: 15_000_000, Amount: 1000})
	write(Operation{ID: "new", Kind: Buy, Date: "2026-09-04", Sequence: 1, Quantity: 1_000_000, Price: 8_000_000})
	write(Operation{ID: "old-dividend", Kind: Dividend, Date: "2026-09-05", Sequence: 1, Amount: 500, CycleID: "old"})
	write(Operation{ID: "backfill-dividend", Kind: Dividend, Date: "2026-09-03", Sequence: 1, Amount: 100, CycleID: "old"})
	_, err = s.Write(t.Context(), Command{Action: VoidOperation, Key: "void-backfill", Operation: Operation{ID: "backfill-dividend"}, ExpectedVersion: 1, Reason: "Synthetic"})
	require.NoError(t, err)
	rows, err := s.HoldingTransactions(t.Context(), "r", "i", 100, "")
	require.NoError(t, err)
	require.Len(t, rows.Items, 4)
	require.Equal(t, "old", rows.Items[0].CycleID)
	require.Equal(t, "new", rows.Items[1].CycleID)
	require.Equal(t, Sell, rows.Items[2].Kind)
	require.Equal(t, Buy, rows.Items[3].Kind)
	require.Equal(t, "20.00", rows.Items[3].Amount.String())
	require.Equal(t, "old", rows.Items[3].OperationID)
	v, _, err := s.holdingsInputs(t.Context(), "r")
	require.NoError(t, err)
	require.Equal(t, "8.000000", v.Items[0].DilutedCost.String())
}

func TestHoldingsHTTPContractAndStrictWrites(t *testing.T) {
	f := newHTTPFixture(t)
	manualSourceAccount(t, f.store, "a")
	f.instrument(t, "i")
	f.mux = http.NewServeMux()
	Handler{Store: f.store, Now: f.store.now}.Register(f.mux)
	v := f.get(t, "/accounts/a/holdings")
	require.Equal(t, false, v["configured"])
	require.Equal(t, "0", v["manual_version"])
	require.Nil(t, v["cash"])
	require.Equal(t, false, v["complete"])
	require.Empty(t, httpItems(t, v))
	require.NotEmpty(t, v["revision"])
	path := "/accounts/a/holdings/i/transactions"
	payload := `{"expected_version":"1","kind":"buy","date":"2026-09-06","quantity":"1","price":"10","note":"","reason":"Synthetic"}`
	f.request(t, "POST", path, "not-configured", payload, 422)
	zeroVersion := tradeInput("0", Buy, 1, 10, 0)
	_, err := f.store.AddManualTrade(t.Context(), "a", "i", "zero-version", zeroVersion)
	require.ErrorIs(t, err, ErrManualHoldingsRequired)
	putSource(t, f.store, "a", "baseline", "0", 100000)
	_, err = f.store.AddManualTrade(t.Context(), "a", "i", "zero-version", zeroVersion)
	require.ErrorIs(t, err, ErrVersion)
	for _, extra := range []string{`,"amount":"0"`, `,"amount":null`, `,"currency":"EUR"`, `,"cycle_id":"forged"`, `,"quantity":"2"`, `,"fx":{"rate":"1","date":"2026-09-06","source":"Synthetic","fetched_at":"2026-09-06T00:00:00Z","extra":true}`} {
		f.request(t, "POST", path, "invalid", payload[:len(payload)-1]+extra+`}`, 400)
	}
	for _, body := range []string{
		`{"expected_version":"1","kind":"buy","date":"2026-09-06","quantity":"1.0000001","price":"1","reason":"Synthetic"}`,
		`{"expected_version":"1","kind":"dividend","date":"2026-09-06","amount":"1","quantity":null,"reason":"Synthetic"}`,
		`{"expected_version":"1","kind":"buy","date":"2026-09-06","quantity":"1","price":"1","fee":"0.001","reason":"Synthetic"}`,
		`{"expected_version":"1","kind":"buy","date":"2026-09-06","quantity":"1","price":"1","fx":{"rate":"1","date":"2026-09-06","source":"Synthetic","fetched_at":"2026-09-06T00:00:00Z"},"reason":"Synthetic"}`,
	} {
		f.request(t, "POST", path, "invalid", body, 400)
	}
	first := f.request(t, "POST", path, "valid", payload, 201).Body.String()
	require.Equal(t, first, f.request(t, "POST", path, "valid", payload, 201).Body.String())
	result := httpObject(t, first)
	require.Equal(t, "2", result["version"])
	require.Equal(t, "i", result["instrument_id"])
	f.request(t, "GET", path+"?limit=0", "", "", 400)
	f.request(t, "DELETE", path, "delete", "{}", 405)
	f.request(t, "GET", "/accounts/a/holdings?", "", "", 400)
	f.request(t, "HEAD", "/accounts/a/holdings", "", "", 200)
	f.account(t, "replay", "CNY", "100.00", nil)
	r := f.request(t, "POST", "/accounts/replay/holdings/i/transactions", "replay", payload, 422)
	require.Contains(t, r.Body.String(), "manual_holdings_required")
	f.request(t, "PUT", "/accounts/a/current-holdings", "forged", `{"expected_version":"2","cash":"100","positions":[],"trades":{"cycles":{}}}`, 400)
}

func TestManualTradeValidationAndIntegrity(t *testing.T) {
	s := manualTradeFixture(t)
	for _, tc := range []struct {
		name  string
		input ManualTradeInput
		want  error
	}{
		{"cash", tradeInput("1", Buy, 10000, 100, 0), ErrInsufficientCash},
		{"stock", tradeInput("1", Sell, 1, 10, 0), ErrInsufficientStock},
		{"fee", tradeInput("1", Sell, 1, 10, 1001), ErrOperation},
		{"negative-fee", tradeInput("1", Buy, 1, 10, -1), ErrOperation},
	} {
		_, err := s.AddManualTrade(t.Context(), "a", "i", tc.name, tc.input)
		require.ErrorIs(t, err, tc.want)
	}
	appendTrade(t, s, tradeInput("1", Buy, 1, 10, 0))
	_, err := s.db.ExecContext(t.Context(), `DROP TRIGGER manual_trades_no_update`)
	require.NoError(t, err)
	_, err = s.db.ExecContext(t.Context(), `UPDATE manual_trades SET payload=json_set(payload,'$.transaction.amount','999.00')`)
	require.NoError(t, err)
	_, err = s.HoldingTransactions(t.Context(), "a", "i", 20, "")
	require.ErrorIs(t, err, ErrCorrupt)
	_, err = s.AddManualTrade(t.Context(), "a", "i", "trade-1", tradeInput("1", Buy, 1, 10, 0))
	require.ErrorIs(t, err, ErrCorrupt)
}

func TestManualTradesMigrationRetainsOldSnapshotsReceiptsAndRecords(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(t.Context(), root, "ledger")
	require.NoError(t, err)
	script, err := migrations.ReadFile("migrations/001_init.sql")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(t.Context(), fstest.MapFS{"001_init.sql": &fstest.MapFile{Data: script}}))
	s := NewStore(db, func() time.Time { return stockNow })
	manualSourceAccount(t, s, "a")
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Synthetic", Currency: CNY}))
	require.NoError(t, s.InitializeAccount(t.Context(), "Replay", Opening{AccountID: "r", Currency: CNY, Date: "2026-01-01", Cash: 1000}))
	_, err = s.Write(t.Context(), Command{Action: CreateOperation, Key: "old-operation", Reason: "Synthetic", Operation: Operation{ID: "old-op", AccountID: "r", Date: "2026-01-02", Sequence: 1, Kind: Deposit, Amount: 100}})
	require.NoError(t, err)
	old := HoldingsSnapshot{Version: "1", SavedAt: stockNow.UTC().Format(time.RFC3339Nano), Cash: 10000, Positions: []CurrentPosition{{"i", 1_000_000}}}
	var expected CurrentHoldings
	require.NoError(t, db.WithTx(t.Context(), func(tx *sql.Tx) error {
		audit, err := appendAudit(t.Context(), tx, "legacy-put", "create", "current_holdings", "a", "a", 1, old.SavedAt, "human", nil, old, map[string]string{"reason": "Synthetic"})
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(old)
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO current_holdings(account_id,version,audit_id,payload) VALUES('a',1,?,?)`, audit, string(payload)); err != nil {
			return err
		}
		expected = CurrentHoldings{AccountID: "a", AuditID: strconv.FormatInt(audit, 10), Snapshot: &old}
		raw, _ := json.Marshal(struct {
			AccountID string
			Input     CurrentHoldingsInput
		}{"a", CurrentHoldingsInput{ExpectedVersion: "0", Cash: &old.Cash, Positions: old.Positions}})
		response, _ := json.Marshal(expected)
		return saveReceipt(t.Context(), tx, "legacy-put", "current_holdings", string(raw), string(response), audit)
	}))
	var audits, records, receipts string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT group_concat(after_json,'|') FROM audit_log ORDER BY id`).Scan(&audits))
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT group_concat(payload,'|') FROM account_records ORDER BY sequence`).Scan(&records))
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT group_concat(response_json,'|') FROM idempotency_receipts ORDER BY key`).Scan(&receipts))
	require.NoError(t, db.Close())
	db, err = Open(t.Context(), root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	s = NewStore(db, func() time.Time { return stockNow })
	for _, tc := range []struct{ query, want string }{
		{`SELECT group_concat(after_json,'|') FROM audit_log ORDER BY id`, audits},
		{`SELECT group_concat(payload,'|') FROM account_records ORDER BY sequence`, records},
		{`SELECT group_concat(response_json,'|') FROM idempotency_receipts ORDER BY key`, receipts},
	} {
		var got string
		require.NoError(t, db.QueryRowContext(t.Context(), tc.query).Scan(&got))
		require.Equal(t, tc.want, got)
	}
	current, err := s.CurrentHoldings(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, expected, current)
	require.Nil(t, current.Snapshot.Trades)
	require.Equal(t, expected, putSource(t, s, "a", "legacy-put", "0", 10000, CurrentPosition{"i", 1_000_000}))
	appendTrade(t, s, tradeInput("1", Buy, 1, 10, 0))
	require.Equal(t, "unknown", holdingView(t, s).Items[0].CostStatus)
	var afterRecords string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT group_concat(payload,'|') FROM account_records ORDER BY sequence`).Scan(&afterRecords))
	require.Equal(t, records, afterRecords)
	var check string
	require.NoError(t, db.QueryRowContext(t.Context(), `PRAGMA integrity_check`).Scan(&check))
	require.Equal(t, "ok", check)
}

func TestHoldingReplayForeignCostsAndUnknownOpeningNotReinterpreted(t *testing.T) {
	s := importStoreFixture(t)
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "hk", Market: "HK", Code: "00700", Name: "Synthetic", Currency: HKD}))
	for _, id := range []string{"a", "opening"} {
		o := Opening{AccountID: id, Currency: CNY, Date: "2026-01-01", Cash: 100000}
		if id == "opening" {
			o.Positions = []OpeningPosition{{InstrumentID: "hk", Quantity: 1_000_000, Cost: replayMoney(800), DilutedBasis: replayMoney(700)}}
		}
		require.NoError(t, s.InitializeAccount(t.Context(), "Synthetic", o))
	}
	for n, rate := range []Rate{80_000_000, 90_000_000} {
		o := Operation{ID: fmt.Sprintf("foreign-%d", n), AccountID: "a", InstrumentID: "hk", Date: "2026-09-06", Sequence: int64(n + 1), Kind: Buy, Quantity: 1_000_000, Price: 10_000_000, Fee: replayMoney(100), FX: &FXSnapshot{Rate: rate, Date: "2026-09-06", Source: "Synthetic", FetchedAt: "2026-09-06T00:00:00Z"}}
		_, err := s.Write(t.Context(), Command{Action: CreateOperation, Key: o.ID, Operation: o, Reason: "Synthetic"})
		require.NoError(t, err)
	}
	v := holdingView(t, s)
	require.Equal(t, "11.000000", v.Items[0].HoldingCost.String())
	require.Equal(t, "11.000000", v.Items[0].DilutedCost.String())
	book, err := s.State(t.Context())
	require.NoError(t, err)
	average, err := book.Accounts["a"].Cycles["foreign-0"].MovingAverage()
	require.NoError(t, err)
	require.Equal(t, "9.350000", average.String())
	opening, _, err := s.holdingsInputs(t.Context(), "opening")
	require.NoError(t, err)
	require.Equal(t, "unknown", opening.Items[0].CostStatus)
	require.Nil(t, opening.Items[0].HoldingCost)
	require.Nil(t, opening.Items[0].DilutedCost)
	require.Equal(t, Money(800), *book.Accounts["opening"].Cycles[OpeningCycleID("opening", "hk")].RemainingCost)
}

func TestHoldingsZeroDenominatorClosedAndManualFXValidation(t *testing.T) {
	s := manualTradeFixture(t)
	putSource(t, s, "a", "zero-cash", "1", 0)
	v, is, err := s.holdingsInputs(t.Context(), "a")
	require.NoError(t, err)
	h := Handler{Now: s.now, Quotes: valuationQuotes(func(context.Context, []Instrument) map[string]QuoteResult {
		t.Fatal("no network for cash-only or closed holdings")
		return nil
	})}
	require.NoError(t, h.valueHoldings(t.Context(), &v, is))
	require.True(t, v.Complete)
	require.Equal(t, "0.00", v.TotalAssets.String())
	putSource(t, s, "a", "fund", "2", 1000)
	appendTrade(t, s, tradeInput("3", Buy, 1, 10, 0))
	appendTrade(t, s, tradeInput("4", Sell, 1, 10, 1000))
	v, is, err = s.holdingsInputs(t.Context(), "a")
	require.NoError(t, err)
	require.NoError(t, h.valueHoldings(t.Context(), &v, is))
	require.True(t, v.Complete)
	require.Nil(t, v.Items[0].Weight)
	require.Equal(t, "closed", v.Items[0].QuoteStatus)
	require.Equal(t, "0.00", v.Items[0].MarketValue.String())
	require.Nil(t, v.Items[0].Price)
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "hk", Market: "HK", Code: "00700", Name: "Synthetic", Currency: HKD}))
	input := tradeInput("5", Buy, 1, 1, 0)
	_, err = s.AddManualTrade(t.Context(), "a", "hk", "no-fx", input)
	require.ErrorIs(t, err, ErrOperation)
	for _, fx := range []*fxJSON{
		{Rate: 0, Date: "2026-09-06", Source: "Synthetic", FetchedAt: "2026-09-06T00:00:00Z"},
		{Rate: 80_000_000, Date: "2026-09-07", Source: "Synthetic", FetchedAt: "2026-09-06T00:00:00Z"},
		{Rate: 80_000_000, Date: "2026-09-06", Source: "Synthetic", FetchedAt: "2027-01-01T00:00:00Z"},
	} {
		input.FX = fx
		_, err = s.AddManualTrade(t.Context(), "a", "hk", "invalid-fx", input)
		require.ErrorIs(t, err, ErrOperation)
	}
}
