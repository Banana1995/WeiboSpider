package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/stretchr/testify/require"
)

func TestAccountDeleteHTTP(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "10.00", nil)
	f.account(t, "b", "USD", "20.00", nil)
	for _, tc := range []struct {
		path, key, body string
		status          int
	}{
		{"/accounts/a", "delete", `null`, 400},
		{"/accounts/a", "delete", `[]`, 400},
		{"/accounts/a", "delete", `{} {}`, 400},
		{"/accounts/a", "delete", `{"reason":"test"}`, 400},
		{"/accounts/a", "", `{}`, 400},
		{"/accounts/a", "bad key", `{}`, 400},
		{"/accounts/a?x=1", "delete", `{}`, 400},
		{"/accounts/a?", "delete", `{}`, 400},
		{"/accounts/bad.id", "delete", `{}`, 400},
		{"/accounts/missing", "delete", `{}`, 404},
		{"/accounts/a", "create-a", `{}`, 409},
		{"/accounts/a", "delete", "", 415},
		{"/accounts/a", "delete", strings.Repeat(" ", maxLedgerBody) + `{}`, 413},
	} {
		f.request(t, "DELETE", tc.path, tc.key, tc.body, tc.status)
	}
	r := httptest.NewRequest("DELETE", httpTestPrefix+"/accounts/a", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Add("Idempotency-Key", "one")
	r.Header.Add("Idempotency-Key", "two")
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	require.Equal(t, 400, w.Code)
	first := f.request(t, "DELETE", "/accounts/a", "delete", `{}`, 200).Body.String()
	require.JSONEq(t, `{"account_id":"a","deleted":true}`, first)
	require.Equal(t, first, f.request(t, "DELETE", "/accounts/a", "delete", ` { } `, 200).Body.String())
	f.request(t, "DELETE", "/accounts/a", "another-delete", `{}`, 404)
	f.request(t, "DELETE", "/accounts/b", "delete", `{}`, 409)
	for _, suffix := range []string{"", "/current-holdings", "/records", "/import-summary", "/imported-records", "/weekly-jobs", "/valuations", "/valuation"} {
		f.request(t, "GET", "/accounts/a"+suffix, "", "", 404)
	}
	f.request(t, "HEAD", "/accounts/a", "", "", 404)
	require.Len(t, httpItems(t, f.get(t, "/accounts")), 1)
	f.get(t, "/accounts/b")
	for _, key := range []string{"create-a", "new-create"} {
		status := 409
		if key == "create-a" {
			status = 404
		}
		f.request(t, "POST", "/accounts", key, `{"id":"a","name":"Synthetic a","currency":"CNY","opening_date":"2026-01-01"}`, status)
	}
	f.request(t, "PUT", "/accounts/a/current-holdings", "initial-a", `{"expected_version":"0","cash":"10.00","positions":[]}`, 404)
	f.request(t, "PUT", "/accounts/a/current-holdings", "new-holdings", `{"expected_version":"0","cash":"10.00","positions":[]}`, 404)
	require.NotEmpty(t, httpItems(t, f.get(t, "/audit?account_id=a&entity_type=account_delete")))
}

func TestAccountDeleteCleanupAndReplays(t *testing.T) {
	w, _ := weeklyFixture(t, false)
	s := w.store
	data := syntheticImport(t)
	p := parseSynthetic(t, data)
	_, err := s.ConfirmAccountImport(t.Context(), "a", "import-a", p.Digest, false, data)
	require.NoError(t, err)
	c := AccountRecordCommand{Action: CreateOperation, AccountID: "a", ID: "manual-note", Entry: &AccountEntry{Kind: "log", Date: "2026-09-01", Note: "Synthetic"}}
	_, err = s.WriteAccountRecord(t.Context(), "record", c)
	require.NoError(t, err)
	require.NoError(t, w.Tick(t.Context()))
	job := weeklyJobFor(t, w, "a")
	require.Equal(t, "succeeded", job.Status)
	before := (&httpFixture{store: s}).snapshot(t)
	_, err = s.DeleteAccount(t.Context(), "a", "delete")
	require.NoError(t, err)
	for _, table := range []string{"accounts", "account_records", "current_holdings", "weekly_jobs"} {
		var count int
		require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&count))
		require.Zero(t, count, table)
	}
	after := (&httpFixture{store: s}).snapshot(t)
	require.Equal(t, before["audit_log"], after["audit_log"][:len(before["audit_log"])])
	// Keys and exact historical financial audit evidence survive deletion.
	require.Len(t, after["audit_log"], len(before["audit_log"])+1)
	for _, row := range before["idempotency_receipts"] {
		require.Contains(t, after["idempotency_receipts"], row)
	}
	_, err = s.WriteAccountRecord(t.Context(), "record", c)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.WriteAccountRecord(t.Context(), "new-record", c)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.ConfirmAccountImport(t.Context(), "a", "import-a", p.Digest, false, data)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.ConfirmAccountImport(t.Context(), "a", "new-import", p.Digest, true, data)
	require.ErrorIs(t, err, ErrConflict)
	_, err = s.ConfirmAccountImport(t.Context(), "a", "new-import", p.Digest, false, data)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO accounts VALUES('a','reuse','CNY','2026-01-01',0,1)`)
	require.Error(t, err)
	require.NoError(t, w.Tick(t.Context()))
	weeklyCount(t, w, 0)
	require.ErrorIs(t, w.completeCarry(t.Context(), job), errWeeklyFence)
	require.ErrorIs(t, w.complete(t.Context(), job, Valuation{}, nil), errWeeklyFence)
	require.NoError(t, w.fail(t.Context(), job, "timeout"))
}

func TestAccountDeleteRollback(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "1.00", nil)
	for _, trigger := range []string{
		`CREATE TRIGGER deletion_failure BEFORE DELETE ON accounts BEGIN SELECT RAISE(ABORT,'synthetic'); END`,
		`CREATE TRIGGER deletion_failure BEFORE INSERT ON idempotency_receipts WHEN NEW.kind='account_delete' BEGIN SELECT RAISE(ABORT,'synthetic'); END`,
	} {
		_, err := f.store.db.ExecContext(t.Context(), trigger)
		require.NoError(t, err)
		before := f.snapshot(t)
		_, err = f.store.DeleteAccount(t.Context(), "a", "delete")
		require.Error(t, err)
		require.Equal(t, before, f.snapshot(t))
		_, err = f.store.db.ExecContext(t.Context(), `DROP TRIGGER deletion_failure`)
		require.NoError(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := f.store.DeleteAccount(ctx, "a", "delete")
	require.ErrorIs(t, err, context.Canceled)
	_, err = f.store.DeleteAccount(t.Context(), "a", "delete")
	require.NoError(t, err)
}

func TestAccountDeleteConcurrentRetriesAndWeeklyFetch(t *testing.T) {
	w, _ := weeklyFixture(t, true)
	started, release := make(chan struct{}), make(chan struct{})
	original := w.quotes
	w.quotes = valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
		close(started)
		<-release
		return original.Fetch(ctx, is)
	})
	done := make(chan error, 1)
	go func() { done <- w.Tick(t.Context()) }()
	<-started
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			raw, err := w.store.DeleteAccount(t.Context(), "a", "delete")
			if err == nil && string(raw) != `{"account_id":"a","deleted":true}` {
				err = ErrCorrupt
			}
			errs <- err
		})
	}
	wg.Wait()
	close(release)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, <-done)
	weeklyCount(t, w, 0)
	historyCount(t, w.store, 0)
	var count int
	require.NoError(t, w.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log WHERE entity_type='account_delete'`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestAccountDeleteMigrationPopulatedAndReopen(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(t.Context(), root, "ledger")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	initial, err := migrations.ReadFile("migrations/001_init.sql")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(t.Context(), fstest.MapFS{"001_init.sql": &fstest.MapFile{Data: initial}}))
	clock := &weeklyClock{}
	clock.set("2026-09-12T08:00:00+08:00")
	f := &httpFixture{store: NewStore(db, clock.now), mux: http.NewServeMux()}
	Handler{Store: f.store}.Register(f.mux)
	f.account(t, "a", "CNY", "1.00", nil)
	data := syntheticImport(t)
	p := parseSynthetic(t, data)
	_, err = f.store.ConfirmAccountImport(t.Context(), "a", "import", p.Digest, false, data)
	require.NoError(t, err)
	worker, err := NewWeeklyWorker(f.store, nil, nil, WeeklyConfig{Enabled: true, Time: DefaultWeeklyTime}, nil)
	require.NoError(t, err)
	require.NoError(t, worker.Tick(t.Context()))
	require.Equal(t, "succeeded", weeklyJobFor(t, worker, "a").Status)
	before := f.snapshot(t)
	files, err := fs.Sub(migrations, "migrations")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(t.Context(), files))
	require.Equal(t, before, f.snapshot(t))
	_, err = f.store.ConfirmAccountImport(t.Context(), "a", "import", p.Digest, false, data)
	require.NoError(t, err)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&count))
	require.Zero(t, count)
	_, err = f.store.DeleteAccount(t.Context(), "a", "delete")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	reopened, err := Open(t.Context(), root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	raw, err := NewStore(reopened, nil).DeleteAccount(t.Context(), "a", "delete")
	require.NoError(t, err)
	var result AccountDeletion
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, AccountDeletion{"a", true}, result)
	require.NoError(t, reopened.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&count))
	require.Zero(t, count)
	_, err = reopened.ExecContext(t.Context(), `UPDATE audit_log SET action='changed'`)
	require.Error(t, err)
	_, err = reopened.ExecContext(t.Context(), `DELETE FROM idempotency_receipts`)
	require.Error(t, err)
}

func TestAccountDeleteCorruptReceipt(t *testing.T) {
	for _, statement := range []string{
		`UPDATE idempotency_receipts SET response_json='{"account_id":"b","deleted":true}' WHERE key='delete'`,
		`UPDATE idempotency_receipts SET response_json='{"account_id":"a","deleted":false}' WHERE key='delete'`,
		`UPDATE idempotency_receipts SET audit_id=(SELECT min(id) FROM audit_log) WHERE key='delete'`,
		`UPDATE idempotency_receipts SET request_hash=printf('%064d',0) WHERE key='delete'`,
	} {
		f := newHTTPFixture(t)
		f.account(t, "a", "CNY", "1.00", nil)
		_, err := f.store.DeleteAccount(t.Context(), "a", "delete")
		require.NoError(t, err)
		require.NoError(t, f.store.db.WithTx(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(t.Context(), `DROP TRIGGER idempotency_receipts_no_update; `+statement)
			return err
		}))
		_, err = f.store.DeleteAccount(t.Context(), "a", "delete")
		require.ErrorIs(t, err, ErrCorrupt)
	}
}

func TestAccountDeleteImportedCreationAndCarry(t *testing.T) {
	s := importStoreFixture(t)
	data := syntheticImport(t)
	p := parseSynthetic(t, data)
	_, err := s.ConfirmAccountImport(t.Context(), "imported", "import", p.Digest, true, data)
	require.NoError(t, err)
	clock := &weeklyClock{}
	clock.set("2026-09-12T08:00:00+08:00")
	s.now = clock.now
	w, err := NewWeeklyWorker(s, nil, nil, WeeklyConfig{Enabled: true, Time: DefaultWeeklyTime}, nil)
	require.NoError(t, err)
	require.NoError(t, w.Tick(t.Context()))
	job := weeklyJobFor(t, w, "imported")
	require.Equal(t, "succeeded", job.Status)
	require.Equal(t, "account_record_carry", job.Source)
	_, err = s.DeleteAccount(t.Context(), "imported", "delete")
	require.NoError(t, err)
	_, err = s.ConfirmAccountImport(t.Context(), "imported", "import", p.Digest, true, data)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.ConfirmAccountImport(t.Context(), "imported", "fresh", p.Digest, true, data)
	require.ErrorIs(t, err, ErrConflict)
	require.ErrorIs(t, w.completeCarry(t.Context(), job), errWeeklyFence)
	require.NoError(t, w.Tick(t.Context()))
	weeklyCount(t, w, 0)
	var count int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&count))
	require.Zero(t, count)
}
