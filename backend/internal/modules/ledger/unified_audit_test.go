package ledger

import (
	"sync"
	"testing"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/stretchr/testify/require"
)

// Deliberately disable append-only protection in corruption tests only. These
// writable adapters are never migrations or application compatibility surfaces.
func allowAuditCorruption(t *testing.T, db *database.DB) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `DROP TRIGGER IF EXISTS audit_log_no_update;
		DROP TRIGGER IF EXISTS audit_log_no_delete;
		DROP TRIGGER IF EXISTS idempotency_receipts_no_update;
		DROP TRIGGER IF EXISTS idempotency_receipts_no_delete`)
	require.NoError(t, err)
}

func AllowAuditCorruptionForTest(t *testing.T, db *database.DB) { allowAuditCorruption(t, db) }

func auditCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log`).Scan(&n))
	return n
}

func TestUnifiedImportManualAutomaticAndAudit(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "100.00", nil)
	data := syntheticImport(t)
	p := parseSynthetic(t, data)
	original, err := f.store.ConfirmAccountImport(t.Context(), "a", "init", p.Digest, false, data)
	require.NoError(t, err)
	basisEntry(t, f, "manual-postflow", "2026-09-01", "cash_flow", `"20"`, `"120"`)
	basisEntry(t, f, "manual-cash", "2026-09-02", "cash_flow", `"5"`, "null")
	saved := sampleValuation(t, f.store, "a", Handler{})
	recordID := "valuation-" + saved.HistoryID
	b := basisRead(t, f, "a", "", "")
	require.Len(t, b.Points, 8)
	require.Equal(t, Money(10000), *b.Closing.Assets)
	var canonical int
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records WHERE account_id='a'`).Scan(&canonical))
	require.Equal(t, 8, canonical)
	for _, name := range []string{"valuation_history", "valuation_basis", "account_record_revisions", "operation_revisions", "account_imports", "imported_account_records"} {
		var exists int
		require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name=?)`, name).Scan(&exists))
		require.Zero(t, exists)
	}
	before := auditCount(t, f.store)
	again, err := f.store.ConfirmAccountImport(t.Context(), "a", "init", p.Digest, false, data)
	require.NoError(t, err)
	require.Equal(t, original, again)
	require.Equal(t, before, auditCount(t, f.store))
	f.request(t, "GET", "/audit?account_id=a&limit=30", "", "", 200)
	f.request(t, "GET", "/audit/1", "", "", 200)
	f.request(t, "HEAD", "/accounts/a/valuation", "", "", 200)
	require.Equal(t, before, auditCount(t, f.store))
	f.request(t, "PUT", "/audit/1", "x", `{}`, 405)
	f.request(t, "PUT", "/accounts/a/records/manual-cash", "correct-flow", `{"expected_version":"1","reason":"synthetic","entry":{"kind":"cash_flow","date":"2026-09-02","flow":"6"}}`, 200)
	require.Equal(t, "100.00", f.get(t, "/accounts/a")["cash"])
	f.request(t, "PUT", "/accounts/a/records/"+recordID, "correct-auto", `{"expected_version":"1","reason":"synthetic correction","entry":{"kind":"asset","date":"2026-09-06","total_assets":"106"}}`, 200)
	current := f.get(t, "/accounts/a/records/"+recordID)
	require.Equal(t, true, current["manual_assertion"])
	require.Nil(t, current["flow"])
	f.request(t, "PUT", "/accounts/a/records/"+recordID, "auto-not-flow", `{"expected_version":"2","reason":"synthetic","entry":{"kind":"cash_flow","date":"2026-09-06","flow":"1"}}`, 422)
	f.request(t, "DELETE", "/accounts/a/records/"+recordID, "void-auto", `{"expected_version":"2","reason":"synthetic"}`, 200)
	historyID, err := positiveInteger(saved.HistoryID)
	require.NoError(t, err)
	history, err := f.store.ValuationHistory(t.Context(), "a", historyID)
	require.NoError(t, err)
	require.Equal(t, Money(10000), *history.Valuation.TotalAssets)
	f.request(t, "GET", "/accounts/a/analysis-basis?track=reported", "", "", 400)
}

func TestUnifiedAtomicFailureAndInitializationEmpty(t *testing.T) {
	for _, target := range []string{"account_records", "audit_log", "idempotency_receipts"} {
		t.Run(target, func(t *testing.T) {
			f := reportedFixture(t)
			before := auditCount(t, f.store)
			_, err := f.store.db.ExecContext(t.Context(), `CREATE TRIGGER fail_unified BEFORE INSERT ON `+target+` BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
			require.NoError(t, err)
			f.request(t, "POST", "/accounts/a/records", "fail", `{"id":"manual-fail","entry":{"kind":"asset","date":"2020-01-01","total_assets":"1"}}`, 500)
			require.Equal(t, before, auditCount(t, f.store))
			var n int
			require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&n))
			require.Zero(t, n)
		})
	}
	f := reportedFixture(t)
	basisEntry(t, f, "manual-first", "2020-01-01", "asset", "null", `"1"`)
	data := syntheticImport(t)
	p := parseSynthetic(t, data)
	before := auditCount(t, f.store)
	_, err := f.store.ConfirmAccountImport(t.Context(), "a", "late", p.Digest, false, data)
	require.Error(t, err)
	require.Equal(t, before, auditCount(t, f.store))
}

func TestUnifiedCanonicalAuditIntegrity(t *testing.T) {
	f := reportedFixture(t)
	basisEntry(t, f, "manual-a", "2020-01-01", "asset", "null", `"10"`)
	for _, statement := range []string{`DELETE FROM audit_log`, `UPDATE audit_log SET action='changed'`, `DELETE FROM account_records`} {
		_, err := f.store.db.ExecContext(t.Context(), statement)
		require.Error(t, err)
	}
	var adapters int
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_schema WHERE type='trigger' AND sql LIKE '%INSTEAD OF%'`).Scan(&adapters))
	require.Zero(t, adapters)
	allowAuditCorruption(t, f.store.db)
	_, err := f.store.db.ExecContext(t.Context(), `DELETE FROM idempotency_receipts; DELETE FROM audit_log WHERE entity_type='account_record'`)
	require.NoError(t, err)
	f.request(t, "GET", "/accounts/a/records", "", "", 500)
	f.request(t, "GET", "/accounts/a/analysis-basis", "", "", 500)
}

func TestUnifiedAccountIsolationAndConcurrentInitialization(t *testing.T) {
	f := newHTTPFixture(t)
	for _, id := range []string{"a", "b", "c"} {
		f.account(t, id, "CNY", "100.00", nil)
	}
	create := `{"id":"manual-flow","entry":{"kind":"cash_flow","date":"2026-01-02","flow":"-10"}}`
	first := f.request(t, "POST", "/accounts/a/records", "first", create, 201).Body.String()
	before := auditCount(t, f.store)
	f.request(t, "POST", "/accounts/a/records", "first", create, 201)
	require.Equal(t, before, auditCount(t, f.store))
	f.request(t, "PUT", "/accounts/a/records/manual-flow", "replace", `{"expected_version":"1","reason":"correct","entry":{"kind":"cash_flow","date":"2026-01-02","flow":"-20"}}`, 200)
	require.Equal(t, "-20.00", basisRead(t, f, "a", "", "").NetFlow)
	require.Equal(t, "0.00", basisRead(t, f, "b", "", "").NetFlow)
	require.Equal(t, "0.00", basisRead(t, f, "c", "", "").NetFlow)
	f.request(t, "PUT", "/accounts/a/records/manual-flow", "reverse-flow", `{"expected_version":"2","reason":"correct","entry":{"kind":"cash_flow","date":"2026-01-02","flow":"20"}}`, 200)
	require.Equal(t, "20.00", basisRead(t, f, "a", "", "").NetFlow)
	require.Equal(t, "0.00", basisRead(t, f, "c", "", "").NetFlow)
	f.request(t, "DELETE", "/accounts/a/records/manual-flow", "void", `{"expected_version":"3","reason":"synthetic"}`, 200)
	for _, id := range []string{"a", "b", "c"} {
		require.Equal(t, "0.00", basisRead(t, f, id, "", "").NetFlow)
	}
	require.Equal(t, first, f.request(t, "POST", "/accounts/a/records", "first", create, 201).Body.String())
	var n int
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&n))
	require.Equal(t, 1, n)
	// A concurrent manual write can win the empty-account race, but neither
	// transaction may observe an empty account after the other has committed.
	f = reportedFixture(t)
	data := syntheticImport(t)
	p := parseSynthetic(t, data)
	var wg sync.WaitGroup
	var importErr, manualErr error
	amount := Money(1)
	wg.Go(func() { _, importErr = f.store.ConfirmAccountImport(t.Context(), "a", "init", p.Digest, false, data) })
	wg.Go(func() {
		_, manualErr = f.store.WriteAccountRecord(t.Context(), "manual", AccountRecordCommand{Action: CreateOperation, AccountID: "a", ID: "manual-race", Entry: &AccountEntry{Kind: "asset", Date: "2020-01-01", TotalAssets: &amount}})
	})
	wg.Wait()
	require.NoError(t, manualErr)
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&n))
	if importErr == nil {
		require.Equal(t, 6, n)
	} else {
		require.Equal(t, 1, n)
	}
}

func TestUnifiedWeeklyFailureSuccessAndVoidedRetryAudit(t *testing.T) {
	w, clock := weeklyFixture(t, false)
	_, err := w.store.db.ExecContext(t.Context(), `CREATE TRIGGER synthetic_quote_failure BEFORE INSERT ON audit_log WHEN NEW.entity_type='valuation' BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
	require.NoError(t, err)
	require.NoError(t, w.Tick(t.Context()))
	var status string
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT json_extract(after_json,'$.status') FROM audit_log WHERE entity_type='weekly_job' AND account_id='a' ORDER BY id DESC LIMIT 1`).Scan(&status))
	require.Equal(t, "failed", status)
	var n int
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log WHERE entity_type='valuation'`).Scan(&n))
	require.Zero(t, n)
	_, err = w.store.db.ExecContext(t.Context(), `DROP TRIGGER synthetic_quote_failure`)
	require.NoError(t, err)
	clock.set("2026-09-12T08:05:00+08:00")
	require.NoError(t, w.Tick(t.Context()))
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log WHERE entity_type='weekly_job' AND action='succeeded'`).Scan(&n))
	require.Equal(t, 1, n)
	_, err = w.store.WriteAccountRecord(t.Context(), "void-weekly", AccountRecordCommand{Action: VoidOperation, AccountID: "a", ID: "valuation-1", ExpectedVersion: "1", Reason: "synthetic void"})
	require.NoError(t, err)
	before := auditCount(t, w.store)
	require.NoError(t, w.Tick(t.Context()))
	require.Equal(t, before, auditCount(t, w.store))
	var voided bool
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT voided FROM account_records WHERE id='valuation-1'`).Scan(&voided))
	require.True(t, voided)
	_, err = w.store.ValuationHistory(t.Context(), "a", 1)
	require.NoError(t, err)
}
