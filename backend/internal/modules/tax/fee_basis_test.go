package tax

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpeningFeeFollowsDisposalYear(t *testing.T) {
	// Synthetic round-number amounts keep the public fixture independent of
	// private statements: the old year incurs 12 but recognizes none of it.
	buy := trade("20241014", "证券", "700", "买入开仓", "HKD", "100", "-2000", "12")
	sell := trade("20250318", "证券", "00700", "卖出平仓", "HKD", "-100", "3000", "8")
	r := Calculate([]Record{buy, sell}, DefaultRates(), Filter{})
	y2024 := find(t, r, 2024, "HKD")
	y2025 := find(t, r, 2025, "HKD")
	require.Equal(t, "0.00", y2024.Net)
	require.Equal(t, "0.00", y2024.TradeFees)
	require.Equal(t, "12.00", y2024.IncurredTradeFees)
	require.Equal(t, "12.00", y2024.DeferredOpenFees)
	require.Equal(t, "980.00", y2025.Net)
	require.Equal(t, "20.00", y2025.TradeFees)
	require.Equal(t, "8.00", y2025.IncurredTradeFees)
	require.Equal(t, "0.00", y2025.DeferredOpenFees)
	require.Equal(t, "12.00", y2025.RealizedOpenFees)
	require.Equal(t, "8.00", y2025.RealizedCloseFees)
	require.Equal(t, "0", r.Entries[0].Profit)
	require.Equal(t, "12", r.Entries[0].CapitalizedFee)
	require.Equal(t, "20.000000000000", r.Entries[1].Fees)
	requireDetailsReconcile(t, r)
	require.Equal(t, "980.00", find(t, Calculate([]Record{buy, sell}, DefaultRates(), Filter{From: "2025-01-01", To: "2025-12-31"}), 2025, "HKD").Net)
}

func TestPartialSalesCarryFeesAndRespectHistoricalCutoff(t *testing.T) {
	rows := []Record{
		trade("20240101", "证券", "700", "买入开仓", "HKD", "100", "-1000", "10"),
		cash("20250101", "银证转账", "HKD", "100"),
		trade("20250602", "证券", "700", "卖出平仓", "HKD", "-40", "600", "2"),
		trade("20260102", "证券", "700", "卖出平仓", "HKD", "-60", "900", "3"),
	}
	r := Calculate(rows, DefaultRates(), Filter{})
	requireDetailsReconcile(t, r)
	y2024, y2025, y2026 := find(t, r, 2024, "HKD"), find(t, r, 2025, "HKD"), find(t, r, 2026, "HKD")
	require.Equal(t, "10.00", y2024.DeferredOpenFees)
	require.Equal(t, "6.00", y2025.DeferredOpenFees)
	require.Equal(t, "6.00", y2025.TradeFees)
	require.Equal(t, "194.00", y2025.Net)
	require.Equal(t, "0.00", y2026.DeferredOpenFees)
	require.Equal(t, "9.00", y2026.TradeFees)
	require.Equal(t, "291.00", y2026.Net)
	early := Calculate(rows, DefaultRates(), Filter{From: "2025-01-01", To: "2025-05-31"})
	require.Equal(t, "10.00", find(t, early, 2025, "HKD").DeferredOpenFees)
	require.Equal(t, "0.00", find(t, early, 2025, "HKD").TradeFees)
	after := Calculate(rows, DefaultRates(), Filter{From: "2025-01-01", To: "2025-12-31"})
	require.Equal(t, "6.00", find(t, after, 2025, "HKD").DeferredOpenFees)
}

func TestCrossAccountFeeRecognitionAndMissingFeeIsolation(t *testing.T) {
	buy := trade("20240101", "证券", "700", "买入开仓", "HKD", "50", "-500", "5")
	buy.AccountID = "opening-account"
	sell := trade("20250101", "证券", "700", "卖出平仓", "HKD", "-75", "1125", "6")
	sell.AccountID = "closing-account"
	r := Calculate([]Record{buy, sell}, DefaultRates(), Filter{Account: "closing-account"})
	y := find(t, r, 2025, "HKD")
	require.Equal(t, "241.00", y.Net)
	require.Equal(t, "9.00", y.TradeFees)
	require.Equal(t, "6.00", y.IncurredTradeFees)
	require.Equal(t, "2.00", y.UnmatchedCloseFees)
	require.Equal(t, "0.00", y.DeferredOpenFees)
	require.False(t, y.Complete)
	require.Equal(t, "2.000000000000", r.Missing[0].Fee)
	requireDetailsReconcile(t, r)
}

func TestFractionalFeeConservationOnCompleteLiquidation(t *testing.T) {
	rows := []Record{trade("20240101", "基金", "F", "申购", "USD", "3", "-3", "1")}
	for _, day := range []string{"20250101", "20250102", "20250103"} {
		rows = append(rows, trade(day, "基金", "F", "赎回", "USD", "-1", "2", "0.1"))
	}
	r := Calculate(rows, DefaultRates(), Filter{})
	require.Equal(t, "1.30", find(t, r, 2025, "USD").TradeFees)
	require.Equal(t, "0.00", find(t, r, 2025, "USD").DeferredOpenFees)
	require.Equal(t, "1.70", find(t, r, 2025, "USD").Net)
	requireDetailsReconcile(t, r)
}
