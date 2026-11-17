package options

import (
	"context"
	"fmt"
	"io/fs"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/stretchr/testify/require"
)

func sampleCall() Quote {
	q := sampleQuote()
	q.Contract.Type = "CALL"
	q.Contract.Code = "US.AAPL261213C110000"
	q.Contract.Expiry = "2026-12-13"
	q.Contract.Strike = "110"
	q.Contract.Name = "Test Call"
	q.Bid = ptr("3")
	q.Ask = ptr("3.2")
	q.Delta = ptr("0.3")
	return q
}
func callSnapshot(t *testing.T, s *Store, id string, quotes ...Quote) Snapshot {
	t.Helper()
	v := Snapshot{ID: id, Symbol: "US.AAPL", OptionType: "CALL", Name: "苹果", Source: "fixture", CapturedAt: stamp(s.now()), ValuationDate: "2026-11-13", Complete: true, Underlying: ptr("100"), Quotes: quotes}
	require.NoError(t, s.Publish(t.Context(), v))
	return v
}

func TestCoveredCallCapitalAndOptionIncome(t *testing.T) {
	q, in := sampleCall(), sampleInput()
	c, _, err := calculate(q, "2026-11-13", in, nil, ptr("100"))
	require.NoError(t, err)
	require.Equal(t, "stock_value", c.CapitalBasis)
	require.Equal(t, "100", *c.CoveredShares)
	require.Equal(t, "10000.00", *c.Capital, "stock market value, not strike times shares")
	require.Equal(t, "11000.00", *c.StrikeProceeds)
	require.Equal(t, "300.00", *c.Income)
	require.Equal(t, "0.00", *c.Intrinsic)
	require.Equal(t, "300.00", *c.TimeValue)
	require.Equal(t, "36.500000", *c.APR)
	require.Equal(t, "97.000000", *c.Cost)
	require.Equal(t, "113.000000", *c.NetSalePrice)
	require.Nil(t, c.Fee)
	require.Nil(t, c.Net)
	in.Mode = "net"
	c, _, err = calculate(q, "2026-11-13", in, nil, ptr("100"))
	require.NoError(t, err)
	require.Equal(t, "9700.000000", *c.Base)
	require.Equal(t, "37.628866", *c.APR)
	in.Quantity = 2
	in.FeeOverride = ptr("5")
	c, _, err = calculate(q, "2026-11-13", in, nil, ptr("100"))
	require.NoError(t, err)
	require.Equal(t, "595.00", *c.Income)
	require.Equal(t, "20000.00", *c.Capital)
	require.Equal(t, "200", *c.CoveredShares)
	require.Equal(t, "112.975000", *c.NetSalePrice)
	require.Equal(t, "600.00", *c.TimeValue)
	q.Contract.Strike = "90"
	q.Bid = ptr("12")
	q.Ask = ptr("12.5")
	c, _, err = calculate(q, "2026-11-13", sampleInput(), nil, ptr("100"))
	require.NoError(t, err)
	require.Equal(t, "1000.00", *c.Intrinsic)
	require.Equal(t, "200.00", *c.TimeValue)
	q = sampleCall()
	c, _, err = calculate(q, "2026-11-13", sampleInput(), nil, nil)
	require.NoError(t, err)
	require.Equal(t, "300.00", *c.Income)
	require.Nil(t, c.Capital)
	require.Nil(t, c.APR)
	require.Equal(t, "underlying_unavailable", c.Status)
}

func TestCoveredCallExpiryIndependentCashAndStockModel(t *testing.T) {
	for _, strike := range []int{90, 110, 150} {
		for _, cost := range []int{80, 100, 125} {
			for _, terminal := range []int{0, 100, 200} {
				for _, n := range []int{1, 3} {
					q := sampleCall()
					q.Contract.Strike = fmt.Sprint(strike)
					in := sampleInput()
					in.Quantity = n
					in.FeeOverride = ptr("2")
					in.Scenario = &ScenarioInput{Kind: "expiry", Price: fmt.Sprint(terminal), StockCost: ptr(fmt.Sprint(cost)), Fee: ptr("1"), Interest: ptr("3")}
					result, _, err := calculate(q, "2026-11-13", in, nil, ptr("100"))
					require.NoError(t, err)
					// Independent cash ledger: buy stock, collect premium, fees/interest,
					// then either sell at the strike or mark the retained stock at ST.
					shares := int64(100 * n)
					cash := -int64(cost)*shares*100 + int64(300*n)*100 - 200 - 100 + 300
					if terminal > strike {
						cash += int64(strike) * shares * 100
					} else {
						cash += int64(terminal) * shares * 100
					}
					expected := new(big.Rat).SetFrac64(cash, 100).FloatString(2)
					require.Equal(t, expected, *result.CoveredPnL)
					base := in
					base.Scenario = nil
					plain, _, err := calculate(q, "2026-11-13", base, nil, ptr("100"))
					require.NoError(t, err)
					require.Equal(t, plain.APR, result.APR)
					require.Equal(t, plain.Income, result.Income)
				}
			}
		}
	}
	in := sampleInput()
	in.Scenario = &ScenarioInput{Kind: "expiry", Price: "150"}
	c, _, err := calculate(sampleCall(), "2026-11-13", in, nil, ptr("100"))
	require.NoError(t, err)
	require.Nil(t, c.CoveredPnL)
	in.Scenario.StockCost = ptr("100")
	_, _, err = calculate(sampleQuote(), "2026-11-13", in, nil, ptr("100"))
	require.ErrorIs(t, err, ErrInput)
	in.Scenario = &ScenarioInput{Kind: "close", Price: "1.5", Fee: ptr("3"), Interest: ptr("4")}
	in.FeeOverride = ptr("2")
	c, _, err = calculate(sampleCall(), "2026-11-13", in, nil, ptr("100"))
	require.NoError(t, err)
	require.Equal(t, "149.00", *c.ScenarioPnL)
	require.Nil(t, c.CoveredPnL)
}

func TestCallFiltersAndStoredConditionsAreIndependent(t *testing.T) {
	s := testStore(t)
	w, err := s.AddWatch(t.Context(), "US.AAPL")
	require.NoError(t, err)
	w.Filter.MaxStrike = "80"
	w.CallFilter.MinStrike = "100"
	_, err = s.SaveWatch(t.Context(), w.Code, w)
	require.NoError(t, err)
	put := sampleQuote()
	putSnapshot(t, s, "put", put)
	otm, itm := sampleCall(), sampleCall()
	itm.Contract.Strike = "90"
	itm.Contract.Code = "US.AAPL261213C90000"
	itm.Bid = ptr("12")
	itm.Ask = ptr("12.5")
	adjusted := sampleCall()
	adjusted.Contract.Code = "US.AAPL1261213C110000"
	adjusted.Contract.Standard = "NON_STANDARD"
	adjusted.Contract.Multiplier = 50
	callSnapshot(t, s, "call", otm, itm, adjusted)
	query := CandidateQuery{OptionType: "CALL", Mode: "full", Sort: "date", Limit: 10}
	page, err := s.Candidates(t.Context(), query)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, otm.Contract.Code, page.Items[0].Quote.Contract.Code)
	require.Equal(t, 1, page.Excluded["合约属性未确认"])
	query.Filter = Filter{Moneyness: "out", MinSalePrice: "113"}
	page, err = s.Candidates(t.Context(), query)
	require.NoError(t, err)
	require.Equal(t, 1, page.Total)
	query.Filter.MinSalePrice = "113.01"
	page, err = s.Candidates(t.Context(), query)
	require.NoError(t, err)
	require.Zero(t, page.Total)
	watches, err := s.Watchlist(t.Context())
	require.NoError(t, err)
	w = watches[0]
	w.CallFilter = Filter{}
	_, err = s.SaveWatch(t.Context(), w.Code, w)
	require.NoError(t, err)
	query.Filter = Filter{Moneyness: "in"}
	page, err = s.Candidates(t.Context(), query)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, itm.Contract.Code, page.Items[0].Quote.Contract.Code)
	query.OptionType = "PUT"
	query.Filter = Filter{}
	page, err = s.Candidates(t.Context(), query)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "put", page.Items[0].Snapshot.ID)
	query.OptionType = "ALL"
	_, err = s.Candidates(t.Context(), query)
	require.ErrorIs(t, err, ErrInput)
}

func TestCallManualTasksDirectoryCacheAndReceipts(t *testing.T) {
	s := testStore(t)
	_, err := s.AddWatch(t.Context(), "US.AAPL")
	require.NoError(t, err)
	p := &fakeProvider{gate: make(chan struct{})}
	w := NewScanner(t.Context(), s, p)
	defer w.Close()
	first, err := w.SubmitType(t.Context(), "put-request", []string{"US.AAPL"}, "PUT")
	require.NoError(t, err)
	_, err = w.SubmitType(t.Context(), "put-request", []string{"US.AAPL"}, "CALL")
	require.ErrorIs(t, err, ErrReplay)
	_, err = w.SubmitType(t.Context(), "call-while-put-runs", []string{"US.AAPL"}, "CALL")
	require.ErrorIs(t, err, ErrBusy)
	close(p.gate)
	waitScanner(t, w)
	putLatest, err := s.Latest(t.Context())
	require.NoError(t, err)
	second, err := w.SubmitType(t.Context(), "call-request", []string{"US.AAPL"}, "CALL")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	waitScanner(t, w)
	latest, err := s.Latest(t.Context())
	require.NoError(t, err)
	require.Len(t, latest, 2)
	for _, snap := range latest {
		if optionSide(snap.OptionType) == "PUT" {
			require.Equal(t, putLatest[0].ID, snap.ID)
		} else {
			require.Equal(t, "CALL", snap.Quotes[0].Contract.Type)
		}
	}
	var directories, histories int
	require.NoError(t, s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM directories").Scan(&directories))
	require.Equal(t, 2, directories)
	require.NoError(t, s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM daily_history").Scan(&histories))
	require.Equal(t, 2, histories)
	count := p.calls.Load()
	replay, err := w.SubmitType(t.Context(), "call-request", []string{"US.AAPL"}, "CALL")
	require.NoError(t, err)
	require.Equal(t, second.ID, replay.ID)
	require.Equal(t, count, p.calls.Load())
	_, err = w.SubmitType(t.Context(), "call-again", []string{"US.AAPL"}, "CALL")
	require.NoError(t, err)
	waitScanner(t, w)
	require.Equal(t, count+2, p.calls.Load(), "cached CALL directory skips expiration and chain requests")
	_, err = w.SubmitType(t.Context(), "bad", []string{"US.AAPL"}, "ALL")
	require.ErrorIs(t, err, ErrInput)
}

func TestMigrationPreservesPutSnapshotsFavoritesAndReceipts(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(t.Context(), dir, "options")
	require.NoError(t, err)
	legacy := fstest.MapFS{}
	for _, name := range []string{"001_workspace.sql", "002_retention_state.sql", "003_scan_receipts.sql"} {
		raw, err := fs.ReadFile(migrationFiles, "migrations/"+name)
		require.NoError(t, err)
		legacy[name] = &fstest.MapFile{Data: raw}
	}
	require.NoError(t, db.Migrate(t.Context(), legacy))
	q := sampleQuote()
	snap := Snapshot{ID: "legacy", Symbol: "US.AAPL", ValuationDate: "2026-11-13", CapturedAt: "2026-11-13T20:00:00.000000000Z", Quotes: []Quote{q}, Underlying: ptr("90")}
	fav := Favorite{ID: "legacy-favorite", FormatVersion: 1, CreatedAt: snap.CapturedAt, Candidate: Candidate{Snapshot: snap, Quote: q, Input: sampleInput(), Result: Calculation{Version: "put-premium-act365-v1", Income: ptr("390.00"), APR: ptr("39.541667")}}}
	rawSnap, rawFavorite := encode(snap), encode(fav)
	_, err = db.ExecContext(t.Context(), "INSERT INTO snapshots(id,symbol,captured_at,data) VALUES(?,?,?,?)", snap.ID, snap.Symbol, snap.CapturedAt, rawSnap)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "INSERT INTO latest(symbol,snapshot_id) VALUES(?,?)", snap.Symbol, snap.ID)
	require.NoError(t, err)
	hash := fingerprint([]any{snap.ID, q.Contract.Code, sampleInput()})
	_, err = db.ExecContext(t.Context(), "INSERT INTO favorites(id,fingerprint,snapshot_id,contract_code,created_at,data) VALUES(?,?,?,?,?,?)", fav.ID, hash, snap.ID, q.Contract.Code, fav.CreatedAt, rawFavorite)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "INSERT INTO receipts VALUES(?,?,?)", "old-save", hash, fav.ID)
	require.NoError(t, err)
	run := Run{ID: "legacy-run", State: "succeeded", CreatedAt: snap.CapturedAt, Targets: []Target{{Symbol: "US.AAPL", State: "succeeded"}}}
	_, err = db.ExecContext(t.Context(), "INSERT INTO scan_runs VALUES(?,?,?,?)", run.ID, run.CreatedAt, run.State, encode(run))
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "INSERT INTO scan_receipts VALUES(?,?,?)", "old-scan", fingerprint([]string{"US.AAPL"}), run.ID)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	s, err := Open(t.Context(), dir, DefaultLimits())
	require.NoError(t, err)
	defer s.Close()
	var after string
	require.NoError(t, s.db.QueryRowContext(t.Context(), "SELECT data FROM favorites WHERE id=?", fav.ID).Scan(&after))
	require.Equal(t, rawFavorite, after)
	require.NoError(t, s.db.QueryRowContext(t.Context(), "SELECT data FROM snapshots WHERE id=?", snap.ID).Scan(&after))
	require.Equal(t, rawSnap, after)
	old, err := s.SaveFavorite(t.Context(), "old-save", snap.ID, q.Contract.Code, sampleInput())
	require.NoError(t, err)
	require.Equal(t, "put-premium-act365-v1", old.Candidate.Result.Version)
	p := &fakeProvider{}
	scanner := NewScanner(t.Context(), s, p)
	defer scanner.Close()
	r, err := scanner.SubmitType(t.Context(), "old-scan", []string{"US.AAPL"}, "PUT")
	require.NoError(t, err)
	require.Equal(t, run.ID, r.ID)
	require.Zero(t, p.calls.Load())
	callSnapshot(t, s, "call-after-migration", sampleCall())
	latest, err := s.Latest(t.Context())
	require.NoError(t, err)
	require.Len(t, latest, 2)
	require.NoError(t, s.db.QueryRowContext(t.Context(), "SELECT snapshot_id FROM latest WHERE symbol='US.AAPL' AND option_type='PUT'").Scan(&after))
	require.Equal(t, snap.ID, after)
	require.NoError(t, s.Close())
	s2, err := Open(t.Context(), dir, DefaultLimits())
	require.NoError(t, err)
	defer s2.Close()
	latest, err = s2.Latest(t.Context())
	require.NoError(t, err)
	require.Len(t, latest, 2)
}

func TestCallHTTPReadsAreManualAndTypeChecked(t *testing.T) {
	s := testStore(t)
	_, err := s.AddWatch(t.Context(), "US.AAPL")
	require.NoError(t, err)
	p := &fakeProvider{}
	scanner := NewScanner(t.Context(), s, p)
	defer scanner.Close()
	mux := http.NewServeMux()
	Handler{s, scanner, p}.Register(mux)
	for _, path := range []string{"/candidates?option_type=CALL", "/history?symbol=US.AAPL&option_type=CALL", "/favorites"} {
		r := httptest.NewRequest("GET", prefix+path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	require.Zero(t, p.calls.Load())
	for _, path := range []string{"/candidates?option_type=ALL", "/candidates?option_type=CALL&option_type=PUT", "/history?option_type=BUY"} {
		r := httptest.NewRequest("GET", prefix+path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		require.Equal(t, 400, w.Code)
	}
	r := httptest.NewRequest("POST", prefix+"/scans", strings.NewReader(`{"request_id":"call-http","symbols":["US.AAPL"],"option_type":"CALL"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code)
	waitScanner(t, scanner)
	page, err := s.Candidates(t.Context(), CandidateQuery{OptionType: "CALL", Mode: "full", Sort: "date", Limit: 50})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "CALL", page.Items[0].Quote.Contract.Type)
}

func TestOldQuoteExpiresWithoutChangingFrozenValuation(t *testing.T) {
	s := testStore(t)
	_, err := s.AddWatch(t.Context(), "US.AAPL")
	require.NoError(t, err)
	q := sampleQuote()
	q.Contract.Expiry = "2026-11-16"
	putSnapshot(t, s, "old-date", q)
	favorite, err := s.SaveFavorite(t.Context(), "save-old-date", "old-date", q.Contract.Code, sampleInput())
	require.NoError(t, err)
	s.now = func() time.Time { return time.Date(2026, 11, 16, 15, 0, 0, 0, time.UTC) }
	page, err := s.Candidates(context.Background(), CandidateQuery{Mode: "full", Sort: "date", Limit: 50})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.Equal(t, 1, page.Excluded["已到期"])
	frozen, err := s.Favorite(t.Context(), favorite.ID)
	require.NoError(t, err)
	require.Equal(t, encode(favorite), encode(frozen))
	require.Equal(t, 3, frozen.Candidate.Result.DTE)
}
