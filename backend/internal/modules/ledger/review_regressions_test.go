package ledger

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReviewHistoricalReceiptAuditIntegrity(t *testing.T) {
	for _, kind := range []string{"import", "manual", "account"} {
		for _, damage := range []string{"wrong-link", "changed"} {
			t.Run(kind+"-"+damage, func(t *testing.T) {
				f := newHTTPFixture(t)
				var replay func() error
				var entity string
				switch kind {
				case "import":
					data := syntheticImport(t)
					p := parseSynthetic(t, data)
					original, err := f.store.ConfirmAccountImport(t.Context(), "a", "init", p.Digest, true, data)
					require.NoError(t, err)
					replay = func() error {
						r, e := f.store.ConfirmAccountImport(t.Context(), "a", "init", p.Digest, true, data)
						if e == nil {
							require.Equal(t, original, r)
						}
						return e
					}
					entity = "import_row"
				case "manual":
					f = reportedFixture(t)
					m := Money(100)
					c := AccountRecordCommand{Action: CreateOperation, AccountID: "a", ID: "manual-a", Entry: &AccountEntry{Kind: "asset", Date: "2020-01-01", TotalAssets: &m}}
					original, err := f.store.WriteAccountRecord(t.Context(), "manual-key", c)
					require.NoError(t, err)
					replay = func() error {
						r, e := f.store.WriteAccountRecord(t.Context(), "manual-key", c)
						if e == nil {
							require.Equal(t, original, r)
						}
						return e
					}
					f.request(t, "PUT", "/accounts/a/records/manual-a", "correct", `{"expected_version":"1","reason":"synthetic","entry":{"kind":"asset","date":"2020-01-01","total_assets":"2"}}`, 200)
					entity = "account_record"
				case "account":
					input := ReportedAccountInput{ID: "a", Name: "Synthetic <b>literal</b>", Currency: CNY, OpeningDate: "2020-01-01"}
					original, err := f.store.CreateReportedAccount(t.Context(), "account", input)
					require.NoError(t, err)
					replay = func() error {
						r, e := f.store.CreateReportedAccount(t.Context(), "account", input)
						if e == nil {
							require.Equal(t, original, r)
						}
						return e
					}
					entity = "account"
				}
				require.NoError(t, replay())
				allowAuditCorruption(t, f.store.db)
				var err error
				if damage == "wrong-link" {
					f.instrument(t, "unrelated")
					_, err = f.store.db.ExecContext(t.Context(), `UPDATE idempotency_receipts SET audit_id=(SELECT id FROM audit_log WHERE entity_type='instrument' AND entity_id='unrelated') WHERE key IN ('init','manual-key','account')`)
				} else {
					_, err = f.store.db.ExecContext(t.Context(), `UPDATE audit_log SET after_json=json_set(after_json,'$.unknown','corrupt') WHERE entity_type=? AND version=1`, entity)
				}
				require.NoError(t, err)
				before := auditCount(t, f.store)
				require.ErrorIs(t, replay(), ErrCorrupt)
				require.Equal(t, before, auditCount(t, f.store))
			})
		}
	}
}

func TestReviewReadValuationAndIdempotentSave(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "100.00", nil)
	before := auditCount(t, f.store)
	for _, method := range []string{"GET", "HEAD", "GET"} {
		f.request(t, method, "/accounts/a/valuation", "", "", 200)
	}
	var n int
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&n))
	require.Zero(t, n)
	require.Equal(t, before, auditCount(t, f.store))
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&n))
	require.Equal(t, 11, n)
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_schema WHERE type='view'`).Scan(&n))
	require.Zero(t, n)
	original := f.request(t, "POST", "/accounts/a/valuation", "save", "{}", 200).Body.String()
	f.request(t, "PUT", "/accounts/a/records/valuation-1", "correct", `{"expected_version":"1","reason":"correction","entry":{"kind":"asset","date":"2026-09-06","total_assets":"200.00"}}`, 200)
	before = auditCount(t, f.store)
	for _, method := range []string{"GET", "HEAD", "GET"} {
		f.request(t, method, "/accounts/a/valuation", "", "", 200)
	}
	require.Equal(t, original, f.request(t, "POST", "/accounts/a/valuation", "save", "{}", 200).Body.String())
	require.Equal(t, Money(20000), *basisRead(t, f, "a", "", "").Closing.Assets)
	require.Equal(t, before, auditCount(t, f.store))
	f.request(t, "POST", "/accounts/a/valuation", "save", `{"total_assets":"1000"}`, 400)
	f.request(t, "POST", "/accounts/a/valuation", "missing-body", "", 415)
	f.request(t, "POST", "/accounts/a/valuation", "", "{}", 400)
	f.request(t, "POST", "/accounts/a/valuation", "correct", "{}", 409)
	f.request(t, "POST", "/operations", "save", httpMutation(t, httpCash("new", "a", "2026-01-02", "1", "deposit", "1"), ""), 409)
}

func TestReviewSavedRetryDoesNotFetchAndConcurrentSave(t *testing.T) {
	s := valuationFixture(t, []OpeningPosition{{InstrumentID: "i", Quantity: 1000000}}, []Instrument{{ID: "i", Market: "SZ", Code: "000001", Name: "Synthetic", Currency: CNY}})
	var calls atomic.Int64
	mux := http.NewServeMux()
	Handler{Store: s, Now: s.now, Quotes: valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
		calls.Add(1)
		return validValuationQuotes(ctx, is)
	})}.Register(mux)
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", ledgerPrefix+"/accounts/a/valuation", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "same-save")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for range 2 {
		wg.Go(func() {
			w := request()
			if w.Code != 200 {
				t.Errorf("save status %d", w.Code)
			}
			results <- w.Body.String()
		})
	}
	wg.Wait()
	close(results)
	var first string
	for r := range results {
		if first == "" {
			first = r
		} else {
			require.Equal(t, first, r)
		}
	}
	before := calls.Load()
	events := auditCount(t, s)
	require.Equal(t, first, request().Body.String())
	require.Equal(t, before, calls.Load())
	require.Equal(t, events, auditCount(t, s))
	historyCount(t, s, 1)
	_, err := s.db.ExecContext(t.Context(), `DROP TRIGGER audit_log_no_delete; DELETE FROM audit_log WHERE entity_type='account_record'`)
	require.NoError(t, err)
	require.Equal(t, 500, request().Code)
	require.Equal(t, before, calls.Load())
}

func TestReviewExplicitSaveGuardsAndRollback(t *testing.T) {
	for _, target := range []string{"account_records", "audit_log", "idempotency_receipts"} {
		t.Run(target, func(t *testing.T) {
			f := newHTTPFixture(t)
			f.account(t, "a", "CNY", "100.00", nil)
			before := auditCount(t, f.store)
			_, err := f.store.db.ExecContext(t.Context(), `CREATE TRIGGER fail_explicit_save BEFORE INSERT ON `+target+` BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
			require.NoError(t, err)
			f.request(t, "POST", "/accounts/a/valuation", "save", "{}", 500)
			require.Equal(t, before, auditCount(t, f.store))
			historyCount(t, f.store, 0)
			var n int
			require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&n))
			require.Zero(t, n)
		})
	}
	s := valuationFixture(t, []OpeningPosition{{InstrumentID: "i", Quantity: 1000000}}, []Instrument{{ID: "i", Market: "SZ", Code: "000001", Name: "Synthetic", Currency: CNY}})
	mux := historyMux(s)
	before := auditCount(t, s)
	historyRequest(t, mux, "POST", "a/valuation", 422)
	require.Equal(t, before, auditCount(t, s))
	historyCount(t, s, 0)
	for _, tc := range []struct {
		body, content string
		code          int
	}{{"", "application/json", 400}, {"{}", "text/plain", 415}, {`{"amount":"1"}`, "application/json", 400}, {`null`, "application/json", 400}} {
		r := httptest.NewRequest("POST", ledgerPrefix+"/accounts/a/valuation", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.content)
		r.Header.Set("Idempotency-Key", "guard")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		require.Equal(t, tc.code, w.Code)
	}
}
