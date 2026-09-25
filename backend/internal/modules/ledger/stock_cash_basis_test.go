package ledger

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestStockHistoricalDividendDoesNotNeedFXWhenCashAlreadyCoversIt(t *testing.T) {
	event := testDividend("HK", "00700", "", "2026-05-15", "2026-06-01", 5300000)
	entries := []StockEntry{
		{ID: "buy", InstrumentID: "i", Kind: "buy", Date: "2026-04-01", Sequence: 1, Quantity: 100000000, Amount: 4000000},
		{ID: "div", InstrumentID: "i", Kind: "dividend", Date: event.ExDate, Sequence: 2, Quantity: 100000000, Amount: 53000, Event: &event, Cycle: "buy"},
	}
	for _, anchor := range []*CashAnchor{nil, {Amount: 12300, Date: "2026-09-01", Sequence: 2}} {
		j := StockJournal{Openings: []CurrentPosition{}, Cash: anchor}
		p, err := replayStocks(j, entries, "2026-09-25")
		require.NoError(t, err)
		m, err := metricsFor(p["i"], nil)
		require.NoError(t, err)
		require.Zero(t, m.PendingDividend)
		require.Equal(t, Money(53000), m.Dividends)
		cash, err := stockCash(j, entries, "2026-09-25")
		require.NoError(t, err)
		if anchor == nil {
			require.Zero(t, cash)
		} else {
			require.Equal(t, anchor.Amount, cash)
		}
	}
	// An entitlement with a payment AFTER the cash baseline needs its settlement FX.
	j := StockJournal{Openings: []CurrentPosition{}, Cash: &CashAnchor{Amount: 12300, Date: "2026-05-20", Sequence: 1}}
	p, err := replayStocks(j, entries, "2026-09-25")
	require.NoError(t, err)
	m, err := metricsFor(p["i"], nil)
	require.NoError(t, err)
	require.Equal(t, Money(53000), m.PendingDividend)
}
