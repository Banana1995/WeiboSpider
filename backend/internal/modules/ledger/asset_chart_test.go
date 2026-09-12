package ledger

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestChartSnapshotContainsIndependentFactsNotHoldings(t *testing.T) {
	f := reportedFixture(t)
	manualSourceAccount(t, f.store, "b")
	basisEntry(t, f, "manual-base", "2024-01-01", "asset", "null", `"1000"`)
	basisEntry(t, f, "manual-in", "2024-01-02", "cash_flow", `"50"`, "null")
	basisEntry(t, f, "manual-out", "2024-01-03", "cash_flow", `"-10"`, "null")
	basisEntry(t, f, "manual-note", "2024-01-03", "log", "null", "null")
	before := basisRead(t, f, "a", "", "")
	require.Equal(t, Money(104000), *before.Closing.Assets)
	require.Equal(t, "40.00", before.NetFlow)
	require.Equal(t, "0.00", *before.Returns.Profit.Value)
	require.Len(t, before.Points, 4)
	require.Nil(t, before.Points[1].Record.TotalAssets)
	require.Equal(t, "log", before.Points[3].Status)
	putSource(t, f.store, "a", "direct-cash", "0", 999999)
	after := basisRead(t, f, "a", "", "")
	require.Equal(t, before, after)
	require.Empty(t, basisRead(t, f, "b", "", "").Points)
	snapshot := f.snapshot(t)
	f.request(t, "HEAD", "/accounts/a/analysis-basis", "", "", 200)
	require.Equal(t, snapshot, f.snapshot(t))
}

func TestChartConcurrentReadsKeepFlowNoteAndVersionInOneSnapshot(t *testing.T) {
	f := reportedFixture(t)
	amount := Money(100)
	c := AccountRecordCommand{Action: CreateOperation, AccountID: "a", ID: "manual-flow", Entry: &AccountEntry{Kind: "cash_flow", Date: "2024-01-02", Flow: &amount, Note: "version-1"}}
	_, err := f.store.WriteAccountRecord(t.Context(), "create", c)
	require.NoError(t, err)
	finished := make(chan error, 1)
	go func() {
		for version := 2; version <= 20; version++ {
			n := Money(version * 100)
			_, err := f.store.WriteAccountRecord(t.Context(), fmt.Sprint("edit-", version), AccountRecordCommand{Action: ReplaceOperation, AccountID: "a", ID: c.ID, ExpectedVersion: fmt.Sprint(version - 1), Reason: "Synthetic", Entry: &AccountEntry{Kind: "cash_flow", Date: "2024-01-02", Flow: &n, Note: fmt.Sprint("version-", version)}})
			if err != nil {
				finished <- err
				return
			}
		}
		finished <- nil
	}()
	for range 20 {
		b := basisRead(t, f, "a", "", "")
		require.Len(t, b.Points, 1)
		p := b.Points[0]
		require.Equal(t, "version-"+p.Version, p.Record.Note)
		require.Equal(t, p.Version+".00", p.Flow.String())
		require.Equal(t, p.Flow.String(), b.NetFlow)
	}
	require.NoError(t, <-finished)
	before := basisRead(t, f, "a", "", "")
	n := Money(2000)
	_, err = f.store.WriteAccountRecord(t.Context(), "note-only", AccountRecordCommand{Action: ReplaceOperation, AccountID: "a", ID: c.ID, ExpectedVersion: "20", Reason: "Synthetic", Entry: &AccountEntry{Kind: "cash_flow", Date: "2024-01-02", Flow: &n, Note: "<img onerror=synthetic>new note"}})
	require.NoError(t, err)
	after := basisRead(t, f, "a", "", "")
	require.NotEqual(t, before.Revision, after.Revision)
	require.Equal(t, before.NetFlow, after.NetFlow)
	require.Equal(t, "<img onerror=synthetic>new note", after.Points[0].Record.Note)
}
