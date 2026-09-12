package ledger_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
	"github.com/Banana1995/WeiboSpider/backend/internal/modules/liquor"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func schemaStore(t *testing.T, db *database.DB) *ledger.Store {
	t.Helper()
	return ledger.NewStore(db, func() time.Time {
		return time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	})
}

func seedSchemaDB(t *testing.T, db *database.DB) {
	t.Helper()
	s := schemaStore(t, db)
	require.NoError(t, s.AddInstrument(t.Context(), ledger.Instrument{
		ID: "i", Market: "TEST", Code: "001", Name: "Synthetic", Currency: ledger.CNY,
	}))
	require.NoError(t, s.InitializeAccount(t.Context(), "Synthetic", ledger.Opening{
		AccountID: "a", Currency: ledger.CNY, Date: "2026-01-01", Cash: 10_000,
		Positions: []ledger.OpeningPosition{{InstrumentID: "i", Quantity: 1_000_000}},
	}))
	_, err := s.Write(t.Context(), ledger.Command{
		Action: ledger.CreateOperation,
		Key:    "deposit-key",
		Reason: "Synthetic",
		Operation: ledger.Operation{
			ID: "deposit", AccountID: "a", Date: "2026-01-02", Sequence: 1,
			Kind: ledger.Deposit, Amount: 100,
		},
	})
	require.NoError(t, err)
}

func TestSchemaOpenIdempotentAndIndependentLiquor(t *testing.T) {
	root := t.TempDir()
	liquorDB, err := database.Open(t.Context(), root, "liquor")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, liquorDB.Close()) })
	_, err = liquor.NewStore(t.Context(), liquorDB)
	require.NoError(t, err)

	var migrationHistory string
	for attempt := range 3 {
		db, err := ledger.Open(t.Context(), root)
		require.NoError(t, err)
		if attempt == 0 {
			seedSchemaDB(t, db)
		}
		var path, history string
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list() WHERE name='main'").Scan(&path))
		expectedPath, err := filepath.EvalSymlinks(filepath.Join(root, "ledger.db"))
		require.NoError(t, err)
		require.Equal(t, expectedPath, path)
		require.NoError(t, db.QueryRowContext(t.Context(),
			"SELECT group_concat(name || ':' || checksum) FROM (SELECT * FROM schema_migrations ORDER BY name)").Scan(&history))
		if attempt == 0 {
			migrationHistory = history
		}
		require.Equal(t, migrationHistory, history)
		require.Contains(t, history, "001_init.sql:")
		var count int
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM schema_migrations").Scan(&count))
		require.Equal(t, 2, count)
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM operations").Scan(&count))
		require.Equal(t, 1, count)
		duplicate, err := ledger.Open(t.Context(), root)
		if duplicate != nil {
			require.NoError(t, duplicate.Close())
		}
		require.Nil(t, duplicate)
		require.ErrorIs(t, err, database.ErrInUse)
		require.NoError(t, db.Close())
	}

	var count int
	require.NoError(t, liquorDB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_schema
		WHERE name IN ('accounts','instruments','opening_positions','operations','account_records','audit_log','idempotency_receipts','weekly_jobs')`).Scan(&count))
	require.Zero(t, count)
}

func TestSchemaFreshInstallShape(t *testing.T) {
	db, err := ledger.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	expected := []string{"accounts", "instruments", "opening_positions", "operations", "audit_log", "account_records", "idempotency_receipts", "weekly_jobs", "current_holdings", "schema_migrations", "manual_trades"}
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_schema
		WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&count))
	require.Equal(t, len(expected), count)
	for _, table := range expected {
		var exists int
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name=?)`, table).Scan(&exists))
		require.Equal(t, 1, exists, table)
	}
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_schema WHERE type='view'`).Scan(&count))
	require.Zero(t, count)

	legacy := []string{"operation_revisions", "write_receipts", "account_record_revisions", "account_record_receipts", "account_imports", "imported_account_records", "account_import_receipts", "valuation_history", "valuation_basis", "account_record_order", "effective_account_records", "analysis_changes"}
	for _, name := range legacy {
		var exists int
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name=?)`, name).Scan(&exists))
		require.Zero(t, exists, name)
	}
	for _, table := range expected[:8] {
		var strict int
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT strict FROM pragma_table_list WHERE name=?`, table).Scan(&strict))
		require.Equal(t, 1, strict, table)
	}
}

func TestSchemaCoreConstraintsAndAppendOnlyHistory(t *testing.T) {
	db, err := ledger.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	seedSchemaDB(t, db)

	for _, statement := range []string{
		`INSERT INTO accounts(id,name,currency,opening_date,opening_cash_minor,version,accounting_mode) VALUES('bad','Bad','EUR','2026-01-01',0,1,'holdings')`,
		`INSERT INTO opening_positions(account_id,instrument_id,quantity_micros) VALUES('missing','i',1)`,
		`INSERT INTO operations(id,kind,business_date,sequence,account_id,amount_minor,status,version,created_at,updated_at) VALUES('bad','deposit','2026-01-02',2,'a',0,'active',1,'now','now')`,
		`UPDATE accounts SET name='Changed' WHERE id='a'`,
		`DELETE FROM instruments WHERE id='i'`,
		`DELETE FROM operations WHERE id='deposit'`,
		`UPDATE audit_log SET after_json='{}'`,
		`DELETE FROM audit_log`,
		`UPDATE idempotency_receipts SET response_json='{}'`,
		`DELETE FROM idempotency_receipts`,
		`DELETE FROM account_records`,
	} {
		_, err := db.ExecContext(t.Context(), statement)
		require.Error(t, err, statement)
		var sqliteErr sqlite3.Error
		require.ErrorAs(t, err, &sqliteErr)
		require.Equal(t, sqlite3.ErrConstraint, sqliteErr.Code)
	}
}

func TestSchemaOpenMigrationFailureReleasesLease(t *testing.T) {
	root := t.TempDir()
	db, err := ledger.Open(t.Context(), root)
	require.NoError(t, err)
	var checksum string
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT checksum FROM schema_migrations WHERE name='001_init.sql'").Scan(&checksum))
	_, err = db.ExecContext(t.Context(), "UPDATE schema_migrations SET checksum='tampered-test-checksum' WHERE name='001_init.sql'")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	for range 2 {
		failed, err := ledger.Open(t.Context(), root)
		if failed != nil {
			require.NoError(t, failed.Close())
		}
		require.Nil(t, failed)
		require.ErrorIs(t, err, database.ErrMigration)
	}
	repair, err := database.Open(t.Context(), root, "ledger")
	require.NoError(t, err)
	_, err = repair.ExecContext(t.Context(), "UPDATE schema_migrations SET checksum=? WHERE name='001_init.sql'", checksum)
	require.NoError(t, err)
	require.NoError(t, repair.Close())
	reopened, err := ledger.Open(t.Context(), root)
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
}

func TestSchemaOpenCanceledContextDoesNotCreateDatabase(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	root := filepath.Join(t.TempDir(), "not-created")
	db, err := ledger.Open(ctx, root)
	if db != nil {
		require.NoError(t, db.Close())
	}
	require.Nil(t, db)
	require.ErrorIs(t, err, context.Canceled)
	_, err = os.Stat(root)
	require.ErrorIs(t, err, os.ErrNotExist)
}
