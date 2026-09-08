package ledger

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChartSnapshotIncludesAllOperationsNotesAndOnlyCapitalFlows(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "1000.00", nil)
	f.account(t, "b", "CNY", "1000.00", nil)
	f.instrument(t, "stock")
	for i, kind := range []string{"deposit", "buy", "sell", "deposit_buy", "sell_withdraw", "transfer", "dividend", "withdrawal"} {
		op := httpCash(kind, "a", "2026-01-02", fmt.Sprint(i+1), kind, "10")
		if kind == "buy" || kind == "sell" || kind == "deposit_buy" || kind == "sell_withdraw" {
			op["instrument_id"], op["quantity"], op["price"] = "stock", "1", "1"
			if kind == "buy" || kind == "sell" {
				op["amount"] = "0"
			}
		}
		if kind == "transfer" {
			op["to_account_id"] = "b"
		}
		if kind == "dividend" {
			op["instrument_id"], op["cycle_id"] = "stock", "buy"
		}
		f.request(t, "POST", "/operations", kind, httpMutation(t, op, ""), 201)
	}
	basisEntry(t, f, "manual-independent", "2026-01-02", "cash_flow", `"999"`, `"9999"`)
	before := f.snapshot(t)
	b := basisRead(t, f, "a", "holdings", "&from=2026-01-02&to=2026-01-02")
	require.Len(t, b.Points, 9)
	require.Equal(t, Money(999900), *b.Closing.Assets)
	require.Equal(t, "989.00", b.NetFlow)
	for _, p := range b.Points[:8] {
		require.NotNil(t, p.Operation)
		require.NotNil(t, p.Record)
		require.Nil(t, p.Assets)
		require.False(t, p.Selected)
		require.Equal(t, p.Version, p.Operation.Version)
		if p.Operation.Operation.ID == "buy" || p.Operation.Operation.ID == "sell" || p.Operation.Operation.ID == "dividend" {
			require.Nil(t, p.Flow)
			require.Equal(t, "log", p.Status)
		} else {
			require.NotNil(t, p.Flow)
			require.Equal(t, "unavailable", p.Status)
		}
	}
	other := basisRead(t, f, "b", "holdings", "")
	require.Len(t, other.Points, 1)
	require.Equal(t, "10.00", other.NetFlow)
	reported := basisRead(t, f, "a", "reported", "")
	require.Len(t, reported.Points, 9)
	require.Nil(t, reported.Points[8].Operation)
	f.request(t, "HEAD", "/accounts/a/analysis-basis", "", "", 200)
	require.Equal(t, before, f.snapshot(t), "chart reads never write operations/receipts")
	var count int
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records WHERE origin IN ('currentrefresh','weekly')`).Scan(&count))
	require.Zero(t, count, "chart GET/HEAD must not save an observation")
}

func TestChartConcurrentReadsKeepFlowNoteAndVersionInOneSnapshot(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "1000.00", nil)
	op := Operation{ID: "deposit", AccountID: "a", Date: "2026-01-02", Sequence: 1, Kind: Deposit, Amount: 100}
	_, err := f.store.Write(t.Context(), Command{Action: CreateOperation, Key: "create", Operation: op, Note: "version-1", Reason: "Synthetic"})
	require.NoError(t, err)
	finished := make(chan error, 1)
	go func() {
		for version := int64(2); version <= 20; version++ {
			o := op
			o.Amount = Money(version * 100)
			_, err := f.store.Write(t.Context(), Command{Action: ReplaceOperation, Key: fmt.Sprint("edit-", version), Operation: o, ExpectedVersion: version - 1, Note: fmt.Sprint("version-", version), Reason: "Synthetic"})
			if err != nil {
				finished <- err
				return
			}
		}
		finished <- nil
	}()
	for i := 0; i < 20; i++ {
		b := basisRead(t, f, "a", "holdings", "")
		require.Len(t, b.Points, 1)
		p := b.Points[0]
		require.Equal(t, "version-"+p.Version, p.Operation.Note)
		require.Equal(t, p.Version+".00", p.Flow.String())
		require.Equal(t, p.Flow.String(), b.NetFlow)
		require.Equal(t, p.Version, p.Operation.Version)
	}
	require.NoError(t, <-finished)
	before := basisRead(t, f, "a", "holdings", "")
	op.Amount = 2000
	_, err = f.store.Write(t.Context(), Command{Action: ReplaceOperation, Key: "note-only", Operation: op, ExpectedVersion: 20, Note: "<img onerror=synthetic>new note", Reason: "Synthetic"})
	require.NoError(t, err)
	after := basisRead(t, f, "a", "holdings", "")
	require.NotEqual(t, before.Revision, after.Revision)
	require.Equal(t, before.NetFlow, after.NetFlow)
	require.Equal(t, "<img onerror=synthetic>new note", after.Points[0].Operation.Note)
}
