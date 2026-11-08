package ledger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPChannelsSplitHoldingAndSell(t *testing.T) {
	s, _ := mcpFixture(t)
	_, err := s.Write(t.Context(), "ledger_set_opening_positions", mcpOpenings("openings", "0",
		MCPOpeningPosition{MCPInstrumentSelector{"SH", "600036"}, "华泰", "1000"},
		MCPOpeningPosition{MCPInstrumentSelector{"SH", "600036"}, "国金", "500"}))
	require.NoError(t, err)

	book, err := s.StockBook(t.Context(), MCPBookQuery{AccountID: "a"})
	require.NoError(t, err)
	require.Len(t, book.Positions, 1)
	require.Equal(t, Quantity(1500000000), book.Positions[0].Quantity)
	require.Equal(t, []ChannelQuantity{{"华泰", 1000000000}, {"国金", 500000000}}, book.Positions[0].Channels)

	buy := mcpTrade("buy-huatai", "1")
	buy.Channel, buy.Quantity = "华泰", "200"
	got, err := s.Write(t.Context(), "ledger_record_trade", buy)
	require.NoError(t, err)
	require.Equal(t, "华泰", got.Entry.Channel)
	require.Equal(t, []ChannelQuantity{{"华泰", 1200000000}, {"国金", 500000000}}, mustChannels(t, s))

	// Selling more than one channel holds fails even though the total is enough.
	over := mcpTrade("sell-guojin-over", "2")
	over.Side, over.Channel, over.Quantity, over.Date = "sell", "国金", "600", "2026-09-06"
	_, err = s.Write(t.Context(), "ledger_record_trade", over)
	require.Equal(t, "stock_quantity", mcpToolFailure(err).Code)

	sell := mcpTrade("sell-guojin", "2")
	sell.Side, sell.Channel, sell.Quantity, sell.Date = "sell", "国金", "400", "2026-09-06"
	sold, err := s.Write(t.Context(), "ledger_record_trade", sell)
	require.NoError(t, err)
	require.Equal(t, "国金", sold.Entry.Channel)
	require.Equal(t, []ChannelQuantity{{"华泰", 1200000000}, {"国金", 100000000}}, mustChannels(t, s))

	// Entries without a channel keep working and show up as an unnamed bucket.
	plain := mcpTrade("buy-plain", "3")
	plain.Quantity = "50"
	_, err = s.Write(t.Context(), "ledger_record_trade", plain)
	require.NoError(t, err)
	require.Equal(t, []ChannelQuantity{{"", 50000000}, {"华泰", 1200000000}, {"国金", 100000000}}, mustChannels(t, s))
}

func mustChannels(t *testing.T, s MCPService) []ChannelQuantity {
	t.Helper()
	book, err := s.StockBook(t.Context(), MCPBookQuery{AccountID: "a"})
	require.NoError(t, err)
	return book.Positions[0].Channels
}

func TestMCPChannelValidationAndCorrection(t *testing.T) {
	s, _ := mcpFixture(t)
	long := make([]byte, 129)
	for n := range long {
		long[n] = 'a'
	}
	bad := mcpTrade("bad-channel", "0")
	bad.Channel = string(long)
	_, err := s.Write(t.Context(), "ledger_record_trade", bad)
	require.Equal(t, "invalid_input", mcpToolFailure(err).Code)

	first := mcpTrade("first", "0")
	first.Channel = " 华泰 "
	got, err := s.Write(t.Context(), "ledger_record_trade", first)
	require.NoError(t, err)
	require.Equal(t, "华泰", got.Entry.Channel)

	edit := MCPEditInput{MCPWriteFields: MCPWriteFields{RequestID: "move-channel", AccountID: "a", ExpectedVersion: "1"},
		EntryID: got.Entry.ID, Action: "replace", Reason: "渠道录错",
		Replacement: &MCPReplacement{Kind: "buy", Channel: "国金", Date: "2026-09-06", Quantity: "10", Price: "10", Fee: "0", Currency: CNY}}
	fixed, err := s.Write(t.Context(), "ledger_edit_entry", edit)
	require.NoError(t, err)
	require.Equal(t, "国金", fixed.Entry.Channel)
	require.Equal(t, []ChannelQuantity{{"国金", 10000000}}, mustChannels(t, s))
}

func TestMCPTradeSchemaAcceptsChannel(t *testing.T) {
	s, _ := mcpFixture(t)
	c, _ := mcpClient(t, s)
	out := mcpCall[MCPStockWriteResult](t, c, "ledger_record_trade", map[string]any{
		"request_id": "sdk-channel", "account_id": "a", "expected_version": "0",
		"instrument": map[string]any{"market": "SH", "code": "600036"},
		"side": "buy", "date": "2026-09-06", "quantity": "10", "price": "10",
		"fee": "0", "currency": "CNY", "channel": "华泰满意",
	})
	require.Equal(t, "华泰满意", out.Entry.Channel)
	contract := mcpCall[MCPWriteContract](t, c, "ledger_get_write_contract", MCPContractQuery{ToolName: "ledger_record_trade", AccountID: "a"})
	found := false
	for _, field := range contract.Fields {
		if field.Path == "channel" {
			found = true
		}
	}
	require.True(t, found)
}
