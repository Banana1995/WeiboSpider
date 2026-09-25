package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func stockFixture(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	db, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	now := time.Date(2026, 9, 6, 5, 0, 0, 0, time.UTC)
	s := NewStore(db, func() time.Time { return now })
	manualSourceAccount(t, s, "a")
	return s, &now
}
func stockWrite(t *testing.T, s *Store, key string, c StockCommand) StockWriteResult {
	t.Helper()
	raw, err := s.WriteStock(t.Context(), "a", key, c)
	require.NoError(t, err)
	var out StockWriteResult
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}
func buyCommand(id, version, date string, q, p int64) StockCommand {
	return StockCommand{Action: "create", ExpectedVersion: version, ID: id, Entry: &StockInput{InstrumentID: "i", Kind: "buy", Date: date, Quantity: Quantity(q * 1000000), Price: Price(p * 1000000), FX: 100000000}}
}
func stockSecurity(currency Currency) *instrumentJSON {
	if currency == HKD {
		return &instrumentJSON{"i", "HK", "00700", "Synthetic", HKD}
	}
	return &instrumentJSON{"i", "SH", "600036", "Synthetic", CNY}
}
func readBook(t *testing.T, s *Store) StockBook {
	t.Helper()
	b, err := (Handler{Store: s}).stockBookView(t.Context(), "a")
	require.NoError(t, err)
	return b
}

func TestStockBookCashAnchorTradingCorrectionsAndReceipts(t *testing.T) {
	s, _ := stockFixture(t)
	first := buyCommand("first", "0", "2026-01-01", 1000, 10)
	first.Security = stockSecurity(CNY)
	raw, err := s.WriteStock(t.Context(), "a", "first-key", first)
	require.NoError(t, err)
	b := readBook(t, s)
	require.Equal(t, Money(0), b.Cash)
	require.Equal(t, Quantity(1000000000), b.Items[0].Quantity)
	require.Equal(t, Price(10000000), *b.Items[0].Metrics.Cost)
	cash := Money(500000)
	stockWrite(t, s, "cash", StockCommand{Action: "cash", ExpectedVersion: "1", Cash: &cash})
	buy := buyCommand("add", "2", "2026-09-06", 100, 12)
	buy.Entry.Fee = 100
	stockWrite(t, s, "add", buy)
	sell := buyCommand("sell", "3", "2026-09-06", 100, 13)
	sell.Entry.Kind = "sell"
	sell.Entry.Fee = 100
	stockWrite(t, s, "sell", sell)
	b = readBook(t, s)
	require.Equal(t, Money(509800), b.Cash)
	require.Equal(t, Quantity(1000000000), b.Items[0].Quantity)
	count := auditCount(t, s)
	again, err := s.WriteStock(t.Context(), "a", "first-key", first)
	require.NoError(t, err)
	require.Equal(t, string(raw), string(again))
	require.Equal(t, count, auditCount(t, s))
	_, err = s.WriteStock(t.Context(), "a", "old-version", buy)
	require.ErrorIs(t, err, ErrVersion)
	// Old buys change the stock's cost, not the already-entered actual cash balance.
	first.Action = "replace"
	first.Security = nil
	first.ExpectedVersion = "4"
	first.Entry.Price = 11000000
	stockWrite(t, s, "fix-old", first)
	require.Equal(t, Money(509800), readBook(t, s).Cash)
	// Correcting a post-anchor cash movement replays cash from the anchor.
	sell.Action = "replace"
	sell.ExpectedVersion = "5"
	sell.Entry.Price = 14000000
	stockWrite(t, s, "fix-sell", sell)
	require.Equal(t, Money(519800), readBook(t, s).Cash)
	before := auditCount(t, s)
	tooMuch := buyCommand("too-much", "6", "2026-09-06", 1000, 100)
	_, err = s.WriteStock(t.Context(), "a", "too-much", tooMuch)
	require.ErrorIs(t, err, ErrStockCash)
	require.Equal(t, before, auditCount(t, s))
	tooMuch.Entry.Kind = "sell"
	tooMuch.Entry.Quantity = 2000000000
	_, err = s.WriteStock(t.Context(), "a", "too-many-shares", tooMuch)
	require.ErrorIs(t, err, ErrStockQuantity)
	stockWrite(t, s, "delete-sell", StockCommand{Action: "void", ExpectedVersion: "6", ID: "sell"})
	require.Equal(t, Money(379900), readBook(t, s).Cash)
	var records int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&records))
	require.Zero(t, records)
	_, err = s.PutCurrentHoldings(t.Context(), "a", "legacy", CurrentHoldingsInput{ExpectedVersion: "7", Cash: &cash, Positions: []CurrentPosition{}})
	require.ErrorIs(t, err, ErrStockManaged)
	_, err = s.DeleteAccount(t.Context(), "a", "delete-account")
	require.NoError(t, err)
	_, err = s.WriteStock(t.Context(), "a", "first-key", first)
	require.Error(t, err)
}

func TestStockBookExposesTotalAssetsPositionsValueAndWeight(t *testing.T) {
	s, _ := stockFixture(t)
	buy := buyCommand("b", "0", "2026-01-01", 100, 10)
	buy.Security = stockSecurity(CNY)
	stockWrite(t, s, "b", buy)
	cash := Money(10000)
	stockWrite(t, s, "cash", StockCommand{Action: "cash", ExpectedVersion: "1", Cash: &cash})
	h := Handler{Store: s, Quotes: valuationQuotes(func(_ context.Context, instruments []Instrument) map[string]QuoteResult {
		rows := map[string]QuoteResult{}
		for _, i := range instruments {
			symbol, _ := quoteSymbol(i)
			rows[i.ID] = QuoteResult{Quote: &Quote{Symbol: symbol, Price: 12000000, Currency: i.Currency, Source: "Tencent", Date: "2026-09-06", QuotedAt: "2026-09-06T15:00:00+08:00", FetchedAt: "2026-09-06T07:00:00Z"}}
		}
		return rows
	})}
	book, err := h.stockBookView(t.Context(), "a")
	require.NoError(t, err)
	require.True(t, book.Complete)
	require.Equal(t, Money(120000), *book.PositionsValue)
	require.Equal(t, Money(130000), *book.TotalAssets)
	require.Len(t, book.Items, 1)
	require.Equal(t, "92.31", *book.Items[0].Weight)
}

func TestStockCostFormulaCyclesAndNegativeDilution(t *testing.T) {
	j := StockJournal{Openings: []CurrentPosition{}}
	entries := []StockEntry{
		{ID: "b1", InstrumentID: "i", Kind: "buy", Date: "2026-01-01", Sequence: 1, Quantity: 10000000, Amount: 10100},
		{ID: "s1", InstrumentID: "i", Kind: "sell", Date: "2026-01-02", Sequence: 2, Quantity: 4000000, Amount: 7800},
		{ID: "b2", InstrumentID: "i", Kind: "buy", Date: "2026-01-03", Sequence: 3, Quantity: 4000000, Amount: 12300},
	}
	p, err := replayStocks(j, entries, "2026-09-06")
	require.NoError(t, err)
	market := Money(30000)
	m, err := metricsFor(p["i"], &market)
	require.NoError(t, err)
	require.Equal(t, Price(16000000), *m.Cost)
	require.Equal(t, Price(14600000), *m.DilutedCost)
	require.Equal(t, Money(15400), *m.TotalProfit)
	require.Equal(t, "68.75", *m.TotalRate)
	entries = append(entries, StockEntry{ID: "d", InstrumentID: "i", Kind: "dividend", Date: "2026-01-04", Sequence: 4, Amount: 20000})
	p, err = replayStocks(j, entries, "2026-09-06")
	require.NoError(t, err)
	m, err = metricsFor(p["i"], &market)
	require.NoError(t, err)
	require.Equal(t, Price(-5400000), *m.DilutedCost)
	entries = append(entries, StockEntry{ID: "close", InstrumentID: "i", Kind: "sell", Date: "2026-01-05", Sequence: 5, Quantity: 10000000, Amount: 30000})
	p, err = replayStocks(j, entries, "2026-09-06")
	require.NoError(t, err)
	m, err = metricsFor(p["i"], nil)
	require.NoError(t, err)
	require.Nil(t, m.Cost)
	require.Equal(t, Money(35400), *m.TotalProfit)
	entries = append(entries, StockEntry{ID: "reopen", InstrumentID: "i", Kind: "buy", Date: "2026-01-06", Sequence: 6, Quantity: 1000000, Amount: 5000})
	p, err = replayStocks(j, entries, "2026-09-06")
	require.NoError(t, err)
	m, err = metricsFor(p["i"], nil)
	require.NoError(t, err)
	require.Equal(t, Price(50000000), *m.DilutedCost)
}

type stockDivFixture struct {
	events []DividendEvent
	calls  int
	err    error
}

func (p *stockDivFixture) FetchDividends(context.Context, Instrument) (DividendCache, error) {
	p.calls++
	if p.err != nil {
		return DividendCache{}, p.err
	}
	return DividendCache{Events: p.events, FX: map[string]FXQuote{}, Blocks: []string{}}, nil
}
func testDividend(market, code, record, ex, pay string, price Price) DividendEvent {
	currency := CNY
	if market == "HK" {
		currency = HKD
	}
	return DividendEvent{dividendID(market, code, ex), market, code, currency, price, record, ex, pay, "Eastmoney", "Synthetic cash dividend"}
}

func TestAutomaticDividendEligibilityPaymentCorrectionAndSuppression(t *testing.T) {
	s, now := stockFixture(t)
	*now = time.Date(2026, 9, 1, 5, 0, 0, 0, time.UTC)
	buy := buyCommand("initial", "0", "2026-09-01", 100, 10)
	buy.Security = stockSecurity(CNY)
	stockWrite(t, s, "buy", buy)
	cash := Money(100000)
	stockWrite(t, s, "cash", StockCommand{Action: "cash", ExpectedVersion: "1", Cash: &cash})
	*now = time.Date(2026, 9, 6, 5, 0, 0, 0, time.UTC)
	stockWrite(t, s, "late-buy", buyCommand("late", "2", "2026-09-03", 30, 10))
	event := testDividend("SH", "600036", "2026-09-02", "2026-09-03", "2026-09-08", 2000000)
	provider := &stockDivFixture{events: []DividendEvent{event}}
	worker := NewDividendWorker(s, provider, nil, nil)
	require.NoError(t, worker.Tick(t.Context()))
	b := readBook(t, s)
	require.Len(t, b.Entries, 3)
	require.Equal(t, Money(70000), b.Cash)
	require.Equal(t, Money(20000), b.Items[0].Metrics.Dividends)
	require.Equal(t, Money(20000), b.Items[0].Metrics.PendingDividend)
	div := b.Entries[2]
	require.Equal(t, Quantity(100000000), div.Quantity)
	count := auditCount(t, s)
	require.NoError(t, worker.Tick(t.Context()))
	require.Equal(t, count, auditCount(t, s))
	require.Equal(t, 1, provider.calls)
	*now = time.Date(2026, 9, 8, 5, 0, 0, 0, time.UTC)
	provider.err = errors.New("synthetic offline")
	require.NoError(t, worker.Tick(t.Context()))
	b = readBook(t, s)
	require.Equal(t, Money(90000), b.Cash)
	require.Zero(t, b.Items[0].Metrics.PendingDividend)
	require.Contains(t, b.SyncMessage, "未完成")
	count = auditCount(t, s)
	require.NoError(t, worker.Tick(t.Context()))
	require.Equal(t, count, auditCount(t, s))
	buy.Action = "replace"
	buy.Security = nil
	buy.ExpectedVersion = b.Version
	buy.Entry.Quantity = 200000000
	stockWrite(t, s, "correct-eligible-shares", buy)
	b = readBook(t, s)
	require.Equal(t, Money(110000), b.Cash)
	require.Equal(t, Money(40000), b.Items[0].Metrics.Dividends)
	stockWrite(t, s, "actual-dividend", StockCommand{Action: "replace", ExpectedVersion: b.Version, ID: div.ID, Entry: &StockInput{InstrumentID: "i", Kind: "dividend", Date: event.ExDate, Amount: 15000, FX: 100000000}})
	require.NoError(t, worker.Tick(t.Context()))
	b = readBook(t, s)
	require.Equal(t, Money(85000), b.Cash)
	require.Equal(t, Money(15000), b.Items[0].Metrics.Dividends)
	stockWrite(t, s, "suppress", StockCommand{Action: "void", ExpectedVersion: b.Version, ID: div.ID})
	require.NoError(t, worker.Tick(t.Context()))
	b = readBook(t, s)
	require.Equal(t, Money(70000), b.Cash)
	require.Zero(t, b.Items[0].Metrics.Dividends)
	// Re-entering current cash establishes a new anchor; old payouts cannot reappear.
	cash = 12345
	stockWrite(t, s, "new-cash", StockCommand{Action: "cash", ExpectedVersion: b.Version, Cash: &cash})
	require.NoError(t, worker.Tick(t.Context()))
	require.Equal(t, cash, readBook(t, s).Cash)
}

func TestStockConcurrentCASAndRollback(t *testing.T) {
	s, _ := stockFixture(t)
	c := buyCommand("b", "0", "2026-01-01", 10, 10)
	c.Security = stockSecurity(CNY)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"x", "y"} {
		wg.Add(1)
		go func(key string) { defer wg.Done(); _, err := s.WriteStock(t.Context(), "a", key, c); results <- err }(key)
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else {
			require.ErrorIs(t, err, ErrVersion)
		}
	}
	require.Equal(t, 1, wins)
	before := auditCount(t, s)
	_, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER reject_stock_receipt BEFORE INSERT ON idempotency_receipts WHEN NEW.kind='stock_command' BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
	require.NoError(t, err)
	_, err = s.WriteStock(t.Context(), "a", "rollback", buyCommand("c", "1", "2026-01-02", 1, 10))
	require.Error(t, err)
	require.Equal(t, before, auditCount(t, s))
	require.Len(t, readBook(t, s).Entries, 1)
}

func TestStockLegacyOpeningCanBeCompletedWithoutDoubleCounting(t *testing.T) {
	s, _ := stockFixture(t)
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{"i", "SH", "600036", "Synthetic", CNY}))
	putSource(t, s, "a", "legacy", "0", 10000, CurrentPosition{"i", 10000000})
	b := readBook(t, s)
	require.True(t, b.Items[0].Opening)
	require.Nil(t, b.Items[0].Metrics.Cost)
	c := buyCommand("initial", "1", "2026-09-06", 10, 10)
	c.ReplaceOpening = true
	stockWrite(t, s, "complete", c)
	b = readBook(t, s)
	require.False(t, b.Items[0].Opening)
	require.Equal(t, Quantity(10000000), b.Items[0].Quantity)
	require.Equal(t, Money(10000), b.Cash)
}

func TestDividendProviderDatesUnitsAndNonCashExclusion(t *testing.T) {
	p := NewPublicDividends()
	p.client.Transport = stockTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"fhyx":[{"SECURITY_CODE":"600036","IMPL_PLAN_PROFILE":"10派10.03元","ASSIGN_PROGRESS":"实施方案","EQUITY_RECORD_DATE":"2026-07-09 00:00:00","EX_DIVIDEND_DATE":"2026-07-10 00:00:00","PAY_CASH_DATE":"2026-07-13 00:00:00"},{"SECURITY_CODE":"600036","IMPL_PLAN_PROFILE":"10派20元","ASSIGN_PROGRESS":"预披露"}]}`
		if strings.Contains(r.URL.Host, "datacenter") {
			body = `{"success":true,"result":{"pages":1,"data":[{"SECURITY_CODE":"00700","EX_DIVIDEND_DATE":"2026/05/15","DIVIDEND_DATE":"2026/06/01","PLAN_EXPLAIN":"每股派港币5.3元"},{"SECURITY_CODE":"00700","EX_DIVIDEND_DATE":"2023/01/05","DIVIDEND_DATE":"2023/03/24","PLAN_EXPLAIN":"特殊说明:每10股分派1股美团B类普通股股份(相当于每股派18.13港元)"}]}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	c, err := p.FetchDividends(t.Context(), Instrument{"i", "SH", "600036", "Synthetic", CNY})
	require.NoError(t, err)
	require.Len(t, c.Events, 1)
	require.Equal(t, Price(1003000), c.Events[0].PerShare)
	require.Equal(t, "2026-07-13", c.Events[0].PayDate)
	c, err = p.FetchDividends(t.Context(), Instrument{"i", "HK", "00700", "Synthetic", HKD})
	require.NoError(t, err)
	require.Len(t, c.Events, 1)
	require.Equal(t, Price(5300000), c.Events[0].PerShare)
	require.Equal(t, "2026-06-01", c.Events[0].PayDate)
	for _, plan := range []string{"每10股分派1股美团B类普通股股份(相当于每股派18.13港元)", "每股派美元5.3元", "5.3", "每股派5.3元"} {
		_, ok := dividendPrice(plan, true, HKD)
		require.False(t, ok, plan)
	}
}

func TestStockHTTPStrictIsolationAndReadOnly(t *testing.T) {
	f := newHTTPFixture(t)
	manualSourceAccount(t, f.store, "a")
	manualSourceAccount(t, f.store, "b")
	c := buyCommand("buy", "0", "2026-01-01", 100, 10)
	c.Security = stockSecurity(CNY)
	f.request(t, "POST", "/accounts/a/stock-book", "stock", httpPayload(t, c), 200)
	before := auditCount(t, f.store)
	f.request(t, "GET", "/accounts/a/stock-book", "", "", 200)
	f.request(t, "HEAD", "/accounts/a/stock-book", "", "", 200)
	require.Equal(t, before, auditCount(t, f.store))
	f.request(t, "GET", "/accounts/a/stock-book?x=1", "", "", 400)
	f.request(t, "POST", "/accounts/a/stock-book", "bad", `{"action":"cash","expected_version":"1","cash":10}`, 400)
	f.request(t, "POST", "/accounts/b/stock-book", "wrong-account", `{"action":"void","expected_version":"0","id":"buy"}`, 404)
	var count int
	require.NoError(t, f.store.db.WithTx(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), `SELECT count(*) FROM stock_entries WHERE account_id='b'`).Scan(&count)
	}))
	require.Zero(t, count)
}

type stockFXFixture struct{ calls int }

func (f *stockFXFixture) Fetch(_ context.Context, r FXRequest) (FXQuote, error) {
	f.calls++
	return FXQuote{Base: r.Base, Quote: r.Quote, Mode: r.Mode, RequestedDate: r.Date, Rate: 90000000, Date: r.Date, Source: "Synthetic", FetchedAt: "2026-09-08T05:00:00Z"}, nil
}
func TestStockHKDividendAfterLiquidationUsesEntitledSharesAndFixedFX(t *testing.T) {
	s, now := stockFixture(t)
	*now = time.Date(2026, 9, 1, 5, 0, 0, 0, time.UTC)
	buy := buyCommand("old", "0", "2026-09-01", 100, 10)
	buy.Security = stockSecurity(HKD)
	buy.Entry.FX = 0
	stockWrite(t, s, "old", buy)
	cash := Money(100000)
	stockWrite(t, s, "cash", StockCommand{Action: "cash", ExpectedVersion: "1", Cash: &cash})
	*now = time.Date(2026, 9, 6, 5, 0, 0, 0, time.UTC)
	sell := buyCommand("close", "2", "2026-09-03", 100, 10)
	sell.Entry.Kind = "sell"
	sell.Entry.FX = 90000000
	stockWrite(t, s, "close", sell)
	reopen := buyCommand("new", "3", "2026-09-03", 50, 20)
	reopen.Entry.FX = 90000000
	stockWrite(t, s, "new", reopen)
	event := testDividend("HK", "00700", "", "2026-09-03", "2026-09-08", 5300000)
	f := &stockFXFixture{}
	worker := NewDividendWorker(s, &stockDivFixture{events: []DividendEvent{event}}, f, nil)
	require.NoError(t, worker.Tick(t.Context()))
	b := readBook(t, s)
	require.Equal(t, Money(100000), b.Cash)
	require.Equal(t, Price(20000000), *b.Items[0].Metrics.DilutedCost)
	require.Equal(t, Money(53000), b.Items[0].Metrics.PendingDividend)
	require.Equal(t, 0, f.calls)
	*now = time.Date(2026, 9, 8, 5, 0, 0, 0, time.UTC)
	require.NoError(t, worker.Tick(t.Context()))
	b = readBook(t, s)
	require.Equal(t, Money(147700), b.Cash)
	require.Equal(t, Money(53000), b.Items[0].Metrics.Dividends)
	require.Equal(t, Price(20000000), *b.Items[0].Metrics.DilutedCost)
	require.Equal(t, 1, f.calls)
	count := auditCount(t, s)
	require.NoError(t, worker.Tick(t.Context()))
	require.Equal(t, count, auditCount(t, s))
	require.Equal(t, 1, f.calls)
}
