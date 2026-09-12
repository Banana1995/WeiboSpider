package ledger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const benchmarkFrom = "2026-01-01"
const benchmarkTo = "2026-01-31"

type benchmarkFunc func(context.Context, string, string, string) (Benchmark, error)

func (f benchmarkFunc) Fetch(ctx context.Context, code, from, to string) (Benchmark, error) {
	return f(ctx, code, from, to)
}

func benchmarkJSON(rows ...string) string {
	return `{"code":"200","msg":"Success","data":[` + strings.Join(rows, ",") + `]}`
}

func benchmarkRow(date, close string) string {
	return fmt.Sprintf(`{"tradeDate":%q,"indexCode":"H00300","indexNameCnAll":"沪深300全收益指数","close":%s}`, date, close)
}

func benchmarkProvider(t *testing.T, body string) (*CSIndexBenchmark, *int32) {
	t.Helper()
	var calls int32
	p := newCSIndexBenchmark(stockTransport(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		require.Equal(t, "https", r.URL.Scheme)
		require.Equal(t, "www.csindex.com.cn", r.URL.Host)
		require.Equal(t, "/csindex-home/perf/index-perf", r.URL.Path)
		require.Equal(t, "H00300", r.URL.Query().Get("indexCode"))
		require.Equal(t, "20260101", r.URL.Query().Get("startDate"))
		require.Equal(t, "20260131", r.URL.Query().Get("endDate"))
		require.Equal(t, "Mozilla/5.0", r.Header.Get("User-Agent"))
		require.Equal(t, "https://www.csindex.com.cn/", r.Header.Get("Referer"))
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	}), func() time.Time { return stockNow })
	return p, &calls
}

func TestBenchmarkReturnMath(t *testing.T) {
	p, _ := benchmarkProvider(t, benchmarkJSON(
		benchmarkRow("20260102", "200000000"),
		benchmarkRow("20260105", "200000001"),
		benchmarkRow("20260106", "199999999"),
	))
	result, err := p.Fetch(t.Context(), "H00300", benchmarkFrom, benchmarkTo)
	require.NoError(t, err)
	require.Equal(t, Benchmark{
		Code: "H00300", Name: "沪深300全收益", Currency: CNY, Source: "中证指数",
		From: "2026-01-02", To: "2026-01-06",
		Items: []BenchmarkItem{
			{Date: "2026-01-02", Close: "200000000", Return: "0.00000000"},
			{Date: "2026-01-05", Close: "200000001", Return: "0.00000001"},
			{Date: "2026-01-06", Close: "199999999", Return: "-0.00000001"},
		},
	}, result)

	p, _ = benchmarkProvider(t, benchmarkJSON(
		benchmarkRow("20260102", "3"),
		benchmarkRow("20260103", "4"),
	))
	result, err = p.Fetch(t.Context(), "H00300", benchmarkFrom, benchmarkTo)
	require.NoError(t, err)
	require.Equal(t, "0.33333333", result.Items[1].Return)
	require.Regexp(t, `^-?\d+\.\d{8}$`, result.Items[1].Return)
}

func TestBenchmarkEmptyData(t *testing.T) {
	p, calls := benchmarkProvider(t, benchmarkJSON())
	result, err := p.Fetch(t.Context(), "H00300", benchmarkFrom, benchmarkTo)
	require.NoError(t, err)
	require.NotNil(t, result.Items)
	require.Empty(t, result.Items)
	require.Equal(t, benchmarkFrom, result.From)
	require.Equal(t, benchmarkTo, result.To)
	require.Equal(t, int32(1), atomic.LoadInt32(calls))
}

func TestBenchmarkRejectsBadPayloads(t *testing.T) {
	for _, body := range []string{
		``, `null`, `[]`, `{}`, `{"code":"200"}`,
		`{"code":"200","data":null}`,
		`{"code":"500","data":[]}`,
		`{"code":200,"data":[]}`,
		`{"code":"200","data":[]} trailing`,
		`{"code":"200","data":[],"code":"200"}`,
		`{"code":"200","msg":"Success","data":"bad"}`,
		benchmarkJSON(benchmarkRow("20261301", "100")),
		benchmarkJSON(benchmarkRow("20260102", "0")),
		benchmarkJSON(benchmarkRow("20260102", "-1")),
		benchmarkJSON(benchmarkRow("20260102", "1e3")),
		benchmarkJSON(benchmarkRow("20260102", "NaN")),
		benchmarkJSON(benchmarkRow("20260102", "100"), benchmarkRow("20260102", "101")),
		benchmarkJSON(benchmarkRow("20260103", "100"), benchmarkRow("20260102", "101")),
	} {
		p, _ := benchmarkProvider(t, body)
		result, err := p.Fetch(t.Context(), "H00300", benchmarkFrom, benchmarkTo)
		require.ErrorIs(t, err, ErrBenchmarkUnavailable, body)
		require.Empty(t, result.Items)
	}
}

func TestBenchmarkRejectsTooManyItems(t *testing.T) {
	rows := make([]string, 0, benchmarkMaxItems+1)
	date := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for range benchmarkMaxItems + 1 {
		rows = append(rows, benchmarkRow(date.Format("20060102"), "100"))
		date = date.AddDate(0, 0, 1)
	}
	_, err := parseBenchmark([]byte(benchmarkJSON(rows...)), "H00300", "2020-01-01", "2099-12-31", benchmarkIndexes["H00300"])
	require.ErrorIs(t, err, ErrBenchmarkUnavailable)
}

func TestBenchmarkQueryValidation(t *testing.T) {
	p, calls := benchmarkProvider(t, benchmarkJSON())
	for _, tc := range []struct{ code, from, to string }{
		{"H00301", benchmarkFrom, benchmarkTo},
		{"h00300", benchmarkFrom, benchmarkTo},
		{"", benchmarkFrom, benchmarkTo},
		{"H00300", "2026-1-1", benchmarkTo},
		{"H00300", "2026-02-30", benchmarkTo},
		{"H00300", benchmarkFrom, "2026-1-31"},
		{"H00300", benchmarkTo, benchmarkFrom},
		{"H00300", "2000-01-01", "2016-01-01"},
	} {
		result, err := p.Fetch(t.Context(), tc.code, tc.from, tc.to)
		require.ErrorIs(t, err, ErrQuery, tc.code, tc.from, tc.to)
		require.Empty(t, result.Items)
	}
	require.Zero(t, atomic.LoadInt32(calls))
}

func TestBenchmarkNetworkBounds(t *testing.T) {
	for _, failure := range []string{"status", "redirect", "oversize", "network", "timeout", "read"} {
		t.Run(failure, func(t *testing.T) {
			p := newCSIndexBenchmark(stockTransport(func(r *http.Request) (*http.Response, error) {
				response := &http.Response{StatusCode: 200, Header: make(http.Header)}
				switch failure {
				case "status":
					response.StatusCode = 503
				case "redirect":
					response.StatusCode = 302
					response.Header.Set("Location", "https://evil.example/")
				case "oversize":
					response.Body = io.NopCloser(strings.NewReader(strings.Repeat(" ", maxBenchmarkBody+1)))
				case "network":
					return nil, errors.New("network failure")
				case "timeout":
					<-r.Context().Done()
					return nil, r.Context().Err()
				case "read":
					response.Body = io.NopCloser(benchmarkBrokenReader{})
				}
				if response.Body == nil {
					response.Body = io.NopCloser(strings.NewReader(benchmarkJSON()))
				}
				return response, nil
			}), func() time.Time { return stockNow })
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			result, err := p.Fetch(ctx, "H00300", benchmarkFrom, benchmarkTo)
			want := ErrBenchmarkUnavailable
			if failure == "timeout" {
				want = ErrBenchmarkTimeout
			}
			require.ErrorIs(t, err, want)
			require.Empty(t, result.Items)
			require.Empty(t, stockQuoteSlots)
		})
	}
}

type benchmarkBrokenReader struct{}

func (benchmarkBrokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestBenchmarkRedirectAndCancellation(t *testing.T) {
	var calls int32
	p := newCSIndexBenchmark(stockTransport(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://evil.example/"}}, Body: http.NoBody}, nil
	}), func() time.Time { return stockNow })
	_, err := p.Fetch(t.Context(), "H00300", benchmarkFrom, benchmarkTo)
	require.ErrorIs(t, err, ErrBenchmarkUnavailable)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))

	// A canceled context must not consume an upstream slot or make a request.
	for range cap(stockQuoteSlots) {
		stockQuoteSlots <- struct{}{}
	}
	defer func() {
		for range cap(stockQuoteSlots) {
			<-stockQuoteSlots
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = p.Fetch(ctx, "H00300", benchmarkFrom, benchmarkTo)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

func TestBenchmarkCache(t *testing.T) {
	now := stockNow
	var calls int32
	p := newCSIndexBenchmark(stockTransport(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return &http.Response{StatusCode: 200, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(benchmarkJSON(benchmarkRow("20260102", "100"))))}, nil
	}), func() time.Time { return now })
	first, err := p.Fetch(t.Context(), "H00300", benchmarkFrom, benchmarkTo)
	require.NoError(t, err)
	second, err := p.Fetch(t.Context(), "H00300", benchmarkFrom, benchmarkTo)
	require.NoError(t, err)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
	require.Equal(t, first.Items, second.Items)
	_, err = p.Fetch(t.Context(), "H00300", "2026-02-01", "2026-02-28")
	require.NoError(t, err)
	require.Equal(t, int32(2), atomic.LoadInt32(&calls))
	now = now.Add(benchmarkCacheTTL + time.Second)
	_, err = p.Fetch(t.Context(), "H00300", benchmarkFrom, benchmarkTo)
	require.NoError(t, err)
	require.Equal(t, int32(3), atomic.LoadInt32(&calls))
}

func TestBenchmarkCacheBounded(t *testing.T) {
	var calls int32
	p := newCSIndexBenchmark(stockTransport(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return &http.Response{StatusCode: 200, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(benchmarkJSON(benchmarkRow("20260102", "100"))))}, nil
	}), func() time.Time { return stockNow })
	for n := range benchmarkCacheMax + 6 {
		to := stockNow.AddDate(0, 0, n).Format(time.DateOnly)
		_, err := p.Fetch(t.Context(), "H00300", benchmarkFrom, to)
		require.NoError(t, err)
	}
	p.mu.Lock()
	size := len(p.cache)
	p.mu.Unlock()
	require.LessOrEqual(t, size, benchmarkCacheMax)
	require.Equal(t, int32(benchmarkCacheMax+6), atomic.LoadInt32(&calls))
}

func TestBenchmarkHTTP(t *testing.T) {
	valid := Benchmark{Code: "H00300", Name: "沪深300全收益", Currency: CNY, Source: "中证指数",
		From: benchmarkFrom, To: benchmarkTo, Items: []BenchmarkItem{{Date: benchmarkFrom, Close: "100", Return: "0.00000000"}}}
	for _, method := range []string{"GET", "HEAD"} {
		mux := http.NewServeMux()
		Handler{Benchmark: benchmarkFunc(func(ctx context.Context, code, from, to string) (Benchmark, error) {
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.WithinDuration(t, time.Now().Add(benchmarkTimeout), deadline, time.Second)
			require.Equal(t, "H00300", code)
			require.Equal(t, benchmarkFrom, from)
			require.Equal(t, benchmarkTo, to)
			return valid, nil
		})}.Register(mux)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, ledgerPrefix+"/benchmark?code=H00300&from="+benchmarkFrom+"&to="+benchmarkTo, nil))
		require.Equal(t, 200, w.Code)
		if method == "HEAD" {
			require.Empty(t, w.Body.String())
		} else {
			require.JSONEq(t, `{"code":"H00300","name":"沪深300全收益","currency":"CNY","source":"中证指数","from":"2026-01-01","to":"2026-01-31","items":[{"date":"2026-01-01","close":"100","return":"0.00000000"}]}`, w.Body.String())
		}
	}
	for _, q := range []string{
		"", "code=H00300", "from=" + benchmarkFrom + "&to=" + benchmarkTo,
		"code=H00300&from=" + benchmarkFrom,
		"code=H00300&from=" + benchmarkFrom + "&to=" + benchmarkTo + "&extra=1",
		"code=H00300&code=H00300&from=" + benchmarkFrom + "&to=" + benchmarkTo,
		"code=H00301&from=" + benchmarkFrom + "&to=" + benchmarkTo,
		"code=H00300&from=2026-1-1&to=" + benchmarkTo,
		"code=H00300&from=" + benchmarkTo + "&to=" + benchmarkFrom,
		"code=H00300&from=2000-01-01&to=2016-01-01",
	} {
		calls := 0
		mux := http.NewServeMux()
		Handler{Benchmark: benchmarkFunc(func(context.Context, string, string, string) (Benchmark, error) {
			calls++
			return Benchmark{}, nil
		})}.Register(mux)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", ledgerPrefix+"/benchmark?"+q, nil))
		require.Equal(t, 400, w.Code, q)
		require.Contains(t, w.Body.String(), `"code":"invalid_query"`, q)
		require.Zero(t, calls, q)
	}
	for _, tc := range []struct {
		name, code string
		provider   BenchmarkProvider
		status     int
	}{
		{"nil", "benchmark_unavailable", nil, 502},
		{"failure", "benchmark_unavailable", benchmarkFunc(func(context.Context, string, string, string) (Benchmark, error) {
			return Benchmark{}, errors.New("private upstream details")
		}), 502},
		{"timeout", "benchmark_timeout", benchmarkFunc(func(context.Context, string, string, string) (Benchmark, error) {
			return Benchmark{}, ErrBenchmarkTimeout
		}), 504},
		{"deadline", "benchmark_timeout", benchmarkFunc(func(context.Context, string, string, string) (Benchmark, error) {
			return Benchmark{}, fmt.Errorf("wrapped: %w", context.DeadlineExceeded)
		}), 504},
		{"canceled", "request_canceled", benchmarkFunc(func(context.Context, string, string, string) (Benchmark, error) {
			return Benchmark{}, context.Canceled
		}), 408},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			Handler{Benchmark: tc.provider}.Register(mux)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("GET", ledgerPrefix+"/benchmark?code=H00300&from="+benchmarkFrom+"&to="+benchmarkTo, nil))
			require.Equal(t, tc.status, w.Code)
			require.Contains(t, w.Body.String(), `"code":"`+tc.code+`"`)
			require.NotContains(t, w.Body.String(), "private upstream")
		})
	}
	mux := http.NewServeMux()
	Handler{}.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", ledgerPrefix+"/benchmark?code=H00300&from="+benchmarkFrom+"&to="+benchmarkTo, nil))
	require.Equal(t, 405, w.Code)
	require.Equal(t, "GET, HEAD", w.Header().Get("Allow"))
}
