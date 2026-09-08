package ledger_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
	"github.com/mattn/go-sqlite3"
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
	require.NoError(t, f.store.InitializeAccount(t.Context(), "Synthetic "+id, ledger.Opening{
		AccountID: id, Currency: ledger.CNY, Date: "2026-01-01", Cash: cash,
	}))
}

func (f *storeSpecFixture) instrument(t *testing.T, id string, currency ledger.Currency) ledger.Instrument {
	t.Helper()
	i := ledger.Instrument{ID: id, Market: "TEST", Code: id, Name: "Synthetic " + id, Currency: currency}
	require.NoError(t, f.store.AddInstrument(t.Context(), i))
	return i
}

func storeSpecMoney(v ledger.Money) *ledger.Money { return &v }

func storeSpecCreate(op ledger.Operation) ledger.Command {
	return ledger.Command{Action: ledger.CreateOperation, Key: "create-" + op.ID, Operation: op, Reason: "Synthetic initial entry"}
}

func storeSpecCash(id, account, date string, seq int64, kind ledger.Kind, amount ledger.Money) ledger.Operation {
	return ledger.Operation{ID: id, AccountID: account, Date: date, Sequence: seq, Kind: kind, Amount: amount}
}

func storeSpecTrade(id, date string, seq int64, kind ledger.Kind, quantity ledger.Quantity, price ledger.Price) ledger.Operation {
	return ledger.Operation{ID: id, AccountID: "a", InstrumentID: "i", Date: date, Sequence: seq, Kind: kind, Quantity: quantity, Price: price}
}

func storeSpecWrite(t *testing.T, s *ledger.Store, command ledger.Command) ledger.Record {
	t.Helper()
	record, err := s.Write(t.Context(), command)
	require.NoError(t, err)
	require.Equal(t, command.Operation.ID, record.Operation.ID)
	got, err := s.GetOperation(t.Context(), record.Operation.ID)
	require.NoError(t, err)
	require.Equal(t, record, got)
	return record
}

func storeSpecState(t *testing.T, s *ledger.Store) *ledger.Book {
	t.Helper()
	book, err := s.State(t.Context())
	require.NoError(t, err)
	require.NotNil(t, book)
	return book
}

func storeSpecRevisions(t *testing.T, s *ledger.Store, id string, want ...ledger.Revision) {
	t.Helper()
	got, err := s.Revisions(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// Compare every persisted value, not just balances: failed writes must leave no
// changed metadata, hidden audit entries, or receipts that prevent a later retry.
func storeSpecSnapshot(t *testing.T, db *database.DB) map[string][][]any {
	t.Helper()
	result := make(map[string][][]any)
	for _, table := range []string{"accounts", "instruments", "opening_positions", "operations", "account_records", "audit_log", "idempotency_receipts", "weekly_jobs"} {
		rows, err := db.QueryContext(t.Context(), "SELECT * FROM "+table+" ORDER BY 1, 2")
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
			for i, value := range values {
				if b, ok := value.([]byte); ok {
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

func storeSpecReject(t *testing.T, f *storeSpecFixture, command ledger.Command, cause error) {
	t.Helper()
	before := storeSpecSnapshot(t, f.db)
	book := storeSpecState(t, f.store)
	got, err := f.store.Write(t.Context(), command)
	if cause == nil {
		require.Error(t, err)
	} else {
		require.ErrorIs(t, err, cause)
	}
	require.Equal(t, ledger.Record{}, got)
	require.Equal(t, before, storeSpecSnapshot(t, f.db))
	require.Equal(t, book, storeSpecState(t, f.store))
}

func TestStoreSetupImmutableAndExactOpeningRoundTrip(t *testing.T) {
	f := newStoreSpecFixture(t)
	const exact = 9_007_199_254_740_993 // One beyond float64's exact integer range.
	i := f.instrument(t, "known", ledger.CNY)
	f.instrument(t, "unknown", ledger.USD)
	f.instrument(t, "zero", ledger.HKD)
	f.instrument(t, "independent", ledger.CNY)
	before := storeSpecSnapshot(t, f.db)
	require.NoError(t, f.store.AddInstrument(t.Context(), i))
	require.Equal(t, before, storeSpecSnapshot(t, f.db))
	for _, change := range []func(*ledger.Instrument){
		func(i *ledger.Instrument) { i.Currency = ledger.USD },
		func(i *ledger.Instrument) { i.Code = "changed" },
		func(i *ledger.Instrument) { i.Name = "Changed name" },
		func(i *ledger.Instrument) { i.ID = "duplicate-identity" },
	} {
		changed := i
		change(&changed)
		require.ErrorIs(t, f.store.AddInstrument(t.Context(), changed), ledger.ErrConflict)
		require.Equal(t, before, storeSpecSnapshot(t, f.db))
	}
	opening := ledger.Opening{AccountID: "a", Currency: ledger.CNY, Date: "2026-01-01", Cash: exact, Positions: []ledger.OpeningPosition{
		{InstrumentID: "known", Quantity: exact, Cost: storeSpecMoney(exact), DilutedBasis: storeSpecMoney(-exact)},
		{InstrumentID: "unknown", Quantity: 1_234_567},
		{InstrumentID: "zero", Quantity: 1, Cost: storeSpecMoney(0), DilutedBasis: storeSpecMoney(0)},
		{InstrumentID: "independent", Quantity: 2, Cost: storeSpecMoney(123)},
	}}
	require.NoError(t, f.store.InitializeAccount(t.Context(), "Synthetic opening", opening))
	before = storeSpecSnapshot(t, f.db)
	require.Len(t, before["accounts"], 1)
	require.Len(t, before["opening_positions"], 4)
	require.Empty(t, before["operations"])
	require.Empty(t, before["idempotency_receipts"])
	for _, change := range []func(*ledger.Opening){
		func(*ledger.Opening) {},
		func(o *ledger.Opening) { o.Cash = 1 },
		func(o *ledger.Opening) { o.Currency = ledger.USD },
		func(o *ledger.Opening) { o.Date = "2026-01-02" },
		func(o *ledger.Opening) { o.Positions = nil },
	} {
		changed := opening
		change(&changed)
		require.ErrorIs(t, f.store.InitializeAccount(t.Context(), "Replacement opening", changed), ledger.ErrConflict)
		require.Equal(t, before, storeSpecSnapshot(t, f.db))
	}
	f.reopen(t)
	book := storeSpecState(t, f.store)
	require.Len(t, book.Accounts, 1)
	require.Empty(t, book.Movements, "opening is not an external deposit")
	a := book.Accounts["a"]
	require.Equal(t, ledger.CNY, a.Currency)
	require.Equal(t, ledger.Money(exact), a.Cash)
	require.Len(t, a.Cycles, 4)
	for _, p := range opening.Positions {
		id := ledger.OpeningCycleID("a", p.InstrumentID)
		require.Equal(t, id, a.Positions[p.InstrumentID])
		cycle := a.Cycles[id]
		require.NotNil(t, cycle)
		require.Equal(t, p.Quantity, cycle.Quantity)
		require.Equal(t, p.Cost, cycle.RemainingCost)
		require.Equal(t, p.DilutedBasis, cycle.DilutedBasis)
		if p.Cost == nil {
			require.Nil(t, cycle.RealizedProfit)
		} else {
			require.Equal(t, storeSpecMoney(0), cycle.RealizedProfit)
		}
	}
	// Returned state is detached from the persisted opening.
	a.Cash = 0
	*a.Cycles[ledger.OpeningCycleID("a", "known")].RemainingCost = 0
	require.Equal(t, before, storeSpecSnapshot(t, f.db))
	require.Equal(t, ledger.Money(exact), storeSpecState(t, f.store).Accounts["a"].Cash)
	deposit := storeSpecCreate(storeSpecCash("exact-deposit", "a", "2026-01-02", 1, ledger.Deposit, exact))
	record := storeSpecWrite(t, f.store, deposit)
	require.Equal(t, ledger.Money(exact*2), storeSpecState(t, f.store).Accounts["a"].Cash)
	storeSpecRevisions(t, f.store, deposit.Operation.ID, ledger.Revision{Record: record, Reason: deposit.Reason})
	storeSpecWrite(t, f.store, storeSpecCreate(storeSpecCash("exact-withdrawal", "a", "2026-01-02", 2, ledger.Withdrawal, exact)))
	f.reopen(t)
	require.Equal(t, ledger.Money(exact), storeSpecState(t, f.store).Accounts["a"].Cash)
	got, err := f.store.GetOperation(t.Context(), deposit.Operation.ID)
	require.NoError(t, err)
	require.Equal(t, record, got)
}

func TestStoreOpeningFailureIsAtomic(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.instrument(t, "i", ledger.CNY)
	f.instrument(t, "j", ledger.CNY)
	opening := ledger.Opening{AccountID: "a", Currency: ledger.CNY, Date: "2026-01-01", Cash: 123, Positions: []ledger.OpeningPosition{
		{InstrumentID: "i", Quantity: 1}, {InstrumentID: "j", Quantity: 2},
	}}
	_, err := f.db.ExecContext(t.Context(), `CREATE TEMP TRIGGER store_spec_opening_failure
		BEFORE INSERT ON opening_positions WHEN NEW.instrument_id='j'
		BEGIN SELECT RAISE(ABORT, 'synthetic opening failure'); END`)
	require.NoError(t, err)
	before := storeSpecSnapshot(t, f.db)
	err = f.store.InitializeAccount(t.Context(), "Synthetic opening", opening)
	var sqliteErr sqlite3.Error
	require.ErrorAs(t, err, &sqliteErr)
	require.Equal(t, sqlite3.ErrConstraintTrigger, sqliteErr.ExtendedCode)
	require.Equal(t, before, storeSpecSnapshot(t, f.db))
	_, err = f.db.ExecContext(t.Context(), "DROP TRIGGER store_spec_opening_failure")
	require.NoError(t, err)
	require.NoError(t, f.store.InitializeAccount(t.Context(), "Synthetic opening", opening))
	require.Len(t, storeSpecState(t, f.store).Accounts["a"].Cycles, 2)
}

func TestStoreVersionSnapshotsVoidAndDurableOriginalReceipts(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 0)
	f.instrument(t, "i", ledger.USD)
	fx := ledger.FXSnapshot{Rate: 712_345_678, Date: "2026-01-01", Source: "synthetic-manual", FetchedAt: "2026-01-02T00:00:00.123456789Z"}
	create := storeSpecCreate(storeSpecTrade("stable-user-id", "2026-01-02", 1, ledger.DepositBuy, 1_500_000, 2_000_000))
	create.Operation.Amount, create.Operation.Fee, create.Operation.FX = 3_000, storeSpecMoney(1), &fx
	create.Note = "Original note\nwith detail"
	v1 := storeSpecWrite(t, f.store, create)
	require.Equal(t, int64(1), v1.Version)
	require.Equal(t, create.Operation, v1.Operation)
	require.Equal(t, create.Note, v1.Note)
	require.Equal(t, f.now.Format(time.RFC3339Nano), v1.CreatedAt)
	require.Equal(t, v1.CreatedAt, v1.UpdatedAt)
	require.Equal(t, ledger.Money(856), storeSpecState(t, f.store).Accounts["a"].Cash)
	storeSpecRevisions(t, f.store, create.Operation.ID, ledger.Revision{Record: v1, Reason: create.Reason})

	f.now = f.now.Add(time.Hour)
	replace := ledger.Command{Action: ledger.ReplaceOperation, Key: "replace-stable", Operation: create.Operation, ExpectedVersion: 1, Note: "Corrected note", Reason: "Correct quantity and funding"}
	replace.Operation.Quantity, replace.Operation.Amount = 2_000_000, 4_000
	v2 := storeSpecWrite(t, f.store, replace)
	require.Equal(t, int64(2), v2.Version)
	require.Equal(t, replace.Operation, v2.Operation)
	require.Equal(t, replace.Note, v2.Note)
	require.Equal(t, v1.CreatedAt, v2.CreatedAt)
	require.Equal(t, f.now.Format(time.RFC3339Nano), v2.UpdatedAt)
	require.NotEqual(t, v1.UpdatedAt, v2.UpdatedAt)
	require.Equal(t, ledger.Money(1_143), storeSpecState(t, f.store).Accounts["a"].Cash)
	storeSpecRevisions(t, f.store, create.Operation.ID, ledger.Revision{Record: v1, Reason: create.Reason}, ledger.Revision{Record: v2, Reason: replace.Reason})
	stale := replace
	stale.Key = "stale-replace"
	storeSpecReject(t, f, stale, ledger.ErrVersion)
	stale.ExpectedVersion = 99
	storeSpecReject(t, f, stale, ledger.ErrVersion)
	staleVoid := ledger.Command{Action: ledger.VoidOperation, Key: "stale-void", Operation: ledger.Operation{ID: create.Operation.ID}, ExpectedVersion: 1, Reason: "Stale void"}
	storeSpecReject(t, f, staleVoid, ledger.ErrVersion)

	f.now = f.now.Add(time.Hour)
	void := ledger.Command{Action: ledger.VoidOperation, Key: "void-stable", Operation: ledger.Operation{ID: create.Operation.ID}, ExpectedVersion: 2, Reason: "Remove mistaken entry"}
	v3 := storeSpecWrite(t, f.store, void)
	wantVoid := v2.Operation
	wantVoid.Voided = true
	require.Equal(t, wantVoid, v3.Operation, "void retains every original business field including FX and fee")
	require.Equal(t, v2.Note, v3.Note)
	require.Equal(t, int64(3), v3.Version)
	require.Equal(t, v1.CreatedAt, v3.CreatedAt)
	require.Equal(t, f.now.Format(time.RFC3339Nano), v3.UpdatedAt)
	storeSpecRevisions(t, f.store, create.Operation.ID,
		ledger.Revision{Record: v1, Reason: create.Reason}, ledger.Revision{Record: v2, Reason: replace.Reason}, ledger.Revision{Record: v3, Reason: void.Reason})
	book := storeSpecState(t, f.store)
	require.Zero(t, book.Accounts["a"].Cash)
	require.Empty(t, book.Accounts["a"].Cycles)
	require.Empty(t, book.Movements)
	restore := replace
	restore.Key, restore.ExpectedVersion = "restore-forbidden", 3
	storeSpecReject(t, f, restore, ledger.ErrVoided)
	voidAgain := void
	voidAgain.Key, voidAgain.ExpectedVersion = "void-again", 3
	storeSpecReject(t, f, voidAgain, ledger.ErrVoided)
	duplicateID := create
	duplicateID.Key = "reuse-stable-id"
	storeSpecReject(t, f, duplicateID, ledger.ErrConflict)

	for _, reopen := range []bool{false, true} {
		if reopen {
			f.now = f.now.Add(24 * time.Hour)
			f.reopen(t)
		}
		before := storeSpecSnapshot(t, f.db)
		for _, retry := range []struct {
			command ledger.Command
			record  ledger.Record
		}{{create, v1}, {replace, v2}, {void, v3}} {
			got, err := f.store.Write(t.Context(), retry.command)
			require.NoError(t, err)
			require.Equal(t, retry.record, got, "retry returns the exact original response, not the latest version")
		}
		require.Equal(t, before, storeSpecSnapshot(t, f.db))
		got, err := f.store.GetOperation(t.Context(), create.Operation.ID)
		require.NoError(t, err)
		require.Equal(t, v3, got)
	}
}

func TestStoreIdempotencyConflictsIncludeEntireCommand(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 0)
	create := storeSpecCreate(storeSpecCash("deposit", "a", "2026-01-02", 1, ledger.Deposit, 100))
	v1 := storeSpecWrite(t, f.store, create)
	before := storeSpecSnapshot(t, f.db)
	got, err := f.store.Write(t.Context(), create)
	require.NoError(t, err)
	require.Equal(t, v1, got)
	require.Equal(t, before, storeSpecSnapshot(t, f.db))
	for _, tc := range []struct {
		name   string
		change func(*ledger.Command)
	}{
		{"amount", func(c *ledger.Command) { c.Operation.Amount++ }},
		{"stable ID", func(c *ledger.Command) { c.Operation.ID = "other" }},
		{"date", func(c *ledger.Command) { c.Operation.Date = "2026-01-03" }},
		{"sequence", func(c *ledger.Command) { c.Operation.Sequence++ }},
		{"note", func(c *ledger.Command) { c.Note = "Different note" }},
		{"reason", func(c *ledger.Command) { c.Reason = "Different reason" }},
		{"replace action", func(c *ledger.Command) { c.Action, c.ExpectedVersion = ledger.ReplaceOperation, 1 }},
		{"void action", func(c *ledger.Command) {
			c.Action, c.ExpectedVersion, c.Operation = ledger.VoidOperation, 1, ledger.Operation{ID: "deposit"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := create
			tc.change(&changed)
			storeSpecReject(t, f, changed, ledger.ErrIdempotency)
		})
	}
	replace := ledger.Command{Action: ledger.ReplaceOperation, Key: "replace-key", Operation: create.Operation, ExpectedVersion: 1, Reason: "Correction"}
	replace.Operation.Amount = 200
	storeSpecWrite(t, f.store, replace)
	changed := replace
	changed.ExpectedVersion = 2
	storeSpecReject(t, f, changed, ledger.ErrIdempotency)
	changed = replace
	changed.Action, changed.ExpectedVersion = ledger.CreateOperation, 0
	storeSpecReject(t, f, changed, ledger.ErrIdempotency)
	otherID := storeSpecCreate(storeSpecCash("other", "a", "2026-01-03", 1, ledger.Deposit, 100))
	otherID.Key = replace.Key
	storeSpecReject(t, f, otherID, ledger.ErrIdempotency)
	otherKey := create
	otherKey.Key = "different-key-same-ID"
	storeSpecReject(t, f, otherKey, ledger.ErrConflict)
}

func TestStoreCommandValidationAndMissingRecords(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 100)
	create := storeSpecCreate(storeSpecCash("deposit", "a", "2026-01-02", 1, ledger.Deposit, 100))
	storeSpecWrite(t, f.store, create)
	for _, tc := range []struct {
		name   string
		change func(*ledger.Command)
	}{
		{"empty key", func(c *ledger.Command) { c.Key = "" }},
		{"empty stable ID", func(c *ledger.Command) { c.Operation.ID = "" }},
		{"empty reason", func(c *ledger.Command) { c.Reason = "" }},
		{"blank reason", func(c *ledger.Command) { c.Reason = " \t\n" }},
		{"unknown action", func(c *ledger.Command) { c.Action = "restore" }},
		{"create positive expected", func(c *ledger.Command) { c.ExpectedVersion = 1 }},
		{"create negative expected", func(c *ledger.Command) { c.ExpectedVersion = -1 }},
		{"replace zero expected", func(c *ledger.Command) { c.Action = ledger.ReplaceOperation }},
		{"replace negative expected", func(c *ledger.Command) { c.Action, c.ExpectedVersion = ledger.ReplaceOperation, -1 }},
		{"void zero expected", func(c *ledger.Command) { c.Action, c.Operation = ledger.VoidOperation, ledger.Operation{ID: "deposit"} }},
		{"void negative expected", func(c *ledger.Command) {
			c.Action, c.Operation, c.ExpectedVersion = ledger.VoidOperation, ledger.Operation{ID: "deposit"}, -1
		}},
		{"void full operation", func(c *ledger.Command) { c.Action, c.ExpectedVersion = ledger.VoidOperation, 1 }},
		{"void note", func(c *ledger.Command) {
			c.Action, c.ExpectedVersion, c.Operation, c.Note = ledger.VoidOperation, 1, ledger.Operation{ID: "deposit"}, "Not permitted"
		}},
		{"caller sets voided", func(c *ledger.Command) { c.Operation.Voided = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := create
			command.Key = "invalid-command"
			tc.change(&command)
			storeSpecReject(t, f, command, ledger.ErrOperation)
		})
	}
	for _, action := range []ledger.WriteAction{ledger.ReplaceOperation, ledger.VoidOperation} {
		command := ledger.Command{Action: action, Key: "missing-key", Operation: ledger.Operation{ID: "missing"}, ExpectedVersion: 1, Reason: "Missing operation"}
		if action == ledger.ReplaceOperation {
			command.Operation = storeSpecCash("missing", "a", "2026-01-02", 2, ledger.Deposit, 100)
		}
		storeSpecReject(t, f, command, ledger.ErrNotFound)
	}
	record, err := f.store.GetOperation(t.Context(), "missing")
	require.ErrorIs(t, err, ledger.ErrNotFound)
	require.Equal(t, ledger.Record{}, record)
	revisions, err := f.store.Revisions(t.Context(), "missing")
	require.ErrorIs(t, err, ledger.ErrNotFound)
	require.Nil(t, revisions)
}

func TestStoreFrozenFXAndNullableFeeRoundTrip(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 10_000)
	f.instrument(t, "i", ledger.USD)
	fx := ledger.FXSnapshot{Rate: 712_345_678, Date: "2026-01-01", Source: "synthetic-first-snapshot", FetchedAt: "2026-01-02T00:00:00.123456789Z"}
	first := storeSpecCreate(storeSpecTrade("first-fx", "2026-01-02", 1, ledger.Buy, 1_234_567, 2_345_678))
	first.Operation.FX = &fx
	storeSpecWrite(t, f.store, first)
	frozen, err := f.store.GetOperation(t.Context(), first.Operation.ID)
	require.NoError(t, err)
	require.Equal(t, first.Operation, frozen.Operation)
	require.Nil(t, frozen.Operation.Fee)
	// Neither caller-owned pointers nor a later supplied rate may alter history.
	fx.Rate, fx.Date, fx.Source, fx.FetchedAt = 800_000_000, "2026-01-02", "synthetic-new-snapshot", "2026-01-03T00:00:00Z"
	second := storeSpecCreate(storeSpecTrade("second-fx", "2026-01-03", 1, ledger.Buy, 1_500_000, 2_000_000))
	second.Operation.FX, second.Operation.Fee = &fx, storeSpecMoney(0)
	secondRecord := storeSpecWrite(t, f.store, second)
	require.Equal(t, storeSpecMoney(0), secondRecord.Operation.Fee)
	fx.Rate = 900_000_000
	*second.Operation.Fee = 99
	f.reopen(t)
	got, err := f.store.GetOperation(t.Context(), first.Operation.ID)
	require.NoError(t, err)
	require.Equal(t, frozen, got)
	storeSpecRevisions(t, f.store, first.Operation.ID, ledger.Revision{Record: frozen, Reason: first.Reason})
	got, err = f.store.GetOperation(t.Context(), second.Operation.ID)
	require.NoError(t, err)
	require.Equal(t, ledger.Rate(800_000_000), got.Operation.FX.Rate)
	require.Equal(t, "synthetic-new-snapshot", got.Operation.FX.Source)
	require.Equal(t, storeSpecMoney(0), got.Operation.Fee)
	book := storeSpecState(t, f.store)
	// First notional rounds to 290 cents, then FX to 2066; second costs 2400.
	require.Equal(t, ledger.Money(5_534), book.Accounts["a"].Cash)
	cycle := book.Accounts["a"].Cycles["first-fx"]
	require.NotNil(t, cycle)
	require.Equal(t, ledger.Quantity(2_734_567), cycle.Quantity)
	require.Equal(t, storeSpecMoney(4_466), cycle.RemainingCost)
	require.Len(t, book.Movements, 2)
	require.False(t, book.Movements[0].FeeProvided)
	require.True(t, book.Movements[1].FeeProvided)
	got.Operation.FX.Source = "mutated-read-result"
	storeSpecRevisions(t, f.store, first.Operation.ID, ledger.Revision{Record: frozen, Reason: first.Reason})
	unchanged, err := f.store.GetOperation(t.Context(), second.Operation.ID)
	require.NoError(t, err)
	require.Equal(t, "synthetic-new-snapshot", unchanged.Operation.FX.Source)
}

func TestStoreCombinationCreatesAreAtomic(t *testing.T) {
	for _, kind := range []ledger.Kind{ledger.DepositBuy, ledger.Transfer} {
		t.Run(string(kind), func(t *testing.T) {
			f := newStoreSpecFixture(t)
			f.account(t, "a", 0)
			f.account(t, "b", 0)
			f.instrument(t, "i", ledger.CNY)
			var command ledger.Command
			if kind == ledger.DepositBuy {
				command = storeSpecCreate(storeSpecTrade("combo", "2026-01-02", 1, kind, 1_000_000, 10_000_000))
				command.Operation.Amount, command.Operation.Fee = 999, storeSpecMoney(0)
			} else {
				command = storeSpecCreate(storeSpecCash("combo", "a", "2026-01-02", 1, kind, 999))
				command.Operation.ToAccountID = "b"
			}
			storeSpecReject(t, f, command, ledger.ErrInsufficientCash)
			missing, err := f.store.GetOperation(t.Context(), "combo")
			require.ErrorIs(t, err, ledger.ErrNotFound)
			require.Equal(t, ledger.Record{}, missing)
			// The identical failed request/key becomes legal after earlier funding.
			storeSpecWrite(t, f.store, storeSpecCreate(storeSpecCash("fund", "a", "2026-01-01", 1, ledger.Deposit, 1_000)))
			record := storeSpecWrite(t, f.store, command)
			require.Equal(t, int64(1), record.Version)
			storeSpecRevisions(t, f.store, "combo", ledger.Revision{Record: record, Reason: command.Reason})
			book := storeSpecState(t, f.store)
			require.Len(t, book.Movements, 3)
			if kind == ledger.DepositBuy {
				require.Equal(t, ledger.Money(999), book.Accounts["a"].Cash)
				require.Equal(t, ledger.Money(0), book.Accounts["b"].Cash)
				require.Equal(t, storeSpecMoney(1_000), book.Accounts["a"].Cycles["combo"].RemainingCost)
				require.Equal(t, []ledger.Movement{
					{OperationID: "combo", Date: "2026-01-02", AccountID: "a", Kind: ledger.Deposit, CashDelta: 999, CapitalFlow: 999},
					{OperationID: "combo", Date: "2026-01-02", AccountID: "a", Kind: ledger.Buy, CashDelta: -1_000, FeeProvided: true},
				}, book.Movements[1:])
			} else {
				require.Equal(t, ledger.Money(1), book.Accounts["a"].Cash)
				require.Equal(t, ledger.Money(999), book.Accounts["b"].Cash)
				require.Equal(t, []ledger.Movement{
					{OperationID: "combo", Date: "2026-01-02", AccountID: "a", Kind: ledger.Transfer, CashDelta: -999, CapitalFlow: -999, Counterparty: "b"},
					{OperationID: "combo", Date: "2026-01-02", AccountID: "b", Kind: ledger.Transfer, CashDelta: 999, CapitalFlow: 999, Counterparty: "a"},
				}, book.Movements[1:])
			}
			persisted := storeSpecSnapshot(t, f.db)
			require.Len(t, persisted["operations"], 2, "a combined action is one operation, not independently saved legs")
			require.Len(t, persisted["idempotency_receipts"], 2)
			var operationAudits int
			require.NoError(t, f.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log WHERE entity_type='operation'`).Scan(&operationAudits))
			require.Equal(t, 2, operationAudits)
			f.reopen(t)
			require.Equal(t, book, storeSpecState(t, f.store))
		})
	}
}

func TestStoreCorrectingMiddleSaleReplaysLaterCost(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 50_000)
	f.instrument(t, "i", ledger.CNY)
	for _, op := range []ledger.Operation{
		storeSpecTrade("buy-first", "2026-01-02", 1, ledger.Buy, 10_000_000, 10_000_000),
		storeSpecTrade("sell-middle", "2026-01-03", 1, ledger.Sell, 4_000_000, 20_000_000),
		storeSpecTrade("buy-later", "2026-01-04", 1, ledger.Buy, 4_000_000, 30_000_000),
		storeSpecTrade("sell-last", "2026-01-05", 1, ledger.Sell, 5_000_000, 40_000_000),
	} {
		storeSpecWrite(t, f.store, storeSpecCreate(op))
	}
	book := storeSpecState(t, f.store)
	require.Equal(t, ledger.Money(56_000), book.Accounts["a"].Cash)
	require.Equal(t, &ledger.Cycle{ID: "buy-first", InstrumentID: "i", Quantity: 5_000_000,
		RemainingCost: storeSpecMoney(9_000), DilutedBasis: storeSpecMoney(-6_000), RealizedProfit: storeSpecMoney(15_000)}, book.Accounts["a"].Cycles["buy-first"])
	last, err := f.store.GetOperation(t.Context(), "sell-last")
	require.NoError(t, err)
	replace := ledger.Command{Action: ledger.ReplaceOperation, Key: "correct-middle-sale", ExpectedVersion: 1, Reason: "Correct sold quantity",
		Operation: storeSpecTrade("sell-middle", "2026-01-03", 1, ledger.Sell, 2_000_000, 20_000_000)}
	storeSpecWrite(t, f.store, replace)
	book = storeSpecState(t, f.store)
	require.Equal(t, ledger.Money(52_000), book.Accounts["a"].Cash)
	require.Equal(t, &ledger.Cycle{ID: "buy-first", InstrumentID: "i", Quantity: 7_000_000,
		RemainingCost: storeSpecMoney(11_667), DilutedBasis: storeSpecMoney(-2_000), RealizedProfit: storeSpecMoney(13_667)}, book.Accounts["a"].Cycles["buy-first"])
	storeSpecWrite(t, f.store, ledger.Command{Action: ledger.VoidOperation, Key: "void-middle-sale", Operation: ledger.Operation{ID: "sell-middle"}, ExpectedVersion: 2, Reason: "Remove sale"})
	f.reopen(t)
	book = storeSpecState(t, f.store)
	require.Equal(t, ledger.Money(48_000), book.Accounts["a"].Cash)
	require.Equal(t, &ledger.Cycle{ID: "buy-first", InstrumentID: "i", Quantity: 9_000_000,
		RemainingCost: storeSpecMoney(14_143), DilutedBasis: storeSpecMoney(2_000), RealizedProfit: storeSpecMoney(12_143)}, book.Accounts["a"].Cycles["buy-first"])
	got, err := f.store.GetOperation(t.Context(), "sell-last")
	require.NoError(t, err)
	require.Equal(t, last, got, "replay changes derived cost, not downstream business facts or versions")
	storeSpecRevisions(t, f.store, "sell-last", ledger.Revision{Record: last, Reason: storeSpecCreate(last.Operation).Reason})
}

func TestStoreEarlyBuyCorrectionsRollbackAndFailedKeyCanRetry(t *testing.T) {
	for _, consequence := range []string{"oversell", "overdraft"} {
		for _, action := range []ledger.WriteAction{ledger.ReplaceOperation, ledger.VoidOperation} {
			t.Run(consequence+"/"+string(action), func(t *testing.T) {
				f := newStoreSpecFixture(t)
				f.account(t, "a", 0)
				f.instrument(t, "i", ledger.CNY)
				early := storeSpecCreate(storeSpecTrade("early-buy", "2026-01-02", 1, ledger.DepositBuy, 5_000_000, 1_000_000))
				early.Operation.Amount = 1_000
				v1 := storeSpecWrite(t, f.store, early)
				var downstream ledger.Operation
				cause := ledger.ErrInsufficientStock
				if consequence == "oversell" {
					downstream = storeSpecTrade("downstream", "2026-01-03", 1, ledger.Sell, 4_000_000, 2_000_000)
				} else {
					cause = ledger.ErrInsufficientCash
					downstream = storeSpecCash("downstream", "a", "2026-01-03", 1, ledger.Withdrawal, 400)
				}
				storeSpecWrite(t, f.store, storeSpecCreate(downstream))
				change := ledger.Command{Action: action, Key: "retry-after-repair", Operation: early.Operation, ExpectedVersion: 1, Note: "Corrected early entry", Reason: "Correct early purchase"}
				if action == ledger.VoidOperation {
					change.Operation, change.Note = ledger.Operation{ID: "early-buy"}, ""
				} else if consequence == "oversell" {
					change.Operation.Quantity = 3_000_000
				} else {
					change.Operation.Price = 2_000_000
				}
				storeSpecReject(t, f, change, cause)
				// Check that rejection identifies the dependent entry, not the valid edit.
				got, err := f.store.Write(t.Context(), change)
				require.Equal(t, ledger.Record{}, got)
				var operationErr *ledger.OperationError
				require.ErrorAs(t, err, &operationErr)
				require.Equal(t, downstream.ID, operationErr.ID)
				storeSpecRevisions(t, f.store, "early-buy", ledger.Revision{Record: v1, Reason: early.Reason})
				storeSpecWrite(t, f.store, ledger.Command{Action: ledger.VoidOperation, Key: "repair-downstream", Operation: ledger.Operation{ID: downstream.ID}, ExpectedVersion: 1, Reason: "Remove dependent entry"})
				v2 := storeSpecWrite(t, f.store, change)
				require.Equal(t, int64(2), v2.Version)
				storeSpecRevisions(t, f.store, "early-buy", ledger.Revision{Record: v1, Reason: early.Reason}, ledger.Revision{Record: v2, Reason: change.Reason})
				before := storeSpecSnapshot(t, f.db)
				got, err = f.store.Write(t.Context(), change)
				require.NoError(t, err)
				require.Equal(t, v2, got)
				require.Equal(t, before, storeSpecSnapshot(t, f.db))
				book := storeSpecState(t, f.store)
				if action == ledger.VoidOperation {
					require.Zero(t, book.Accounts["a"].Cash)
					require.Empty(t, book.Accounts["a"].Cycles)
				} else if consequence == "oversell" {
					require.Equal(t, ledger.Money(700), book.Accounts["a"].Cash)
					require.Equal(t, ledger.Quantity(3_000_000), book.Accounts["a"].Cycles["early-buy"].Quantity)
				} else {
					require.Zero(t, book.Accounts["a"].Cash)
					require.Equal(t, storeSpecMoney(1_000), book.Accounts["a"].Cycles["early-buy"].RemainingCost)
				}
			})
		}
	}
}

func TestStoreGlobalDateSequenceConflictsAndReordering(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 0)
	f.account(t, "b", 0)
	first := storeSpecCreate(storeSpecCash("first", "a", "2026-01-02", 1, ledger.Deposit, 100))
	storeSpecWrite(t, f.store, first)
	second := storeSpecCreate(storeSpecCash("second", "b", "2026-01-03", 1, ledger.Deposit, 200))
	storeSpecWrite(t, f.store, second)
	third := storeSpecCreate(storeSpecCash("third", "b", "2026-01-02", 2, ledger.Deposit, 300))
	storeSpecWrite(t, f.store, third)
	createCollision := storeSpecCreate(storeSpecCash("collision", "b", "2026-01-02", 1, ledger.Deposit, 10))
	storeSpecReject(t, f, createCollision, ledger.ErrOperation)
	changeDate := ledger.Command{Action: ledger.ReplaceOperation, Key: "change-date", Operation: second.Operation, ExpectedVersion: 1, Reason: "Move business date"}
	changeDate.Operation.Date = "2026-01-02"
	storeSpecReject(t, f, changeDate, ledger.ErrOperation)
	changeSequence := ledger.Command{Action: ledger.ReplaceOperation, Key: "change-sequence", Operation: third.Operation, ExpectedVersion: 1, Reason: "Move daily sequence"}
	changeSequence.Operation.Sequence = 1
	storeSpecReject(t, f, changeSequence, ledger.ErrOperation)
	storeSpecWrite(t, f.store, ledger.Command{Action: ledger.VoidOperation, Key: "void-first", Operation: ledger.Operation{ID: "first"}, ExpectedVersion: 1, Reason: "Void first"})
	storeSpecReject(t, f, createCollision, ledger.ErrOperation)
	storeSpecReject(t, f, changeDate, ledger.ErrOperation)
	// Voiding does not release a historical order slot; moving an active entry does.
	move := ledger.Command{Action: ledger.ReplaceOperation, Key: "move-third", Operation: third.Operation, ExpectedVersion: 1, Reason: "Move to a free slot"}
	move.Operation.Date = "2026-01-04"
	storeSpecWrite(t, f.store, move)
	createCollision.Operation.Sequence = 2
	storeSpecWrite(t, f.store, createCollision)
	book := storeSpecState(t, f.store)
	require.Equal(t, []string{"collision", "second", "third"}, []string{book.Movements[0].OperationID, book.Movements[1].OperationID, book.Movements[2].OperationID})

	storeSpecWrite(t, f.store, storeSpecCreate(storeSpecCash("spend", "b", "2026-01-05", 1, ledger.Withdrawal, 500)))
	move.Key, move.ExpectedVersion, move.Operation.Date = "move-after-spend", 2, "2026-01-06"
	storeSpecReject(t, f, move, ledger.ErrInsufficientCash)
}

func TestStoreTransferCorrectionsAffectBothAccounts(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 1_000)
	f.account(t, "b", 0)
	f.account(t, "c", 0)
	create := storeSpecCreate(storeSpecCash("transfer", "a", "2026-01-02", 1, ledger.Transfer, 600))
	create.Operation.ToAccountID = "b"
	v1 := storeSpecWrite(t, f.store, create)
	book := storeSpecState(t, f.store)
	require.Equal(t, ledger.Money(400), book.Accounts["a"].Cash)
	require.Equal(t, ledger.Money(600), book.Accounts["b"].Cash)
	change := ledger.Command{Action: ledger.ReplaceOperation, Key: "change-transfer", Operation: create.Operation, ExpectedVersion: 1, Note: "Correct both legs", Reason: "Correct transfer amount"}
	change.Operation.Amount = 800
	v2 := storeSpecWrite(t, f.store, change)
	book = storeSpecState(t, f.store)
	require.Equal(t, ledger.Money(200), book.Accounts["a"].Cash)
	require.Equal(t, ledger.Money(800), book.Accounts["b"].Cash)
	require.Equal(t, []ledger.Movement{
		{OperationID: "transfer", Date: "2026-01-02", AccountID: "a", Kind: ledger.Transfer, CashDelta: -800, CapitalFlow: -800, Counterparty: "b"},
		{OperationID: "transfer", Date: "2026-01-02", AccountID: "b", Kind: ledger.Transfer, CashDelta: 800, CapitalFlow: 800, Counterparty: "a"},
	}, book.Movements)
	void := ledger.Command{Action: ledger.VoidOperation, Key: "void-transfer", Operation: ledger.Operation{ID: "transfer"}, ExpectedVersion: 2, Reason: "Remove both transfer legs"}
	v3 := storeSpecWrite(t, f.store, void)
	book = storeSpecState(t, f.store)
	require.Equal(t, ledger.Money(1_000), book.Accounts["a"].Cash)
	require.Zero(t, book.Accounts["b"].Cash)
	require.Empty(t, book.Movements)
	want := v2.Operation
	want.Voided = true
	require.Equal(t, want, v3.Operation)
	require.Equal(t, v2.Note, v3.Note)
	storeSpecRevisions(t, f.store, "transfer", ledger.Revision{Record: v1, Reason: create.Reason}, ledger.Revision{Record: v2, Reason: change.Reason}, ledger.Revision{Record: v3, Reason: void.Reason})

	// A full replacement can change destination without leaving a stale credit.
	create.Operation.ID, create.Operation.Sequence, create.Key = "reroute", 2, "create-reroute"
	storeSpecWrite(t, f.store, create)
	change.Operation, change.ExpectedVersion, change.Key = create.Operation, 1, "reroute-destination"
	change.Operation.ToAccountID = "c"
	storeSpecWrite(t, f.store, change)
	f.reopen(t)
	book = storeSpecState(t, f.store)
	require.Equal(t, ledger.Money(400), book.Accounts["a"].Cash)
	require.Zero(t, book.Accounts["b"].Cash)
	require.Equal(t, ledger.Money(600), book.Accounts["c"].Cash)
	require.Len(t, book.Movements, 2)
	require.Equal(t, "c", book.Movements[0].Counterparty)
	require.Equal(t, "c", book.Movements[1].AccountID)
}

func TestStoreTransferDownstreamConsumptionRejectsCorrections(t *testing.T) {
	for _, side := range []string{"recipient", "sender"} {
		t.Run(side, func(t *testing.T) {
			f := newStoreSpecFixture(t)
			f.account(t, "a", 1_000)
			f.account(t, "b", 0)
			f.account(t, "c", 0)
			transfer := storeSpecCreate(storeSpecCash("transfer", "a", "2026-01-02", 1, ledger.Transfer, 600))
			transfer.Operation.ToAccountID = "b"
			original := storeSpecWrite(t, f.store, transfer)
			account, amount := "b", ledger.Money(500)
			if side == "sender" {
				account, amount = "a", 300
			}
			storeSpecWrite(t, f.store, storeSpecCreate(storeSpecCash("spend", account, "2026-01-03", 1, ledger.Withdrawal, amount)))
			change := ledger.Command{Action: ledger.ReplaceOperation, Key: "transfer-retry", Operation: transfer.Operation, ExpectedVersion: 1, Reason: "Correct transfer"}
			if side == "recipient" {
				change.Operation.Amount = 400
				storeSpecReject(t, f, ledger.Command{Action: ledger.VoidOperation, Key: "void-consumed-transfer", Operation: ledger.Operation{ID: "transfer"}, ExpectedVersion: 1, Reason: "Void consumed transfer"}, ledger.ErrInsufficientCash)
				reroute := change
				reroute.Key, reroute.Operation.Amount, reroute.Operation.ToAccountID = "reroute-consumed-transfer", 600, "c"
				storeSpecReject(t, f, reroute, ledger.ErrInsufficientCash)
			} else {
				change.Operation.Amount = 800
			}
			storeSpecReject(t, f, change, ledger.ErrInsufficientCash)
			storeSpecRevisions(t, f.store, "transfer", ledger.Revision{Record: original, Reason: transfer.Reason})
			storeSpecWrite(t, f.store, ledger.Command{Action: ledger.VoidOperation, Key: "remove-consumption", Operation: ledger.Operation{ID: "spend"}, ExpectedVersion: 1, Reason: "Remove downstream consumption"})
			corrected := storeSpecWrite(t, f.store, change)
			require.Equal(t, int64(2), corrected.Version)
			book := storeSpecState(t, f.store)
			require.Equal(t, ledger.Money(1_000)-change.Operation.Amount, book.Accounts["a"].Cash)
			require.Equal(t, change.Operation.Amount, book.Accounts["b"].Cash)
		})
	}
}

func TestStorePersistenceFailuresRollbackOperationRevisionAndReceipt(t *testing.T) {
	for _, table := range []string{"audit_log", "idempotency_receipts"} {
		for _, kind := range []ledger.Kind{ledger.DepositBuy, ledger.Transfer} {
			for _, action := range []ledger.WriteAction{ledger.CreateOperation, ledger.ReplaceOperation, ledger.VoidOperation} {
				t.Run(table+"/"+string(kind)+"/"+string(action), func(t *testing.T) {
					f := newStoreSpecFixture(t)
					f.account(t, "a", 2_000)
					f.account(t, "b", 0)
					f.instrument(t, "i", ledger.CNY)
					var op ledger.Operation
					if kind == ledger.DepositBuy {
						op = storeSpecTrade("atomic", "2026-01-02", 1, kind, 1_000_000, 10_000_000)
						op.Amount = 1_200
					} else {
						op = storeSpecCash("atomic", "a", "2026-01-02", 1, kind, 1_200)
						op.ToAccountID = "b"
					}
					create := storeSpecCreate(op)
					command := create
					var wantRevisions []ledger.Revision
					if action != ledger.CreateOperation {
						original := storeSpecWrite(t, f.store, create)
						wantRevisions = append(wantRevisions, ledger.Revision{Record: original, Reason: create.Reason})
						command.Action, command.Key, command.ExpectedVersion = action, "atomic-change", 1
						command.Operation.Amount, command.Note, command.Reason = 1_500, "Replacement note", "Synthetic correction"
						if action == ledger.VoidOperation {
							command.Operation, command.Note = ledger.Operation{ID: op.ID}, ""
						}
					}
					_, err := f.db.ExecContext(t.Context(), "CREATE TEMP TRIGGER store_spec_write_failure BEFORE INSERT ON "+table+`
						BEGIN SELECT RAISE(ABORT, 'synthetic persistence failure'); END`)
					require.NoError(t, err)
					before := storeSpecSnapshot(t, f.db)
					book := storeSpecState(t, f.store)
					got, err := f.store.Write(t.Context(), command)
					var sqliteErr sqlite3.Error
					require.ErrorAs(t, err, &sqliteErr)
					// Match the trigger code, not its deliberately sanitized message.
					require.Equal(t, sqlite3.ErrConstraintTrigger, sqliteErr.ExtendedCode, "must reach the targeted persistence stage")
					require.Equal(t, ledger.Record{}, got)
					require.Equal(t, before, storeSpecSnapshot(t, f.db))
					require.Equal(t, book, storeSpecState(t, f.store))
					if action == ledger.CreateOperation {
						got, err = f.store.GetOperation(t.Context(), op.ID)
						require.ErrorIs(t, err, ledger.ErrNotFound)
						require.Equal(t, ledger.Record{}, got)
					} else {
						storeSpecRevisions(t, f.store, op.ID, wantRevisions...)
					}
					_, err = f.db.ExecContext(t.Context(), "DROP TRIGGER store_spec_write_failure")
					require.NoError(t, err)
					committed := storeSpecWrite(t, f.store, command)
					require.Equal(t, command.ExpectedVersion+1, committed.Version)
					wantRevisions = append(wantRevisions, ledger.Revision{Record: committed, Reason: command.Reason})
					storeSpecRevisions(t, f.store, op.ID, wantRevisions...)
					after := storeSpecSnapshot(t, f.db)
					require.Len(t, after["operations"], 1)
					require.Len(t, after["idempotency_receipts"], len(wantRevisions))
					var operationAudits int
					require.NoError(t, f.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log WHERE entity_type='operation'`).Scan(&operationAudits))
					require.Equal(t, len(wantRevisions), operationAudits)
					got, err = f.store.Write(t.Context(), command)
					require.NoError(t, err)
					require.Equal(t, committed, got)
					require.Equal(t, after, storeSpecSnapshot(t, f.db))
				})
			}
		}
	}
}

func TestStoreConcurrentWrites(t *testing.T) {
	for _, scenario := range []string{"duplicate-key", "different-withdrawal-keys", "same-expected-version"} {
		t.Run(scenario, func(t *testing.T) {
			f := newStoreSpecFixture(t)
			f.account(t, "a", 1_000)
			base := storeSpecCreate(storeSpecCash("concurrent", "a", "2026-01-02", 1, ledger.Withdrawal, 800))
			var original ledger.Record
			if scenario == "same-expected-version" {
				base.Operation.Kind, base.Operation.Amount = ledger.Deposit, 100
				original = storeSpecWrite(t, f.store, base)
			}
			const workers = 12
			commands := make([]ledger.Command, workers)
			for i := range commands {
				commands[i] = base
				switch scenario {
				case "different-withdrawal-keys":
					commands[i].Key = fmt.Sprintf("withdraw-key-%d", i)
					commands[i].Operation.ID = fmt.Sprintf("withdraw-%d", i)
					commands[i].Operation.Sequence = int64(i + 1)
				case "same-expected-version":
					commands[i].Action, commands[i].ExpectedVersion = ledger.ReplaceOperation, 1
					commands[i].Key = fmt.Sprintf("replace-key-%d", i)
					commands[i].Operation.Amount = ledger.Money(200 + i)
					commands[i].Note, commands[i].Reason = fmt.Sprintf("Note %d", i), fmt.Sprintf("Correction %d", i)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			start := make(chan struct{})
			var ready, finished sync.WaitGroup
			ready.Add(workers)
			finished.Add(workers)
			records, errs := make([]ledger.Record, workers), make([]error, workers)
			for i := range workers {
				go func() {
					defer finished.Done()
					// Independent Store instances must coordinate through the database.
					s := ledger.NewStore(f.db, func() time.Time { return f.now })
					ready.Done()
					<-start
					records[i], errs[i] = s.Write(ctx, commands[i])
				}()
			}
			ready.Wait()
			close(start)
			finished.Wait()
			successes, winner := 0, -1
			for i, err := range errs {
				if err == nil {
					successes++
					winner = i
					continue
				}
				require.Equal(t, ledger.Record{}, records[i])
				if scenario == "same-expected-version" {
					require.ErrorIs(t, err, ledger.ErrVersion)
				} else if scenario == "different-withdrawal-keys" {
					require.ErrorIs(t, err, ledger.ErrInsufficientCash)
				} else {
					require.NoError(t, err)
				}
			}
			if scenario == "duplicate-key" {
				require.Equal(t, workers, successes)
				for _, record := range records {
					require.Equal(t, records[0], record)
					require.Equal(t, int64(1), record.Version)
				}
			} else {
				require.Equal(t, 1, successes)
			}
			require.GreaterOrEqual(t, winner, 0)
			persisted := storeSpecSnapshot(t, f.db)
			require.Len(t, persisted["operations"], 1)
			book := storeSpecState(t, f.store)
			if scenario == "same-expected-version" {
				require.Equal(t, int64(2), records[winner].Version)
				require.Equal(t, ledger.Money(1_000)+commands[winner].Operation.Amount, book.Accounts["a"].Cash)
				require.Len(t, persisted["idempotency_receipts"], 2)
				storeSpecRevisions(t, f.store, base.Operation.ID, ledger.Revision{Record: original, Reason: base.Reason}, ledger.Revision{Record: records[winner], Reason: commands[winner].Reason})
			} else {
				require.Equal(t, ledger.Money(200), book.Accounts["a"].Cash)
				require.Len(t, persisted["idempotency_receipts"], 1)
				storeSpecRevisions(t, f.store, commands[winner].Operation.ID, ledger.Revision{Record: records[winner], Reason: commands[winner].Reason})
			}
			var operationAudits int
			require.NoError(t, f.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log WHERE entity_type='operation'`).Scan(&operationAudits))
			require.Equal(t, len(persisted["idempotency_receipts"]), operationAudits)
			got, err := f.store.Write(t.Context(), commands[winner])
			require.NoError(t, err)
			require.Equal(t, records[winner], got)
			require.Equal(t, persisted, storeSpecSnapshot(t, f.db))
		})
	}
}

func TestStoreCanceledContextReturnsNoResultsOrDatabaseChanges(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 1_000)
	create := storeSpecCreate(storeSpecCash("existing", "a", "2026-01-02", 1, ledger.Deposit, 100))
	storeSpecWrite(t, f.store, create)
	before := storeSpecSnapshot(t, f.db)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, f.store.AddInstrument(ctx, ledger.Instrument{ID: "i", Market: "TEST", Code: "i", Name: "Synthetic", Currency: ledger.CNY}), context.Canceled)
	require.ErrorIs(t, f.store.InitializeAccount(ctx, "Synthetic canceled", ledger.Opening{AccountID: "b", Currency: ledger.CNY, Date: "2026-01-01"}), context.Canceled)
	for _, command := range []ledger.Command{
		create, // Even the receipt lookup honors cancellation.
		storeSpecCreate(storeSpecCash("new", "a", "2026-01-02", 2, ledger.Deposit, 100)),
		{Action: ledger.ReplaceOperation, Key: "canceled-replace", Operation: create.Operation, ExpectedVersion: 1, Reason: "Canceled correction"},
		{Action: ledger.VoidOperation, Key: "canceled-void", Operation: ledger.Operation{ID: "existing"}, ExpectedVersion: 1, Reason: "Canceled void"},
	} {
		got, err := f.store.Write(ctx, command)
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, ledger.Record{}, got)
		require.Equal(t, before, storeSpecSnapshot(t, f.db))
	}
	book, err := f.store.State(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, book)
	record, err := f.store.GetOperation(ctx, "existing")
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, ledger.Record{}, record)
	revisions, err := f.store.Revisions(ctx, "existing")
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, revisions)
	require.Equal(t, before, storeSpecSnapshot(t, f.db))

	// The injectable clock runs inside a new Write transaction, after its receipt
	// lookup. Cancel there as well, rather than testing only the preflight path.
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	s := ledger.NewStore(f.db, func() time.Time {
		cancel()
		return f.now
	})
	command := storeSpecCreate(storeSpecCash("cancel-during-write", "a", "2026-01-03", 1, ledger.Deposit, 100))
	record, err = s.Write(ctx, command)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, ledger.Record{}, record)
	require.Equal(t, before, storeSpecSnapshot(t, f.db))
	committed := storeSpecWrite(t, f.store, command)
	require.Equal(t, int64(1), committed.Version)
}

func TestStoreRejectsInvalidUTF8WithoutPoisoningTheLedger(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 10_000)
	f.instrument(t, "i", ledger.USD)
	bad := string([]byte{'s', 0xff})
	op := storeSpecTrade("fx-text", "2026-01-02", 1, ledger.Buy, 1_000_000, 1_000_000)
	op.FX = &ledger.FXSnapshot{Rate: 700_000_000, Date: "2026-01-02", Source: bad, FetchedAt: "2026-01-02T12:00:00Z"}
	command := storeSpecCreate(op)
	storeSpecReject(t, f, command, ledger.ErrOperation)
	op.FX.Source = "synthetic"
	committed := storeSpecWrite(t, f.store, command)
	// Caller-owned pointer mutation must not change the successful response.
	op.FX.Rate = 800_000_000
	require.Equal(t, ledger.Rate(700_000_000), committed.Operation.FX.Rate)
	f.reopen(t)
	require.Equal(t, ledger.Money(9_300), storeSpecState(t, f.store).Accounts["a"].Cash)
	op.FX.Rate = 700_000_000
	retried, err := f.store.Write(t.Context(), command)
	require.NoError(t, err)
	require.Equal(t, committed, retried)
}
