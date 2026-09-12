package ledger

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountRecordsHTTPWorkflow(t *testing.T) {
	f := newHTTPFixture(t)
	account := `{"id":"reported","name":"Synthetic","currency":"CNY","opening_date":"2020-01-01"}`
	created := f.request(t, "POST", "/accounts", "create", account, 201).Body.String()
	require.Equal(t, created, f.request(t, "POST", "/accounts", "create", account, 201).Body.String())
	require.Nil(t, httpObject(t, created)["opening_cash"])
	f.request(t, "POST", "/accounts", "other", account, 409)
	f.request(t, "POST", "/accounts", "create", `{"id":"other","name":"Synthetic","currency":"CNY","opening_date":"2020-01-01"}`, 409)
	empty := httpObject(t, f.request(t, "GET", "/accounts/reported/effective-summary", "", "", 200).Body.String())
	require.Nil(t, empty["latest_assets"])
	require.Nil(t, empty["from"])
	payload := `{"id":"manual-a","entry":{"kind":"cash_flow","date":"2099-01-01","flow":"-90071992547409.01","total_assets":null,"note":""}}`
	first := f.request(t, "POST", "/accounts/reported/records", "entry", payload, 201).Body.String()
	require.Nil(t, httpObject(t, first)["total_assets"])
	replace := `{"expected_version":"1","reason":"Synthetic correction","entry":{"kind":"asset","date":"2099-01-02","flow":null,"total_assets":"0.00","note":"Synthetic"}}`
	f.request(t, "PUT", "/accounts/reported/records/manual-a", "replace", replace, 200)
	f.request(t, "PUT", "/accounts/reported/records/manual-a", "stale", replace, 409)
	require.Equal(t, first, f.request(t, "POST", "/accounts/reported/records", "entry", payload, 201).Body.String())
	summary := httpObject(t, f.request(t, "GET", "/accounts/reported/effective-summary", "", "", 200).Body.String())
	require.Nil(t, summary["latest_assets"])
	require.Nil(t, summary["latest_asset_date"])
	require.Equal(t, "0.00", f.get(t, "/accounts/reported/records/manual-a")["total_assets"])
	require.Equal(t, "0.00", summary["total_out"])
	f.request(t, "DELETE", "/accounts/reported/records/manual-a", "void", `{"expected_version":"2","reason":"Synthetic void"}`, 200)
	f.request(t, "PUT", "/accounts/reported/records/manual-a", "voided", `{"expected_version":"3","reason":"Synthetic","entry":{"kind":"log","date":"2020-01-01","note":"Synthetic"}}`, 409)
	revisions := httpItems(t, httpObject(t, f.request(t, "GET", "/accounts/reported/records/manual-a/revisions", "", "", 200).Body.String()))
	require.Len(t, revisions, 3)
	require.Equal(t, "-90071992547409.01", revisions[0]["record"].(map[string]any)["flow"])
	require.Len(t, httpItems(t, httpObject(t, f.request(t, "GET", "/accounts/reported/records?status=active", "", "", 200).Body.String())), 0)
	require.Len(t, httpItems(t, httpObject(t, f.request(t, "GET", "/accounts/reported/records?status=voided", "", "", 200).Body.String())), 1)
	for _, path := range []string{"/accounts/reported/records?from=invalid", "/accounts/reported/records?limit=0", "/accounts/reported/records?cursor=no", "/accounts/reported/records/manual-a/revisions?bad=x&cursor=1", "/accounts/reported/effective-summary?"} {
		f.request(t, "GET", path, "", "", 400)
	}
	f.request(t, "HEAD", "/accounts/reported/records", "", "", 200)
	f.request(t, "GET", "/accounts/missing/records", "", "", 404)
	f.request(t, "GET", "/accounts/reported/records/missing", "", "", 404)
}

func TestAccountRecordsImportEditsAndReupload(t *testing.T) {
	f := newHTTPFixture(t)
	data := syntheticImport(t)
	p := parseSynthetic(t, data)
	_, err := f.store.ConfirmAccountImport(t.Context(), "a", "import-key", p.Digest, true, data)
	require.NoError(t, err)
	original, err := f.store.ImportSummary(t.Context(), "a")
	require.NoError(t, err)
	originals, err := f.store.ImportedRecords(t.Context(), "a", OperationQuery{Limit: 100})
	require.NoError(t, err)
	rows := httpItems(t, httpObject(t, f.request(t, "GET", "/accounts/a/records", "", "", 200).Body.String()))
	require.Len(t, rows, 5)
	id := rows[0]["id"].(string)
	_, err = f.store.db.ExecContext(t.Context(), `CREATE TRIGGER synthetic_edit_failure BEFORE INSERT ON idempotency_receipts WHEN NEW.key='edit-import' BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
	require.NoError(t, err)
	f.request(t, "PUT", "/accounts/a/records/"+id, "edit-import", `{"expected_version":"1","reason":"Synthetic","entry":{"kind":"cash_flow","date":"2020-01-04","flow":"-999999.00","total_assets":"0","note":""}}`, 500)
	var failedRevisions int
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_log WHERE entity_type='account_record'`).Scan(&failedRevisions))
	require.Equal(t, 5, failedRevisions)
	require.Equal(t, "1", httpObject(t, f.request(t, "GET", "/accounts/a/records/"+id, "", "", 200).Body.String())["version"])
	_, err = f.store.db.ExecContext(t.Context(), `DROP TRIGGER synthetic_edit_failure`)
	require.NoError(t, err)
	f.request(t, "PUT", "/accounts/a/records/"+id, "edit-import", `{"expected_version":"1","reason":"Synthetic","entry":{"kind":"cash_flow","date":"2020-01-04","flow":"-999999.00","total_assets":"0","note":""}}`, 200)
	f.request(t, "DELETE", "/accounts/a/records/"+rows[1]["id"].(string), "void-import", `{"expected_version":"1","reason":"Synthetic"}`, 200)
	f.request(t, "POST", "/accounts/a/records", "manual", `{"id":"manual-new","entry":{"kind":"log","date":"2020-01-04","note":"Synthetic log"}}`, 201)
	before := f.request(t, "GET", "/accounts/a/records", "", "", 200).Body.String()
	result, err := f.store.ConfirmAccountImport(t.Context(), "a", "new-import-key", p.Digest, true, data)
	require.NoError(t, err)
	require.True(t, result.Duplicate)
	require.Equal(t, before, f.request(t, "GET", "/accounts/a/records", "", "", 200).Body.String())
	unchanged, err := f.store.ImportSummary(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, original, unchanged)
	unchangedRows, err := f.store.ImportedRecords(t.Context(), "a", OperationQuery{Limit: 100})
	require.NoError(t, err)
	require.Equal(t, originals, unchangedRows)
	revisions := httpItems(t, httpObject(t, f.request(t, "GET", "/accounts/a/records/"+id+"/revisions", "", "", 200).Body.String()))
	require.Len(t, revisions, 2)
	require.Equal(t, revisions[0]["record"].(map[string]any)["original"], revisions[1]["record"].(map[string]any)["original"])
	f.account(t, "holdings", "CNY", "1.00", nil)
	cash := f.request(t, "GET", "/accounts/holdings", "", "", 200).Body.String()
	f.request(t, "POST", "/accounts/holdings/records", "independent", `{"id":"manual-out","entry":{"kind":"cash_flow","date":"2020-01-01","flow":"-999999","total_assets":null}}`, 201)
	require.Equal(t, cash, f.request(t, "GET", "/accounts/holdings", "", "", 200).Body.String())
}

func TestAccountRecordsStrictValidationAndExactSummary(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "1.00", nil)
	for i, payload := range []string{
		`{"id":"manual-a","entry":{"kind":"asset","date":"2020-01-01"}}`,
		`{"id":"manual-a","entry":{"kind":"cash_flow","date":"2020-01-01","flow":null}}`,
		`{"id":"manual-a","entry":{"kind":"log","date":"2020-01-01","note":" "}}`,
		`{"id":"manual-a","entry":{"kind":"asset","date":"2020-01-01","total_assets":"-1"}}`,
		`{"id":"manual-a","entry":{"kind":"asset","date":"2020-01-01","total_assets":0}}`,
		`{"id":"manual-a","entry":{"kind":"asset","date":"2020-01-01","total_assets":"1.001"}}`,
		`{"id":"manual-a","entry":{"kind":"asset","date":"2020-01-01","total_assets":"1","Total_assets":"2"}}`,
		`{"id":"manual-a","entry":{"kind":"asset","date":"2020-01-01","total_assets":"1","total_assets":"2"}}`,
		`{"id":"manual-a","entry":{"kind":"log","date":"2020-01-01","note":"Synthetic","flow":"0"}}`,
		`{"id":"import-1","entry":{"kind":"asset","date":"2020-01-01","total_assets":"1"}}`,
	} {
		f.request(t, "POST", "/accounts/a/records", fmt.Sprintf("bad-%d", i), payload, 400)
	}
	for i := 0; i < 3; i++ {
		f.request(t, "POST", "/accounts/a/records", fmt.Sprintf("key-%d", i), fmt.Sprintf(`{"id":"manual-%d","entry":{"kind":"cash_flow","date":"2099-01-01","flow":"92233720368547758.07","total_assets":"0"}}`, i), 201)
	}
	summary := httpObject(t, f.request(t, "GET", "/accounts/a/effective-summary", "", "", 200).Body.String())
	require.Equal(t, "276701161105643274.21", summary["total_in"])
	require.Nil(t, summary["latest_assets"])
	require.Equal(t, float64(0), summary["latest_asset_count"])
	basisEntry(t, f, "manual-today", "2026-09-06", "asset", "null", `"0"`)
	nowSummary := f.get(t, "/accounts/a/effective-summary")
	require.Equal(t, "0.00", nowSummary["latest_assets"])
	require.Equal(t, float64(1), nowSummary["latest_asset_count"])
	page1 := httpObject(t, f.request(t, "GET", "/accounts/a/records?limit=2", "", "", 200).Body.String())
	page2 := httpObject(t, f.request(t, "GET", "/accounts/a/records?limit=2&cursor="+page1["next_cursor"].(string), "", "", 200).Body.String())
	require.Len(t, httpItems(t, page1), 2)
	require.Len(t, httpItems(t, page2), 2)
}

func TestAccountRecordsRollbackConcurrencyAndDurableReceipts(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), dir)
	require.NoError(t, err)
	s := NewStore(db, nil)
	input := ReportedAccountInput{"a", "Synthetic", CNY, "2020-01-01"}
	first, err := s.CreateReportedAccount(t.Context(), "account", input)
	require.NoError(t, err)
	zero := Money(0)
	c := AccountRecordCommand{Action: CreateOperation, AccountID: "a", ID: "manual-a", Entry: &AccountEntry{Kind: "asset", Date: "2020-01-01", TotalAssets: &zero}}
	_, err = db.ExecContext(t.Context(), `CREATE TRIGGER synthetic_failure BEFORE INSERT ON idempotency_receipts WHEN NEW.key='write' BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
	require.NoError(t, err)
	_, err = s.WriteAccountRecord(t.Context(), "write", c)
	require.Error(t, err)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM account_records`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_log WHERE entity_type='account_record'`).Scan(&count))
	require.Zero(t, count)
	_, err = db.ExecContext(t.Context(), `DROP TRIGGER synthetic_failure`)
	require.NoError(t, err)
	receipt, err := s.WriteAccountRecord(t.Context(), "write", c)
	require.NoError(t, err)
	replace := c
	replace.Action = ReplaceOperation
	replace.ExpectedVersion = "1"
	replace.Reason = "Synthetic"
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			_, err := s.WriteAccountRecord(t.Context(), fmt.Sprintf("replace-%d", i), replace)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrVersion)
			conflicts++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflicts)
	require.NoError(t, db.Close())
	db, err = Open(t.Context(), dir)
	require.NoError(t, err)
	defer db.Close()
	s = NewStore(db, nil)
	replay, err := s.WriteAccountRecord(t.Context(), "write", c)
	require.NoError(t, err)
	require.JSONEq(t, string(receipt), string(replay))
	for _, statement := range []string{`DELETE FROM audit_log`, `UPDATE audit_log SET metadata_json='{}'`, `DELETE FROM idempotency_receipts`, `UPDATE idempotency_receipts SET request_json='{}'`} {
		_, err := db.ExecContext(t.Context(), statement)
		require.Error(t, err)
	}
	accountReplay, err := s.CreateReportedAccount(t.Context(), "account", input)
	require.NoError(t, err)
	require.JSONEq(t, string(first), string(accountReplay))
	c.Entry = &AccountEntry{Kind: "log", Date: "2020-01-01", Note: "Synthetic different"}
	_, err = s.WriteAccountRecord(t.Context(), "write", c)
	require.ErrorIs(t, err, ErrIdempotency)
}
