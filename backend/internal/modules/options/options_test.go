package options

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func sampleQuote() Quote {
	return Quote{Contract: Contract{Code: "US.AAPL261228P80000", Symbol: "US.AAPL", Name: "Test Put", Expiry: "2026-12-28", Strike: "80", Multiplier: 100, Type: "PUT", Standard: "STANDARD", Settlement: "PHYSICAL", Currency: "USD"}, Bid: ptr("3.9"), Ask: ptr("4.1"), OI: ptr("1234"), Volume: ptr("20"), IV: ptr("25"), Delta: ptr("-0.3"), Status: "ok"}
}
func sampleInput() Input { return Input{Quantity: 1, Basis: "bid", Mode: "full"} }
func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(t.Context(), t.TempDir(), DefaultLimits())
	require.NoError(t, e)
	s.now = func() time.Time { return time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}
func putSnapshot(t *testing.T, s *Store, id string, quotes ...Quote) Snapshot {
	t.Helper()
	v := Snapshot{ID: id, Symbol: "US.AAPL", Name: "苹果", Source: "test fixture", StartedAt: stamp(s.now()), CapturedAt: stamp(s.now()), ValuationDate: "2026-11-13", Complete: true, TotalContracts: len(quotes), Quotes: quotes, Underlying: ptr("90")}
	require.NoError(t, s.Publish(t.Context(), v))
	return v
}

func TestPublicExamplesAndExactFees(t *testing.T) {
	q, in := sampleQuote(), sampleInput()
	c, _, e := calc(q, "2026-11-13", in, nil)
	require.NoError(t, e)
	require.Equal(t, "390.00", *c.Gross)
	require.Nil(t, c.Fee)
	require.Nil(t, c.Net)
	require.Equal(t, "39.541667", *c.APR)
	in.Mode = "net"
	c, _, e = calc(q, "2026-11-13", in, nil)
	require.NoError(t, e)
	require.Equal(t, "7610.000000", *c.Base)
	require.Equal(t, "41.568112", *c.APR)
	q.Contract.Strike = "195"
	q.Contract.Expiry = "2026-01-16"
	q.Bid = ptr("14.55")
	q.Ask = ptr("14.6")
	in.Mode = "full"
	c, _, e = calc(q, "2025-06-12", in, nil)
	require.NoError(t, e)
	require.Equal(t, 218, c.DTE)
	require.Equal(t, "12.492943", *c.APR)
	in.Mode = "net"
	c, _, e = calc(q, "2025-06-12", in, nil)
	require.NoError(t, e)
	require.Equal(t, "13.500271", *c.APR)
	fees := &FeeProfile{Open: FeeRule{"0.5", "2", "1", "0.1"}}
	q = sampleQuote()
	in = sampleInput()
	in.FeeVersion = 2
	one, _, e := calc(q, "2026-11-13", in, fees)
	require.NoError(t, e)
	require.Equal(t, "3.10", *one.Fee)
	in.Quantity = 2
	two, _, e := calc(q, "2026-11-13", in, fees)
	require.NoError(t, e)
	require.Equal(t, "3.20", *two.Fee)
	require.Equal(t, "776.80", *two.Net)
	in.Quantity = 10
	ten, _, e := calc(q, "2026-11-13", in, fees)
	require.NoError(t, e)
	require.Equal(t, "7.00", *ten.Fee)
}

func TestScenarioIndependentCashFlow(t *testing.T) {
	// Independent integer-cent ledger: premium cash - fees - exercise cash + stock value.
	for _, strike := range []int{50, 80, 195} {
		for _, premium := range []int{100, 390, 1455} {
			for _, qty := range []int{1, 2, 5} {
				for _, terminal := range []int{0, strike - 10, strike, strike + 10} {
					q := sampleQuote()
					q.Contract.Strike = fmt.Sprint(strike)
					q.Bid = ptr(fmt.Sprintf("%d.%02d", premium/100, premium%100))
					q.Ask = q.Bid
					in := sampleInput()
					in.Quantity = qty
					in.FeeOverride = ptr("2.00")
					in.Scenario = &ScenarioInput{Kind: "expiry", Price: fmt.Sprint(terminal), Fee: ptr("1"), Interest: ptr("3")}
					c, _, e := calc(q, "2026-11-13", in, nil)
					require.NoError(t, e)
					cashCents := premium*100*qty - 200 - 100 + 300
					if terminal < strike {
						cashCents -= strike * 100 * 100 * qty
						cashCents += terminal * 100 * 100 * qty
					}
					expected := new(big.Rat).SetFrac64(int64(cashCents), 100).FloatString(2)
					require.Equal(t, expected, *c.ScenarioPnL)
					base := in
					base.Scenario = nil
					plain, _, e := calc(q, "2026-11-13", base, nil)
					require.NoError(t, e)
					require.Equal(t, plain.Income, c.Income)
					require.Equal(t, plain.APR, c.APR)
				}
			}
		}
	}
	q := sampleQuote()
	in := sampleInput()
	in.FeeOverride = ptr("2")
	in.Scenario = &ScenarioInput{Kind: "close", Price: "1.5", Fee: ptr("3"), Interest: ptr("4")}
	c, _, e := calc(q, "2026-11-13", in, nil)
	require.NoError(t, e)
	require.Equal(t, "239.00", *c.ScenarioPnL)
}
func TestTimeMissingQuotesAndInvalidBases(t *testing.T) {
	for _, v := range [][3]string{{"2026-03-07", "2026-03-09", "2"}, {"2026-10-31", "2026-11-02", "2"}, {"2026-11-13", "2026-11-20", "7"}} {
		n, e := Days(v[0], v[1])
		require.NoError(t, e)
		require.Equal(t, v[2], fmt.Sprint(n))
	}
	require.Equal(t, "2026-11-13", dateET(time.Date(2026, 11, 14, 4, 0, 0, 0, time.FixedZone("Beijing", 8*3600))))
	q, in := sampleQuote(), sampleInput()
	q.Bid = ptr("0")
	c, _, e := calc(q, "2026-11-13", in, nil)
	require.NoError(t, e)
	require.Nil(t, c.Income)
	require.Nil(t, c.APR)
	require.Equal(t, "quote_unavailable", c.Status)
	q = sampleQuote()
	q.Ask = ptr("1")
	c, _, e = calc(q, "2026-11-13", in, nil)
	require.NoError(t, e)
	require.Nil(t, c.Income)
	in.Basis = "custom"
	in.Price = ptr("80")
	in.Mode = "net"
	c, _, e = calc(q, "2026-11-13", in, nil)
	require.NoError(t, e)
	require.Equal(t, "invalid_base", c.Status)
	require.Nil(t, c.APR)
	in = sampleInput()
	q = sampleQuote()
	for _, day := range []string{q.Contract.Expiry, "2026-12-29"} {
		c, _, e = calc(q, day, in, nil)
		require.NoError(t, e)
		require.Nil(t, c.APR)
		require.NotNil(t, c.Income)
	}
	q.Status = "metadata_invalid"
	c, _, e = calc(q, "2026-11-13", in, nil)
	require.NoError(t, e)
	require.Equal(t, "unsupported_contract", c.Status)
	for _, bad := range []string{"NaN", "1e3", "-1", "1.0000000001", ""} {
		in.Basis = "custom"
		in.Price = ptr(bad)
		_, _, e = calc(sampleQuote(), "2026-11-13", in, nil)
		require.ErrorIs(t, e, ErrInput)
	}
	in = sampleInput()
	in.Interest = &InterestInput{Principal: "8000", AnnualRate: "3", Days: 45}
	c, _, e = calc(sampleQuote(), "2026-11-13", in, nil)
	require.NoError(t, e)
	require.Equal(t, "29.59", *c.Interest)
	require.Equal(t, "42.541667", *c.CombinedAPR)
	in.Interest.Days = 30
	c, _, e = calc(sampleQuote(), "2026-11-13", in, nil)
	require.NoError(t, e)
	require.Nil(t, c.CombinedAPR)
}

func TestFrozenFavoritesPruneReopenAndReceipts(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(t.Context(), dir, DefaultLimits())
	require.NoError(t, e)
	now := time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	_, e = s.AddWatch(t.Context(), "US.AAPL")
	require.NoError(t, e)
	q := sampleQuote()
	putSnapshot(t, s, "old", q)
	in := sampleInput()
	in.Basis = "custom"
	in.Price = ptr("3.9000")
	f, e := s.SaveFavorite(t.Context(), "request-one", "old", q.Contract.Code, in)
	require.NoError(t, e)
	require.Equal(t, "3.9000", *in.Price, "normalization must not mutate caller")
	in.Price = ptr("3.9")
	duplicate, e := s.SaveFavorite(t.Context(), "request-two", "old", q.Contract.Code, in)
	require.NoError(t, e)
	require.Equal(t, f.ID, duplicate.ID)
	in.Quantity = 2
	_, e = s.SaveFavorite(t.Context(), "request-one", "old", q.Contract.Code, in)
	require.ErrorIs(t, e, ErrReplay)
	in.Quantity = 1
	original := encode(f)
	now = now.AddDate(0, 0, 8)
	q.Bid = ptr("6")
	q.Ask = ptr("7")
	putSnapshot(t, s, "new", q)
	require.NoError(t, s.Prune(t.Context()))
	_, e = s.Snapshot(t.Context(), "old")
	require.ErrorIs(t, e, ErrNotFound)
	require.NoError(t, s.RemoveWatch(t.Context(), "US.AAPL"))
	replayed, e := s.SaveFavorite(t.Context(), "request-one", "old", q.Contract.Code, in)
	require.NoError(t, e)
	require.Equal(t, original, encode(replayed))
	require.NoError(t, s.Close())
	s, e = Open(t.Context(), dir, DefaultLimits())
	require.NoError(t, e)
	defer s.Close()
	restored, e := s.Favorite(t.Context(), f.ID)
	require.NoError(t, e)
	require.Equal(t, original, encode(restored))
	page, e := s.Favorites(t.Context(), "", 1)
	require.NoError(t, e)
	require.Equal(t, 1, page.Total)
	require.NoError(t, s.DeleteFavorite(t.Context(), f.ID))
	_, e = s.SaveFavorite(t.Context(), "request-one", "old", q.Contract.Code, in)
	require.ErrorIs(t, e, ErrNotFound, "retry cannot resurrect a canceled favorite")
}
func TestFavoriteConcurrentDedupeAndAtomicPublication(t *testing.T) {
	s := testStore(t)
	q := sampleQuote()
	putSnapshot(t, s, "one", q)
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f, e := s.SaveFavorite(context.Background(), fmt.Sprintf("req-%d", i), "one", q.Contract.Code, sampleInput())
			ids <- f.ID
			errs <- e
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	require.Len(t, unique, 1)
	_, e := s.db.ExecContext(t.Context(), "CREATE TRIGGER fail_latest BEFORE UPDATE ON latest BEGIN SELECT RAISE(ABORT,'test rollback'); END;")
	require.NoError(t, e)
	v := Snapshot{ID: "failed", Symbol: "US.AAPL", CapturedAt: stamp(s.now()), Quotes: []Quote{q}}
	require.Error(t, s.Publish(t.Context(), v))
	_, e = s.Snapshot(t.Context(), "failed")
	require.ErrorIs(t, e, ErrNotFound)
	latest, e := s.Latest(t.Context())
	require.NoError(t, e)
	require.Equal(t, "one", latest[0].ID)
}
func TestCandidateExactFilteringPaginationAndVersion(t *testing.T) {
	s := testStore(t)
	_, e := s.AddWatch(t.Context(), "US.AAPL")
	require.NoError(t, e)
	a, b, missing := sampleQuote(), sampleQuote(), sampleQuote()
	b.Contract.Code = "US.AAPL261228P80001"
	b.Bid = ptr("3.900000001")
	missing.Contract.Code = "US.AAPL261228P50000"
	missing.Bid = nil
	putSnapshot(t, s, "one", a, b, missing)
	q := CandidateQuery{Mode: "full", Sort: "yield", Descending: true, Limit: 1}
	first, e := s.Candidates(t.Context(), q)
	require.NoError(t, e)
	require.Equal(t, b.Contract.Code, first.Items[0].Quote.Contract.Code, "sort uses unrounded APR")
	require.Equal(t, 2, first.Total)
	require.Equal(t, 2, first.Available)
	require.Equal(t, 1, first.Excluded["Bid 不可用"], "a missing bid never becomes a zero-premium row")
	q.Offset = 1
	q.View = first.View
	second, e := s.Candidates(t.Context(), q)
	require.NoError(t, e)
	require.Equal(t, a.Contract.Code, second.Items[0].Quote.Contract.Code)
	putSnapshot(t, s, "two", a)
	_, e = s.Candidates(t.Context(), q)
	require.ErrorIs(t, e, ErrVersion)
	putSnapshot(t, s, "three", a, missing)
	q.Offset = 0
	q.Filter.MinAPR = "1"
	page, e := s.Candidates(t.Context(), q)
	require.NoError(t, e)
	require.Equal(t, 1, page.Total)
	require.Equal(t, 1, page.Excluded["Bid 不可用"])
	q.Filter = Filter{Budget: "16000"}
	page, e = s.Candidates(t.Context(), q)
	require.NoError(t, e)
	require.Equal(t, "2", *page.Items[0].BudgetQuantity)
}

func TestExpiredContractsAreHiddenButCounted(t *testing.T) {
	s := testStore(t)
	_, e := s.AddWatch(t.Context(), "US.AAPL")
	require.NoError(t, e)
	live, expired := sampleQuote(), sampleQuote()
	expired.Contract.Code = "US.AAPL261113P80000"
	expired.Contract.Expiry = "2026-11-13"
	putSnapshot(t, s, "mixed", live, expired)
	page, e := s.Candidates(t.Context(), CandidateQuery{Mode: "full", Sort: "date", Limit: 50})
	require.NoError(t, e)
	require.Equal(t, 1, page.Total)
	require.Equal(t, 1, page.Available)
	require.Equal(t, 1, page.Excluded["已到期"])
	require.Equal(t, live.Contract.Code, page.Items[0].Quote.Contract.Code)
	// The frozen snapshot still records the expired contract for history.
	snap, e := s.Snapshot(t.Context(), "mixed")
	require.NoError(t, e)
	require.Len(t, snap.Quotes, 2)
}

type fakeProvider struct {
	calls      atomic.Int64
	quoteCalls atomic.Int64
	failSymbol string
	gate       chan struct{}
	at         time.Time
}

func (p *fakeProvider) State(ctx context.Context) (ProviderState, error) {
	p.calls.Add(1)
	if p.gate != nil {
		select {
		case <-ctx.Done():
			return ProviderState{}, ctx.Err()
		case <-p.gate:
		}
	}
	return ProviderState{Connected: true, USOptions: "LV1"}, nil
}
func (p *fakeProvider) Instruments(context.Context, string) ([]Instrument, error) {
	p.calls.Add(1)
	return nil, nil
}
func (p *fakeProvider) Expirations(context.Context, string) ([]string, error) {
	p.calls.Add(1)
	return []string{"2026-12-28"}, nil
}
func (p *fakeProvider) Chain(_ context.Context, symbol, _, _, side string) ([]Contract, error) {
	p.calls.Add(1)
	c := sampleQuote().Contract
	c.Symbol = symbol
	c.Code = strings.Replace(c.Code, "AAPL", strings.TrimPrefix(symbol, "US."), 1)
	c.Type = side
	if side == "CALL" {
		c.Code = strings.Replace(c.Code, "P80000", "C80000", 1)
	}
	return []Contract{c}, nil
}
func (p *fakeProvider) Quotes(_ context.Context, cs []Contract, symbol string) (QuoteBatch, error) {
	p.calls.Add(1)
	p.quoteCalls.Add(1)
	if symbol == p.failSymbol {
		return QuoteBatch{}, &ProviderError{"permission", "test permission denied", false}
	}
	q := sampleQuote()
	q.Contract = cs[0]
	return QuoteBatch{Quotes: []Quote{q}, Underlying: ptr("90"), MarketState: "REST"}, nil
}
func waitScanner(t *testing.T, w *Scanner) {
	t.Helper()
	require.Eventually(t, func() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.active == nil }, 5*time.Second, time.Millisecond)
}
func TestScannerManualOnlyDedupePartialFailureAndRecovery(t *testing.T) {
	s := testStore(t)
	p := &fakeProvider{gate: make(chan struct{})}
	w := NewScanner(t.Context(), s, p)
	defer w.Close()
	for _, code := range []string{"US.AAPL", "US.MSFT"} {
		_, e := s.AddWatch(t.Context(), code)
		require.NoError(t, e)
	}
	_, _, e := w.Status(t.Context())
	require.NoError(t, e)
	_, e = s.Latest(t.Context())
	require.NoError(t, e)
	require.Zero(t, p.calls.Load())
	r, e := w.Submit(t.Context(), "manual-one", nil)
	require.NoError(t, e)
	same, e := w.Submit(t.Context(), "double-click", []string{"US.AAPL"})
	require.NoError(t, e)
	require.Equal(t, r.ID, same.ID)
	close(p.gate)
	waitScanner(t, w)
	_, run, e := w.Status(t.Context())
	require.NoError(t, e)
	require.Equal(t, "succeeded", run.State)
	before, e := s.Latest(t.Context())
	require.NoError(t, e)
	require.Len(t, before, 2)
	count := p.calls.Load()
	replayed, e := w.Submit(t.Context(), "double-click", []string{"US.AAPL"})
	require.NoError(t, e)
	require.Equal(t, r.ID, replayed.ID)
	require.Equal(t, count, p.calls.Load(), "merged request retries must not collect again after completion")
	s.now = func() time.Time { return time.Date(2026, 11, 13, 20, 10, 0, 0, time.UTC) }
	for i := 0; i < 5; i++ {
		_, _, e = w.Status(t.Context())
		require.NoError(t, e)
	}
	require.Equal(t, count, p.calls.Load(), "advancing beyond five minutes cannot trigger quotes")
	p.failSymbol = "US.MSFT"
	_, e = w.Submit(t.Context(), "manual-two", nil)
	require.NoError(t, e)
	waitScanner(t, w)
	_, run, e = w.Status(t.Context())
	require.NoError(t, e)
	require.Equal(t, "partial", run.State)
	after, e := s.Latest(t.Context())
	require.NoError(t, e)
	require.NotEqual(t, before[0].ID, after[0].ID)
	require.Equal(t, before[1].ID, after[1].ID)
	require.NoError(t, s.saveRun(t.Context(), Run{ID: "crashed", State: "running", Targets: []Target{{Symbol: "US.AAPL", State: "running"}}}))
	count = p.calls.Load()
	require.NoError(t, s.recover(t.Context()))
	saved, e := readJSON[Run](t.Context(), s.db, "SELECT data FROM scan_runs WHERE id='crashed'")
	require.NoError(t, e)
	require.Equal(t, "interrupted", saved.State)
	require.Equal(t, count, p.calls.Load())
}
func TestHTTPReadsNeverCollectAndStrictMoney(t *testing.T) {
	s := testStore(t)
	p := &fakeProvider{}
	w := NewScanner(t.Context(), s, p)
	defer w.Close()
	mux := http.NewServeMux()
	Handler{s, w, p}.Register(mux)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, prefix+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		return out
	}
	for _, path := range []string{"/status", "/watchlist", "/settings", "/candidates", "/favorites", "/instruments/search?q=英伟达", "/instruments/search?q=Microsoft"} {
		out := request("GET", path, "")
		require.Equal(t, 200, out.Code, out.Body.String())
	}
	require.Zero(t, p.calls.Load())
	require.Equal(t, 200, request("POST", "/watchlist", `{"code":"US.AAPL"}`).Code)
	require.Zero(t, p.calls.Load())
	putSnapshot(t, s, "one", sampleQuote())
	payload := map[string]any{"snapshot_id": "one", "contract_code": sampleQuote().Contract.Code, "input": sampleInput()}
	out := request("POST", "/calculations", encode(payload))
	require.Equal(t, 200, out.Code, out.Body.String())
	var c Candidate
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &c))
	require.Equal(t, "390.00", *c.Result.Income)
	for _, body := range []string{`{"snapshot_id":"one","contract_code":"x","input":{"quantity":1,"basis":"custom","price":2,"mode":"full"}}`, `{"snapshot_id":"one","extra":true}`, `{} {}`} {
		require.Equal(t, 400, request("POST", "/calculations", body).Code)
	}
	require.Equal(t, 405, request("DELETE", "/settings", "").Code)
	require.Zero(t, p.calls.Load())
	bad := sampleInput()
	bad.FeeVersion = 999
	_, _, e := s.Calculate(t.Context(), "one", sampleQuote().Contract.Code, bad)
	require.True(t, errors.Is(e, ErrNotFound))
}

func TestHistoryQuotaKeepsLatestAndFrozenFavorites(t *testing.T) {
	s := testStore(t)
	s.limits.HistoryBytes = 1024
	q := sampleQuote()
	putSnapshot(t, s, "first", q)
	f, e := s.SaveFavorite(t.Context(), "keep-forever", "first", q.Contract.Code, sampleInput())
	require.NoError(t, e)
	for _, id := range []string{"second", "third", "fourth"} {
		putSnapshot(t, s, id, q)
	}
	require.NoError(t, s.Prune(t.Context()))
	require.True(t, s.HistoryLimited(t.Context()))
	_, e = s.Snapshot(t.Context(), "fourth")
	require.NoError(t, e)
	copy, e := s.Favorite(t.Context(), f.ID)
	require.NoError(t, e)
	require.Equal(t, encode(f), encode(copy))
	var bytes int64
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT
		(SELECT COALESCE(SUM(length(data)),0) FROM snapshots WHERE id NOT IN (SELECT snapshot_id FROM latest))+
		(SELECT COALESCE(SUM(length(data)),0) FROM daily_history)`).Scan(&bytes))
	require.LessOrEqual(t, bytes, s.limits.HistoryBytes)
}

func TestManualRunContractBudgetIsSharedAcrossStocks(t *testing.T) {
	s := testStore(t)
	s.limits.MaxContracts = 1
	for _, code := range []string{"US.AAPL", "US.MSFT"} {
		_, e := s.AddWatch(t.Context(), code)
		require.NoError(t, e)
	}
	p := &fakeProvider{}
	w := NewScanner(t.Context(), s, p)
	defer w.Close()
	_, e := w.Submit(t.Context(), "limited", nil)
	require.NoError(t, e)
	waitScanner(t, w)
	_, run, e := w.Status(t.Context())
	require.NoError(t, e)
	require.Equal(t, "partial", run.State)
	require.Equal(t, int64(1), p.quoteCalls.Load())
	require.Contains(t, run.Targets[1].Error, "上限")
}

func calc(q Quote, day string, in Input, fees *FeeProfile) (Calculation, map[string]*big.Rat, error) {
	return calculate(q, day, in, fees, nil)
}

func TestPremiumSplitsIntrinsicAndTimeValue(t *testing.T) {
	q, in := sampleQuote(), sampleInput()
	c, _, e := calculate(q, "2026-11-13", in, nil, ptr("90"))
	require.NoError(t, e)
	require.Equal(t, "0.00", *c.Intrinsic)
	require.Equal(t, "390.00", *c.TimeValue)
	c, _, e = calculate(q, "2026-11-13", in, nil, ptr("70"))
	require.NoError(t, e)
	require.Equal(t, "1000.00", *c.Intrinsic)
	require.Equal(t, "-610.00", *c.TimeValue)
	in.Quantity = 2
	c, _, e = calculate(q, "2026-11-13", in, nil, ptr("75"))
	require.NoError(t, e)
	require.Equal(t, "1000.00", *c.Intrinsic)
	require.Equal(t, "-220.00", *c.TimeValue)
	// Trading fees change net income, never the option's value split.
	in.FeeOverride = ptr("5")
	c, _, e = calculate(q, "2026-11-13", in, nil, ptr("75"))
	require.NoError(t, e)
	require.Equal(t, "-220.00", *c.TimeValue)
	require.Equal(t, "775.00", *c.Income)
	c, _, e = calculate(q, "2026-11-13", sampleInput(), nil, nil)
	require.NoError(t, e)
	require.Nil(t, c.Intrinsic)
	require.Nil(t, c.TimeValue)
}
