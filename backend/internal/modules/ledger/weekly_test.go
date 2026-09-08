package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type weeklyClock struct{ value atomic.Value }

func (c *weeklyClock) set(stamp string) {
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		panic(err)
	}
	c.value.Store(t)
}
func (c *weeklyClock) now() time.Time { return c.value.Load().(time.Time) }

func weeklyFixture(t *testing.T, stock bool) (*WeeklyWorker, *weeklyClock) {
	t.Helper()
	var instruments []Instrument
	var positions []OpeningPosition
	if stock {
		instruments = []Instrument{{ID: "i", Market: "HK", Code: "00700", Name: "Synthetic", Currency: HKD}}
		positions = []OpeningPosition{{InstrumentID: "i", Quantity: 1_234_567}}
	}
	s := valuationFixture(t, positions, instruments)
	clock := &weeklyClock{}
	clock.set("2026-09-12T08:00:00+08:00")
	s.now = clock.now
	w, err := NewWeeklyWorker(s, valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), valuationTimeout)
		rows := validValuationQuotes(ctx, is)
		for _, row := range rows {
			row.Quote.Date = "2026-09-11"
			row.Quote.QuotedAt = "2026-09-11T16:00:00+08:00"
			row.Quote.FetchedAt = clock.now().Format(time.RFC3339Nano)
		}
		return rows
	}), valuationFX(func(ctx context.Context, r FXRequest) (FXQuote, error) {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), quoteNetworkTimeout)
		return FXQuote{Base: r.Base, Quote: r.Quote, Mode: "latest", RequestedDate: "2026-09-12", Rate: 91_234_567, Date: "2026-09-11", Source: "Tencent/spot/HKDCNY", QuotedAt: "2026-09-11T16:00:00+08:00", FetchedAt: clock.now().Format(time.RFC3339Nano)}, nil
	}), WeeklyConfig{Enabled: true, Time: DefaultWeeklyTime}, nil)
	require.NoError(t, err)
	return w, clock
}

func weeklyJobFor(t *testing.T, w *WeeklyWorker, account string) WeeklyJob {
	t.Helper()
	page, err := w.store.ListWeeklyJobs(t.Context(), account, "", 0, 100)
	require.NoError(t, err)
	require.NotEmpty(t, page.Items)
	return page.Items[0]
}

func weeklyCount(t *testing.T, w *WeeklyWorker, want int) {
	t.Helper()
	var n int
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM weekly_jobs`).Scan(&n))
	require.Equal(t, want, n)
}

func TestWeeklyScheduleWindowDisabledAndCatchup(t *testing.T) {
	for _, tc := range []struct {
		stamp             string
		enabled, eligible bool
		next              string
	}{
		{"2026-09-11T23:59:59+08:00", true, false, "2026-09-12T08:00:00+08:00"},
		{"2026-09-12T07:59:59+08:00", true, false, "2026-09-12T08:00:00+08:00"},
		{"2026-09-12T00:00:00Z", true, true, "2026-09-19T08:00:00+08:00"},
		{"2026-09-12T23:59:59+08:00", true, true, "2026-09-19T08:00:00+08:00"},
		{"2026-09-12T16:00:00Z", true, false, "2026-09-19T08:00:00+08:00"},
		{"2026-09-12T08:00:00+08:00", false, false, "2026-09-19T08:00:00+08:00"},
	} {
		t.Run(tc.stamp+strconv.FormatBool(tc.enabled), func(t *testing.T) {
			w, clock := weeklyFixture(t, false)
			clock.set(tc.stamp)
			w.cfg.Enabled = tc.enabled
			w.quotes = valuationQuotes(func(context.Context, []Instrument) map[string]QuoteResult { t.Fatal("cash must not fetch"); return nil })
			require.Equal(t, tc.next, w.next(clock.now()).Format(time.RFC3339))
			for range 3 {
				require.NoError(t, w.Tick(t.Context()))
			}
			count := 0
			if tc.eligible {
				count = 1
				require.Equal(t, "succeeded", weeklyJobFor(t, w, "a").Status)
			}
			weeklyCount(t, w, count)
			historyCount(t, w.store, count)
		})
	}
}

func TestWeeklyCompleteExactFrozenAndManualAppend(t *testing.T) {
	w, _ := weeklyFixture(t, true)
	require.NoError(t, w.Tick(t.Context()))
	j := weeklyJobFor(t, w, "a")
	require.Equal(t, "succeeded", j.Status)
	require.Equal(t, "holdings_current", j.Source)
	require.Equal(t, 1, j.Attempts)
	require.NotNil(t, j.HistoryID)
	h, err := w.store.ValuationHistory(t.Context(), "a", 1)
	require.NoError(t, err)
	require.Equal(t, "2026-09-12", h.Valuation.AsOf)
	require.Equal(t, "2026-09-11", h.Valuation.Items[0].Quote.Date)
	require.Equal(t, Money(11390), *h.Valuation.TotalAssets)
	require.Equal(t, 1, h.SchemaVersion)
	require.Empty(t, h.Valuation.HistoryID)
	before, _ := json.Marshal(h)
	for range 4 {
		require.NoError(t, w.Tick(t.Context()))
	}
	historyCount(t, w.store, 1)
	v, is, err := w.store.valuationInputs(t.Context(), "a")
	require.NoError(t, err)
	require.NoError(t, valuePositions(t.Context(), &v, is, nil, nil, w.store.now)) // Incomplete does not affect the saved observation.
	h2, err := w.store.ValuationHistory(t.Context(), "a", 1)
	require.NoError(t, err)
	after, _ := json.Marshal(h2)
	require.Equal(t, before, after)
	// Existing synchronous GET remains append-only, including the scheduled day.
	mux := http.NewServeMux()
	Handler{Store: w.store, Quotes: w.quotes, FX: w.fx, Now: w.store.now}.Register(mux)
	historyRequest(t, mux, "POST", "a/valuation", 200)
	historyRequest(t, mux, "POST", "a/valuation", 200)
	historyCount(t, w.store, 3)
	weeklyCount(t, w, 1)
	var basis int
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log WHERE entity_type='valuation'`).Scan(&basis))
	require.Equal(t, 3, basis)
	var flows int
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM operations`).Scan(&flows))
	require.Zero(t, flows)
}

func TestWeeklyCashZeroReportedCarryAndNoSource(t *testing.T) {
	w, _ := weeklyFixture(t, false)
	require.NoError(t, w.store.InitializeAccount(t.Context(), "Zero", Opening{AccountID: "zero", Currency: USD, Date: "2026-01-01"}))
	_, err := w.store.CreateReportedAccount(t.Context(), "reported", ReportedAccountInput{ID: "reported", Name: "Synthetic", Currency: CNY, OpeningDate: "2020-01-01"})
	require.NoError(t, err)
	_, err = w.store.CreateReportedAccount(t.Context(), "empty", ReportedAccountInput{ID: "empty", Name: "Empty", Currency: CNY, OpeningDate: "2020-01-01"})
	require.NoError(t, err)
	amount := Money(90000)
	_, err = w.store.WriteAccountRecord(t.Context(), "asset", AccountRecordCommand{Action: CreateOperation, AccountID: "reported", ID: "manual-old", Entry: &AccountEntry{Kind: "asset", Date: "2026-09-01", TotalAssets: &amount}})
	require.NoError(t, err)
	require.NoError(t, w.Tick(t.Context()))
	j := weeklyJobFor(t, w, "reported")
	require.Equal(t, "succeeded", j.Status)
	require.Empty(t, j.ErrorCode)
	require.Equal(t, "account_record_carry", j.Source)
	require.NotNil(t, j.HistoryID)
	require.Equal(t, 1, j.Attempts)
	jobID, err := strconv.ParseInt(j.ID, 10, 64)
	require.NoError(t, err)
	detail, err := w.store.GetWeeklyJob(t.Context(), "reported", jobID)
	require.NoError(t, err)
	require.NotNil(t, detail.Carry)
	require.Equal(t, *j.HistoryID, detail.Carry.ID)
	require.Equal(t, "2026-09-12", detail.Carry.AsOf)
	require.Equal(t, amount, detail.Carry.TotalAssets)
	require.Equal(t, AccountRecordSource{ID: "manual-old", AccountID: "reported", Sequence: "1", Version: "1", Date: "2026-09-01", Origin: "manual", TotalAssets: amount}, detail.Carry.SourceRecord)
	record, err := scanAccountRecord(w.store.db.QueryRowContext(t.Context(), accountRecordSelect+` WHERE account_id='reported' AND sequence=?`, *j.HistoryID))
	require.NoError(t, err)
	require.Equal(t, "weekly_carry", record.Origin)
	require.Equal(t, amount, *record.TotalAssets)
	require.Equal(t, detail.Carry.SourceRecord, *record.CarriedFrom)
	basis, err := w.store.AnalysisBasis(t.Context(), "reported", "2026-09-01", "2026-09-12", 0)
	require.NoError(t, err)
	require.Len(t, basis.Points, 2)
	require.Equal(t, "carried", basis.Points[1].Status)
	require.Equal(t, "manual-old", basis.Points[1].SourceID)
	require.Equal(t, "2026-09-01", basis.Points[1].SourceDate)
	wired, err := json.Marshal(detail)
	require.NoError(t, err)
	require.Contains(t, string(wired), `"carry":{"id":"`)
	require.Contains(t, string(wired), `"total_assets":"900.00"`)

	empty := weeklyJobFor(t, w, "empty")
	require.Equal(t, "skipped", empty.Status)
	require.Equal(t, "no_source", empty.ErrorCode)
	require.Equal(t, "account_record_carry", empty.Source)
	require.Nil(t, empty.HistoryID)
	require.Equal(t, 1, empty.Attempts)
	j = weeklyJobFor(t, w, "zero")
	require.Equal(t, "succeeded", j.Status)
	id, err := strconv.ParseInt(*j.HistoryID, 10, 64)
	require.NoError(t, err)
	h, err := w.store.ValuationHistory(t.Context(), "zero", id)
	require.NoError(t, err)
	require.Zero(t, *h.Valuation.TotalAssets)
	var count int
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records WHERE account_id='reported'`).Scan(&count))
	require.Equal(t, 2, count)
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log WHERE account_id='reported' AND entity_type='weekly_carry'`).Scan(&count))
	require.Equal(t, 1, count)
	historyCount(t, w.store, 2)
}

func TestWeeklyCarryCompletionIsAtomicAndRetries(t *testing.T) {
	w, clock := weeklyFixture(t, false)
	_, err := w.store.CreateReportedAccount(t.Context(), "reported", ReportedAccountInput{ID: "reported", Name: "Synthetic", Currency: CNY, OpeningDate: "2020-01-01"})
	require.NoError(t, err)
	amount := Money(12345)
	_, err = w.store.WriteAccountRecord(t.Context(), "asset", AccountRecordCommand{Action: CreateOperation, AccountID: "reported", ID: "manual-source", Entry: &AccountEntry{Kind: "asset", Date: "2026-09-01", TotalAssets: &amount}})
	require.NoError(t, err)
	_, err = w.store.db.ExecContext(t.Context(), `CREATE TRIGGER weekly_carry_fault BEFORE INSERT ON audit_log WHEN NEW.entity_type='weekly_carry' BEGIN SELECT RAISE(ABORT,'synthetic private exception'); END`)
	require.NoError(t, err)
	require.NoError(t, w.Tick(t.Context()))
	j := weeklyJobFor(t, w, "reported")
	require.Equal(t, "failed", j.Status)
	require.Equal(t, "storage_error", j.ErrorCode)
	require.Equal(t, 1, j.Attempts)
	var count int
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records WHERE account_id='reported'`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log WHERE entity_type='weekly_carry'`).Scan(&count))
	require.Zero(t, count)
	_, err = w.store.db.ExecContext(t.Context(), `DROP TRIGGER weekly_carry_fault`)
	require.NoError(t, err)
	clock.set("2026-09-12T08:05:00+08:00")
	require.NoError(t, w.Tick(t.Context()))
	j = weeklyJobFor(t, w, "reported")
	require.Equal(t, "succeeded", j.Status)
	require.Equal(t, 2, j.Attempts)
	require.NotNil(t, j.HistoryID)
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records WHERE account_id='reported'`).Scan(&count))
	require.Equal(t, 2, count)
}

func TestWeeklyIncompleteRetriesCapAndOtherAccounts(t *testing.T) {
	w, clock := weeklyFixture(t, true)
	var calls int
	w.quotes = valuationQuotes(func(context.Context, []Instrument) map[string]QuoteResult { calls++; return nil })
	require.NoError(t, w.store.InitializeAccount(t.Context(), "Other", Opening{AccountID: "z", Currency: CNY, Date: "2026-01-01", Cash: 100}))
	require.NoError(t, w.Tick(t.Context()))
	j := weeklyJobFor(t, w, "a")
	require.Equal(t, "failed", j.Status)
	require.Equal(t, "incomplete_valuation", j.ErrorCode)
	require.Nil(t, j.HistoryID)
	require.Equal(t, "succeeded", weeklyJobFor(t, w, "z").Status)
	clock.set("2026-09-12T08:04:59+08:00")
	require.NoError(t, w.Tick(t.Context()))
	require.Equal(t, 1, calls)
	clock.set("2026-09-12T08:05:00+08:00")
	require.NoError(t, w.Tick(t.Context()))
	require.Equal(t, 2, calls)
	clock.set("2026-09-12T08:34:59+08:00")
	require.NoError(t, w.Tick(t.Context()))
	require.Equal(t, 2, calls)
	clock.set("2026-09-12T08:35:00+08:00")
	require.NoError(t, w.Tick(t.Context()))
	require.Equal(t, 3, calls)
	clock.set("2026-09-12T23:00:00+08:00")
	for range 5 {
		require.NoError(t, w.Tick(t.Context()))
	}
	require.Equal(t, 3, calls)
	j = weeklyJobFor(t, w, "a")
	require.Equal(t, 3, j.Attempts)
	require.Nil(t, j.NextAttemptAt)
	historyCount(t, w.store, 1)
	clock.set("2026-09-19T08:00:00+08:00")
	require.NoError(t, w.Tick(t.Context()))
	require.Equal(t, 4, calls)
	weeklyCount(t, w, 4)
}

func TestWeeklyBasisChangedDuringIOAndUnrelatedAllowed(t *testing.T) {
	for _, kind := range []string{"same_account", "transfer_in", "other_account", "reported_track", "new_instrument"} {
		t.Run(kind, func(t *testing.T) {
			w, clock := weeklyFixture(t, true)
			require.NoError(t, w.store.InitializeAccount(t.Context(), "Other", Opening{AccountID: "b", Currency: CNY, Date: "2026-01-01", Cash: 10000}))
			provider := w.quotes
			w.quotes = valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
				// A nested domain write would deadlock if network held the DB connection.
				var err error
				switch kind {
				case "reported_track":
					a := Money(1)
					_, err = w.store.WriteAccountRecord(ctx, "reported-write", AccountRecordCommand{Action: CreateOperation, AccountID: "a", ID: "manual-one", Entry: &AccountEntry{Kind: "asset", Date: "2026-09-12", TotalAssets: &a}})
				case "new_instrument":
					err = w.store.AddInstrument(ctx, Instrument{ID: "unused", Market: "SH", Code: "600001", Name: "Other", Currency: CNY})
				default:
					op := Operation{ID: "mutation", AccountID: "a", Date: "2026-09-12", Sequence: 1, Kind: Deposit, Amount: 100}
					if kind == "other_account" {
						op.AccountID = "b"
					}
					if kind == "transfer_in" {
						op.AccountID = "b"
						op.ToAccountID = "a"
						op.Kind = Transfer
					}
					_, err = w.store.Write(ctx, Command{Action: CreateOperation, Key: "mutation", Operation: op, Reason: "synthetic"})
				}
				require.NoError(t, err)
				return provider.Fetch(ctx, is)
			})
			require.NoError(t, w.Tick(t.Context()))
			j := weeklyJobFor(t, w, "a")
			if kind == "same_account" || kind == "transfer_in" {
				require.Equal(t, "failed", j.Status)
				require.Equal(t, "basis_changed", j.ErrorCode)
				require.Nil(t, j.HistoryID)
				w.quotes = provider
				clock.set("2026-09-12T08:05:00+08:00")
				require.NoError(t, w.Tick(t.Context()))
				j = weeklyJobFor(t, w, "a")
				require.Equal(t, "succeeded", j.Status)
				require.Equal(t, 2, j.Attempts)
			} else {
				require.Equal(t, "succeeded", j.Status)
			}
			historyCount(t, w.store, 2)
		})
	}
}

func TestWeeklyConcurrentTicksAndCancellation(t *testing.T) {
	w, _ := weeklyFixture(t, true)
	entered, release := make(chan struct{}), make(chan struct{})
	provider := w.quotes
	w.quotes = valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Fetch(ctx, is)
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Tick(ctx) }()
	<-entered
	var group sync.WaitGroup
	for range 10 {
		group.Go(func() { require.NoError(t, w.Tick(t.Context())) })
	}
	group.Wait()
	j := weeklyJobFor(t, w, "a")
	require.Equal(t, "running", j.Status)
	require.Equal(t, 1, j.Attempts)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	j = weeklyJobFor(t, w, "a")
	require.Equal(t, "failed", j.Status)
	require.Equal(t, "canceled", j.ErrorCode)
	historyCount(t, w.store, 0)
	weeklyCount(t, w, 1)
}

func TestWeeklyProviderTimeoutAndInvalidData(t *testing.T) {
	for _, kind := range []string{"timeout", "future_fetch", "wrong_symbol", "wrong_currency", "wrong_fx", "missing_fx", "overflow"} {
		t.Run(kind, func(t *testing.T) {
			w, clock := weeklyFixture(t, true)
			provider := w.quotes
			if kind == "overflow" {
				_, err := w.store.db.ExecContext(t.Context(), `DROP TRIGGER opening_positions_no_update; UPDATE opening_positions SET quantity_micros=9223372036854775807`)
				require.NoError(t, err)
			}
			w.quotes = valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
				if kind == "timeout" {
					<-ctx.Done()
					return nil
				}
				rows := provider.Fetch(ctx, is)
				q := rows["i"].Quote
				switch kind {
				case "future_fetch":
					q.FetchedAt = clock.now().Add(time.Hour).Format(time.RFC3339)
				case "wrong_symbol":
					q.Symbol = "sh600000"
				case "wrong_currency":
					q.Currency = USD
				case "overflow":
					q.Price = Price(9223372036854775807)
				}
				return rows
			})
			if kind == "missing_fx" {
				w.fx = nil
			}
			if kind == "wrong_fx" {
				w.fx = valuationFX(func(context.Context, FXRequest) (FXQuote, error) {
					return FXQuote{Base: USD, Quote: HKD, Rate: 100}, nil
				})
			}
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			err := w.Tick(ctx)
			if kind == "timeout" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.NoError(t, err)
			}
			j := weeklyJobFor(t, w, "a")
			require.Equal(t, "failed", j.Status)
			require.Nil(t, j.HistoryID)
			if kind == "timeout" {
				require.Equal(t, "timeout", j.ErrorCode)
			}
			historyCount(t, w.store, 0)
		})
	}
}

func TestWeeklyCrossMidnightAndOldSlotsNeverBackfill(t *testing.T) {
	w, clock := weeklyFixture(t, true)
	provider := w.quotes
	clock.set("2026-09-12T23:59:59+08:00")
	w.quotes = valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
		rows := provider.Fetch(ctx, is)
		clock.set("2026-09-13T00:00:00+08:00")
		return rows
	})
	require.NoError(t, w.Tick(t.Context()))
	j := weeklyJobFor(t, w, "a")
	require.Equal(t, "skipped", j.Status)
	require.Equal(t, "expired", j.ErrorCode)
	historyCount(t, w.store, 0)
	// Old running/pending/retry slots can be retired on Sunday, never repriced.
	for n, status := range []string{"pending", "running", "failed"} {
		id := fmt.Sprintf("old%d", n)
		require.NoError(t, w.store.InitializeAccount(t.Context(), "Old", Opening{AccountID: id, Currency: CNY, Date: "2026-01-01"}))
		var lease any
		if status == "running" {
			lease = "2026-09-13T00:01:00.000000000Z"
		}
		_, err := w.store.db.ExecContext(t.Context(), `INSERT INTO weekly_jobs(account_id,scheduled_business_date,source,status,attempts,created_at,started_at,next_attempt_at,lease_until) VALUES(?,'2026-09-12','holdings_current',?,1,?,?,?,?)`, id, status, clock.now().Format(weeklyStamp), clock.now().Format(weeklyStamp), clock.now().Format(weeklyStamp), lease)
		require.NoError(t, err)
	}
	w.quotes = valuationQuotes(func(context.Context, []Instrument) map[string]QuoteResult {
		t.Fatal("Sunday must not fetch")
		return nil
	})
	require.NoError(t, w.Tick(t.Context()))
	for n := range 3 {
		j := weeklyJobFor(t, w, fmt.Sprintf("old%d", n))
		require.Equal(t, "skipped", j.Status)
		require.Equal(t, "expired", j.ErrorCode)
	}
	historyCount(t, w.store, 0)
	weeklyCount(t, w, 4)
}

func TestWeeklyAtomicCompletionRollbackAndFence(t *testing.T) {
	for _, table := range []string{"audit_log", "account_records", "weekly_jobs"} {
		t.Run(table, func(t *testing.T) {
			w, clock := weeklyFixture(t, false)
			statement := `CREATE TRIGGER weekly_fault BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'synthetic private exception'); END`
			if table == "audit_log" {
				statement = `CREATE TRIGGER weekly_fault BEFORE INSERT ON audit_log WHEN NEW.entity_type='valuation' BEGIN SELECT RAISE(ABORT,'synthetic private exception'); END`
			}
			if table == "weekly_jobs" {
				statement = `CREATE TRIGGER weekly_fault BEFORE UPDATE ON weekly_jobs WHEN NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'synthetic private exception'); END`
			}
			_, err := w.store.db.ExecContext(t.Context(), statement)
			require.NoError(t, err)
			require.NoError(t, w.Tick(t.Context()))
			j := weeklyJobFor(t, w, "a")
			require.Equal(t, "failed", j.Status)
			require.Equal(t, "storage_error", j.ErrorCode)
			require.Nil(t, j.HistoryID)
			historyCount(t, w.store, 0)
			var n int
			require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log WHERE entity_type='valuation'`).Scan(&n))
			require.Zero(t, n)
			_, err = w.store.db.ExecContext(t.Context(), `DROP TRIGGER weekly_fault`)
			require.NoError(t, err)
			clock.set("2026-09-12T08:05:00+08:00")
			require.NoError(t, w.Tick(t.Context()))
			successful := weeklyJobFor(t, w, "a")
			require.Equal(t, "succeeded", successful.Status)
			require.Equal(t, 2, successful.Attempts)
			v, is, err := w.store.valuationInputs(t.Context(), "a")
			require.NoError(t, err)
			require.NoError(t, valuePositions(t.Context(), &v, is, nil, nil, w.store.now))
			require.ErrorIs(t, w.complete(t.Context(), j, v, is), errWeeklyFence)
			require.NoError(t, w.fail(t.Context(), j, "timeout"))
			require.Equal(t, successful, weeklyJobFor(t, w, "a"))
			historyCount(t, w.store, 1)
			_, err = w.store.db.ExecContext(t.Context(), `UPDATE weekly_jobs SET status='failed',history_id=NULL WHERE id=?`, j.ID)
			require.Error(t, err)
			require.NoError(t, w.store.InitializeAccount(t.Context(), "Other", Opening{AccountID: "b", Currency: CNY, Date: "2026-01-01"}))
			for _, statement := range []string{
				`INSERT INTO weekly_jobs(account_id,scheduled_business_date,source,status,created_at) VALUES('a','2026-09-19','none','pending','now')`,
				`INSERT INTO weekly_jobs(account_id,scheduled_business_date,source,status,created_at) VALUES('a','not-a-date','holdings_current','pending','now')`,
				`INSERT INTO weekly_jobs(account_id,scheduled_business_date,source,status,created_at) VALUES('a','2026-09-13','holdings_current','pending','now')`,
				`INSERT INTO weekly_jobs(account_id,scheduled_business_date,source,status,attempts,created_at,finished_at) VALUES('a','2026-09-19','holdings_current','succeeded',1,'now','now')`,
				`INSERT INTO weekly_jobs(account_id,scheduled_business_date,source,status,attempts,created_at,finished_at,history_id) VALUES('b','2026-09-12','holdings_current','succeeded',1,'now','now',1)`,
				`INSERT INTO weekly_jobs(account_id,scheduled_business_date,source,status,attempts,created_at,finished_at,history_id) VALUES('a','2026-09-19','holdings_current','succeeded',1,'now','now',999)`,
				`DELETE FROM weekly_jobs`,
			} {
				_, err := w.store.db.ExecContext(t.Context(), statement)
				require.Error(t, err)
			}
			weeklyCount(t, w, 1)
		})
	}
}

func TestWeeklyRestartRecoversRunningAndPreservesSuccess(t *testing.T) {
	root := t.TempDir()
	db, err := Open(t.Context(), root)
	require.NoError(t, err)
	clock := &weeklyClock{}
	clock.set("2026-09-12T08:00:00+08:00")
	s := NewStore(db, clock.now)
	require.NoError(t, s.InitializeAccount(t.Context(), "Synthetic", Opening{AccountID: "a", Currency: CNY, Date: "2026-01-01", Cash: 123}))
	_, err = db.ExecContext(t.Context(), `INSERT INTO weekly_jobs(account_id,scheduled_business_date,source,status,attempts,created_at,started_at,lease_until) VALUES('a','2026-09-12','holdings_current','running',1,?,?,?)`, clock.now().Format(weeklyStamp), clock.now().Format(weeklyStamp), clock.now().Add(time.Minute).Format(weeklyStamp))
	require.NoError(t, err)
	require.NoError(t, db.Close())
	for n := range 2 {
		db, err = Open(t.Context(), root)
		require.NoError(t, err)
		s = NewStore(db, clock.now)
		w, err := NewWeeklyWorker(s, nil, nil, WeeklyConfig{Enabled: true, Time: DefaultWeeklyTime}, nil)
		require.NoError(t, err)
		require.NoError(t, w.Tick(t.Context()))
		if n == 0 {
			j := weeklyJobFor(t, w, "a")
			require.Equal(t, "failed", j.Status)
			require.Equal(t, "interrupted", j.ErrorCode)
			historyCount(t, s, 0)
			clock.set("2026-09-12T08:05:00+08:00")
			require.NoError(t, w.Tick(t.Context()))
		}
		j := weeklyJobFor(t, w, "a")
		require.Equal(t, "succeeded", j.Status)
		require.Equal(t, 2, j.Attempts)
		historyCount(t, s, 1)
		require.NoError(t, db.Close())
	}
}

func TestWeeklyFailedCleanupLeaseRecoveryDoesNotBlockOtherAccounts(t *testing.T) {
	w, clock := weeklyFixture(t, false)
	require.NoError(t, w.store.InitializeAccount(t.Context(), "Other", Opening{AccountID: "b", Currency: CNY, Date: "2026-01-01"}))
	_, err := w.store.db.ExecContext(t.Context(), `CREATE TRIGGER weekly_fault BEFORE UPDATE ON weekly_jobs WHEN OLD.account_id='a' AND OLD.status='running' BEGIN SELECT RAISE(ABORT,'synthetic private detail'); END`)
	require.NoError(t, err)
	require.Error(t, w.Tick(t.Context()))
	j := weeklyJobFor(t, w, "a")
	require.Equal(t, "running", j.Status)
	require.Nil(t, j.HistoryID)
	require.Equal(t, "succeeded", weeklyJobFor(t, w, "b").Status)
	historyCount(t, w.store, 1)
	require.NoError(t, w.Tick(t.Context()))
	require.Equal(t, j, weeklyJobFor(t, w, "a"))
	_, err = w.store.db.ExecContext(t.Context(), `DROP TRIGGER weekly_fault`)
	require.NoError(t, err)
	clock.set("2026-09-12T08:01:13+08:00")
	require.NoError(t, w.Tick(t.Context()))
	recovered := weeklyJobFor(t, w, "a")
	require.Equal(t, "interrupted", recovered.ErrorCode)
	require.Equal(t, 1, recovered.Attempts)
	clock.set("2026-09-12T08:06:13+08:00")
	require.NoError(t, w.Tick(t.Context()))
	require.Equal(t, "succeeded", weeklyJobFor(t, w, "a").Status)
	historyCount(t, w.store, 2)
}

func TestWeeklyCompletionLateClockRollsBackBeforeCommit(t *testing.T) {
	w, clock := weeklyFixture(t, false)
	_, err := w.store.db.ExecContext(t.Context(), `INSERT INTO weekly_jobs(account_id,scheduled_business_date,source,status,created_at) VALUES('a','2026-09-12','holdings_current','pending',?)`, clock.now().Format(weeklyStamp))
	require.NoError(t, err)
	j, err := w.claim(t.Context(), "2026-09-12")
	require.NoError(t, err)
	require.NotNil(t, j)
	v, is, err := w.store.valuationInputs(t.Context(), "a")
	require.NoError(t, err)
	require.NoError(t, valuePositions(t.Context(), &v, is, nil, nil, clock.now))
	before := weeklyJobFor(t, w, "a")
	_, err = w.store.db.ExecContext(t.Context(), `CREATE TRIGGER weekly_ignore BEFORE UPDATE ON weekly_jobs WHEN NEW.status='succeeded' BEGIN SELECT RAISE(IGNORE); END`)
	require.NoError(t, err)
	require.ErrorIs(t, w.complete(t.Context(), *j, v, is), errWeeklyFence)
	historyCount(t, w.store, 0)
	require.Equal(t, before, weeklyJobFor(t, w, "a"))
	_, err = w.store.db.ExecContext(t.Context(), `DROP TRIGGER weekly_ignore`)
	require.NoError(t, err)
	calls := 0
	w.store.now = func() time.Time {
		calls++
		if calls >= 4 {
			clock.set("2026-09-13T00:00:00+08:00")
		}
		return clock.now()
	}
	require.ErrorIs(t, w.complete(t.Context(), *j, v, is), errWeeklyExpired)
	historyCount(t, w.store, 0)
	require.Equal(t, before, weeklyJobFor(t, w, "a"))
	w.store.now = clock.now
	require.NoError(t, w.fail(t.Context(), *j, "expired"))
	require.Equal(t, "skipped", weeklyJobFor(t, w, "a").Status)
}

func TestWeeklyPartialPositionsNeverSaveCashOnly(t *testing.T) {
	w, _ := weeklyFixture(t, true)
	require.NoError(t, w.store.AddInstrument(t.Context(), Instrument{ID: "second", Market: "SH", Code: "600000", Name: "Synthetic", Currency: CNY}))
	_, err := w.store.db.ExecContext(t.Context(), `INSERT INTO opening_positions(account_id,instrument_id,quantity_micros) VALUES('a','second',1000000)`)
	require.NoError(t, err)
	provider := w.quotes
	w.quotes = valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
		rows := provider.Fetch(ctx, is)
		delete(rows, "second")
		return rows
	})
	require.NoError(t, w.Tick(t.Context()))
	j := weeklyJobFor(t, w, "a")
	require.Equal(t, "incomplete_valuation", j.ErrorCode)
	require.Nil(t, j.HistoryID)
	historyCount(t, w.store, 0)
}

func TestWeeklyBoundedEnrollmentAndLateAccounts(t *testing.T) {
	w, _ := weeklyFixture(t, false)
	for n := range weeklyBatch + 2 {
		require.NoError(t, w.store.InitializeAccount(t.Context(), "Synthetic", Opening{AccountID: fmt.Sprintf("b%03d", n), Currency: CNY, Date: "2026-01-01"}))
	}
	require.NoError(t, w.Tick(t.Context()))
	weeklyCount(t, w, weeklyBatch)
	historyCount(t, w.store, weeklyBatch)
	require.NoError(t, w.Tick(t.Context()))
	weeklyCount(t, w, weeklyBatch+3)
	require.NoError(t, w.store.InitializeAccount(t.Context(), "Late", Opening{AccountID: "late", Currency: CNY, Date: "2026-01-01"}))
	require.NoError(t, w.Tick(t.Context()))
	require.Equal(t, "succeeded", weeklyJobFor(t, w, "late").Status)
	historyCount(t, w.store, weeklyBatch+4)
}

func TestWeeklyRunWaitsRatherThanSpinsAndDisabledDoesNotWrite(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			w, _ := weeklyFixture(t, false)
			w.cfg.Enabled = enabled
			var reads atomic.Int64
			now := w.store.now
			w.store.now = func() time.Time { reads.Add(1); return now() }
			ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
			defer cancel()
			require.NoError(t, w.Run(ctx))
			require.Less(t, reads.Load(), int64(30))
			if !enabled {
				require.Zero(t, reads.Load())
				weeklyCount(t, w, 0)
				historyCount(t, w.store, 0)
			} else {
				historyCount(t, w.store, 1)
			}
		})
	}
}

func TestWeeklyReadAPIsStrictIsolatedAndNeverTrigger(t *testing.T) {
	w, _ := weeklyFixture(t, false)
	mux := http.NewServeMux()
	Handler{Store: w.store, Weekly: w}.Register(mux)
	call := func(method, path string, status int) []byte {
		t.Helper()
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest(method, ledgerPrefix+path, nil))
		require.Equal(t, status, r.Code, r.Body.String())
		if method == "HEAD" {
			require.Empty(t, r.Body.String())
		}
		return r.Body.Bytes()
	}
	for _, method := range []string{"GET", "HEAD"} {
		call(method, "/weekly-status", 200)
		call(method, "/accounts/a/weekly-jobs", 200)
	}
	weeklyCount(t, w, 0)
	historyCount(t, w.store, 0)
	var status map[string]any
	require.NoError(t, json.Unmarshal(call("GET", "/weekly-status", 200), &status))
	require.Equal(t, true, status["enabled"])
	require.Equal(t, "08:00", status["time"])
	require.Equal(t, "Asia/Shanghai", status["timezone"])
	require.Equal(t, true, status["window_open"])
	require.NoError(t, w.Tick(t.Context()))
	for _, method := range []string{"GET", "HEAD"} {
		call(method, "/accounts/a/weekly-jobs?status=succeeded&limit=1", 200)
		call(method, "/accounts/a/weekly-jobs/1", 200)
		call(method, "/accounts/missing/weekly-jobs/1", 404)
		call(method, "/accounts/missing/weekly-jobs", 404)
		call(method, "/accounts/a/weekly-jobs/999", 404)
	}
	require.NoError(t, w.store.InitializeAccount(t.Context(), "Other", Opening{AccountID: "b", Currency: CNY, Date: "2026-01-01"}))
	call("GET", "/accounts/b/weekly-jobs/1", 404)
	for _, suffix := range []string{"?", "?status=all", "?status=", "?status=bogus", "?limit=0", "?limit=101", "?limit=1&limit=2", "?cursor=0", "?cursor=01", "?cursor=-1", "?account_id=b", "?unknown=1", "?cursor=9223372036854775808", "/0", "/-1", "/01", "/x", "/1?limit=1"} {
		call("GET", "/accounts/a/weekly-jobs"+suffix, 400)
	}
	for _, suffix := range []string{"?", "?&", "?enabled=true"} {
		call("GET", "/weekly-status"+suffix, 400)
	}
	for _, path := range []string{"/weekly-status", "/accounts/a/weekly-jobs", "/accounts/a/weekly-jobs/1"} {
		call("POST", path, 405)
	}
	weeklyCount(t, w, 1)
	historyCount(t, w.store, 1)
	w.cfg.Enabled = false
	require.NoError(t, json.Unmarshal(call("GET", "/weekly-status", 200), &status))
	require.Equal(t, false, status["enabled"])
	require.Nil(t, status["next_scheduled_at"])
}
