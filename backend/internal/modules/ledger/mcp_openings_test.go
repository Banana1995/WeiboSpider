package ledger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func mcpOpenings(key, version string, positions ...MCPOpeningPosition) MCPOpeningsInput {
	return MCPOpeningsInput{MCPWriteFields: MCPWriteFields{RequestID: key, AccountID: "a", ExpectedVersion: version}, Positions: positions}
}

func TestMCPOpeningPositionsThroughSDK(t *testing.T) {
	s, _ := mcpFixture(t)
	c, _ := mcpClient(t, s)
	out := mcpCall[MCPStockWriteResult](t, c, "ledger_set_opening_positions", map[string]any{
		"request_id": "sdk-openings", "account_id": "a", "expected_version": "0",
		"positions": []any{map[string]any{"instrument": map[string]any{"market": "SH", "code": "600036"}, "quantity": "100"}},
	})
	require.Equal(t, "1", out.VersionAfter)
	require.Len(t, out.PositionsChanged, 1)
	require.True(t, out.PositionsChanged[0].OpeningAfter)
	contract := mcpCall[MCPWriteContract](t, c, "ledger_get_write_contract", MCPContractQuery{ToolName: "ledger_set_opening_positions", AccountID: "a"})
	require.Equal(t, "2026-11-08", contract.ContractVersion)
	require.NotEmpty(t, contract.Fields)
}

func TestMCPOpeningPositionsRecordQuantitiesOnly(t *testing.T) {
	s, _ := mcpFixture(t)
	first, err := s.Write(t.Context(), "ledger_set_opening_positions", mcpOpenings("openings", "0",
		MCPOpeningPosition{MCPInstrumentSelector{"SH", "600036"}, "100"},
		MCPOpeningPosition{MCPInstrumentSelector{"HK", "00700"}, "200"}))
	require.NoError(t, err)
	require.Equal(t, "0", first.VersionBefore)
	require.Equal(t, "1", first.VersionAfter)
	require.Equal(t, "opening_basis", first.CashEffectReason)
	require.Nil(t, first.Entry)
	require.Len(t, first.PositionsChanged, 2)
	for _, change := range first.PositionsChanged {
		require.True(t, change.OpeningAfter)
		require.Nil(t, change.CostAfter)
	}
	require.Contains(t, first.Notices, "unknown_opening_cost")
	require.Zero(t, first.Cash.After)

	book, err := s.StockBook(t.Context(), MCPBookQuery{AccountID: "a"})
	require.NoError(t, err)
	require.Equal(t, "1", book.Version)
	require.Nil(t, book.CashAnchor)
	require.Len(t, book.Positions, 2)
	for _, position := range book.Positions {
		require.True(t, position.Opening)
		require.Nil(t, position.Metrics.Cost)
	}
	require.Empty(t, book.Entries)

	// The retry must return the frozen receipt and never add a version.
	retry, err := s.Write(t.Context(), "ledger_set_opening_positions", mcpOpenings("openings", "0",
		MCPOpeningPosition{MCPInstrumentSelector{"SH", "600036"}, "100"},
		MCPOpeningPosition{MCPInstrumentSelector{"HK", "00700"}, "200"}))
	require.NoError(t, err)
	require.Equal(t, first, retry)

	// A second submission replaces the whole opening list, not just adds.
	replaced, err := s.Write(t.Context(), "ledger_set_opening_positions", mcpOpenings("openings-2", "1",
		MCPOpeningPosition{MCPInstrumentSelector{"SH", "600036"}, "150"}))
	require.NoError(t, err)
	require.Equal(t, "2", replaced.VersionAfter)
	book, err = s.StockBook(t.Context(), MCPBookQuery{AccountID: "a"})
	require.NoError(t, err)
	require.Len(t, book.Positions, 1)
	require.Equal(t, Quantity(150000000), book.Positions[0].Quantity)
}

func TestMCPOpeningPositionsConflictWithTrades(t *testing.T) {
	s, _ := mcpFixture(t)
	_, err := s.Write(t.Context(), "ledger_record_trade", mcpTrade("buy", "0"))
	require.NoError(t, err)
	_, err = s.Write(t.Context(), "ledger_set_opening_positions", mcpOpenings("openings", "1",
		MCPOpeningPosition{MCPInstrumentSelector{"SH", "600036"}, "100"}))
	require.Equal(t, "opening_conflict", mcpToolFailure(err).Code)
	book, err := s.StockBook(t.Context(), MCPBookQuery{AccountID: "a"})
	require.NoError(t, err)
	require.Equal(t, "1", book.Version)
	require.False(t, book.Positions[0].Opening)
}

func TestMCPOpeningPositionsValidationAndCompletion(t *testing.T) {
	s, _ := mcpFixture(t)
	_, err := s.Write(t.Context(), "ledger_set_opening_positions", mcpOpenings("empty", "0"))
	require.Equal(t, "invalid_input", mcpToolFailure(err).Code)
	_, err = s.Write(t.Context(), "ledger_set_opening_positions", mcpOpenings("zero", "0",
		MCPOpeningPosition{MCPInstrumentSelector{"SH", "600036"}, "0"}))
	require.Equal(t, "invalid_precision", mcpToolFailure(err).Code)
	_, err = s.Write(t.Context(), "ledger_set_opening_positions", mcpOpenings("duplicate", "0",
		MCPOpeningPosition{MCPInstrumentSelector{"SH", "600036"}, "1"},
		MCPOpeningPosition{MCPInstrumentSelector{"SH", "600036"}, "2"}))
	require.Equal(t, "invalid_input", mcpToolFailure(err).Code)
	_, err = s.Write(t.Context(), "ledger_set_opening_positions", mcpOpenings("unsupported", "0",
		MCPOpeningPosition{MCPInstrumentSelector{"US", "AAPL"}, "1"}))
	require.Equal(t, "unsupported_instrument", mcpToolFailure(err).Code)
	_, err = s.Write(t.Context(), "ledger_set_opening_positions", mcpOpenings("stale", "4",
		MCPOpeningPosition{MCPInstrumentSelector{"SH", "600036"}, "1"}))
	require.Equal(t, "version_conflict", mcpToolFailure(err).Code)

	_, err = s.Write(t.Context(), "ledger_set_opening_positions", mcpOpenings("openings", "0",
		MCPOpeningPosition{MCPInstrumentSelector{"SH", "600036"}, "100"}))
	require.NoError(t, err)

	// Existing quantity is converted into a real buy, so cost stops being unknown.
	complete := mcpTrade("complete", "1")
	complete.Mode, complete.Date, complete.Quantity = "complete_opening", "2026-01-01", "100"
	completed, err := s.Write(t.Context(), "ledger_record_trade", complete)
	require.NoError(t, err)
	require.Equal(t, Quantity(100000000), completed.PositionsChanged[0].QuantityAfter)
	require.False(t, completed.PositionsChanged[0].OpeningAfter)
	require.NotNil(t, completed.PositionsChanged[0].CostAfter)
	require.NotContains(t, completed.Notices, "unknown_opening_cost")
}
