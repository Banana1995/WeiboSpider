package ledger

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var fxTestNow = time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)

func spotFixture(pair, price, stamp, date string) string {
	fields := make([]string, 22)
	fields[0], fields[1], fields[2], fields[3], fields[5], fields[21] = "310", "\xbb\xe3", pair, price, stamp, date
	return "v_wh" + pair + "=\"" + strings.Join(fields, "~") + "\";"
}

func TestTencentSpotValidation(t *testing.T) {
	good := spotFixture("USDCNY", "6.7108", "20260905025951", "2026-09-04")
	rate, date, stamp, err := parseTencentSpot([]byte(good), "USDCNY", fxTestNow)
	require.NoError(t, err)
	require.Equal(t, "6.71080000", rate.String())
	require.Equal(t, "2026-09-04", date)
	require.Equal(t, "2026-09-05T02:59:51+08:00", stamp)
	for _, bad := range []string{good + good, "", strings.Replace(good, "USDCNY~", "USDHKD~", 1),
		spotFixture("USDCNY", "1", "20260907000000", "2026-09-04"),
		spotFixture("USDCNY", "1", "20260230000000", "2026-02-28"),
		spotFixture("USDCNY", "1", "20260905025951", "2026-09-06"),
		spotFixture("USDCNY", "1", "20260905025951", "2026-02-30")} {
		_, _, _, err := parseTencentSpot([]byte(bad), "USDCNY", fxTestNow)
		require.ErrorIs(t, err, ErrFXUnavailable)
	}
	for _, price := range []string{"0", "-1", "NaN", "Inf", "1e2", "1.000000001", "92233720369"} {
		_, _, _, err := parseTencentSpot([]byte(spotFixture("USDCNY", price, "20260905025951", "2026-09-04")), "USDCNY", fxTestNow)
		require.ErrorIs(t, err, ErrFXUnavailable)
	}
}

func TestTencentHistoricalValidation(t *testing.T) {
	wrap := func(rows string) []byte { return []byte(`{"code":0,"data":{"whUSDCNY":{"day":` + rows + `}}}`) }
	rate, date, err := parseTencentClose(wrap(`[["2026-09-04","9","6.7","9","6","0"],["2026-09-03","8","6.6","9","6","0"]]`), "USDCNY", "2026-08-07", "2026-09-06")
	require.NoError(t, err)
	require.Equal(t, "6.70000000", rate.String())
	require.Equal(t, "2026-09-04", date) // Sunday resolves to Friday's close, not open.
	for _, rows := range []string{`[]`, `[["2026-09-07","1","1","1","1","0"]]`,
		`[["2026-02-30","1","1","1","1","0"]]`, `[["2026-08-01","1","1","1","1","0"]]`,
		`[["2026-09-04","1","0","1","1","0"]]`, `[["2026-09-04","1","-1","1","1","0"]]`,
		`[["2026-09-04","1","NaN","1","1","0"]]`, `[["2026-09-04","1",6.7,"1","1","0"]]`,
		`[["2026-09-04","1","1","1","1","0"],["2026-09-04","1","1","1","1","0"]]`} {
		_, _, err := parseTencentClose(wrap(rows), "USDCNY", "2026-08-07", "2026-09-06")
		require.ErrorIs(t, err, ErrFXUnavailable, rows)
	}
	for _, body := range []string{`{}`, `{"code":1,"data":{}}`, `{"code":0,"code":0,"data":{}}`, string(wrap(`[]`)) + `{}`} {
		_, _, err := parseTencentClose([]byte(body), "USDCNY", "2026-08-07", "2026-09-06")
		require.Error(t, err)
	}
}

func TestReciprocalRate(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"3", "0.33333333"}, {"6", "0.16666667"}, {"512", "0.00195313"}, {"0.00000001", "100000000.00000000"}} {
		rate, err := ParseRate(tc.input)
		require.NoError(t, err)
		got, err := reciprocalRate(rate)
		require.NoError(t, err)
		require.Equal(t, tc.want, got.String())
	}
	for _, input := range []Rate{0, -1, Rate(math.MaxInt64)} {
		_, err := reciprocalRate(input)
		require.ErrorIs(t, err, ErrPrecision)
	}
}

func TestTencentFetchPairsAndModes(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if param := r.URL.Query().Get("param"); param != "" {
			parts := strings.Split(param, ",")
			require.Equal(t, []string{"day", "2026-08-07", "2026-09-06", "40", "qfq"}, parts[1:])
			fmt.Fprintf(w, `{"code":0,"data":{"%s":{"day":[["2026-09-04","9","2","9","1","0"]]}}}`, parts[0])
		} else {
			fmt.Fprint(w, spotFixture(strings.TrimPrefix(r.URL.Query().Get("q"), "wh"), "2", "20260905025951", "2026-09-04"))
		}
	}))
	defer server.Close()
	p := newTencentFX(server.Client().Transport, server.URL, server.URL, func() time.Time { return fxTestNow })
	require.Zero(t, calls.Load())
	for _, mode := range []string{"latest", "historical"} {
		for _, base := range []Currency{USD, HKD, CNY} {
			for _, quote := range []Currency{USD, HKD, CNY} {
				req := FXRequest{Base: base, Quote: quote, Mode: mode}
				if mode == "historical" {
					req.Date = "2026-09-06"
				}
				got, err := p.Fetch(t.Context(), req)
				require.NoError(t, err)
				require.Equal(t, "2026-09-06", got.RequestedDate)
				require.Equal(t, "2026-09-06T00:00:00Z", got.FetchedAt)
				if base == quote {
					require.Equal(t, "identity", got.Source)
					require.Equal(t, "1.00000000", got.Rate.String())
					continue
				}
				pair := string(base) + string(quote)
				inverse := pair != "USDCNY" && pair != "USDHKD" && pair != "HKDCNY"
				kind := "spot"
				if mode == "historical" {
					kind = "close"
					require.Empty(t, got.QuotedAt)
				} else {
					require.NotEmpty(t, got.QuotedAt)
				}
				wantRate, suffix := "2.00000000", ""
				if inverse {
					pair = string(quote) + string(base)
					wantRate, suffix = "0.50000000", "/inverse"
				}
				require.Equal(t, "Tencent/"+kind+"/"+pair+suffix, got.Source)
				require.Equal(t, wantRate, got.Rate.String())
				require.Equal(t, "2026-09-04", got.Date)
			}
		}
	}
	require.EqualValues(t, 12, calls.Load())
}

func TestTencentFailuresAndCancellation(t *testing.T) {
	for _, kind := range []string{"status", "redirect", "oversized", "malformed", "timeout", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch kind {
				case "status":
					http.Error(w, "secret upstream body", 500)
				case "redirect":
					http.Redirect(w, r, "/secret", 302)
				case "oversized":
					fmt.Fprint(w, strings.Repeat("x", (256<<10)+1))
				case "malformed":
					fmt.Fprint(w, "secret malformed body")
				case "timeout", "cancel":
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			p := newTencentFX(server.Client().Transport, server.URL, server.URL, func() time.Time { return fxTestNow })
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			if kind == "cancel" {
				cancel()
			}
			got, err := p.Fetch(ctx, FXRequest{Base: USD, Quote: CNY, Mode: "latest"})
			require.Equal(t, FXQuote{}, got)
			want := ErrFXUnavailable
			if kind == "timeout" {
				want = ErrFXTimeout
			}
			if kind == "cancel" {
				want = context.Canceled
			}
			require.ErrorIs(t, err, want)
			require.NotContains(t, err.Error(), "secret")
			require.LessOrEqual(t, calls.Load(), int32(1))
		})
	}
}

func TestTencentConcurrencyAndInFlightCancellation(t *testing.T) {
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
			fmt.Fprint(w, spotFixture("USDCNY", "2", "20260905025951", "2026-09-04"))
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	p := newTencentFX(server.Client().Transport, server.URL, server.URL, func() time.Time { return fxTestNow })
	req := FXRequest{Base: USD, Quote: CNY, Mode: "latest"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			_, err := p.Fetch(ctx, req)
			require.ErrorIs(t, err, context.Canceled)
		})
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("upstream request did not arrive")
		}
	}
	waitCtx, stop := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer stop()
	_, err := p.Fetch(waitCtx, req)
	require.ErrorIs(t, err, ErrFXTimeout)
	require.Empty(t, entered) // The fifth request timed out in the bounded queue.
	cancel()
	wg.Wait()
	require.Empty(t, p.slots)
}

func TestTencentRejectsInvalidRequestsWithoutNetwork(t *testing.T) {
	p := newTencentFX(nil, "http://invalid.test", "http://invalid.test", func() time.Time { return fxTestNow })
	for _, req := range []FXRequest{
		{Base: "EUR", Quote: CNY, Mode: "latest"},
		{Base: USD, Quote: CNY, Mode: "latest", Date: "2026-09-06"},
		{Base: USD, Quote: CNY, Mode: "historical", Date: "2026-09-07"},
	} {
		_, err := p.Fetch(t.Context(), req)
		require.True(t, err == ErrQuery || err == ErrUnsupportedCurrency)
	}
	// Beijing has reached tomorrow even while the UTC date is still today.
	now := time.Date(2026, 9, 5, 17, 0, 0, 0, time.UTC)
	require.NoError(t, validateFX(FXRequest{Base: USD, Quote: CNY, Mode: "historical", Date: "2026-09-06"}, now))
	_, date, _, err := parseTencentSpot([]byte(spotFixture("USDCNY", "2", "20260801000000", "2026-07-31")), "USDCNY", fxTestNow)
	require.NoError(t, err) // No age cutoff or silent relabeling of old spot data.
	require.Equal(t, "2026-07-31", date)
}

type fxStub func(context.Context, FXRequest) (FXQuote, error)

func (f fxStub) Fetch(ctx context.Context, req FXRequest) (FXQuote, error) { return f(ctx, req) }

func TestFXEndpoint(t *testing.T) {
	var calls int
	h := Handler{Now: func() time.Time { return fxTestNow }, FX: fxStub(func(ctx context.Context, req FXRequest) (FXQuote, error) {
		calls++
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), fxTimeout)
		return FXQuote{Base: req.Base, Quote: req.Quote, Mode: req.Mode, RequestedDate: "2026-09-06", Rate: 671080000, Date: "2026-09-04", Source: "Tencent/spot/USDCNY", FetchedAt: "2026-09-06T00:00:00Z"}, nil
	})}
	mux := http.NewServeMux()
	h.Register(mux)
	for _, tc := range []struct {
		method, query string
		status        int
		code          string
	}{
		{"GET", "base=USD&quote=CNY&mode=latest", 200, ""},
		{"HEAD", "base=USD&quote=CNY&mode=latest", 200, ""},
		{"GET", "base=USD&quote=CNY&mode=historical&date=2026-09-06", 200, ""},
		{"POST", "base=USD&quote=CNY&mode=latest", 405, "method_not_allowed"},
		{"GET", "base=EUR&quote=CNY&mode=latest", 400, "unsupported_currency"},
		{"GET", "base=usd&quote=CNY&mode=latest", 400, "unsupported_currency"},
		{"GET", "base=USD&quote=CNY", 400, "invalid_query"},
		{"GET", "quote=CNY&mode=latest", 400, "invalid_query"},
		{"GET", "base=USD&quote=CNY&mode=other", 400, "invalid_query"},
		{"GET", "base=USD&quote=CNY&mode=latest&date=2026-09-06", 400, "invalid_query"},
		{"GET", "base=USD&quote=CNY&mode=historical", 400, "invalid_query"},
		{"GET", "base=USD&quote=CNY&mode=historical&date=2026-09-07", 400, "invalid_query"},
		{"GET", "base=USD&quote=CNY&mode=historical&date=2026-02-30", 400, "invalid_query"},
		{"GET", "base=USD&quote=CNY&mode=latest&mode=latest", 400, "invalid_query"},
		{"GET", "base=USD&quote=CNY&mode=latest&url=x", 400, "invalid_query"},
		{"GET", "base=USD&quote=CNY&mode=latest&date=", 400, "invalid_query"},
		{"GET", "base=USD&quote=CNY&mode=latest&bad=%zz", 400, "invalid_query"},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, ledgerPrefix+"/fx?"+tc.query, nil))
		require.Equal(t, tc.status, w.Code, tc.query)
		if tc.method == "HEAD" {
			require.Empty(t, w.Body.String())
		} else if tc.code != "" {
			require.Contains(t, w.Body.String(), `"code":"`+tc.code+`"`)
		} else {
			require.JSONEq(t, `{"base":"USD","quote":"CNY","mode":"`+strings.Split(strings.Split(tc.query, "mode=")[1], "&")[0]+`","requested_date":"2026-09-06","rate":"6.71080000","date":"2026-09-04","source":"Tencent/spot/USDCNY","fetched_at":"2026-09-06T00:00:00Z"}`, w.Body.String())
		}
	}
	require.Equal(t, 3, calls)
	for _, cause := range []error{ErrFXUnavailable, ErrFXTimeout, context.DeadlineExceeded, context.Canceled, fmt.Errorf("secret URL https://private")} {
		h.FX = fxStub(func(context.Context, FXRequest) (FXQuote, error) { return FXQuote{}, cause })
		m := http.NewServeMux()
		h.Register(m)
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest("GET", ledgerPrefix+"/fx?base=USD&quote=CNY&mode=latest", nil))
		status := 502
		if cause == ErrFXTimeout || cause == context.DeadlineExceeded {
			status = 504
		}
		require.Equal(t, status, w.Code)
		require.NotContains(t, w.Body.String(), "secret")
	}
	h.FX = nil
	m := http.NewServeMux()
	h.Register(m)
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", ledgerPrefix+"/fx?base=USD&quote=CNY&mode=latest", nil))
	require.Equal(t, 502, w.Code)
}

func TestFXRefetchDoesNotRewriteSnapshots(t *testing.T) {
	var price atomic.Value
	price.Store("6.7")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, spotFixture("USDCNY", price.Load().(string), "20260905025951", "2026-09-04"))
	}))
	defer server.Close()
	p := newTencentFX(server.Client().Transport, server.URL, server.URL, func() time.Time { return fxTestNow })
	db, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	s := NewStore(db, func() time.Time { return fxTestNow })
	require.NoError(t, s.InitializeAccount(t.Context(), "Synthetic CNY", Opening{AccountID: "cny", Currency: CNY, Date: "2026-01-01", Cash: 10000}))
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "usd-stock", Market: "TEST", Code: "TEST", Name: "Synthetic", Currency: USD}))
	quote, err := p.Fetch(t.Context(), FXRequest{Base: USD, Quote: CNY, Mode: "latest"})
	require.NoError(t, err)
	command := Command{Action: CreateOperation, Key: "fx-fixed", Reason: "Synthetic FX purchase", Operation: Operation{
		ID: "purchase", Kind: Buy, AccountID: "cny", InstrumentID: "usd-stock", Date: "2026-09-06", Sequence: 1, Quantity: 1000000, Price: 1000000,
		FX: &FXSnapshot{Rate: quote.Rate, Date: quote.Date, Source: quote.Source, FetchedAt: quote.FetchedAt},
	}}
	first, err := s.Write(t.Context(), command)
	require.NoError(t, err)
	revisions, err := s.Revisions(t.Context(), "purchase")
	require.NoError(t, err)
	price.Store("7.2")
	second, err := p.Fetch(t.Context(), FXRequest{Base: USD, Quote: CNY, Mode: "latest"})
	require.NoError(t, err)
	require.NotEqual(t, first.Operation.FX.Rate, second.Rate)
	stored, err := s.GetOperation(t.Context(), "purchase")
	require.NoError(t, err)
	require.Equal(t, first, stored)
	retry, err := s.Write(t.Context(), command)
	require.NoError(t, err)
	require.Equal(t, first, retry)
	command.Operation.FX = &FXSnapshot{Rate: second.Rate, Date: second.Date, Source: second.Source, FetchedAt: second.FetchedAt}
	_, err = s.Write(t.Context(), command)
	require.ErrorIs(t, err, ErrIdempotency) // A new snapshot cannot reuse the key.
	after, err := s.Revisions(t.Context(), "purchase")
	require.NoError(t, err)
	require.Equal(t, revisions, after)
	stored, err = s.GetOperation(t.Context(), "purchase")
	require.NoError(t, err)
	require.Equal(t, first, stored)
}
