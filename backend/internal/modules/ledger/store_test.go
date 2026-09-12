package ledger_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
	"github.com/stretchr/testify/require"
)

type storeSpecFixture struct {
	db    *database.DB
	store *ledger.Store
	root  string
	now   time.Time
}

func newStoreSpecFixture(t *testing.T) *storeSpecFixture {
	t.Helper()
	f := &storeSpecFixture{root: t.TempDir(), now: time.Date(2026, 9, 6, 10, 11, 12, 123456789, time.UTC)}
	var err error
	f.db, err = ledger.Open(t.Context(), f.root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.db.Close()) })
	f.store = ledger.NewStore(f.db, func() time.Time { return f.now })
	return f
}
func (f *storeSpecFixture) reopen(t *testing.T) {
	t.Helper()
	require.NoError(t, f.db.Close())
	var err error
	f.db, err = ledger.Open(t.Context(), f.root)
	require.NoError(t, err)
	f.store = ledger.NewStore(f.db, func() time.Time { return f.now })
}
func (f *storeSpecFixture) account(t *testing.T, id string, cash ledger.Money) {
	t.Helper()
	_, err := f.store.CreateReportedAccount(t.Context(), "create-"+id, ledger.ReportedAccountInput{ID: id, Name: "Synthetic " + id, Currency: ledger.CNY, OpeningDate: "2026-01-01"})
	require.NoError(t, err)
	_, err = f.store.PutCurrentHoldings(t.Context(), id, "current-"+id, ledger.CurrentHoldingsInput{ExpectedVersion: "0", Cash: &cash, Positions: []ledger.CurrentPosition{}})
	require.NoError(t, err)
}
func (f *storeSpecFixture) instrument(t *testing.T, id string, currency ledger.Currency) ledger.Instrument {
	t.Helper()
	i := ledger.Instrument{ID: id, Market: "TEST", Code: id, Name: "Synthetic " + id, Currency: currency}
	require.NoError(t, f.store.AddInstrument(t.Context(), i))
	return i
}
func storeSpecMoney(v ledger.Money) *ledger.Money { return &v }
func storeSpecSnapshot(t *testing.T, db *database.DB) map[string][][]any {
	t.Helper()
	result := map[string][][]any{}
	for _, table := range []string{"accounts", "instruments", "current_holdings", "account_records", "audit_log", "idempotency_receipts", "weekly_jobs"} {
		rows, err := db.QueryContext(t.Context(), "SELECT * FROM "+table+" ORDER BY 1,2")
		require.NoError(t, err)
		columns, err := rows.Columns()
		require.NoError(t, err)
		result[table] = make([][]any, 0)
		for rows.Next() {
			values, targets := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			require.NoError(t, rows.Scan(targets...))
			for i, v := range values {
				if b, ok := v.([]byte); ok {
					values[i] = string(b)
				}
			}
			result[table] = append(result[table], values)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
	return result
}
func recordCommand() ledger.AccountRecordCommand {
	return ledger.AccountRecordCommand{Action: ledger.CreateOperation, AccountID: "a", ID: "manual-flow", Entry: &ledger.AccountEntry{Kind: "cash_flow", Date: "2024-01-02", Flow: storeSpecMoney(100), Note: "original"}}
}

func TestStoreCurrentInputsExactRoundTripAndImmutableIdentity(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 9_007_199_254_740_993)
	i := f.instrument(t, "i", ledger.CNY)
	input := ledger.CurrentHoldingsInput{ExpectedVersion: "1", Cash: storeSpecMoney(0), Positions: []ledger.CurrentPosition{{InstrumentID: "i", Quantity: 9_007_199_254_740_993}}}
	saved, err := f.store.PutCurrentHoldings(t.Context(), "a", "quantities", input)
	require.NoError(t, err)
	require.Equal(t, input.Positions, saved.Snapshot.Positions)
	require.Equal(t, "9007199254.740993", saved.Snapshot.Positions[0].Quantity.String())
	f.reopen(t)
	loaded, err := f.store.CurrentHoldings(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, saved, loaded)
	before := storeSpecSnapshot(t, f.db)
	i.Name = "changed"
	require.ErrorIs(t, f.store.AddInstrument(t.Context(), i), ledger.ErrConflict)
	for _, sql := range []string{`UPDATE accounts SET name='other'`, `DELETE FROM accounts`, `UPDATE instruments SET currency='USD'`, `DELETE FROM instruments`, `DELETE FROM audit_log`, `DELETE FROM idempotency_receipts`} {
		_, err := f.db.ExecContext(t.Context(), sql)
		require.Error(t, err)
	}
	require.Equal(t, before, storeSpecSnapshot(t, f.db))
}

func TestStoreRecordVersionsVoidAndDurableOriginalReceipts(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 1234)
	c := recordCommand()
	original, err := f.store.WriteAccountRecord(t.Context(), "original", c)
	require.NoError(t, err)
	c.Action, c.ExpectedVersion, c.Reason = ledger.ReplaceOperation, "1", "correction"
	c.Entry = &ledger.AccountEntry{Kind: "asset", Date: "2024-01-01", TotalAssets: storeSpecMoney(200)}
	edited, err := f.store.WriteAccountRecord(t.Context(), "edit", c)
	require.NoError(t, err)
	var record ledger.AccountRecord
	require.NoError(t, json.Unmarshal(edited, &record))
	require.Equal(t, "2", record.Version)
	require.Nil(t, record.Flow)
	c.Action, c.ExpectedVersion, c.Entry = ledger.VoidOperation, "2", nil
	voided, err := f.store.WriteAccountRecord(t.Context(), "void", c)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(voided, &record))
	require.True(t, record.Voided)
	require.Equal(t, "3", record.Version)
	f.reopen(t)
	before := storeSpecSnapshot(t, f.db)
	again, err := f.store.WriteAccountRecord(t.Context(), "original", recordCommand())
	require.NoError(t, err)
	require.Equal(t, original, again)
	require.Equal(t, before, storeSpecSnapshot(t, f.db))
	c.ExpectedVersion = "3"
	_, err = f.store.WriteAccountRecord(t.Context(), "second-void", c)
	require.ErrorIs(t, err, ledger.ErrVoided)
	_, state, err := f.store.Account(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, ledger.Money(1234), state.Cash)
}

func TestStoreIdempotencyCoversEveryRecordFieldAndAccount(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 0)
	f.account(t, "b", 0)
	c := recordCommand()
	_, err := f.store.WriteAccountRecord(t.Context(), "key", c)
	require.NoError(t, err)
	for _, mutate := range []func(*ledger.AccountRecordCommand){func(c *ledger.AccountRecordCommand) { c.AccountID = "b" }, func(c *ledger.AccountRecordCommand) { c.ID = "manual-other" }, func(c *ledger.AccountRecordCommand) { c.Entry.Date = "2024-01-03" }, func(c *ledger.AccountRecordCommand) { c.Entry.Flow = storeSpecMoney(200) }, func(c *ledger.AccountRecordCommand) { c.Entry.TotalAssets = storeSpecMoney(100) }, func(c *ledger.AccountRecordCommand) { c.Entry.Note = "changed" }} {
		next := recordCommand()
		mutate(&next)
		before := storeSpecSnapshot(t, f.db)
		_, err := f.store.WriteAccountRecord(t.Context(), "key", next)
		require.ErrorIs(t, err, ledger.ErrIdempotency)
		require.Equal(t, before, storeSpecSnapshot(t, f.db))
	}
	_, err = f.store.PutCurrentHoldings(t.Context(), "a", "key", ledger.CurrentHoldingsInput{ExpectedVersion: "1", Cash: storeSpecMoney(0), Positions: []ledger.CurrentPosition{}})
	require.ErrorIs(t, err, ledger.ErrIdempotency)
}

func TestStorePersistenceFailuresRollbackRecordAuditAndReceipt(t *testing.T) {
	for _, target := range []string{"account_records", "audit_log", "idempotency_receipts", "commit"} {
		t.Run(target, func(t *testing.T) {
			f := newStoreSpecFixture(t)
			f.account(t, "a", 0)
			c := recordCommand()
			var err error
			if target == "commit" {
				_, err = f.db.ExecContext(t.Context(), `CREATE TABLE commit_guard(id TEXT REFERENCES accounts(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fail_write AFTER INSERT ON account_records BEGIN INSERT INTO commit_guard VALUES('absent'); END`)
			} else {
				_, err = f.db.ExecContext(t.Context(), `CREATE TRIGGER fail_write AFTER INSERT ON `+target+` BEGIN SELECT RAISE(FAIL,'private-value'); END`)
			}
			require.NoError(t, err)
			before := storeSpecSnapshot(t, f.db)
			result, err := f.store.WriteAccountRecord(t.Context(), "retryable", c)
			require.Error(t, err)
			require.Empty(t, result)
			require.Equal(t, before, storeSpecSnapshot(t, f.db))
			_, err = f.db.ExecContext(t.Context(), `DROP TRIGGER fail_write`)
			require.NoError(t, err)
			_, err = f.store.WriteAccountRecord(t.Context(), "retryable", c)
			require.NoError(t, err)
		})
	}
}

func TestStoreConcurrentRecordCASAndDuplicateReceipts(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 0)
	c := recordCommand()
	var wg sync.WaitGroup
	results := make(chan json.RawMessage, 12)
	for range 12 {
		wg.Go(func() {
			r, err := f.store.WriteAccountRecord(t.Context(), "same", c)
			if err != nil {
				t.Error(err)
				return
			}
			results <- r
		})
	}
	wg.Wait()
	close(results)
	var first json.RawMessage
	for r := range results {
		if first == nil {
			first = r
		}
		require.Equal(t, first, r)
	}
	var n int
	require.NoError(t, f.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&n))
	require.Equal(t, 1, n)
	errs := make(chan error, 2)
	for k := 0; k < 2; k++ {
		wg.Go(func() {
			next := recordCommand()
			next.Action, next.ExpectedVersion, next.Reason = ledger.ReplaceOperation, "1", "correction"
			next.Entry.Flow = storeSpecMoney(ledger.Money(k + 2))
			_, err := f.store.WriteAccountRecord(t.Context(), fmt.Sprint("race-", k), next)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ledger.ErrVersion)
		}
	}
	require.Equal(t, 1, success)
}

func TestStoreCanceledContextHasNoEffects(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 0)
	before := storeSpecSnapshot(t, f.db)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r, err := f.store.WriteAccountRecord(ctx, "canceled", recordCommand())
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, r)
	_, err = f.store.PutCurrentHoldings(ctx, "a", "canceled-current", ledger.CurrentHoldingsInput{ExpectedVersion: "1", Cash: storeSpecMoney(1), Positions: []ledger.CurrentPosition{}})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, before, storeSpecSnapshot(t, f.db))
}
