package ledger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/simplifiedchinese"
)

type instrumentSearchFunc func(context.Context, string) ([]InstrumentSearchItem, error)

func (f instrumentSearchFunc) Search(ctx context.Context, code string) ([]InstrumentSearchItem, error) {
	return f(ctx, code)
}

func searchRow(t *testing.T, symbol, kind string, currency Currency, change func([]string)) string {
	t.Helper()
	name, err := simplifiedchinese.GBK.NewEncoder().String("\u8bc1\u5238\u540d\u79f0")
	require.NoError(t, err)
	return stockRow(Instrument{Market: strings.ToUpper(symbol[:2]), Code: symbol[2:], Currency: currency}, func(f []string) {
		f[1], f[61], f[82] = name, kind, string(currency)
		if strings.HasPrefix(symbol, "hk") {
			f[63], f[75] = kind, string(currency)
		}
		if change != nil {
			change(f)
		}
	})
}

func searchJSON(symbol, kind string) string {
	return fmt.Sprintf(`{"stock":[{"code":%q,"name":"name","type":%q}],"fund":[]}`, symbol, kind)
}

func searchProvider(t *testing.T, search, quotes string) (*TencentQuotes, *[]string) {
	t.Helper()
	var requests []string
	p := newTencentQuotes(stockTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "https", r.URL.Scheme)
		require.Equal(t, http.MethodGet, r.Method)
		require.Empty(t, r.Header.Get("Authorization"))
		requests = append(requests, r.URL.String())
		body := search
		switch r.URL.Host {
		case "proxy.finance.qq.com":
			require.Equal(t, "/cgi/cgi-bin/smartbox/search", r.URL.Path)
			require.Equal(t, "1", r.URL.Query().Get("stockFlag"))
			require.Equal(t, "1", r.URL.Query().Get("fundFlag"))
			require.Equal(t, "official_website", r.URL.Query().Get("app"))
			require.Equal(t, "1", r.URL.Query().Get("c"))
		case "qt.gtimg.cn":
			require.Equal(t, "/", r.URL.Path)
			body = quotes
		default:
			t.Fatalf("unexpected upstream: %s", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	}), time.Now)
	return p, &requests
}

func TestInstrumentSearchIdentities(t *testing.T) {
	for _, tc := range []struct {
		symbol, kind string
		currency     Currency
	}{
		{"sh600519", "GP-A", CNY}, {"sz000001", "GP-A", CNY},
		{"sh688001", "GP-A-KCB", CNY}, {"sz300001", "GP-A-CYB", CNY},
		{"sh900901", "GP-B", USD}, {"sz200012", "GP-B", HKD},
		{"hk00700", "GP", HKD}, {"hk80700", "GP", CNY}, {"hk09001", "GP", USD},
	} {
		t.Run(tc.symbol, func(t *testing.T) {
			for _, input := range []string{tc.symbol, tc.symbol[2:]} {
				p, requests := searchProvider(t, searchJSON(tc.symbol, tc.kind), searchRow(t, tc.symbol, tc.kind, tc.currency, nil))
				items, err := p.Search(t.Context(), input)
				require.NoError(t, err)
				require.Equal(t, []InstrumentSearchItem{{Name: "\u8bc1\u5238\u540d\u79f0", Market: strings.ToUpper(tc.symbol[:2]), Code: tc.symbol[2:], Currency: tc.currency}}, items)
				require.Len(t, *requests, 2)
				u, err := url.Parse((*requests)[0])
				require.NoError(t, err)
				require.Equal(t, tc.symbol[2:], u.Query().Get("query"))
				require.Equal(t, "https://qt.gtimg.cn/?q="+tc.symbol, (*requests)[1])
			}
		})
	}
	// Returning a valid HK CNY identity must not broaden existing valuation support.
	_, reason := quoteSymbol(Instrument{Market: "HK", Code: "80700", Currency: CNY})
	require.Equal(t, "currency_mismatch", reason)
	// U+4E8A encodes as GBK 81 7E: the trail byte is also the ASCII field delimiter.
	p, _ := searchProvider(t, searchJSON("sh600519", "GP-A"), searchRow(t, "sh600519", "GP-A", CNY, func(f []string) {
		f[1] = "\x81\x7e"
	}))
	items, err := p.Search(t.Context(), "600519")
	require.NoError(t, err)
	require.Equal(t, "\u4e8a", items[0].Name)
}

func TestInstrumentSearchFiltering(t *testing.T) {
	for _, tc := range []struct {
		name, input, search, quotes string
		markets                     []string
	}{
		{"empty", "999999", `{"stock":[],"fund":[]}`, "", nil},
		{"fund", "510300", `{"stock":[{"code":"sh510300","name":"ETF","type":"JJ"}],"fund":[{"code":"510300"}]}`, "", nil},
		{"unknown", "600519", searchJSON("sh600519", "GP-NEW"), "", nil},
		{"other market", "600519", searchJSON("bj600519", "GP-A"), "", nil},
		{"index", "000001", `{"stock":[{"code":"sh000001","name":"index","type":"ZS"},{"code":"sz000001","name":"bank","type":"GP-A"}],"fund":[]}`,
			searchRow(t, "sz000001", "GP-A", CNY, nil), []string{"SZ"}},
		{"related HK", "00700", `{"stock":[{"code":"hk00700","name":"stock","type":"GP"},{"code":"hk80700","name":"related","type":"GP"}],"fund":[]}`,
			searchRow(t, "hk00700", "GP", HKD, nil), []string{"HK"}},
		{"partial", "600519", searchJSON("sh600518", "GP-A"), "", nil},
		{"prefix", "sh000001", searchJSON("sz000001", "GP-A"), "", nil},
		// Synthetic cross-market ambiguity: no first-result auto-selection in the API.
		{"ambiguous", "600519", `{"stock":[{"code":"sh600519","name":"one","type":"GP-A"},{"code":"sz600519","name":"two","type":"GP-A"}],"fund":[]}`,
			searchRow(t, "sh600519", "GP-A", CNY, nil) + searchRow(t, "sz600519", "GP-A", CNY, nil), []string{"SH", "SZ"}},
		{"duplicate", "600519", `{"stock":[{"code":"sh600519","name":"one","type":"GP-A"},{"code":"sh600519","name":"one","type":"GP-A"}],"fund":[]}`,
			searchRow(t, "sh600519", "GP-A", CNY, nil), []string{"SH"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, requests := searchProvider(t, tc.search, tc.quotes)
			items, err := p.Search(t.Context(), tc.input)
			require.NoError(t, err)
			require.NotNil(t, items)
			require.Len(t, items, len(tc.markets))
			for n, market := range tc.markets {
				require.Equal(t, market, items[n].Market)
			}
			if len(items) == 0 {
				require.Len(t, *requests, 1)
			} else {
				require.Len(t, *requests, 2)
			}
		})
	}
}

func TestInstrumentSearchCorruptResponses(t *testing.T) {
	for _, body := range []string{
		``, `{}`, `null`, `{"error":"unavailable"}`, `{"stock":[],"fund":[]} alert(1)`,
		`{"stock":null,"fund":[]}`, `{"stock":[]}`, `{"stock":[],"stock":[],"fund":[]}`,
		`{"stock":[{}],"fund":[]}`, `{"stock":"bad","fund":[]}`,
		searchJSON("sh60051", "GP-A"), searchJSON("sh600519;alert(1)", "GP-A"),
		`{"stock":[{"code":"sh600519","name":"<script>alert(1)</script>","type":"GP-A"}],"fund":[]}`,
		`{"stock":[{"code":"sh600519","name":"\ud800","type":"GP-A"}],"fund":[]}`,
		`{"stock":[{"code":"sh600519","name":"one","type":"GP-A"},{"code":"sh600519","name":"two","type":"GP-B"}],"fund":[]}`,
		"{\"stock\":[],\"fund\":[],\"extra\":\"\xff\"}",
	} {
		p, requests := searchProvider(t, body, "")
		items, err := p.Search(t.Context(), "600519")
		require.ErrorIs(t, err, ErrInstrumentSearchUnavailable, body)
		require.Nil(t, items)
		require.Len(t, *requests, 1)
	}
	valid := searchRow(t, "sh600519", "GP-A", CNY, nil)
	for _, body := range []string{
		"", `v_pv_none_match="1";`, `v_sh600519="short";`,
		strings.Replace(valid, "v_sh600519", "v_sz600519", 1),
		valid + valid, valid + "alert(1);", strings.TrimSuffix(valid, ";"),
		searchRow(t, "sh600519", "GP-A", CNY, func(f []string) { f[2] = "600518" }),
		searchRow(t, "sh600519", "GP-A", CNY, func(f []string) { f[61] = "ZS" }),
		searchRow(t, "sh600519", "GP-A", CNY, func(f []string) { f[61] = "GP-UNKNOWN" }),
		searchRow(t, "sh600519", "GP-A", CNY, func(f []string) { f[82] = "EUR" }),
		searchRow(t, "sh600519", "GP-A", CNY, func(f []string) { f[82] = "USD" }),
		searchRow(t, "sh600519", "GP-A", CNY, func(f []string) { f[1] = "\xff" }),
		searchRow(t, "sh600519", "GP-A", CNY, func(f []string) { f[1] = "\x81" }),
		searchRow(t, "sh600519", "GP-A", CNY, func(f []string) { f[1] = "   " }),
		searchRow(t, "sh600519", "GP-A", CNY, func(f []string) { f[1] = "name\x00" }),
		searchRow(t, "sh600519", "GP-A", CNY, func(f []string) { f[1] = "<script>" }),
		searchRow(t, "sh600519", "GP-A", CNY, func(f []string) { f[1] = `name";alert(1);x="` }),
		searchRow(t, "sh600519", "GP-A", CNY, func(f []string) { f[1] = strings.Repeat("x", 513) }),
	} {
		p, _ := searchProvider(t, searchJSON("sh600519", "GP-A"), body)
		items, err := p.Search(t.Context(), "600519")
		require.ErrorIs(t, err, ErrInstrumentSearchUnavailable, body)
		require.Nil(t, items)
	}
	// One missing candidate invalidates the whole response, never a partial success.
	p, _ := searchProvider(t, `{"stock":[{"code":"sh600519","name":"one","type":"GP-A"},{"code":"sz600519","name":"two","type":"GP-A"}],"fund":[]}`, valid)
	items, err := p.Search(t.Context(), "600519")
	require.ErrorIs(t, err, ErrInstrumentSearchUnavailable)
	require.Nil(t, items)
}

func TestInstrumentSearchHTTP(t *testing.T) {
	for _, tc := range []struct {
		query string
		valid bool
	}{
		{"code=600519", true}, {"code=000001", true}, {"code=00700", true},
		{"code=sh600519", true}, {"code=sz000001", true}, {"code=hk00700", true},
		{"", false}, {"code=", false}, {"q=600519", false}, {"code=600519&code=600519", false},
		{"code=600519&market=SH", false}, {"code=600519&extra=", false}, {"code=%zz", false},
		{"code=600519;x=1", false}, {"code=600519%26q%3Dhk00700", false},
		{"code=SH600519", false}, {"code=hk600519", false}, {"code=sh00700", false},
		{"code=6005", false}, {"code=6005190", false}, {"code=700", false},
		{"code=+600519", false}, {"code=600519%20", false}, {"code=%E8%8C%85%E5%8F%B0", false},
		{"code=60051%00", false}, {"code=https%3A%2F%2Fevil.example", false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			calls := 0
			mux := http.NewServeMux()
			Handler{InstrumentSearch: instrumentSearchFunc(func(ctx context.Context, code string) ([]InstrumentSearchItem, error) {
				calls++
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.WithinDuration(t, time.Now().Add(instrumentSearchTimeout), deadline, time.Second)
				require.Equal(t, strings.TrimPrefix(tc.query, "code="), code)
				return nil, nil
			})}.Register(mux)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("GET", ledgerPrefix+"/instruments/search?"+tc.query, nil))
			if tc.valid {
				require.Equal(t, 200, w.Code)
				require.JSONEq(t, `{"items":[]}`, w.Body.String())
				require.Equal(t, 1, calls)
			} else {
				require.Equal(t, 400, w.Code)
				require.Contains(t, w.Body.String(), `"code":"invalid_query"`)
				require.Zero(t, calls)
				values, err := url.ParseQuery(tc.query)
				if err == nil && values.Get("code") != "600519" {
					p, requests := searchProvider(t, "", "")
					_, err := p.Search(t.Context(), values.Get("code"))
					require.ErrorIs(t, err, ErrQuery)
					require.Empty(t, *requests)
				}
			}
		})
	}
	for _, tc := range []struct {
		name, method, code string
		provider           InstrumentSearchProvider
		status             int
	}{
		{"nil", "GET", "instrument_search_unavailable", nil, 502},
		{"failure", "GET", "instrument_search_unavailable", instrumentSearchFunc(func(context.Context, string) ([]InstrumentSearchItem, error) {
			return nil, errors.New("private upstream details")
		}), 502},
		{"timeout", "GET", "instrument_search_timeout", instrumentSearchFunc(func(context.Context, string) ([]InstrumentSearchItem, error) { return nil, ErrInstrumentSearchTimeout }), 504},
		{"deadline", "GET", "instrument_search_timeout", instrumentSearchFunc(func(context.Context, string) ([]InstrumentSearchItem, error) {
			return nil, fmt.Errorf("wrapped: %w", context.DeadlineExceeded)
		}), 504},
		{"canceled", "GET", "request_canceled", instrumentSearchFunc(func(context.Context, string) ([]InstrumentSearchItem, error) { return nil, context.Canceled }), 408},
		{"method", "POST", "method_not_allowed", nil, 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			Handler{InstrumentSearch: tc.provider}.Register(mux)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(tc.method, ledgerPrefix+"/instruments/search?code=600519", nil))
			require.Equal(t, tc.status, w.Code)
			require.Contains(t, w.Body.String(), `"code":"`+tc.code+`"`)
			require.NotContains(t, w.Body.String(), "private upstream")
			if tc.status == 405 {
				require.Equal(t, "GET, HEAD", w.Header().Get("Allow"))
			}
		})
	}
	mux := http.NewServeMux()
	Handler{InstrumentSearch: instrumentSearchFunc(func(context.Context, string) ([]InstrumentSearchItem, error) {
		return []InstrumentSearchItem{{Name: "name", Market: "SH", Code: "600519", Currency: CNY}, {Name: "other", Market: "SZ", Code: "600519", Currency: CNY}}, nil
	})}.Register(mux)
	for _, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, ledgerPrefix+"/instruments/search?code=600519", nil))
		require.Equal(t, 200, w.Code)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		require.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))
		if method == "HEAD" {
			require.Empty(t, w.Body.String())
		} else {
			require.JSONEq(t, `{"items":[{"name":"name","market":"SH","code":"600519","currency":"CNY"},{"name":"other","market":"SZ","code":"600519","currency":"CNY"}]}`, w.Body.String())
		}
	}
}

func TestInstrumentSearchNetworkBounds(t *testing.T) {
	for _, host := range []string{"proxy.finance.qq.com", "qt.gtimg.cn"} {
		for _, failure := range []string{"status", "redirect", "oversize", "network", "timeout", "read"} {
			t.Run(host+"/"+failure, func(t *testing.T) {
				calls := 0
				p := newTencentQuotes(stockTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					body := searchJSON("sh600519", "GP-A")
					response := &http.Response{StatusCode: 200, Header: make(http.Header)}
					if r.URL.Host == host {
						switch failure {
						case "status":
							response.StatusCode = 503
						case "redirect":
							response.StatusCode = 302
							response.Header.Set("Location", "https://evil.example/")
						case "oversize":
							limit := 256 << 10
							if host == "qt.gtimg.cn" {
								limit = 1 << 20
							}
							body = strings.Repeat(" ", limit+1)
						case "network":
							return nil, errors.New("network failure")
						case "timeout":
							<-r.Context().Done()
							return nil, r.Context().Err()
						case "read":
							response.Body = io.NopCloser(searchBrokenReader{})
							return response, nil
						}
					} else {
						require.Equal(t, "proxy.finance.qq.com", r.URL.Host)
					}
					response.Body = io.NopCloser(strings.NewReader(body))
					return response, nil
				}), time.Now)
				ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
				defer cancel()
				items, err := p.Search(ctx, "600519")
				want := ErrInstrumentSearchUnavailable
				if failure == "timeout" {
					want = ErrInstrumentSearchTimeout
				}
				require.ErrorIs(t, err, want)
				require.Nil(t, items)
				expectedCalls := 1
				if host == "qt.gtimg.cn" {
					expectedCalls = 2
				}
				require.Equal(t, expectedCalls, calls)
				require.Empty(t, stockQuoteSlots)
			})
		}
	}
}

type searchBrokenReader struct{}

func (searchBrokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type searchWaitingBody struct {
	ctx    context.Context
	active *atomic.Int32
}

func (b searchWaitingBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b searchWaitingBody) Close() error {
	b.active.Add(-1)
	return nil
}

func TestInstrumentSearchSharedConcurrency(t *testing.T) {
	var active, peak atomic.Int32
	transport := stockTransport(func(r *http.Request) (*http.Response, error) {
		n := active.Add(1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: searchWaitingBody{r.Context(), &active}}, nil
	})
	var wg sync.WaitGroup
	for n := range 12 {
		wg.Go(func() {
			p := newTencentQuotes(transport, time.Now)
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			if n%2 == 0 {
				_, err := p.Search(ctx, "600519")
				require.ErrorIs(t, err, ErrInstrumentSearchTimeout)
			} else {
				rows := p.Fetch(ctx, []Instrument{{ID: "i", Market: "SH", Code: "600519", Currency: CNY}})
				require.Equal(t, "quote_timeout", rows["i"].ErrorCode)
			}
		})
	}
	wg.Wait()
	require.Equal(t, int32(4), peak.Load())
	require.Zero(t, active.Load())
	require.Empty(t, stockQuoteSlots)
	p, requests := searchProvider(t, "", "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := p.Search(ctx, "600519")
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, *requests)
	// Waiting for a slot must also honor cancellation without making a request.
	for range cap(stockQuoteSlots) {
		stockQuoteSlots <- struct{}{}
	}
	defer func() {
		for range cap(stockQuoteSlots) {
			<-stockQuoteSlots
		}
	}()
	ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	_, err = p.Search(ctx, "600519")
	require.ErrorIs(t, err, ErrInstrumentSearchTimeout)
	require.Empty(t, *requests)
}
