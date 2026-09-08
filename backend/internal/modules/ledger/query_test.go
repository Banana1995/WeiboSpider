package ledger_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
	"github.com/stretchr/testify/require"
)

func TestQueryIdentityPagesAndEmptyArrays(t *testing.T) {
	f := newStoreSpecFixture(t)
	accounts, err := f.store.ListAccounts(t.Context(), ledger.PageQuery{Limit: 30})
	require.NoError(t, err)
	require.Equal(t, []ledger.AccountInfo{}, accounts)
	instruments, err := f.store.ListInstruments(t.Context(), ledger.PageQuery{Limit: 30})
	require.NoError(t, err)
	require.Equal(t, []ledger.Instrument{}, instruments)
	operations, err := f.store.ListOperations(t.Context(), ledger.OperationQuery{Limit: 30})
	require.NoError(t, err)
	require.Equal(t, []ledger.Record{}, operations)
	for _, value := range []any{accounts, instruments, operations} {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		require.Equal(t, "[]", string(encoded))
	}
	for _, id := range []string{"c", "a", "b"} {
		f.account(t, id, 9_007_199_254_740_993)
		f.instrument(t, id, ledger.CNY)
	}
	for _, tc := range []struct {
		after string
		limit int
		ids   []string
	}{
		{"", 2, []string{"a", "b"}},
		{"b", 2, []string{"c"}},
		{"aa", 1, []string{"b"}},
		{"c", 100, []string{}},
	} {
		query := ledger.PageQuery{Limit: tc.limit, After: tc.after}
		accounts, err := f.store.ListAccounts(t.Context(), query)
		require.NoError(t, err)
		instruments, err := f.store.ListInstruments(t.Context(), query)
		require.NoError(t, err)
		accountIDs, instrumentIDs := make([]string, 0), make([]string, 0)
		for _, a := range accounts {
			accountIDs = append(accountIDs, a.ID)
			require.Equal(t, ledger.AccountInfo{AccountingMode: "holdings", ID: a.ID, Name: "Synthetic " + a.ID, Currency: ledger.CNY,
				OpeningDate: "2026-01-01", OpeningCash: 9_007_199_254_740_993, Version: 1}, a)
		}
		for _, i := range instruments {
			instrumentIDs = append(instrumentIDs, i.ID)
			require.Equal(t, ledger.Instrument{ID: i.ID, Market: "TEST", Code: i.ID, Name: "Synthetic " + i.ID, Currency: ledger.CNY}, i)
		}
		require.Equal(t, tc.ids, accountIDs)
		require.Equal(t, tc.ids, instrumentIDs)
	}
}

func TestQueryOperationsFiltersAndCursor(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 1000)
	f.account(t, "b", 0)
	f.account(t, "empty", 0)
	transfer := storeSpecCash("transfer", "a", "2026-01-02", 2, ledger.Transfer, 500)
	transfer.ToAccountID = "b"
	want := make(map[string]ledger.Record)
	for _, op := range []ledger.Operation{
		storeSpecCash("old", "a", "2026-01-01", 9, ledger.Deposit, 9_007_199_254_740_993),
		transfer,
		storeSpecCash("first", "a", "2026-01-02", 1, ledger.Deposit, 100),
		storeSpecCash("spent", "b", "2026-01-02", 3, ledger.Withdrawal, 200),
		storeSpecCash("void", "a", "2026-01-03", 1, ledger.Deposit, 200),
	} {
		want[op.ID] = storeSpecWrite(t, f.store, storeSpecCreate(op))
	}
	want["void"] = storeSpecWrite(t, f.store, ledger.Command{Action: ledger.VoidOperation, Key: "void-it",
		Operation: ledger.Operation{ID: "void"}, ExpectedVersion: 1, Reason: "Synthetic removal"})
	for _, tc := range []struct {
		name  string
		query ledger.OperationQuery
		ids   []string
	}{
		{"default includes voided", ledger.OperationQuery{}, []string{"void", "spent", "transfer", "first", "old"}},
		{"all", ledger.OperationQuery{Status: "all"}, []string{"void", "spent", "transfer", "first", "old"}},
		{"active", ledger.OperationQuery{Status: "active"}, []string{"spent", "transfer", "first", "old"}},
		{"voided", ledger.OperationQuery{Status: "voided"}, []string{"void"}},
		{"source", ledger.OperationQuery{AccountID: "a"}, []string{"void", "transfer", "first", "old"}},
		{"target", ledger.OperationQuery{AccountID: "b"}, []string{"spent", "transfer"}},
		{"empty account", ledger.OperationQuery{AccountID: "empty"}, []string{}},
		{"from inclusive", ledger.OperationQuery{From: "2026-01-03"}, []string{"void"}},
		{"to inclusive", ledger.OperationQuery{To: "2026-01-01"}, []string{"old"}},
		{"exact date", ledger.OperationQuery{From: "2026-01-02", To: "2026-01-02"}, []string{"spent", "transfer", "first"}},
		{"date sequence cursor", ledger.OperationQuery{BeforeDate: "2026-01-02", BeforeSequence: 3}, []string{"transfer", "first", "old"}},
		{"combined target", ledger.OperationQuery{AccountID: "b", From: "2026-01-02", To: "2026-01-02", Status: "active", BeforeDate: "2026-01-02", BeforeSequence: 3}, []string{"transfer"}},
		{"combined source excludes later void", ledger.OperationQuery{AccountID: "a", From: "2026-01-02", To: "2026-01-02", Status: "active", BeforeDate: "2026-01-02", BeforeSequence: 2}, []string{"first"}},
		{"past end", ledger.OperationQuery{BeforeDate: "2026-01-01", BeforeSequence: 9}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.query.Limit = 100
			got, err := f.store.ListOperations(t.Context(), tc.query)
			require.NoError(t, err)
			expected := make([]ledger.Record, 0)
			for _, id := range tc.ids {
				expected = append(expected, want[id])
			}
			require.Equal(t, expected, got)
		})
	}
	query := ledger.OperationQuery{Limit: 2}
	ids := make([]string, 0)
	for {
		page, err := f.store.ListOperations(t.Context(), query)
		require.NoError(t, err)
		require.NotNil(t, page)
		require.LessOrEqual(t, len(page), 2)
		if len(page) == 0 {
			break
		}
		for _, record := range page {
			ids = append(ids, record.Operation.ID)
		}
		last := page[len(page)-1].Operation
		query.BeforeDate, query.BeforeSequence = last.Date, last.Sequence
	}
	require.Equal(t, []string{"void", "spent", "transfer", "first", "old"}, ids)
}

func TestQueryValidationAndNoParameterLeakage(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 0)
	storeSpecWrite(t, f.store, storeSpecCreate(storeSpecCash("one", "a", "2026-01-02", 1, ledger.Deposit, 1)))
	before := storeSpecSnapshot(t, f.db)
	for _, limit := range []int{-1, 0, 101, math.MaxInt} {
		_, err := f.store.ListAccounts(t.Context(), ledger.PageQuery{Limit: limit})
		require.ErrorIs(t, err, ledger.ErrQuery)
		_, err = f.store.ListInstruments(t.Context(), ledger.PageQuery{Limit: limit})
		require.ErrorIs(t, err, ledger.ErrQuery)
		_, err = f.store.ListOperations(t.Context(), ledger.OperationQuery{Limit: limit})
		require.ErrorIs(t, err, ledger.ErrQuery)
		_, err = f.store.RevisionPage(t.Context(), "one", 0, limit)
		require.ErrorIs(t, err, ledger.ErrQuery)
	}
	for _, input := range []string{"' OR 1=1 --", "a%", "a;DROP TABLE accounts", strings.Repeat("x", 129), "a b"} {
		_, err := f.store.ListAccounts(t.Context(), ledger.PageQuery{Limit: 30, After: input})
		require.ErrorIs(t, err, ledger.ErrQuery)
		require.NotContains(t, err.Error(), input)
		_, err = f.store.ListInstruments(t.Context(), ledger.PageQuery{Limit: 30, After: input})
		require.ErrorIs(t, err, ledger.ErrQuery)
		_, _, err = f.store.Account(t.Context(), input)
		require.ErrorIs(t, err, ledger.ErrQuery)
		_, err = f.store.RevisionPage(t.Context(), input, 0, 30)
		require.ErrorIs(t, err, ledger.ErrQuery)
		for _, query := range []ledger.OperationQuery{{AccountID: input}, {From: input}, {To: input}, {Status: input}, {BeforeDate: input, BeforeSequence: 1}} {
			query.Limit = 30
			got, err := f.store.ListOperations(t.Context(), query)
			require.ErrorIs(t, err, ledger.ErrQuery)
			require.Nil(t, got)
			require.NotContains(t, err.Error(), input)
		}
	}
	for _, date := range []string{"2026-02-29", "2026-13-01", "2026-1-02", "0000-01-01", "2026-01-01T00:00:00Z"} {
		for _, query := range []ledger.OperationQuery{{From: date}, {To: date}, {BeforeDate: date, BeforeSequence: 1}} {
			query.Limit = 30
			_, err := f.store.ListOperations(t.Context(), query)
			require.ErrorIs(t, err, ledger.ErrQuery)
		}
	}
	for _, query := range []ledger.OperationQuery{
		{From: "2026-01-03", To: "2026-01-02"}, {Status: "ACTIVE"},
		{BeforeDate: "2026-01-01"}, {BeforeDate: "2026-01-01", BeforeSequence: -1},
		{BeforeSequence: 1}, {BeforeSequence: -1},
	} {
		query.Limit = 30
		_, err := f.store.ListOperations(t.Context(), query)
		require.ErrorIs(t, err, ledger.ErrQuery)
	}
	_, err := f.store.RevisionPage(t.Context(), "one", -1, 30)
	require.ErrorIs(t, err, ledger.ErrQuery)
	_, _, err = f.store.Account(t.Context(), "")
	require.ErrorIs(t, err, ledger.ErrQuery)
	_, err = f.store.RevisionPage(t.Context(), "", 0, 30)
	require.ErrorIs(t, err, ledger.ErrQuery)
	info, state, err := f.store.Account(t.Context(), "unknown")
	require.ErrorIs(t, err, ledger.ErrNotFound)
	require.Equal(t, ledger.AccountInfo{}, info)
	require.Nil(t, state)
	_, err = f.store.ListOperations(t.Context(), ledger.OperationQuery{AccountID: "unknown", From: "9999-12-31", Limit: 30})
	require.ErrorIs(t, err, ledger.ErrNotFound)
	_, err = f.store.RevisionPage(t.Context(), "unknown", 0, 30)
	require.ErrorIs(t, err, ledger.ErrNotFound)
	require.Equal(t, before, storeSpecSnapshot(t, f.db))
}

func TestQueryAccountFullReplayAndExactValues(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 0)
	f.instrument(t, "i", ledger.CNY)
	f.instrument(t, "unknown", ledger.USD)
	const exact = 9_007_199_254_740_993
	require.NoError(t, f.store.InitializeAccount(t.Context(), "Synthetic target", ledger.Opening{
		AccountID: "b", Currency: ledger.CNY, Date: "2026-01-01", Cash: exact,
		Positions: []ledger.OpeningPosition{
			{InstrumentID: "i", Quantity: exact, Cost: storeSpecMoney(exact), DilutedBasis: storeSpecMoney(-exact)},
			{InstrumentID: "unknown", Quantity: 1_234_567},
		},
	}))
	transfer := storeSpecCash("transfer", "a", "2026-01-02", 2, ledger.Transfer, 500)
	transfer.ToAccountID = "b"
	for _, op := range []ledger.Operation{
		storeSpecCash("fund", "a", "2026-01-02", 1, ledger.Deposit, 1000), transfer,
		{ID: "buy", AccountID: "b", InstrumentID: "i", Date: "2026-01-02", Sequence: 3, Kind: ledger.Buy, Quantity: 1_000_000, Price: 1_000_000, Fee: storeSpecMoney(0)},
	} {
		storeSpecWrite(t, f.store, storeSpecCreate(op))
	}
	f.reopen(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	info, state, err := f.store.Account(ctx, "b")
	require.NoError(t, err)
	require.Equal(t, ledger.AccountInfo{AccountingMode: "holdings", ID: "b", Name: "Synthetic target", Currency: ledger.CNY, OpeningDate: "2026-01-01", OpeningCash: exact, Version: 1}, info)
	require.Equal(t, storeSpecState(t, f.store).Accounts["b"], state)
	require.Equal(t, ledger.Money(exact+400), state.Cash)
	cycle := state.Cycles[state.Positions["i"]]
	require.Equal(t, ledger.Quantity(exact+1_000_000), cycle.Quantity)
	require.Equal(t, storeSpecMoney(exact+100), cycle.RemainingCost)
	require.Equal(t, storeSpecMoney(-exact+100), cycle.DilutedBasis)
	require.Nil(t, state.Cycles[state.Positions["unknown"]].RemainingCost)
	operations, err := f.store.ListOperations(ctx, ledger.OperationQuery{AccountID: "b", Limit: 1})
	require.NoError(t, err)
	require.Len(t, operations, 1)
	require.Equal(t, storeSpecMoney(0), operations[0].Operation.Fee)
	require.Equal(t, "buy", operations[0].Operation.ID)
	state.Cash = 0
	*cycle.RemainingCost = 0
	_, reread, err := f.store.Account(ctx, "b")
	require.NoError(t, err)
	require.Equal(t, storeSpecState(t, f.store).Accounts["b"], reread)
	// A future global record must fail replay, even for an unrelated account.
	storeSpecWrite(t, f.store, storeSpecCreate(storeSpecCash("later", "a", "2026-09-06", 1, ledger.Deposit, 1)))
	f.now = time.Date(2026, 9, 5, 15, 59, 59, 0, time.UTC)
	info, state, err = f.store.Account(ctx, "b")
	require.ErrorIs(t, err, ledger.ErrOperation)
	require.Equal(t, ledger.AccountInfo{}, info)
	require.Nil(t, state)
	f.now = f.now.Add(time.Second) // Beijing midnight includes the same global record.
	_, state, err = f.store.Account(ctx, "b")
	require.NoError(t, err)
	require.Equal(t, reread, state)
}

func TestQueryAccountSnapshotBlocksConcurrentChange(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 100)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	written := make(chan error, 1)
	s := ledger.NewStore(f.db, func() time.Time {
		waiting := f.db.Stats().WaitCount
		// The clock is called after reading metadata. Queue a competing operation
		// there and verify the replay still owns the same single connection.
		go func() {
			_, err := f.store.Write(ctx, storeSpecCreate(storeSpecCash("concurrent", "a", "2026-01-02", 1, ledger.Deposit, 100)))
			written <- err
		}()
		require.Eventually(t, func() bool { return f.db.Stats().WaitCount > waiting }, time.Second, time.Millisecond)
		return f.now
	})
	info, state, err := s.Account(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, "Synthetic a", info.Name)
	require.Equal(t, ledger.Money(100), info.OpeningCash)
	require.Equal(t, info.OpeningCash, state.Cash)
	require.NoError(t, <-written)
	info, state, err = f.store.Account(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, "Synthetic a", info.Name)
	require.Equal(t, ledger.Money(100), info.OpeningCash)
	require.Equal(t, ledger.Money(200), state.Cash)
}

func TestQueryRevisionPages(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 0)
	command := storeSpecCreate(storeSpecCash("op", "a", "2026-01-02", 1, ledger.Deposit, 9_007_199_254_740_993))
	want := make([]ledger.Revision, 0)
	for version := int64(1); version <= 4; version++ {
		command.Key = fmt.Sprintf("revision-%d", version)
		command.Reason = fmt.Sprintf("Synthetic revision %d", version)
		if version > 1 {
			command.Action, command.ExpectedVersion = ledger.ReplaceOperation, version-1
			command.Operation.Amount++
		}
		if version == 4 {
			command.Action, command.Operation = ledger.VoidOperation, ledger.Operation{ID: "op"}
		}
		f.now = f.now.Add(time.Minute)
		want = append(want, ledger.Revision{Record: storeSpecWrite(t, f.store, command), Reason: command.Reason})
	}
	for _, tc := range []struct {
		after int64
		limit int
		want  []ledger.Revision
	}{
		{0, 2, want[:2]}, {2, 1, want[2:3]}, {3, 100, want[3:]},
		{0, 100, want}, {4, 30, []ledger.Revision{}}, {math.MaxInt64, 1, []ledger.Revision{}},
	} {
		got, err := f.store.RevisionPage(t.Context(), "op", tc.after, tc.limit)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	all, err := f.store.Revisions(t.Context(), "op")
	require.NoError(t, err)
	require.Equal(t, want, all)
}

func TestQueryCorruptRevisionFailsClosed(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE audit_log SET after_json='{}' WHERE entity_type='operation' AND version=1`,
		`UPDATE audit_log SET after_json=json_remove(after_json,'$.operation.Voided') WHERE entity_type='operation' AND version=1`,
		`UPDATE audit_log SET after_json=json_set(after_json,'$.operation.ID','other') WHERE entity_type='operation' AND version=1`,
		`UPDATE audit_log SET after_json=json_set(after_json,'$.version',2) WHERE entity_type='operation' AND version=1`,
		`UPDATE audit_log SET recorded_at='2026-01-01T00:00:00Z' WHERE entity_type='operation' AND version=1`,
		`UPDATE audit_log SET metadata_json=json_set(metadata_json,'$.reason',char(9)) WHERE entity_type='operation' AND version=1`,
		`DELETE FROM idempotency_receipts WHERE key='create-op'; DELETE FROM audit_log WHERE entity_type='operation' AND version=1`,
	} {
		t.Run(mutation, func(t *testing.T) {
			f := newStoreSpecFixture(t)
			f.account(t, "a", 0)
			command := storeSpecCreate(storeSpecCash("op", "a", "2026-01-02", 1, ledger.Deposit, 100))
			storeSpecWrite(t, f.store, command)
			command.Action, command.Key, command.ExpectedVersion = ledger.ReplaceOperation, "replace", 1
			storeSpecWrite(t, f.store, command)
			ledger.AllowAuditCorruptionForTest(t, f.db)
			_, err := f.db.ExecContext(t.Context(), mutation)
			require.NoError(t, err)
			got, err := f.store.RevisionPage(t.Context(), "op", 0, 1)
			require.ErrorIs(t, err, ledger.ErrCorrupt)
			require.Nil(t, got)
		})
	}
}

func TestQueryOperationIntegrityAndSanitizedErrors(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE operations SET amount_minor=200`,
		`UPDATE audit_log SET after_json='{}' WHERE entity_type='operation'`,
		`DELETE FROM idempotency_receipts; DELETE FROM audit_log WHERE entity_type='operation'`,
		`UPDATE audit_log SET after_json=json_set(after_json,'$.operation.Amount','synthetic-private-value') WHERE entity_type='operation'`,
	} {
		t.Run(mutation, func(t *testing.T) {
			f := newStoreSpecFixture(t)
			f.account(t, "a", 0)
			f.account(t, "b", 0)
			storeSpecWrite(t, f.store, storeSpecCreate(storeSpecCash("op", "a", "2026-01-02", 1, ledger.Deposit, 100)))
			ledger.AllowAuditCorruptionForTest(t, f.db)
			_, err := f.db.ExecContext(t.Context(), mutation)
			require.NoError(t, err)
			got, err := f.store.ListOperations(t.Context(), ledger.OperationQuery{Limit: 30})
			require.ErrorIs(t, err, ledger.ErrCorrupt)
			require.NotContains(t, err.Error(), "synthetic-private-value")
			require.Nil(t, got)
			info, state, err := f.store.Account(t.Context(), "b")
			require.ErrorIs(t, err, ledger.ErrCorrupt)
			require.Equal(t, ledger.AccountInfo{}, info)
			require.Nil(t, state)
		})
	}
}

func TestQueryCanceledContext(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 0)
	storeSpecWrite(t, f.store, storeSpecCreate(storeSpecCash("op", "a", "2026-01-02", 1, ledger.Deposit, 100)))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	accounts, err := f.store.ListAccounts(ctx, ledger.PageQuery{Limit: 30})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, accounts)
	instruments, err := f.store.ListInstruments(ctx, ledger.PageQuery{Limit: 30})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, instruments)
	operations, err := f.store.ListOperations(ctx, ledger.OperationQuery{AccountID: "a", Limit: 30})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, operations)
	revisions, err := f.store.RevisionPage(ctx, "op", 0, 30)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, revisions)
	info, state, err := f.store.Account(ctx, "a")
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, ledger.AccountInfo{}, info)
	require.Nil(t, state)
}
