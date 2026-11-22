package ledger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPAccountFlowLifecycleThroughSDK(t *testing.T) {
	s, _ := mcpFixture(t)
	client, _ := mcpClient(t, s)
	ctx := t.Context()
	book, err := s.StockBook(ctx, MCPBookQuery{AccountID: "a"})
	require.NoError(t, err)
	in := MCPFlowInput{AccountID: "a", RequestID: "transfer", Currency: CNY, Date: "2026-09-05", Direction: "out", Amount: "80.25", SourceRef: "statement-1"}
	r := mcpCall[MCPFlowWriteResult](t, client, flowCreateTool, in)
	require.Equal(t, "-80.25", r.Record.Flow.String())
	require.Equal(t, "1", r.Record.Version)
	note := "checked"
	edit := MCPFlowInput{AccountID: "a", RequestID: "correct", RecordID: r.Record.ID, ExpectedVersion: "1", Currency: CNY, Reason: "statement", Note: &note}
	updated := mcpCall[MCPFlowWriteResult](t, client, flowEditTool, edit)
	require.Equal(t, "2", updated.Record.Version)
	require.Equal(t, "-80.25", updated.Record.Flow.String())
	lookup := mcpCall[MCPReceiptResult](t, client, "ledger_get_write_result", MCPReceiptQuery{"a", "transfer"})
	require.Nil(t, lookup.Result)
	require.Equal(t, r, *lookup.AccountFlow)
	require.Equal(t, updated, mcpCall[MCPFlowWriteResult](t, client, flowEditTool, edit))
	require.Equal(t, r, mcpCall[MCPFlowWriteResult](t, client, flowCreateTool, in))
	old := edit
	old.RequestID = "stale"
	_, err = s.writeFlow(ctx, flowEditTool, old)
	require.Equal(t, "version_conflict", mcpToolFailure(err).Code)
	in.Amount = "81.25"
	_, err = s.writeFlow(ctx, flowCreateTool, in)
	require.ErrorIs(t, err, ErrIdempotency)
	_, err = s.Write(ctx, "ledger_record_trade", mcpTrade("transfer", "0"))
	require.ErrorIs(t, err, ErrIdempotency)
	in.Currency = HKD
	in.RequestID = "wrong-currency"
	_, err = s.writeFlow(ctx, flowCreateTool, in)
	require.Error(t, err)
	contract := mcpCall[MCPWriteContract](t, client, "ledger_get_write_contract", MCPContractQuery{ToolName: flowEditTool, AccountID: "a", RecordID: r.Record.ID})
	require.Equal(t, "2", contract.KnownContext["expected_version"])
	require.NotContains(t, contract.KnownContext, "cash_anchor")
	page := mcpCall[MCPFlowPage](t, client, flowListTool, MCPFlowQuery{AccountID: "a", From: "2026-09-05", To: "2026-09-05"})
	require.Equal(t, []AccountRecord{updated.Record}, page.Items)
	detail := mcpCall[MCPFlowPage](t, client, flowListTool, MCPFlowQuery{AccountID: "a", RecordID: r.Record.ID, IncludeSummary: true})
	require.Equal(t, "statement", *detail.RecordReason)
	require.NotNil(t, detail.Summary)
	summary, err := s.Store.EffectiveSummary(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, "80.25", summary.TotalOut)
	require.Equal(t, 1, summary.RowCount)
	after, err := s.StockBook(ctx, MCPBookQuery{AccountID: "a"})
	require.NoError(t, err)
	require.Equal(t, book, after)
}

func TestMCPFlowEditPreservesImportedEvidenceAndInvalidatesPage(t *testing.T) {
	s, _ := mcpFixture(t)
	client, _ := mcpClient(t, s)
	ctx := t.Context()
	w := workbook(t, [7]string{"转入转出", "2020-01-01", "1", "20", "old note", "", ""}, [7]string{"转入转出", "2020-02-01", "2", "30", "second", "", ""})
	confirmWorkbook(t, s.Store, "a", "first", false, w)
	p := mcpCall[MCPFlowPage](t, client, flowListTool, MCPFlowQuery{AccountID: "a", Limit: 1})
	require.NotNil(t, p.NextCursor)
	p2, err := s.AccountFlows(ctx, MCPFlowQuery{AccountID: "a", Cursor: *p.NextCursor})
	require.NoError(t, err)
	require.Len(t, p2.Items, 1)
	r := p2.Items[0]
	require.NotNil(t, r.Original)
	in := MCPFlowInput{AccountID: "a", RequestID: "edit-import", Currency: CNY, RecordID: r.ID, ExpectedVersion: r.Version, Direction: "in", Amount: "1.50", Reason: "bank statement"}
	got := mcpCall[MCPFlowWriteResult](t, client, flowEditTool, in)
	require.Equal(t, r.Original, got.Record.Original)
	require.Equal(t, r.TotalAssets, got.Record.TotalAssets)
	require.Equal(t, r.Note, got.Record.Note)
	require.Equal(t, "1.50", got.Record.Flow.String())
	_, err = s.AccountFlows(ctx, MCPFlowQuery{AccountID: "a", Cursor: *p.NextCursor})
	require.Equal(t, "stale_cursor", mcpToolFailure(err).Code)
	require.Zero(t, confirmWorkbook(t, s.Store, "a", "after-edit", false, w).ImportedCount)
}
