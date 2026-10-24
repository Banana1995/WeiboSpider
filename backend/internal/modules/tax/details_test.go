package tax

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireDetailsReconcile(t *testing.T, report Report) {
	t.Helper()
	for _, row := range report.Rows {
		if row.Currency == "CNY" {
			continue
		}
		profit, fees, dividend := zero(), zero(), zero()
		for _, entry := range report.Entries {
			year, _ := strconv.Atoi(entry.Record.Date[:4])
			if year != row.Year || entry.Record.Currency != row.Currency {
				continue
			}
			profit.Add(profit, number(entry.Profit))
			fees.Add(fees, number(entry.Fees))
			dividend.Add(dividend, number(entry.Dividends))
		}
		require.Equal(t, row.Net, fixed(profit, 2))
		require.Equal(t, row.Fees, fixed(fees, 2))
		require.Equal(t, row.Dividends, fixed(dividend, 2))
	}
}
func TestAnnualDetailsPreserveAccountsAndCountFeesOnce(t *testing.T) {
	old := trade("20240101", "证券", "325", "买入开仓", "HKD", "100", "-1000", "10")
	old.AccountID = "old"
	current := trade("20250101", "证券", "00325", "买入开仓", "HKD", "100", "-2000", "20")
	current.AccountID = "new"
	close := trade("20250102", "证券", "00325", "卖出平仓", "HKD", "-150", "4500", "30")
	close.AccountID = "new"
	rows := []Record{old, current, close, cash("20250103", "股息收入", "HKD", "50"), cash("20250104", "融资利息", "HKD", "-3"), cash("20250105", "银证转账", "HKD", "5000"), cash("20250106", "", "HKD", "-100"), cash("20250107", "退费", "HKD", "1")}
	r := Calculate(rows, DefaultRates(), Filter{From: "2025-01-01"})
	requireDetailsReconcile(t, r)
	require.Len(t, r.Entries, 5)
	entry := r.Entries[1]
	require.True(t, entry.HasClose)
	require.Len(t, entry.Openings, 2)
	require.Equal(t, "old", entry.Openings[0].Record.AccountID)
	require.Equal(t, "new", entry.Record.AccountID)
	require.Equal(t, "10.000000000000", entry.Openings[0].Fee)
	require.Equal(t, "10.000000000000", entry.Openings[1].Fee)
	require.Equal(t, "2450.000000000000", entry.Profit)
	require.Equal(t, "50.000000000000", entry.Fees)
	require.Equal(t, "0", r.Entries[0].Fees)
	require.Equal(t, "20", r.Entries[0].CapitalizedFee)
	require.Equal(t, "2448.00", find(t, r, 2025, "HKD").Net)
	requireDetailsReconcile(t, Calculate(rows, DefaultRates(), Filter{}))
	requireDetailsReconcile(t, Calculate(rows, DefaultRates(), Filter{Account: "new"}))
}

func TestShortAndMissingDetailEntries(t *testing.T) {
	rows := []Record{trade("20250101", "证券", "X", "卖出开仓", "USD", "-3", "60", "1"), trade("20250102", "证券", "X", "买入平仓", "USD", "2", "-30", "2"), trade("20250103", "证券", "X", "买入平仓", "USD", "2", "-40", "2")}
	r := Calculate(rows, DefaultRates(), Filter{})
	requireDetailsReconcile(t, r)
	require.Len(t, r.Entries, 3)
	require.Equal(t, "7.333333333333", r.Entries[1].Profit)
	require.Equal(t, "1.00", find(t, r, 2025, "USD").UnmatchedCloseFees)
	require.False(t, r.Entries[2].Complete)
	require.Len(t, r.Missing, 1)
}
