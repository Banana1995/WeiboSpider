package ledger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func mcpTax(key, version, amount string) MCPTaxInput {
	return MCPTaxInput{MCPWriteFields: MCPWriteFields{RequestID: key, AccountID: "a", ExpectedVersion: version},
		Instrument: MCPInstrumentSelector{"SH", "600036"}, Date: "2026-09-06", Amount: amount, Currency: CNY, Note: "股息红利税补缴"}
}

func TestMCPDividendTaxDeductsCashAndNetDividend(t *testing.T) {
	s, _ := mcpFixture(t)
	_, err := s.Write(t.Context(), "ledger_set_opening_positions", mcpOpenings("openings", "0",
		MCPOpeningPosition{MCPInstrumentSelector{"SH", "600036"}, "100"}))
	require.NoError(t, err)
	_, err = s.Write(t.Context(), "ledger_set_cash_balance", MCPCashInput{
		MCPWriteFields: MCPWriteFields{RequestID: "cash", AccountID: "a", ExpectedVersion: "1"},
		ObservedDate:   "2026-09-06", Amount: "1000.00", Currency: CNY})
	require.NoError(t, err)

	dividend := MCPDividendInput{MCPWriteFields: MCPWriteFields{RequestID: "dividend", AccountID: "a", ExpectedVersion: "2"},
		Instrument: MCPInstrumentSelector{"SH", "600036"}, Date: "2026-09-06", Amount: "100.00", Currency: CNY}
	paid, err := s.Write(t.Context(), "ledger_record_dividend", dividend)
	require.NoError(t, err)
	require.Equal(t, Money(110000), paid.Cash.After)

	taxed, err := s.Write(t.Context(), "ledger_record_dividend_tax", mcpTax("tax", "3", "20.00"))
	require.NoError(t, err)
	require.Equal(t, Money(108000), taxed.Cash.After)
	require.Equal(t, Money(-2000), taxed.Cash.Delta)
	require.Equal(t, Quantity(100000000), taxed.PositionsChanged[0].QuantityAfter)
	require.Equal(t, Quantity(0), taxed.PositionsChanged[0].Delta)
	require.Equal(t, "tax", taxed.Entry.Kind)
	require.Equal(t, Money(2000), taxed.Entry.Amount)
	require.Zero(t, taxed.Entry.Fee)

	book, err := s.StockBook(t.Context(), MCPBookQuery{AccountID: "a", IncludeEntries: true})
	require.NoError(t, err)
	require.Len(t, book.Entries, 2)
	require.Equal(t, Money(10000), book.Positions[0].Metrics.Dividends)
	require.Equal(t, Money(2000), book.Positions[0].Metrics.Tax)
	require.Equal(t, Quantity(100000000), book.Positions[0].Quantity)
	require.Equal(t, Money(108000), book.Cash)

	// The retry replays the frozen receipt instead of deducting twice.
	again, err := s.Write(t.Context(), "ledger_record_dividend_tax", mcpTax("tax", "3", "20.00"))
	require.NoError(t, err)
	require.Equal(t, taxed, again)

	// Corrections keep the entry inside the amount-only family.
	edit := MCPEditInput{MCPWriteFields: MCPWriteFields{RequestID: "bad-tax", AccountID: "a", ExpectedVersion: "4"},
		EntryID: taxed.Entry.ID, Action: "replace", Reason: "核对税单",
		Replacement: &MCPReplacement{Kind: "buy", Date: "2026-09-06", Quantity: "100", Price: "1", Fee: "0", Currency: CNY}}
	_, err = s.Write(t.Context(), "ledger_edit_entry", edit)
	require.Equal(t, "invalid_input", mcpToolFailure(err).Code)

	edit.RequestID, edit.Replacement = "fix-tax", &MCPReplacement{Kind: "tax", Date: "2026-09-06", Amount: "30.00", Currency: CNY}
	fixed, err := s.Write(t.Context(), "ledger_edit_entry", edit)
	require.NoError(t, err)
	require.Equal(t, Money(3000), fixed.Entry.Amount)
	require.Equal(t, Money(107000), fixed.Cash.After)
	after, err := s.StockBook(t.Context(), MCPBookQuery{AccountID: "a"})
	require.NoError(t, err)
	require.Equal(t, Money(10000), after.Positions[0].Metrics.Dividends)
	require.Equal(t, Money(3000), after.Positions[0].Metrics.Tax)
}

func TestMCPDividendTaxRequiresPositiveAmountAndNoTradeFields(t *testing.T) {
	s, _ := mcpFixture(t)
	_, err := s.Write(t.Context(), "ledger_record_dividend_tax", mcpTax("zero", "0", "0"))
	require.Equal(t, "invalid_precision", mcpToolFailure(err).Code)
	_, err = s.Write(t.Context(), "ledger_record_dividend_tax", mcpTax("unknown-security", "0", "10.00"))
	require.Equal(t, "entry_not_found", mcpToolFailure(err).Code)
}
