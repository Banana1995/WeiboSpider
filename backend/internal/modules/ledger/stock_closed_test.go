package ledger

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func closedCommand(id, version, instrument string, c StockClosed) StockCommand {
	return StockCommand{Action: "create", ExpectedVersion: version, ID: id, Entry: &StockInput{
		InstrumentID: instrument, Kind: "closed", Date: c.ClosedOn, Closed: &c,
	}}
}

func TestClosedHistoryDoesNotChangeCurrentPositionOrCost(t *testing.T) {
	s, _ := stockFixture(t)
	first := buyCommand("first", "0", "2026-01-01", 100, 10)
	first.Security = stockSecurity(CNY)
	opened := stockWrite(t, s, "first-key", first)

	closed := StockClosed{
		OpenedOn: "2025-01-01", ClosedOn: "2025-02-01", Quantity: 100 * 1000000,
		Income: 120000, Expense: 80000, Profit: 40000, ProfitRate: "50.00",
		Flows: []StockClosedFlow{
			{Kind: "buy", Date: "2025-01-01", Price: 8000000, Quantity: 100 * 1000000, Amount: 80000, FX: 100000000},
			{Kind: "sell", Date: "2025-02-01", Price: 12000000, Quantity: 100 * 1000000, Amount: 120000, FX: 100000000},
		},
	}
	stockWrite(t, s, "closed-key", closedCommand("closed-1", opened.Version, "i", closed))

	book := readBook(t, s)
	require.Len(t, book.Items, 1)
	require.Equal(t, Quantity(100*1000000), book.Items[0].Quantity)
	require.NotNil(t, book.Items[0].Metrics.Cost)
	require.Equal(t, Price(10000000), *book.Items[0].Metrics.Cost)
	require.Len(t, book.Closed, 1)
	require.Equal(t, "closed-1", book.Closed[0].ID)
	require.Equal(t, Money(40000), book.Closed[0].Closed.Profit)
}

func TestClosedHistoryWithoutLivePositionIsListedButHasNoPosition(t *testing.T) {
	s, _ := stockFixture(t)
	first := buyCommand("first", "0", "2026-01-01", 100, 10)
	first.Security = stockSecurity(CNY)
	opened := stockWrite(t, s, "first-key", first)
	closed := StockClosed{
		OpenedOn: "2025-01-01", ClosedOn: "2025-02-01", Quantity: 100 * 1000000,
		Income: 120000, Expense: 80000, Profit: 40000, ProfitRate: "50.00",
	}
	closedOut := stockWrite(t, s, "closed-key", closedCommand("closed-1", opened.Version, "i", closed))
	// A second history-only security is not in the live position list.
	second := stockWrite(t, s, "second-key", StockCommand{Action: "create", ExpectedVersion: closedOut.Version, ID: "second",
		Entry:    &StockInput{InstrumentID: "j", Kind: "closed", Date: "2025-02-01", Closed: &closed},
		Security: &instrumentJSON{"j", "SH", "600519", "History", CNY},
	})
	require.NotEmpty(t, second.Version)

	book := readBook(t, s)
	require.Len(t, book.Items, 1)
	require.Len(t, book.Closed, 2)
	// The REST payload shares the lowercase instrument contract with positions;
	// capitalized storage field names make the browser reject the whole book.
	raw, err := json.Marshal(book)
	require.NoError(t, err)
	var wire struct {
		Closed []struct {
			Instrument instrumentJSON `json:"instrument"`
		} `json:"closed"`
	}
	require.NoError(t, json.Unmarshal(raw, &wire))
	require.Equal(t, instrumentJSON{"j", "SH", "600519", "History", CNY}, wire.Closed[0].Instrument)
	require.NotContains(t, string(raw), `"instrument":{"ID"`)
}
