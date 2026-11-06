package ledger

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func contractRequirement(t *testing.T, value MCPWriteContract, field string) MCPConditionalRequirement {
	t.Helper()
	for _, r := range value.ConditionalRequirements {
		if r.Field == field {
			return r
		}
	}
	t.Fatalf("no requirement for %s", field)
	return MCPConditionalRequirement{}
}

func TestMCPContractMatchesPublishedSchemaAndIsReadOnly(t *testing.T) {
	s, _ := mcpFixture(t)
	s.Instruments = mcpSearchFunc(func(context.Context, string) ([]InstrumentSearchItem, error) {
		t.Error("contract discovery must not perform provider IO")
		return nil, nil
	})
	c, _ := mcpClient(t, s)
	list, err := c.ListTools(t.Context(), nil)
	require.NoError(t, err)
	before := mcpLogicalSnapshot(t, s.Store)
	for _, tool := range list.Tools {
		if tool.Annotations.ReadOnlyHint {
			continue
		}
		contract := mcpCall[MCPWriteContract](t, c, "ledger_get_write_contract", MCPContractQuery{ToolName: tool.Name})
		advertised, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		discovered, err := json.Marshal(contract.InputSchema)
		require.NoError(t, err)
		require.JSONEq(t, string(advertised), string(discovered))
		require.Equal(t, "Asia/Shanghai", contract.Timezone)
		require.Contains(t, contract.UnresolvedContext, "account_id")
		for _, example := range contract.Examples {
			require.NoError(t, mcpResolveSchema(tool.InputSchema).Validate(example))
		}
		if tool.Name == "ledger_record_trade" {
			found := false
			for _, f := range contract.Fields {
				if f.Path == "fee" {
					found = true
					require.True(t, f.Required)
					require.Equal(t, "证券币种", f.Unit)
					require.Equal(t, "user", f.ValueSource)
					require.Contains(t, f.Question, "零")
				}
			}
			require.True(t, found)
			require.Equal(t, "undetermined", contractRequirement(t, contract, "fx").Status)
		}
	}
	unknown := mcpCall[MCPWriteContract](t, c, "ledger_get_write_contract", MCPContractQuery{ToolName: "ledger_record_trade", AccountID: "a", Instrument: &MCPInstrumentSelector{"HK", "00700"}, Date: "2026-09-06"})
	require.Contains(t, unknown.UnresolvedContext, "instrument")
	require.Equal(t, "undetermined", contractRequirement(t, unknown, "fx").Status)
	require.Equal(t, before, mcpLogicalSnapshot(t, s.Store))
	for _, q := range []MCPContractQuery{
		{ToolName: "ledger_set_cash_balance", Date: "2026-09-06"},
		{ToolName: "ledger_record_trade", EntryID: "entry"},
		{ToolName: "ledger_record_dividend", Mode: "complete_opening"},
		{ToolName: "ledger_edit_entry", Action: "void", ReplacementKind: "buy"},
	} {
		mcpReject(t, c, "ledger_get_write_contract", q)
	}
	require.Equal(t, before, mcpLogicalSnapshot(t, s.Store))
}

func TestMCPContractFXBranchesAndQuestionRecovery(t *testing.T) {
	s, _ := mcpFixture(t)
	in := mcpTrade("historic-hk", "0")
	in.Instrument, in.Currency = MCPInstrumentSelector{"HK", "00700"}, HKD
	old, err := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	c, _ := mcpClient(t, s)
	query := MCPContractQuery{ToolName: "ledger_record_trade", AccountID: "a", Instrument: &in.Instrument, Date: "2026-09-06"}
	contract := mcpCall[MCPWriteContract](t, c, "ledger_get_write_contract", query)
	require.Equal(t, "optional", contractRequirement(t, contract, "fx").Status)
	require.Contains(t, contractRequirement(t, contract, "fx").Reason, "未设置现金基准")
	mcpCall[MCPStockWriteResult](t, c, "ledger_set_cash_balance", MCPCashInput{MCPWriteFields: MCPWriteFields{RequestID: "cash-fx", AccountID: "a", ExpectedVersion: "1"}, ObservedDate: "2026-09-06", Amount: "1000", Currency: CNY})
	contract = mcpCall[MCPWriteContract](t, c, "ledger_get_write_contract", query)
	require.Equal(t, "required", contractRequirement(t, contract, "fx").Status)
	before := mcpLogicalSnapshot(t, s.Store)
	in.RequestID, in.ExpectedVersion = "new-hk", "2"
	rejected := mcpReject(t, c, "ledger_record_trade", in)
	require.Equal(t, "fx_required", rejected.Error.Code)
	require.Equal(t, []string{"fx"}, rejected.Error.MissingFields)
	require.Equal(t, "question", rejected.Error.QuestionTool)
	require.Equal(t, "none", rejected.WriteEffect)
	require.Equal(t, before, mcpLogicalSnapshot(t, s.Store))
	query.Date = "2026-09-05"
	contract = mcpCall[MCPWriteContract](t, c, "ledger_get_write_contract", query)
	require.Equal(t, "optional", contractRequirement(t, contract, "fx").Status)
	query.Date = ""
	contract = mcpCall[MCPWriteContract](t, c, "ledger_get_write_contract", query)
	require.Equal(t, "undetermined", contractRequirement(t, contract, "fx").Status)
	require.Contains(t, contract.UnresolvedContext, "date")
	editQuery := MCPContractQuery{ToolName: "ledger_edit_entry", AccountID: "a", EntryID: old.Entry.ID, Action: "replace", Date: "2026-09-06", ReplacementKind: "buy"}
	contract = mcpCall[MCPWriteContract](t, c, "ledger_get_write_contract", editQuery)
	require.Equal(t, "required", contractRequirement(t, contract, "replacement").Status)
	require.Equal(t, "optional", contractRequirement(t, contract, "replacement.fx").Status)
	editQuery.Action, editQuery.Date, editQuery.ReplacementKind = "void", "", ""
	contract = mcpCall[MCPWriteContract](t, c, "ledger_get_write_contract", editQuery)
	require.Equal(t, "forbidden", contractRequirement(t, contract, "replacement").Status)
	require.Equal(t, before, mcpLogicalSnapshot(t, s.Store))
	in.FX = "0.9"
	saved := mcpCall[MCPStockWriteResult](t, c, "ledger_record_trade", in)
	require.Equal(t, Money(91000), saved.Cash.After)
	require.Equal(t, Money(-9000), saved.Cash.Delta)
}

func TestMCPNestedFinancialQuestionPaths(t *testing.T) {
	s, _ := mcpFixture(t)
	c, _ := mcpClient(t, s)
	input := map[string]any{"request_id": "edit", "account_id": "a", "expected_version": "0", "entry_id": "entry", "action": "replace", "reason": "核对", "replacement": map[string]any{"kind": "buy", "date": "2026-09-06", "quantity": "1", "price": "10", "currency": "CNY"}}
	rejected := mcpReject(t, c, "ledger_edit_entry", input)
	require.Equal(t, []string{"replacement.fee"}, rejected.Error.MissingFields)
	require.Equal(t, "question", rejected.Error.QuestionTool)
	require.Equal(t, "replacement.fee", rejected.Error.Questions[0].Field)
	require.Equal(t, "证券币种", rejected.Error.Requirements[0].Unit)
}
