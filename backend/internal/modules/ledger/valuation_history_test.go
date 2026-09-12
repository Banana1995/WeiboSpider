package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

var historyKeys atomic.Int64

// Exercise the fixed-record writer directly. The production HTTP valuation
// resource remains read-only; weekly tests separately exercise job idempotency.
func sampleValuation(t *testing.T, s *Store, id string, h Handler) Valuation {
	t.Helper()
	v, instruments, err := s.valuationInputs(t.Context(), id)
	require.NoError(t, err)
	if h.Now == nil {
		h.Now = s.now
	}
	require.NoError(t, h.valuePositions(t.Context(), &v, instruments))
	historyID, err := s.RecordValuation(t.Context(), v, instruments)
	require.NoError(t, err)
	v.HistoryID = historyID
	var wire Valuation
	require.NoError(t, json.Unmarshal([]byte(httpPayload(t, v)), &wire))
	return wire
}

func historyHTTPRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, ledgerPrefix+"/accounts/"+path, nil)
	if method == "POST" {
		r = httptest.NewRequest(method, ledgerPrefix+"/accounts/"+path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "history-save-"+strconv.FormatInt(historyKeys.Add(1), 10))
	}
	return r
}
func historyRequest(t *testing.T, mux http.Handler, method, path string, status int) []byte {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, historyHTTPRequest(method, path))
	require.Equal(t, status, w.Code, w.Body.String())
	if method == "HEAD" {
		require.Empty(t, w.Body.String())
	}
	return w.Body.Bytes()
}

func historyMux(s *Store) *http.ServeMux {
	mux := http.NewServeMux()
	Handler{Store: s, Now: func() time.Time { return s.now() }}.Register(mux)
	return mux
}

func historyCount(t *testing.T, s *Store, want int) {
	t.Helper()
	var count int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records WHERE origin IN ('currentrefresh','weekly')`).Scan(&count))
	require.Equal(t, want, count)
}

func TestHistoryAppendPagesAndReadOnly(t *testing.T) {
	s := valuationFixture(t, nil, nil)
	manualSourceAccount(t, s, "b")
	putSource(t, s, "b", "input-b", "0", 0)
	mux := historyMux(s)
	require.JSONEq(t, `{"items":[]}`, string(historyRequest(t, mux, "GET", "a/valuations", 200)))
	for _, path := range []string{"a/valuation", "a/valuations", "missing/valuations"} {
		status := 200
		if strings.HasPrefix(path, "missing") {
			status = 404
		}
		historyRequest(t, mux, "HEAD", path, status)
	}
	historyCount(t, s, 0)
	var first Valuation
	for n := 1; n <= 3; n++ {
		v := sampleValuation(t, s, "a", Handler{})
		require.Equal(t, strconv.Itoa(n), v.HistoryID)
		require.Len(t, v.LedgerRevision, 64)
		if n == 1 {
			first = v
		} else {
			v.HistoryID = first.HistoryID
			require.Equal(t, first, v)
		}
	}
	sampleValuation(t, s, "b", Handler{})
	s.now = func() time.Time { return stockNow.AddDate(0, 0, 1) }
	sampleValuation(t, s, "a", Handler{})
	var page listJSON[ValuationSummary]
	require.NoError(t, json.Unmarshal(historyRequest(t, mux, "GET", "a/valuations?limit=2", 200), &page))
	require.Equal(t, []string{"5", "3"}, []string{page.Items[0].ID, page.Items[1].ID})
	require.Equal(t, "3", page.NextCursor)
	require.NoError(t, json.Unmarshal(historyRequest(t, mux, "GET", "a/valuations?limit=2&cursor=3", 200), &page))
	require.Equal(t, []string{"2", "1"}, []string{page.Items[0].ID, page.Items[1].ID})
	// Unmarshal into a fresh page because absent optional fields do not clear Go fields.
	page = listJSON[ValuationSummary]{}
	require.NoError(t, json.Unmarshal(historyRequest(t, mux, "GET", "a/valuations?from=2026-09-06&to=2026-09-06", 200), &page))
	require.Len(t, page.Items, 3)
	require.Empty(t, page.NextCursor)
	require.Equal(t, first.Cash, page.Items[0].Cash)
	require.Equal(t, *first.TotalAssets, page.Items[0].TotalAssets)
	require.JSONEq(t, `{"items":[]}`, string(historyRequest(t, mux, "GET", "a/valuations?from=2026-09-08", 200)))
	var detail ValuationHistory
	require.NoError(t, json.Unmarshal(historyRequest(t, mux, "GET", "a/valuations/1", 200), &detail))
	require.Equal(t, "Synthetic", detail.AccountName)
	require.Equal(t, 1, detail.SchemaVersion)
	require.Empty(t, detail.Valuation.HistoryID)
	first.HistoryID = ""
	require.Equal(t, first, detail.Valuation)
	require.NotNil(t, detail.Instruments)
	historyRequest(t, mux, "HEAD", "a/valuations/1", 200)
	historyRequest(t, mux, "HEAD", "a/valuations?limit=1", 200)
	for _, path := range []string{"b/valuations/1", "a/valuations/4", "missing/valuations/1", "missing/valuations", "a/valuations/999"} {
		historyRequest(t, mux, "GET", path, 404)
	}
	for _, suffix := range []string{"?from=2026-02-30", "?to=bad", "?from=2026-09-07&to=2026-09-06", "?limit=0", "?limit=101", "?limit=1&limit=2", "?cursor=0", "?cursor=-1", "?cursor=9223372036854775808", "?cursor=1.0", "?cursor=", "?unknown=x", "?account_id=b", "/0", "/-1", "/1?limit=2", "/x"} {
		historyRequest(t, mux, "GET", "a/valuations"+suffix, 400)
	}
	for _, path := range []string{"a/valuations", "a/valuations/1"} {
		historyRequest(t, mux, "POST", path, 405)
	}
	historyCount(t, s, 5)
}

func TestHistoryForeignFrozenAfterCorrectionsAndMarketChanges(t *testing.T) {
	i := Instrument{ID: "i", Market: "HK", Code: "00700", Name: "Original name", Currency: HKD}
	s := valuationFixture(t, []CurrentPosition{{InstrumentID: "i", Quantity: 1_234_567}}, []Instrument{i})
	putSource(t, s, "a", "cash-edit", "1", 10100, CurrentPosition{"i", 1_234_567})
	price, rate := Price(12_345_678), Rate(91_234_567)
	h := Handler{Store: s, Now: func() time.Time { return stockNow }, Quotes: valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
		rows := validValuationQuotes(ctx, is)
		rows["i"].Quote.Price = price
		return rows
	}), FX: valuationFX(func(context.Context, FXRequest) (FXQuote, error) {
		return FXQuote{Base: HKD, Quote: CNY, Mode: "latest", RequestedDate: "2026-09-06", Rate: rate, Date: "2026-09-05", Source: "Tencent/spot/HKDCNY", QuotedAt: "2026-09-05T15:00:00+08:00", FetchedAt: stockNow.Format(time.RFC3339Nano)}, nil
	})}
	mux := http.NewServeMux()
	h.Register(mux)
	v := sampleValuation(t, s, "a", h)
	historyPath := "a/valuations/" + v.HistoryID
	before := historyRequest(t, mux, "GET", historyPath, 200)
	var saved ValuationHistory
	require.NoError(t, json.Unmarshal(before, &saved))
	v.HistoryID = ""
	require.Equal(t, v, saved.Valuation)
	require.Equal(t, "Original name", saved.Instruments[0].Name)
	require.Equal(t, Quantity(1_234_567), saved.Valuation.Items[0].Quantity)
	require.Equal(t, Money(1390), *saved.Valuation.PositionsValue)
	require.Equal(t, "prior_date", saved.Valuation.Items[0].Status)
	price, rate = 20_000_000, 100_000_000
	putSource(t, s, "a", "replace-input", "2", 10200, CurrentPosition{"i", 2_000_000})
	corrected := sampleValuation(t, s, "a", h)
	require.NotEqual(t, v.LedgerRevision, corrected.LedgerRevision)
	require.NotEqual(t, *v.TotalAssets, *corrected.TotalAssets)
	putSource(t, s, "a", "delete-holding", "3", 0)
	// Frozen identities cannot be changed out of band.
	_, err := s.db.ExecContext(t.Context(), `UPDATE instruments SET name='Changed',code='00005'; UPDATE accounts SET name='Changed'`)
	require.Error(t, err)
	readMux := http.NewServeMux()
	Handler{Store: s, Quotes: valuationQuotes(func(context.Context, []Instrument) map[string]QuoteResult {
		t.Fatal("history called quotes")
		return nil
	}), FX: valuationFX(func(context.Context, FXRequest) (FXQuote, error) { t.Fatal("history called FX"); return FXQuote{}, nil })}.Register(readMux)
	require.Equal(t, before, historyRequest(t, readMux, "GET", historyPath, 200))
	historyRequest(t, readMux, "GET", "a/valuations", 200)
	historyCount(t, s, 2)
}

func TestHistoryLateFinishCapturesOriginalBasisAndClosedIdentities(t *testing.T) {
	i := Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Original", Currency: CNY}
	s := valuationFixture(t, []CurrentPosition{{InstrumentID: "i", Quantity: 1_000_000}}, []Instrument{i})
	v, captured, err := s.valuationInputs(t.Context(), "a")
	require.NoError(t, err)
	h := Handler{Store: s, Now: func() time.Time { return stockNow }, Quotes: valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
		_, err := s.PutCurrentHoldings(ctx, "a", "remove", CurrentHoldingsInput{ExpectedVersion: "1", Cash: replayMoney(20000), Positions: []CurrentPosition{}})
		require.NoError(t, err)
		return validValuationQuotes(ctx, is)
	})}
	require.NoError(t, h.valuePositions(t.Context(), &v, captured))
	require.Equal(t, Money(10000), v.Cash)
	require.Equal(t, Quantity(1_000_000), v.Items[0].Quantity)
	_, err = s.RecordValuation(t.Context(), v, captured)
	require.ErrorIs(t, err, errWeeklyBasis)
	historyCount(t, s, 0)
	current, held, err := s.valuationInputs(t.Context(), "a")
	require.NoError(t, err)
	require.NotEqual(t, v.LedgerRevision, current.LedgerRevision)
	require.NoError(t, h.valuePositions(t.Context(), &current, held))
	id, err := s.RecordValuation(t.Context(), current, held)
	require.NoError(t, err)
	historyID, err := strconv.ParseInt(id, 10, 64)
	require.NoError(t, err)
	detail, err := s.ValuationHistory(t.Context(), "a", historyID)
	require.NoError(t, err)
	require.Empty(t, detail.Instruments)
	require.Empty(t, detail.Valuation.Items)
	require.Equal(t, Money(20000), *detail.Valuation.TotalAssets)
}

func TestHistoryWriteErrorsAndCancellationRollback(t *testing.T) {
	for _, mode := range []string{"after_insert_error", "canceled_before", "canceled_in_transaction", "canceled_after_insert", "commit_error"} {
		t.Run(mode, func(t *testing.T) {
			s := valuationFixture(t, nil, nil)
			v, held, err := s.valuationInputs(t.Context(), "a")
			require.NoError(t, err)
			require.NoError(t, (Handler{Now: func() time.Time { return stockNow }}).valuePositions(t.Context(), &v, held))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "after_insert_error":
				_, err = s.db.ExecContext(t.Context(), `CREATE TRIGGER fail_history AFTER INSERT ON audit_log WHEN NEW.entity_type='valuation' BEGIN SELECT RAISE(FAIL,'synthetic'); END`)
			case "canceled_before":
				cancel()
			case "canceled_in_transaction":
				s.now = func() time.Time { cancel(); return stockNow }
			case "canceled_after_insert":
				conn, e := s.db.Conn(t.Context())
				require.NoError(t, e)
				err = conn.Raw(func(driver any) error {
					return driver.(*sqlite3.SQLiteConn).RegisterFunc("cancel_history", func() int { cancel(); return 1 }, false)
				})
				require.NoError(t, conn.Close())
				if err == nil {
					_, err = s.db.ExecContext(t.Context(), `CREATE TRIGGER cancel_history_insert AFTER INSERT ON audit_log WHEN NEW.entity_type='valuation' BEGIN SELECT cancel_history(); END`)
				}
			case "commit_error":
				_, err = s.db.ExecContext(t.Context(), `CREATE TABLE history_commit_guard (id INTEGER REFERENCES accounts(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER history_commit AFTER INSERT ON audit_log WHEN NEW.entity_type='valuation' BEGIN INSERT INTO history_commit_guard VALUES(999); END`)
			}
			require.NoError(t, err)
			id, err := s.RecordValuation(ctx, v, held)
			require.Error(t, err)
			require.Empty(t, id)
			if strings.HasPrefix(mode, "canceled") {
				require.ErrorIs(t, err, context.Canceled)
			}
			historyCount(t, s, 0)
			if mode == "after_insert_error" || mode == "commit_error" {
				historyRequest(t, historyMux(s), "POST", "a/valuation", 405)
				historyRequest(t, historyMux(s), "GET", "a/valuation", 200)
				historyCount(t, s, 0)
				// HEAD still succeeds without attempting the failing write.
				historyRequest(t, historyMux(s), "HEAD", "a/valuation", 200)
			}
		})
	}
}

func TestHistoryConcurrentObservations(t *testing.T) {
	s := valuationFixture(t, nil, nil)
	const count = 16
	var wg sync.WaitGroup
	ids := make(chan string, count)
	for range count {
		wg.Go(func() {
			v := sampleValuation(t, s, "a", Handler{})
			ids <- v.HistoryID
		})
	}
	wg.Wait()
	close(ids)
	unique := map[string]bool{}
	for id := range ids {
		require.NotEmpty(t, id)
		require.False(t, unique[id])
		unique[id] = true
	}
	require.Len(t, unique, count)
	historyCount(t, s, count)
}

func TestHistoryLargeSharedTimelineIDs(t *testing.T) {
	s := valuationFixture(t, nil, nil)
	mux := historyMux(s)
	stamp := stockNow.Format(time.RFC3339Nano)
	require.NoError(t, s.db.WithTx(t.Context(), func(tx *sql.Tx) error {
		record := AccountRecord{ID: "manual-high", AccountID: "a", AccountEntry: AccountEntry{Kind: "log", Date: "2026-09-06", Note: "Synthetic"}, Sequence: "9007199254740992", Origin: "manual", Version: "1", CreatedAt: stamp, UpdatedAt: stamp}
		_, err := putAccountRecord(t.Context(), tx, &record, nil, "high-sequence", "Synthetic", "human")
		return err
	}))
	v := sampleValuation(t, s, "a", Handler{})
	require.Equal(t, "9007199254740993", v.HistoryID)
	historyRequest(t, mux, "GET", "a/valuations/9007199254740993", 200)
	latest := sampleValuation(t, s, "a", Handler{})
	require.Equal(t, "9007199254740994", latest.HistoryID)
	var page listJSON[ValuationSummary]
	require.NoError(t, json.Unmarshal(historyRequest(t, mux, "GET", "a/valuations?limit=1", 200), &page))
	require.Equal(t, latest.HistoryID, page.Items[0].ID)
	require.Equal(t, latest.HistoryID, page.NextCursor)
	require.NoError(t, json.Unmarshal(historyRequest(t, mux, "GET", "a/valuations?cursor="+latest.HistoryID, 200), &page))
	require.Len(t, page.Items, 1)
	require.Equal(t, v.HistoryID, page.Items[0].ID)
}

func TestHistoryRejectsCorruptQuoteFXAndIdentity(t *testing.T) {
	i := Instrument{ID: "i", Market: "HK", Code: "00700", Name: "Frozen", Currency: HKD}
	s := valuationFixture(t, []CurrentPosition{{InstrumentID: "i", Quantity: 1_000_000}}, []Instrument{i})
	v, held, err := s.valuationInputs(t.Context(), "a")
	require.NoError(t, err)
	h := Handler{Now: func() time.Time { return stockNow }, Quotes: valuationQuotes(validValuationQuotes), FX: valuationFX(func(context.Context, FXRequest) (FXQuote, error) {
		return FXQuote{Base: HKD, Quote: CNY, Mode: "latest", RequestedDate: "2026-09-06", Rate: 91_234_567, Date: "2026-09-06", Source: "Tencent", QuotedAt: "2026-09-06T15:00:00+08:00", FetchedAt: stockNow.Format(time.RFC3339Nano)}, nil
	})}
	require.NoError(t, h.valuePositions(t.Context(), &v, held))
	_, err = s.RecordValuation(t.Context(), v, held)
	require.NoError(t, err)
	var original string
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT after_json FROM audit_log WHERE entity_type='valuation' AND entity_id='1'`).Scan(&original))
	allowAuditCorruption(t, s.db)
	for _, mutate := range []func(*valuationSnapshot){
		func(s *valuationSnapshot) { s.Valuation.Items[0].Quote.Price += 1_000_000 },
		func(s *valuationSnapshot) { s.Valuation.Items[0].Quote.Price = 0 },
		func(s *valuationSnapshot) { s.Valuation.Items[0].Quote.Currency = USD },
		func(s *valuationSnapshot) { s.Valuation.Items[0].Quote.Symbol = "wrong" },
		func(s *valuationSnapshot) { s.Valuation.Items[0].Quote.FetchedAt = "bad" },
		func(s *valuationSnapshot) { s.Valuation.Items[0].Quote.Date = "2026-09-07" },
		func(s *valuationSnapshot) { s.Valuation.Items[0].FX = nil },
		func(s *valuationSnapshot) { s.Valuation.Items[0].FX.Rate = 200_000_000 },
		func(s *valuationSnapshot) { s.Valuation.Items[0].FX.Mode = "historical" },
		func(s *valuationSnapshot) { s.Valuation.Items[0].FX.Base = USD },
		func(s *valuationSnapshot) { s.Valuation.Items[0].FX.QuotedAt = "bad" },
		func(s *valuationSnapshot) { s.Valuation.Items[0].Quantity += 1_000_000 },
		func(s *valuationSnapshot) { s.Valuation.Items[0].Status = "prior_date" },
		func(s *valuationSnapshot) { s.Instruments = nil },
		func(s *valuationSnapshot) { s.Instruments[0].ID = "other" },
		func(s *valuationSnapshot) { s.Instruments[0].Code = "00005" },
		func(s *valuationSnapshot) { s.AccountName = "" },
	} {
		var snapshot valuationSnapshot
		require.NoError(t, json.Unmarshal([]byte(original), &snapshot))
		mutate(&snapshot)
		payload, err := json.Marshal(snapshot)
		require.NoError(t, err)
		_, err = s.db.ExecContext(t.Context(), `UPDATE audit_log SET after_json=? WHERE entity_type='valuation' AND entity_id='1'`, string(payload))
		require.NoError(t, err)
		got, err := s.ValuationHistory(t.Context(), "a", 1)
		require.ErrorIs(t, err, ErrCorrupt)
		require.Equal(t, ValuationHistory{}, got)
		_, err = s.ListValuations(t.Context(), "a", "", "", 0, 30)
		require.ErrorIs(t, err, ErrCorrupt)
	}
}

func TestHistoryCorruptStorageAndInvalidSnapshotsFailClosed(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE audit_log SET after_json='{}' WHERE entity_type='valuation'`,
		`UPDATE audit_log SET after_json='null' WHERE entity_type='valuation'`,
		`UPDATE audit_log SET after_json=json_set(after_json,'$.valuation.cash','999.00') WHERE entity_type='valuation'`,
		`UPDATE audit_log SET after_json=json_remove(after_json,'$.valuation.items') WHERE entity_type='valuation'`,
		`UPDATE audit_log SET after_json=json_set(after_json,'$.valuation.history_id','1') WHERE entity_type='valuation'`,
		`UPDATE audit_log SET after_json=json_set(after_json,'$.valuation.complete',json('false')) WHERE entity_type='valuation'`,
		`UPDATE audit_log SET after_json=json_set(after_json,'$.valuation.ledger_revision','invalid') WHERE entity_type='valuation'`,
		`UPDATE audit_log SET after_json=json_set(after_json,'$.valuation.cash',100) WHERE entity_type='valuation'`,
		`UPDATE audit_log SET after_json=json_set(after_json,'$.unknown',1) WHERE entity_type='valuation'`,
		`UPDATE account_records SET total_assets_minor=1 WHERE origin='currentrefresh'`,
		`UPDATE audit_log SET metadata_json=json_set(metadata_json,'$.calculated_at','invalid') WHERE entity_type='valuation'`,
		`UPDATE audit_log SET recorded_at='invalid' WHERE entity_type='valuation'`,
		`UPDATE audit_log SET metadata_json=json_set(metadata_json,'$.ledger_revision',printf('%064d',0)) WHERE entity_type='valuation'`,
		`UPDATE audit_log SET metadata_json=json_set(metadata_json,'$.schema_version',2) WHERE entity_type='valuation'`,
		`PRAGMA ignore_check_constraints=ON; UPDATE audit_log SET after_json='broken' WHERE entity_type='valuation'`,
	} {
		t.Run(mutation, func(t *testing.T) {
			s := valuationFixture(t, nil, nil)
			mux := historyMux(s)
			sampleValuation(t, s, "a", Handler{})
			allowAuditCorruption(t, s.db)
			_, err := s.db.ExecContext(t.Context(), mutation)
			require.NoError(t, err)
			historyRequest(t, mux, "GET", "a/valuations/1", 500)
			historyRequest(t, mux, "GET", "a/valuations", 500)
			historyRequest(t, mux, "HEAD", "a/valuations/1", 500)
			historyCount(t, s, 1)
		})
	}
	s := valuationFixture(t, nil, nil)
	v, held, err := s.valuationInputs(t.Context(), "a")
	require.NoError(t, err)
	require.NoError(t, (Handler{Now: func() time.Time { return stockNow }}).valuePositions(t.Context(), &v, held))
	for _, mutate := range []func(*Valuation){
		func(v *Valuation) { v.Complete = false }, func(v *Valuation) { v.TotalAssets = nil }, func(v *Valuation) { v.PositionsValue = nil }, func(v *Valuation) { v.HistoryID = "1" }, func(v *Valuation) { v.KnownPositionsValue = 1 }, func(v *Valuation) { v.LedgerAt = "bad" }, func(v *Valuation) { v.Cash++ },
	} {
		bad := v
		mutate(&bad)
		id, err := s.RecordValuation(t.Context(), bad, held)
		require.ErrorIs(t, err, ErrCorrupt)
		require.Empty(t, id)
	}
	historyCount(t, s, 0)
}
