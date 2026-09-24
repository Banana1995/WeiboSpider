package ledger

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoredBenchmarksPersistAndReadWithoutUpstream(t *testing.T) {
	root := t.TempDir()
	db, err := Open(t.Context(), root)
	require.NoError(t, err)
	stored := NewStoredBenchmarks(db)
	_, err = stored.Fetch(t.Context(), "H00300", "2026-01-01", "2026-01-31")
	require.ErrorIs(t, err, ErrBenchmarkUnavailable)
	stamp := time.Date(2026, 1, 31, 8, 0, 0, 0, time.UTC)
	value := Benchmark{Code: "H00300", Name: "沪深300全收益", Currency: CNY, Source: "中证指数",
		Items: []BenchmarkItem{{Date: "2026-01-02", Close: "3"}, {Date: "2026-01-05", Close: "4"}}}
	require.NoError(t, stored.SaveWindow(t.Context(), value, "2026-01-01", "2026-01-31", stamp))
	require.NoError(t, db.Close())
	db, err = Open(t.Context(), root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	stored = NewStoredBenchmarks(db)
	result, err := stored.Fetch(t.Context(), "H00300", "2026-01-01", "2026-01-31")
	require.NoError(t, err)
	require.Equal(t, []BenchmarkItem{
		{Date: "2026-01-02", Close: "3", Return: "0.00000000"},
		{Date: "2026-01-05", Close: "4", Return: "0.33333333"},
	}, result.Items)
	other, err := stored.Fetch(t.Context(), "H00300", "2026-01-05", "2026-01-31")
	require.NoError(t, err)
	require.Equal(t, "0.00000000", other.Items[0].Return)
	value.Items[1].Close = "5"
	require.NoError(t, stored.SaveWindow(t.Context(), value, "2026-01-01", "2026-01-31", stamp.Add(time.Hour)))
	result, err = stored.Fetch(t.Context(), "H00300", "2026-01-01", "2026-01-31")
	require.NoError(t, err)
	require.Equal(t, "0.66666667", result.Items[1].Return)
	value.Items[0].Close = "invalid"
	require.ErrorIs(t, stored.SaveWindow(t.Context(), value, "2026-01-01", "2026-01-31", stamp), ErrBenchmarkUnavailable)
	result, err = stored.Fetch(t.Context(), "H00300", "2026-01-01", "2026-01-31")
	require.NoError(t, err)
	require.Equal(t, "3", result.Items[0].Close)
	require.NoError(t, stored.RecordFailure(t.Context(), "H00300", stamp.Add(2*time.Hour), "timeout"))
	status, err := stored.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, "timeout", status[0].ErrorCode)
	require.Equal(t, "2026-01-05", status[0].LastCloseDate)
	require.NotEmpty(t, status[0].LastSuccessAt)

	mux := http.NewServeMux()
	Handler{Benchmark: stored, BenchmarkStatus: stored}.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", ledgerPrefix+"/benchmark?code=H00300&from=2026-01-01&to=2026-01-31", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"return":"0.66666667"`)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", ledgerPrefix+"/benchmark/status", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"last_close_date":"2026-01-05"`)
}

type benchmarkWindowFunc func(context.Context, string, string, string) (Benchmark, error)

func (f benchmarkWindowFunc) FetchWindow(ctx context.Context, code, from, to string) (Benchmark, error) {
	return f(ctx, code, from, to)
}

func TestBenchmarkWorkerBackfillScheduleAndFailure(t *testing.T) {
	f := reportedFixture(t)
	basisEntry(t, f, "manual-first", "2026-01-02", "asset", "null", `"100"`)
	stored := NewStoredBenchmarks(f.store.db)
	now := time.Date(2026, 9, 24, 20, 1, 0, 0, weeklyBeijing)
	calls := map[string]int{}
	fail := false
	source := benchmarkWindowFunc(func(_ context.Context, code, from, to string) (Benchmark, error) {
		calls[code]++
		if fail && code == "H00300" {
			return Benchmark{}, ErrBenchmarkUnavailable
		}
		definition, _ := benchmarkDefinition(code)
		return Benchmark{Code: code, Name: definition.Name, Currency: definition.Currency, Source: definition.Source,
			Items: []BenchmarkItem{{Date: from, Close: "100"}, {Date: to, Close: "110"}}}, nil
	})
	w := NewBenchmarkWorker(stored, source, nil)
	w.now = func() time.Time { return now }
	require.NoError(t, w.Tick(t.Context()))
	require.GreaterOrEqual(t, calls["H00300"], 2) // Recent refresh plus older history.
	require.NoError(t, w.Tick(t.Context()))
	count := calls["H00300"]
	now = now.Add(12 * time.Hour)
	fail = true
	require.ErrorIs(t, w.Tick(t.Context()), ErrBenchmarkUnavailable)
	require.Equal(t, count+1, calls["H00300"])
	result, err := stored.Fetch(t.Context(), "H00300", "2026-01-01", "2026-09-24")
	require.NoError(t, err)
	require.NotEmpty(t, result.Items)
	status, err := stored.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, "source_unavailable", status[0].ErrorCode)
	require.NoError(t, w.Tick(t.Context())) // Five-minute retry delay.
	require.Equal(t, count+1, calls["H00300"])
	fail = false
	now = now.Add(5 * time.Minute)
	require.NoError(t, w.Tick(t.Context()))
	status, err = stored.Status(t.Context())
	require.NoError(t, err)
	require.Empty(t, status[0].ErrorCode)
}

func TestBenchmarkWorkerDailyWindowsAndMissingCoverage(t *testing.T) {
	p := newBenchmarkService(stockTransport(func(r *http.Request) (*http.Response, error) {
		param := r.URL.Query().Get("param")
		require.True(t, strings.HasPrefix(param, "usINX,day,2020-01-01,2020-01-10,"), param)
		return &http.Response{StatusCode: 200, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(tencentJSON("day", tencentRow("2020-01-02", "100"), tencentRow("2020-01-10", "110"))))}, nil
	}), time.Now)
	value, err := p.FetchWindow(t.Context(), "usINX", "2020-01-01", "2020-01-10")
	require.NoError(t, err)
	require.Equal(t, "2020-01-10", value.To)
	require.Len(t, value.Items, 2)

	db, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	stored := NewStoredBenchmarks(db)
	require.NoError(t, stored.SaveWindow(t.Context(), value, "2020-01-01", "2020-01-10", time.Now()))
	missing, err := stored.Missing(t.Context(), "usINX", "2019-12-30", "2020-01-12")
	require.NoError(t, err)
	require.Equal(t, []benchmarkInterval{{"2019-12-30", "2019-12-31"}, {"2020-01-11", "2020-01-12"}}, missing)
	require.NoError(t, stored.SaveWindow(t.Context(), Benchmark{Code: "usINX", Name: "标普500", Currency: USD, Source: "腾讯",
		Items: []BenchmarkItem{{Date: "2020-01-20", Close: "120"}}}, "2020-01-20", "2020-01-31", time.Now()))
	_, err = stored.Fetch(t.Context(), "usINX", "2020-01-01", "2020-01-31")
	require.ErrorIs(t, err, ErrBenchmarkUnavailable) // Do not silently bridge an unsynced historical gap.
	require.ErrorIs(t, stored.RecordFailure(t.Context(), "usINX", time.Now(), "secret"), ErrOperation)
	_, err = stored.Fetch(t.Context(), "unknown", "2020-01-01", "2020-01-02")
	require.ErrorIs(t, err, ErrQuery)
	require.True(t, errors.Is(benchmarkError(context.DeadlineExceeded), ErrBenchmarkTimeout))
}
