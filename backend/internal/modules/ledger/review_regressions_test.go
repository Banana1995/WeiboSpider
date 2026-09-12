package ledger

import (
	"context"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestReviewHistoricalReceiptAuditIntegrity(t *testing.T) {
	for _, kind := range []string{"import", "manual", "account"} {
		for _, damage := range []string{"wrong-link", "changed"} {
			t.Run(kind+"-"+damage, func(t *testing.T) {
				f := newHTTPFixture(t)
				var retry func() error
				var entity, key string
				switch kind {
				case "import":
					data := syntheticImport(t)
					p := parseSynthetic(t, data)
					first, err := f.store.ConfirmAccountImport(t.Context(), "a", "init", p.Digest, true, data)
					require.NoError(t, err)
					retry = func() error {
						r, e := f.store.ConfirmAccountImport(t.Context(), "a", "init", p.Digest, true, data)
						if e == nil {
							require.Equal(t, first, r)
						}
						return e
					}
					entity, key = "import_row", "init"
				case "manual":
					f = reportedFixture(t)
					c := AccountRecordCommand{Action: CreateOperation, AccountID: "a", ID: "manual-a", Entry: &AccountEntry{Kind: "asset", Date: "2020-01-01", TotalAssets: replayMoney(100)}}
					first, err := f.store.WriteAccountRecord(t.Context(), "manual-key", c)
					require.NoError(t, err)
					retry = func() error {
						r, e := f.store.WriteAccountRecord(t.Context(), "manual-key", c)
						if e == nil {
							require.Equal(t, first, r)
						}
						return e
					}
					f.request(t, "PUT", "/accounts/a/records/manual-a", "correct", `{"expected_version":"1","reason":"synthetic","entry":{"kind":"asset","date":"2020-01-01","total_assets":"2"}}`, 200)
					entity, key = "account_record", "manual-key"
				case "account":
					input := ReportedAccountInput{ID: "a", Name: "Synthetic <b>literal</b>", Currency: CNY, OpeningDate: "2020-01-01"}
					first, err := f.store.CreateReportedAccount(t.Context(), "account", input)
					require.NoError(t, err)
					retry = func() error {
						r, e := f.store.CreateReportedAccount(t.Context(), "account", input)
						if e == nil {
							require.Equal(t, first, r)
						}
						return e
					}
					entity, key = "account", "account"
				}
				require.NoError(t, retry())
				allowAuditCorruption(t, f.store.db)
				var err error
				if damage == "wrong-link" {
					f.instrument(t, "unrelated")
					_, err = f.store.db.ExecContext(t.Context(), `UPDATE idempotency_receipts SET audit_id=(SELECT id FROM audit_log WHERE entity_type='instrument' AND entity_id='unrelated') WHERE key=?`, key)
				} else {
					_, err = f.store.db.ExecContext(t.Context(), `UPDATE audit_log SET after_json=json_set(after_json,'$.unknown','corrupt') WHERE entity_type=? AND version=1`, entity)
				}
				require.NoError(t, err)
				before := f.snapshot(t)
				require.ErrorIs(t, retry(), ErrCorrupt)
				require.Equal(t, before, f.snapshot(t))
			})
		}
	}
}

func TestReviewValuationReadsCannotOverwriteCorrectedOrVoidedAssets(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "100.00", nil)
	v := sampleValuation(t, f.store, "a", Handler{})
	path := "/accounts/a/records/valuation-" + v.HistoryID
	f.request(t, "PUT", path, "correct", `{"expected_version":"1","reason":"correction","entry":{"kind":"asset","date":"2026-09-06","total_assets":"200.00"}}`, 200)
	before := f.snapshot(t)
	for _, method := range []string{"GET", "HEAD", "GET"} {
		f.request(t, method, "/accounts/a/valuation", "", "", 200)
	}
	f.request(t, "POST", "/accounts/a/valuation", "no-save", "{}", 405)
	require.Equal(t, before, f.snapshot(t))
	require.Equal(t, Money(20000), *basisRead(t, f, "a", "", "").Closing.Assets)
	f.request(t, "DELETE", path, "void", `{"expected_version":"2","reason":"duplicate"}`, 200)
	before = f.snapshot(t)
	f.request(t, "GET", "/accounts/a/valuation", "", "", 200)
	require.Equal(t, before, f.snapshot(t))
	require.Nil(t, basisRead(t, f, "a", "", "").Closing)
	history, err := f.store.ValuationHistory(t.Context(), "a", mustInt(t, v.HistoryID))
	require.NoError(t, err)
	require.Equal(t, Money(10000), *history.Valuation.TotalAssets)
}

func TestReviewConcurrentCurrentRetriesNeverFetchQuotes(t *testing.T) {
	s := valuationFixture(t, nil, nil)
	var calls atomic.Int64
	mux := http.NewServeMux()
	Handler{Store: s, Quotes: valuationQuotes(func(context.Context, []Instrument) map[string]QuoteResult { calls.Add(1); return nil })}.Register(mux)
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", ledgerPrefix+"/accounts/a/current-holdings", strings.NewReader(`{"expected_version":"1","cash":"200.00","positions":[]}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "same-input")
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
				t.Errorf("status %d", w.Code)
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
	putSource(t, s, "a", "later", "2", 30000)
	before := auditCount(t, s)
	require.Equal(t, first, request().Body.String())
	require.Equal(t, before, auditCount(t, s))
	require.Zero(t, calls.Load())
	historyCount(t, s, 0)
	allowAuditCorruption(t, s.db)
	_, err := s.db.ExecContext(t.Context(), `UPDATE audit_log SET after_json=json_set(after_json,'$.cash','999.00') WHERE entity_type='current_holdings' AND version=2`)
	require.NoError(t, err)
	require.Equal(t, 500, request().Code)
	require.Zero(t, calls.Load())
}

func TestReviewCurrentIdentityAuditAndReceiptFailuresRollback(t *testing.T) {
	for _, target := range []string{"instruments", "current_holdings", "audit_log", "idempotency_receipts"} {
		t.Run(target, func(t *testing.T) {
			f := reportedFixture(t)
			before := f.snapshot(t)
			_, err := f.store.db.ExecContext(t.Context(), `CREATE TRIGGER failure AFTER INSERT ON `+target+` BEGIN SELECT RAISE(FAIL,'synthetic-private'); END`)
			require.NoError(t, err)
			payload := `{"expected_version":"0","cash":"1.00","positions":[{"instrument_id":"new","quantity":"1"}],"securities":[{"id":"new","market":"SH","code":"600000","name":"Synthetic","currency":"CNY"}]}`
			f.request(t, "PUT", "/accounts/a/current-holdings", "retryable", payload, 500)
			require.Equal(t, before, f.snapshot(t))
			require.NotContains(t, f.logs.String(), "synthetic-private")
			_, err = f.store.db.ExecContext(t.Context(), `DROP TRIGGER failure`)
			require.NoError(t, err)
			f.request(t, "PUT", "/accounts/a/current-holdings", "retryable", payload, 200)
		})
	}
}
