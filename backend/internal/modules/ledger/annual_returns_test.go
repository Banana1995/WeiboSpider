package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func annualRead(t *testing.T, f *httpFixture, path string) AnnualReturns {
	t.Helper()
	var result AnnualReturns
	require.NoError(t, json.Unmarshal(f.request(t, "GET", path, "", "", 200).Body.Bytes(), &result))
	return result
}

func TestAnnualReturnsSameSnapshotYearsAndBenchmark(t *testing.T) {
	f := reportedFixture(t)
	basisEntry(t, f, "manual-first", "2024-08-01", "cash_flow", `"100"`, `"100"`)
	basisEntry(t, f, "manual-y1", "2024-12-31", "asset", "null", `"110"`)
	basisEntry(t, f, "manual-in", "2025-01-02", "cash_flow", `"100"`, "null")
	basisEntry(t, f, "manual-y2", "2025-12-31", "asset", "null", `"231"`)
	basisEntry(t, f, "manual-y3", "2026-09-01", "asset", "null", `"250"`)
	index := []BenchmarkItem{
		{Date: "2024-07-31", Close: "100"}, {Date: "2024-08-02", Close: "102"},
		{Date: "2024-12-31", Close: "110"}, {Date: "2025-01-02", Close: "120"},
		{Date: "2025-12-31", Close: "130"}, {Date: "2026-09-01", Close: "120"},
	}
	calls := 0
	provider := benchmarkFunc(func(_ context.Context, code, from, to string) (Benchmark, error) {
		calls++
		require.Equal(t, "H00300", code)
		require.Equal(t, "2024-07-02", from)
		require.Equal(t, "2026-09-01", to)
		return Benchmark{Code: code, Name: "沪深300全收益", Currency: CNY, Source: "中证指数", Items: index}, nil
	})
	f.mux = http.NewServeMux()
	Handler{Store: f.store, Benchmark: provider, Logger: slog.New(slog.NewJSONHandler(&f.logs, nil))}.Register(f.mux)
	before := f.snapshot(t)
	out := annualRead(t, f, "/accounts/a/annual-returns")
	require.Equal(t, before, f.snapshot(t))
	require.Equal(t, 1, calls)
	require.Equal(t, "", out.BenchmarkError)
	require.Equal(t, "H00300", out.BenchmarkCode)
	require.Len(t, out.Years, 3)
	require.Equal(t, []int{2024, 2025, 2026}, []int{out.Years[0].Year, out.Years[1].Year, out.Years[2].Year})
	require.Equal(t, "2024-08-01", out.Years[0].From)
	require.Equal(t, "2024-12-31", out.Years[0].To)
	require.Equal(t, "2024-07-31", out.Years[0].BenchmarkFrom)
	require.Equal(t, "0.100000000000", *out.Years[0].MoneyWeighted.Value)
	require.Equal(t, "10.00", *out.Years[0].Benchmark.Percentage)
	require.Equal(t, "2024-12-31", out.Years[1].From)
	require.Equal(t, "2025-12-31", out.Years[1].To)
	require.Equal(t, "18.18", *out.Years[1].Benchmark.Percentage)
	require.Equal(t, "2026-09-01", out.Years[2].To)
	require.Equal(t, out.Since.From, out.Annualized.From)
	require.Equal(t, out.Since.To, out.Annualized.To)
	require.NotNil(t, out.Annualized.Benchmark.Value)
	whole := basisRead(t, f, "a", "", "").Returns
	require.Equal(t, whole.Dietz, out.Since.MoneyWeighted)
	require.Equal(t, whole.TWR, out.Since.TimeWeighted)
	require.Equal(t, whole.XIRR, out.Annualized.MoneyWeighted)
	require.Equal(t, whole.TWRAnnualized, out.Annualized.TimeWeighted)
	lastYear := basisRead(t, f, "a", "", "from=2025-01-01&to=2025-12-31").Returns
	require.Equal(t, lastYear.Dietz, out.Years[1].MoneyWeighted)
	require.Equal(t, lastYear.TWR, out.Years[1].TimeWeighted)
	require.Empty(t, f.request(t, "HEAD", "/accounts/a/annual-returns", "", "", 200).Body.String())
}

func TestAnnualReturnsGapsAndUnavailableBenchmark(t *testing.T) {
	f := reportedFixture(t)
	basisEntry(t, f, "manual-start", "2024-08-01", "asset", "null", `"100"`)
	basisEntry(t, f, "manual-end", "2026-09-01", "asset", "null", `"120"`)
	result := annualRead(t, f, "/accounts/a/annual-returns")
	require.Equal(t, "benchmark_unavailable", result.BenchmarkError)
	require.Len(t, result.Years, 3)
	require.Equal(t, 2025, result.Years[1].Year)
	require.Nil(t, result.Years[1].MoneyWeighted.Value)
	require.Equal(t, "unavailable", result.Years[1].MoneyWeighted.Status)
	require.Equal(t, "benchmark_unavailable", result.Years[1].Benchmark.Reason)
	require.NotNil(t, result.Since.MoneyWeighted.Value)

	provider := benchmarkFunc(func(_ context.Context, code, from, to string) (Benchmark, error) {
		return Benchmark{Code: code, Name: "沪深300全收益", Currency: CNY, Source: "中证指数",
			Items: []BenchmarkItem{{Date: "2024-08-02", Close: "102"}, {Date: "2026-09-01", Close: "120"}}}, nil
	})
	f.mux = http.NewServeMux()
	Handler{Store: f.store, Benchmark: provider}.Register(f.mux)
	result = annualRead(t, f, "/accounts/a/annual-returns")
	require.Equal(t, "missing_benchmark", result.Since.Benchmark.Reason)
	require.Equal(t, "missing_benchmark", result.Years[0].Benchmark.Reason)
	require.Empty(t, result.BenchmarkError)
	require.NotNil(t, result.Since.TimeWeighted.Value)

	f.mux = http.NewServeMux()
	Handler{Store: f.store, Benchmark: benchmarkFunc(func(context.Context, string, string, string) (Benchmark, error) {
		return Benchmark{}, errors.New("upstream secret")
	})}.Register(f.mux)
	result = annualRead(t, f, "/accounts/a/annual-returns")
	require.Equal(t, "benchmark_unavailable", result.BenchmarkError)
	require.NotNil(t, result.Since.MoneyWeighted.Value)
	require.Empty(t, result.Since.BenchmarkFrom)
}

func TestAnnualBenchmarkExactReturnsAndValidation(t *testing.T) {
	items := []BenchmarkItem{{Date: "2024-12-31", Close: "3"}, {Date: "2025-01-03", Close: "4"}}
	rate, start, end := periodBenchmark(items, "2025-01-01", "2025-01-03")
	require.Equal(t, "0.333333333333", *rate.Value)
	require.Equal(t, "33.33", *rate.Percentage)
	require.Equal(t, "2024-12-31", start)
	require.Equal(t, "2025-01-03", end)
	annual, err := annualizeTWR(t.Context(), big.NewRat(4, 3), "2025-01-01", "2026-01-01", "available")
	require.NoError(t, err)
	require.Equal(t, "0.333333333333", *annual.Value)
	require.Equal(t, "missing_benchmark", func() string {
		rate, _, _ := periodBenchmark(items[1:], "2025-01-01", "2025-01-03")
		return rate.Reason
	}())
	require.Nil(t, indexAsOf(items, "2025-02-05")) // >30 days is not a fresh close

	f := reportedFixture(t)
	f.request(t, "GET", "/accounts/a/annual-returns?benchmark=SPX", "", "", 400)
	f.request(t, "GET", "/accounts/a/annual-returns?benchmark=H00300&other=x", "", "", 400)
	f.request(t, "GET", "/accounts/a/annual-returns?benchmark=H00300&benchmark=H00300", "", "", 400)
	f.request(t, "GET", "/accounts/missing/annual-returns", "", "", 404)
	f.request(t, "POST", "/accounts/a/annual-returns", "", "{}", 405)
}

func TestAnnualBenchmarkLongUSHistoryUsesDailyWindows(t *testing.T) {
	calls := 0
	h := Handler{Benchmark: benchmarkFunc(func(_ context.Context, code, from, to string) (Benchmark, error) {
		calls++
		require.Equal(t, "usINX", code)
		period, _ := tencentGranularity(calendarDays(from, to))
		require.Equal(t, "day", period)
		return Benchmark{Code: code, Name: "标普500", Currency: USD, Source: "腾讯", Items: []BenchmarkItem{}}, nil
	})}
	items, err := h.annualBenchmarkHistory(t.Context(), "usINX", "2020-01-01", "2026-09-01")
	require.NoError(t, err)
	require.Empty(t, items)
	require.Equal(t, 3, calls)
}
