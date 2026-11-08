package ledger

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func mcpClient(t *testing.T, s MCPService) (*mcp.ClientSession, http.Handler) {
	t.Helper()
	h := NewMCPHandler(s, nil)
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "ledger-test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: server.URL}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	return session, h
}

func mcpCall[T any](t *testing.T, c *mcp.ClientSession, name string, args any) T {
	t.Helper()
	result, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, result.IsError, "%+v", result.Content)
	data, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var out mcpEnvelope[T]
	require.NoError(t, json.Unmarshal(data, &out))
	require.True(t, out.OK)
	require.NotNil(t, out.Data)
	require.GreaterOrEqual(t, len(result.Content), 2)
	text, ok := result.Content[1].(*mcp.TextContent)
	require.True(t, ok)
	require.JSONEq(t, string(data), text.Text)
	return *out.Data
}

func TestMCPProtocolElevenToolsThroughSDK(t *testing.T) {
	s, _ := mcpFixture(t)
	c, _ := mcpClient(t, s)
	list, err := c.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, list.Tools, 11)
	for _, tool := range list.Tools {
		require.NotNil(t, tool.InputSchema)
		require.NotNil(t, tool.OutputSchema)
		require.NotNil(t, tool.Annotations)
	}
	accounts := mcpCall[MCPAccounts](t, c, "ledger_list_accounts", MCPAccountQuery{})
	require.Len(t, accounts.Items, 1)
	identities := mcpCall[MCPInstrumentResult](t, c, "ledger_search_instruments", MCPInstrumentQuery{Query: "sh600036"})
	require.Len(t, identities.Items, 1)
	book := mcpCall[MCPBook](t, c, "ledger_get_stock_book", MCPBookQuery{AccountID: "a"})
	require.Equal(t, "0", book.Version)
	contract := mcpCall[MCPWriteContract](t, c, "ledger_get_write_contract", MCPContractQuery{ToolName: "ledger_record_trade", AccountID: "a"})
	require.Equal(t, mcpContractVersion, contract.ContractVersion)
	require.NotEmpty(t, contract.Fields)
	trade := mcpCall[MCPStockWriteResult](t, c, "ledger_record_trade", mcpTrade("protocol-buy", "0"))
	require.Equal(t, "1", trade.VersionAfter)
	dividend := MCPDividendInput{MCPWriteFields: MCPWriteFields{RequestID: "protocol-div", AccountID: "a", ExpectedVersion: "1"}, Instrument: MCPInstrumentSelector{"SH", "600036"}, Date: "2026-09-06", Amount: "2", Currency: CNY}
	div := mcpCall[MCPStockWriteResult](t, c, "ledger_record_dividend", dividend)
	require.Equal(t, "2", div.VersionAfter)
	edit := MCPEditInput{MCPWriteFields: MCPWriteFields{RequestID: "protocol-edit", AccountID: "a", ExpectedVersion: "2"}, EntryID: trade.Entry.ID, Action: "replace", Reason: "核对成交价", Replacement: &MCPReplacement{Kind: "buy", Date: "2026-09-06", Quantity: "10", Price: "11", Fee: "0", Currency: CNY}}
	updated := mcpCall[MCPStockWriteResult](t, c, "ledger_edit_entry", edit)
	require.Equal(t, "3", updated.VersionAfter)
	current := mcpCall[MCPStockWriteResult](t, c, "ledger_set_cash_balance", MCPCashInput{MCPWriteFields: MCPWriteFields{RequestID: "protocol-cash", AccountID: "a", ExpectedVersion: "3"}, ObservedDate: "2026-09-06", Amount: "100", Currency: CNY})
	require.Equal(t, "4", current.VersionAfter)
	r := mcpCall[MCPReceiptResult](t, c, "ledger_get_write_result", MCPReceiptQuery{"a", "protocol-buy"})
	require.Equal(t, trade, *r.Result)
	book = mcpCall[MCPBook](t, c, "ledger_get_stock_book", MCPBookQuery{AccountID: "a", IncludeEntries: true})
	require.Equal(t, "4", book.Version)
	require.Len(t, book.Entries, 2)
	before := auditCount(t, s.Store)
	result, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "ledger_record_trade", Arguments: mcpTrade("stale-protocol", "0")})
	require.NoError(t, err)
	require.True(t, result.IsError)
	data, _ := json.Marshal(result.StructuredContent)
	require.Contains(t, string(data), "version_conflict")
	require.Equal(t, before, auditCount(t, s.Store))
}

func TestMCPProtocolStrictSchemaAndHTTPBoundaries(t *testing.T) {
	s, _ := mcpFixture(t)
	c, h := mcpClient(t, s)
	before := auditCount(t, s.Store)
	for _, args := range []map[string]any{
		{"account_id": "a", "unknown": 1},
		{"account_id": "a", "limit": 101},
		{"Account_ID": "a"},
	} {
		r, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "ledger_get_stock_book", Arguments: args})
		require.NoError(t, err)
		require.True(t, r.IsError)
	}
	badTrade := map[string]any{}
	encoded, _ := json.Marshal(mcpTrade("bad-protocol", "0"))
	require.NoError(t, json.Unmarshal(encoded, &badTrade))
	badTrade["quantity"] = 10
	r, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: "ledger_record_trade", Arguments: badTrade})
	require.NoError(t, err)
	require.True(t, r.IsError)
	for _, test := range []struct {
		name, body, origin string
		status             int
	}{
		{"duplicate", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ledger_record_trade","arguments":{"request_id":"a","request_id":"b"}}}`, "", 400},
		{"two-objects", `{} {}`, "", 400},
		{"large", `{"x":"` + strings.Repeat("a", maxLedgerBody) + `"}`, "", 413},
		{"origin", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "https://other.example", 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "http://127.0.0.1"+MCPPath, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			request.Header.Set("Origin", test.origin)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			require.Equal(t, test.status, response.Code)
		})
	}
	for _, method := range []string{"GET", "DELETE"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(method, "http://127.0.0.1"+MCPPath, nil))
		require.Equal(t, 405, r.Code)
	}
	require.Equal(t, before, auditCount(t, s.Store))
}

func TestMCPLegacyInitializeAndLostResponseRecovery(t *testing.T) {
	s, _ := mcpFixture(t)
	h := NewMCPHandler(s, nil)
	request := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1"+MCPPath, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", "2025-11-25")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	init := request(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"legacy","version":"1"}}}`)
	require.Equal(t, 200, init.Code)
	require.Contains(t, init.Body.String(), "investment-ledger")
	require.Empty(t, init.Header().Get("Mcp-Session-Id"))
	args, _ := json.Marshal(mcpTrade("response-lost", "0"))
	// Complete the actual protocol request, then discard its entire response.
	w := request(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ledger_record_trade","arguments":` + string(args) + `}}`)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"structuredContent"`)
	require.NotContains(t, w.Body.String(), `"isError":true`)
	_, err := io.Copy(io.Discard, w.Result().Body)
	require.NoError(t, err)
	result, err := s.WriteResult(t.Context(), MCPReceiptQuery{"a", "response-lost"})
	require.NoError(t, err)
	require.Equal(t, "committed", result.Status)
	again, err := s.Write(t.Context(), "ledger_record_trade", mcpTrade("response-lost", "0"))
	require.NoError(t, err)
	require.Equal(t, *result.Result, again)
	require.Len(t, readBook(t, s.Store).Entries, 1)
}
