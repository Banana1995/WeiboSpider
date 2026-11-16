package tax

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func incomeCash(date, reason, change, memo string) Record {
	r := cash(date, reason, "USD", change)
	r.Note = memo
	return r
}

func TestStatementWithholdingUsesActualCentsAndReconciles(t *testing.T) {
	var rows []Record
	for _, day := range []string{"20250201", "20250501", "20250801"} {
		rows = append(rows,
			incomeCash(day, "", "13.37", "MSFT 7 SHARES DIVIDENDS"),
			incomeCash(day, "", "-1.34", "MSFT 7 SHARES WITHHOLDING TAX - TAX"),
		)
	}
	// Tax rows need not follow their dividend in file order.
	rows[0], rows[1] = rows[1], rows[0]
	r := Calculate(rows, []Rate{{Year: 2025, Currency: "USD", Value: "7"}}, Filter{})
	s := find(t, r, 2025, "USD")
	require.Equal(t, "40.11", s.Dividends)
	require.Equal(t, "4.02", s.WithheldTax)
	require.Equal(t, "4.02", s.DividendCredit)
	require.Equal(t, "0.00", s.OtherFees)
	require.Equal(t, "0.00", s.Net)
	require.Equal(t, "28.01", *s.DividendTax) // (40.11*20% - 4.02)*7
	require.Empty(t, s.IncomeTaxIssue)
	require.Len(t, r.Entries, 6)
	requireDetailsReconcile(t, r)
	require.Equal(t, "28.01", *find(t, r, 2025, "CNY").Tax)
}

func TestIncomeWithoutWithholdingPaysTwentyPercentAndHKDUsesStatementBasis(t *testing.T) {
	hk := cash("20250101", "股息收入", "HKD", "180")
	hk.Note = "F/D-HKD1/SH(-10%), PAY IN HKD0.9(NET) <SEHK 883 CNOOC> 200 shares"
	rows := []Record{hk, incomeCash("20250101", "股息收入", "100", "MSFT 10 SHARES DIVIDENDS")}
	r := Calculate(rows, []Rate{{Year: 2025, Currency: "HKD", Value: "1"}, {Year: 2025, Currency: "USD", Value: "1"}}, Filter{})
	require.Equal(t, "36.00", *find(t, r, 2025, "HKD").DividendTax)
	require.Equal(t, "0.00", find(t, r, 2025, "HKD").DividendCredit)
	require.Equal(t, "20.00", *find(t, r, 2025, "USD").DividendTax)
	require.Equal(t, "56.00", *find(t, r, 2025, "CNY").Tax)
	requireDetailsReconcile(t, r)
}

func TestInterestIncomeCannotBeOffsetByTradingLoss(t *testing.T) {
	rows := []Record{
		trade("20250101", "证券", "X", "买入开仓", "USD", "10", "-200", "0"),
		trade("20250102", "证券", "X", "卖出平仓", "USD", "-10", "100", "0"),
		incomeCash("20250201", "利息收入", "30", ""),
		incomeCash("20250301", "", "10", "Interest In Feb."),
		incomeCash("20250401", "融资利息", "-2", "Interest In Mar."),
		incomeCash("20250402", "利息收入", "-5", "Interest correction"),
	}
	r := Calculate(rows, []Rate{{Year: 2025, Currency: "USD", Value: "1"}}, Filter{})
	s := find(t, r, 2025, "USD")
	require.Equal(t, "35.00", s.Interest)
	require.Equal(t, "2.00", s.OtherFees)
	require.Equal(t, "-102.00", s.Net)
	require.Equal(t, "0.00", *s.ProfitTax)
	require.Equal(t, "7.00", *s.InterestTax)
	require.Equal(t, "7.00", *s.Tax)
	requireDetailsReconcile(t, r)
}

func TestWithholdingRefundAndAccountIsolation(t *testing.T) {
	div := incomeCash("20250101", "股息收入", "100", "MSFT 10 SHARES DIVIDENDS")
	tax := incomeCash("20250102", "股息税", "-10", "MSFT 10 SHARES WITHHOLDING TAX")
	refund := incomeCash("20250103", "", "2", "MSFT 10 SHARES WITHHOLDING TAX REFUND")
	rates := []Rate{{Year: 2025, Currency: "USD", Value: "1"}}
	r := Calculate([]Record{tax, refund, div}, rates, Filter{})
	require.Equal(t, "8.00", find(t, r, 2025, "USD").DividendCredit)
	require.Equal(t, "12.00", *find(t, r, 2025, "USD").Tax)
	requireDetailsReconcile(t, r)
	tax.AccountID = "other"
	r = Calculate([]Record{div, tax}, rates, Filter{})
	require.NotEmpty(t, find(t, r, 2025, "USD").IncomeTaxIssue)
	require.Nil(t, find(t, r, 2025, "USD").DividendTax)
	require.Nil(t, find(t, r, 2025, "CNY").Tax)
	r = Calculate([]Record{div, tax}, rates, Filter{Account: div.AccountID})
	require.Equal(t, "20.00", *find(t, r, 2025, "USD").Tax)
}

func TestUnresolvedWithholdingNeverProducesAnInventedCredit(t *testing.T) {
	for _, tc := range []struct{ name, date, symbol, amount string }{
		{"excess treaty withholding", "20250102", "MSFT 10 SHARES WITHHOLDING TAX", "-30"},
		{"different security", "20250102", "IBM 10 SHARES WITHHOLDING TAX", "-10"},
		{"different year", "20260102", "MSFT 10 SHARES WITHHOLDING TAX", "-10"},
		{"unknown security", "20250102", "WITHHOLDING TAX", "-10"},
		{"unmatched refund", "20250102", "MSFT 10 SHARES WITHHOLDING TAX REFUND", "10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := []Record{incomeCash("20250101", "股息收入", "100", "MSFT 10 SHARES DIVIDENDS"), incomeCash(tc.date, "", tc.amount, tc.symbol)}
			r := Calculate(rows, []Rate{{Year: 2025, Currency: "USD", Value: "1"}, {Year: 2026, Currency: "USD", Value: "1"}}, Filter{})
			year := 2025
			if tc.date[:4] == "2026" {
				year = 2026
			}
			s := find(t, r, year, "USD")
			require.NotEmpty(t, s.IncomeTaxIssue)
			require.Nil(t, s.Tax)
			require.Equal(t, "0.00", s.OtherFees)
			requireDetailsReconcile(t, r)
		})
	}
}
