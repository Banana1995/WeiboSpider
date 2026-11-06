package ledger

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func mcpLogicalSnapshot(t *testing.T, s *Store) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range []string{"accounts", "account_records", "instruments", "stock_entries", "stock_journals", "current_holdings", "stock_dividend_status", "audit_log", "idempotency_receipts", "sqlite_sequence"} {
		rows, err := s.db.QueryContext(t.Context(), `SELECT * FROM `+table+` ORDER BY rowid`)
		require.NoError(t, err)
		columns, err := rows.Columns()
		require.NoError(t, err)
		data := [][]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			require.NoError(t, rows.Scan(pointers...))
			data = append(data, values)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		raw, err := json.Marshal(data)
		require.NoError(t, err)
		out[table] = string(raw)
	}
	return out
}

func mcpReject(t *testing.T, c *mcp.ClientSession, tool string, args any) mcpEnvelope[any] {
	t.Helper()
	r, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	require.NoError(t, err)
	raw, err := json.Marshal(r.StructuredContent)
	require.NoError(t, err)
	require.True(t, r.IsError, "%s", raw)
	var out mcpEnvelope[any]
	require.NoError(t, json.Unmarshal(raw, &out))
	require.False(t, out.OK)
	require.NotNil(t, out.Error)
	require.Equal(t, tool, out.Error.Tool)
	require.Equal(t, mcpContractVersion, out.Error.ContractVersion)
	require.Len(t, r.Content, 2)
	require.JSONEq(t, string(raw), r.Content[1].(*mcp.TextContent).Text)
	return out
}

func mcpCloneArgs(t *testing.T, input any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func mcpChangeField(input map[string]any, path string, remove bool, value any) {
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		input = input[part].(map[string]any)
	}
	if remove {
		delete(input, parts[len(parts)-1])
	} else {
		input[parts[len(parts)-1]] = value
	}
}

func TestMCPEveryRequiredFieldRejectsWithoutAnyWrite(t *testing.T) {
	s, _ := mcpFixture(t)
	seed, err := s.Write(t.Context(), "ledger_record_trade", mcpTrade("seed", "0"))
	require.NoError(t, err)
	dividend, err := s.Write(t.Context(), "ledger_record_dividend", MCPDividendInput{MCPWriteFields: MCPWriteFields{RequestID: "seed-div", AccountID: "a", ExpectedVersion: "1"}, Instrument: MCPInstrumentSelector{"SH", "600036"}, Date: "2026-09-06", Amount: "1", Currency: CNY})
	require.NoError(t, err)
	s.Dividends = NewDividendWorker(s.Store, nil, nil, nil)
	c, _ := mcpClient(t, s)
	before := mcpLogicalSnapshot(t, s.Store)
	common := map[string]any{"request_id": "strict-test", "account_id": "a", "expected_version": "2"}
	trade := map[string]any{"instrument": map[string]any{"market": "SH", "code": "600036"}, "side": "buy", "date": "2026-09-06", "quantity": "1", "price": "10", "fee": "0", "currency": "CNY"}
	replacement := map[string]any{"kind": "buy", "date": "2026-09-06", "quantity": "10", "price": "11", "fee": "0", "currency": "CNY"}
	// Independent acceptance lists: do not derive these from production schemas.
	for _, test := range []struct {
		name, tool string
		input      map[string]any
		required   []string
	}{
		{"trade", "ledger_record_trade", trade, []string{"instrument", "instrument.market", "instrument.code", "side", "date", "quantity", "price", "fee", "currency"}},
		{"dividend", "ledger_record_dividend", map[string]any{"instrument": map[string]any{"market": "SH", "code": "600036"}, "date": "2026-09-06", "amount": "1", "currency": "CNY"}, []string{"instrument", "instrument.market", "instrument.code", "date", "amount", "currency"}},
		{"cash", "ledger_set_cash_balance", map[string]any{"observed_date": "2026-09-06", "amount": "100", "currency": "CNY"}, []string{"observed_date", "amount", "currency"}},
		{"replace-trade", "ledger_edit_entry", map[string]any{"entry_id": seed.Entry.ID, "action": "replace", "reason": "核对成交", "replacement": replacement}, []string{"entry_id", "action", "reason", "replacement", "replacement.kind", "replacement.date", "replacement.quantity", "replacement.price", "replacement.fee", "replacement.currency"}},
		{"replace-dividend", "ledger_edit_entry", map[string]any{"entry_id": dividend.Entry.ID, "action": "replace", "reason": "核对到账", "replacement": map[string]any{"kind": "dividend", "date": "2026-09-06", "amount": "2", "currency": "CNY"}}, []string{"entry_id", "action", "reason", "replacement", "replacement.kind", "replacement.date", "replacement.amount", "replacement.currency"}},
		{"void", "ledger_edit_entry", map[string]any{"entry_id": dividend.Entry.ID, "action": "void", "reason": "重复记录"}, []string{"entry_id", "action", "reason"}},
	} {
		for k, v := range common {
			test.input[k] = v
		}
		for _, field := range append([]string{"request_id", "account_id", "expected_version"}, test.required...) {
			for _, variant := range []string{"missing", "null", "empty"} {
				t.Run(test.name+"/"+field+"/"+variant, func(t *testing.T) {
					args := mcpCloneArgs(t, test.input)
					var value any
					if variant == "empty" {
						value = ""
					}
					mcpChangeField(args, field, variant == "missing", value)
					r := mcpReject(t, c, test.tool, args)
					require.Equal(t, "none", r.WriteEffect)
					require.Contains(t, r.Error.Fields, field)
					if variant == "missing" {
						require.Contains(t, r.Error.MissingFields, field)
					}
					require.Equal(t, before, mcpLogicalSnapshot(t, s.Store))
					require.Empty(t, s.Dividends.wake)
				})
			}
		}
	}
}

func TestMCPQuestionFeedbackAndCompleteSubmission(t *testing.T) {
	s, _ := mcpFixture(t)
	c, _ := mcpClient(t, s)
	args := mcpCloneArgs(t, mcpTrade("question-flow", "0"))
	for _, field := range []string{"date", "price", "fee"} {
		delete(args, field)
	}
	before := mcpLogicalSnapshot(t, s.Store)
	r := mcpReject(t, c, "ledger_record_trade", args)
	require.ElementsMatch(t, []string{"date", "price", "fee"}, r.Error.MissingFields)
	require.Equal(t, "question", r.Error.QuestionTool)
	require.Equal(t, "ask_user_with_question_then_resubmit", r.Error.NextAction)
	require.Len(t, r.Error.Questions, 3)
	require.Len(t, r.Error.Requirements, 3)
	for _, q := range r.Error.Questions {
		require.Equal(t, "question", q.NextAction)
		require.NotEmpty(t, q.Question)
	}
	require.Equal(t, before, mcpLogicalSnapshot(t, s.Store))
	args["date"], args["price"], args["fee"] = "2026-09-06", "10", "0"
	out := mcpCall[MCPStockWriteResult](t, c, "ledger_record_trade", args)
	require.Equal(t, Money(0), out.Entry.Fee)
	replayed := mcpCall[MCPStockWriteResult](t, c, "ledger_record_trade", args)
	require.Equal(t, out, replayed)
	require.Len(t, readBook(t, s.Store).Entries, 1)
	// Metadata is obtained from tools, not requested as an internal number from users.
	delete(args, "expected_version")
	r = mcpReject(t, c, "ledger_record_trade", args)
	require.Empty(t, r.Error.QuestionTool)
	require.Equal(t, "read_ledger_context", r.Error.Questions[0].NextAction)
}

func TestMCPInvalidFactsNeverBecomeDefaults(t *testing.T) {
	s, _ := mcpFixture(t)
	c, _ := mcpClient(t, s)
	before := mcpLogicalSnapshot(t, s.Store)
	for _, test := range []struct {
		field string
		value any
	}{
		{"quantity", 10}, {"price", 10.5}, {"fee", false}, {"fee", "约10"}, {"fee", "未知"},
		{"fee", "0.001"}, {"quantity", "1.0000001"}, {"price", "9223372036854.775808"},
		{"quantity", "0"}, {"price", "0"}, {"fee", "-0"}, {"fee", "1e2"}, {"price", "1,000"},
		{"price", "¥100"}, {"quantity", " 1"}, {"price", "NaN"}, {"price", "Infinity"},
		{"date", "2026-02-30"}, {"date", "2027-01-01"}, {"instrument.code", "6000"},
		{"instrument.market", "US"}, {"currency", "HKD"}, {"mode", "guess"}, {"fx", "0"},
		{"fx", "1.000000001"}, {"fx", "0.9"}, {"unknown", "ignored?"}, {"Fee", "1"}, {"note", "\u200b"},
	} {
		t.Run(fmt.Sprintf("%s/%v", test.field, test.value), func(t *testing.T) {
			args := mcpCloneArgs(t, mcpTrade("invalid-input", "0"))
			mcpChangeField(args, test.field, false, test.value)
			r := mcpReject(t, c, "ledger_record_trade", args)
			require.Equal(t, "none", r.WriteEffect)
			require.NotEmpty(t, r.Error.Fields)
			require.Equal(t, before, mcpLogicalSnapshot(t, s.Store))
		})
	}
	zero := mcpCall[MCPStockWriteResult](t, c, "ledger_set_cash_balance", map[string]any{"request_id": "zero", "account_id": "a", "expected_version": "0", "observed_date": "2026-09-06", "amount": "0", "currency": "CNY"})
	require.NotNil(t, zero.CashAnchorAfter)
	require.Zero(t, zero.CashAnchorAfter.Amount)
}

func TestMCPBranchErrorsAndBoundedDiagnostics(t *testing.T) {
	s, _ := mcpFixture(t)
	c, _ := mcpClient(t, s)
	before := mcpLogicalSnapshot(t, s.Store)
	for _, args := range []map[string]any{
		{"request_id": "branch", "account_id": "a", "expected_version": "0", "entry_id": "entry", "action": "replace", "reason": "核对", "replacement": map[string]any{}},
		{"request_id": "branch", "account_id": "a", "expected_version": "0", "entry_id": "entry", "action": "void", "reason": "核对", "replacement": nil},
		{"request_id": "branch", "account_id": "a", "expected_version": "0", "entry_id": "entry", "action": "replace", "reason": "核对", "replacement": map[string]any{"kind": "dividend", "date": "2026-09-06", "amount": "1", "currency": "CNY", "quantity": "10"}},
	} {
		r := mcpReject(t, c, "ledger_edit_entry", args)
		require.NotEmpty(t, r.Error.Fields)
		require.Equal(t, "none", r.WriteEffect)
	}
	args := mcpCloneArgs(t, mcpTrade("bounded", "0"))
	for i := 0; i < 50; i++ {
		args[fmt.Sprintf("extra%02d", i)] = "do not echo this value"
	}
	r := mcpReject(t, c, "ledger_record_trade", args)
	require.Len(t, r.Error.InvalidFields, maxMCPFieldIssues)
	require.True(t, r.Error.Truncated)
	encoded, err := json.Marshal(r.Error)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "do not echo this value")
	require.Equal(t, before, mcpLogicalSnapshot(t, s.Store))
}

func TestMCPPublishedOutputSchemas(t *testing.T) {
	s, _ := mcpFixture(t)
	c, _ := mcpClient(t, s)
	listed, err := c.ListTools(t.Context(), nil)
	require.NoError(t, err)
	trade, err := s.Write(t.Context(), "ledger_record_trade", mcpTrade("output", "0"))
	require.NoError(t, err)
	contract, err := s.WriteContract(t.Context(), MCPContractQuery{ToolName: "ledger_record_trade", AccountID: "a"})
	require.NoError(t, err)
	for _, tool := range listed.Tools {
		var value any
		switch tool.Name {
		case "ledger_record_trade":
			value = mcpEnvelope[MCPStockWriteResult]{OK: true, Data: &trade, WriteEffect: "committed"}
		case "ledger_get_write_contract":
			value = mcpEnvelope[MCPWriteContract]{OK: true, Data: &contract}
		default:
			continue
		}
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		wire, err := mcpDecodeValue(raw)
		require.NoError(t, err)
		require.NoError(t, mcpResolveSchema(tool.OutputSchema).Validate(wire), "%s", tool.Name)
	}
}
