package database_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func openDB(t *testing.T, name string) *database.DB {
	t.Helper()
	db, err := database.Open(t.Context(), t.TempDir(), name)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func TestOpen_ConfiguresEveryConnection(t *testing.T) {
	// Given
	db := openDB(t, "example")
	db.SetMaxIdleConns(0)
	for range 2 {
		// When
		var journal string
		var foreignKeys, timeout, synchronous int
		err := db.QueryRowContext(t.Context(), `SELECT journal_mode, foreign_keys, timeout, synchronous
			FROM pragma_journal_mode(), pragma_foreign_keys(), pragma_busy_timeout(), pragma_synchronous()`).
			Scan(&journal, &foreignKeys, &timeout, &synchronous)
		// Then
		require.NoError(t, err)
		require.Equal(t, "wal", journal)
		require.Equal(t, 1, foreignKeys)
		require.Equal(t, 5000, timeout)
		require.Equal(t, 2, synchronous)
	}
}

func TestOpen_IsolatesModulesAndRejectsDuplicateOwner(t *testing.T) {
	// Given
	root := t.TempDir()
	one, err := database.Open(t.Context(), root, "one")
	require.NoError(t, err)
	defer func() { require.NoError(t, one.Close()) }()
	// When
	two, err := database.Open(t.Context(), root, "two")
	require.NoError(t, err)
	defer func() { require.NoError(t, two.Close()) }()
	_, duplicateErr := database.Open(t.Context(), root, "one")
	// Then
	require.ErrorIs(t, duplicateErr, database.ErrInUse)
	_, err = one.ExecContext(t.Context(), "CREATE TABLE private_data (id INTEGER)")
	require.NoError(t, err)
	var count int
	require.NoError(t, two.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE name='private_data'").Scan(&count))
	require.Zero(t, count)
}

func TestOpen_RejectsUnsafeModuleNames(t *testing.T) {
	for _, name := range []string{"../data", "", "one/two", "file:other", "x?mode=ro"} {
		t.Run(name, func(t *testing.T) {
			// Given / When
			_, err := database.Open(t.Context(), t.TempDir(), name)
			// Then
			require.ErrorIs(t, err, database.ErrName)
		})
	}
}

func TestOpen_CanceledContextDoesNotCreateFiles(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	root := filepath.Join(t.TempDir(), "missing")
	db, err := database.Open(ctx, root, "example")
	if db != nil {
		defer db.Close()
	}
	require.ErrorIs(t, err, context.Canceled)
	_, err = os.Stat(root)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestOpen_ReleasesLeaseAfterFailure(t *testing.T) {
	for _, kind := range []string{"directory", "corrupt database"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "example.db")
			if kind == "directory" {
				require.NoError(t, os.Mkdir(path, 0o700))
			} else {
				require.NoError(t, os.WriteFile(path, []byte("not a sqlite database"), 0o600))
			}
			_, err := database.Open(t.Context(), root, "example")
			require.Error(t, err)
			require.NoError(t, os.Remove(path))
			db, err := database.Open(t.Context(), root, "example")
			require.NoError(t, err)
			require.NoError(t, db.Close())
		})
	}
}

func TestOpen_EscapesDirectoryAndLocksAliases(t *testing.T) {
	root := filepath.Join(t.TempDir(), "directory ?#& with spaces")
	db, err := database.Open(t.Context(), root, "example")
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	_, err = db.ExecContext(t.Context(), "CREATE TABLE entries(id INTEGER)")
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "example.db"))
	require.NoError(t, err)
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(root, alias))
	other, err := database.Open(t.Context(), alias, "example")
	if other != nil {
		defer other.Close()
	}
	require.ErrorIs(t, err, database.ErrInUse)
}

func TestOpen_RejectsFileAliasesWithoutChangingTarget(t *testing.T) {
	for _, kind := range []string{"database symlink", "lease symlink", "database hard link"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "other.db")
			// An empty SQLite file is valid and would otherwise be initialized by Open.
			require.NoError(t, os.WriteFile(target, nil, 0o600))
			alias := filepath.Join(root, "example.db")
			if kind == "lease symlink" {
				alias += ".lock"
			}
			if kind == "database hard link" {
				require.NoError(t, os.Link(target, alias))
			} else {
				require.NoError(t, os.Symlink(target, alias))
			}
			db, err := database.Open(t.Context(), root, "example")
			if db != nil {
				defer db.Close()
			}
			require.Error(t, err)
			contents, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Empty(t, contents)
			require.NoError(t, os.Remove(alias))
			db, err = database.Open(t.Context(), root, "example")
			require.NoError(t, err)
			require.NoError(t, db.Close())
		})
	}
}

func TestOpen_LeaseAcrossProcesses(t *testing.T) {
	if root := os.Getenv("DATABASE_LEASE_TEST_DIRECTORY"); root != "" {
		db, err := database.Open(t.Context(), root, "example")
		if db != nil {
			defer db.Close()
		}
		require.ErrorIs(t, err, database.ErrInUse)
		return
	}
	root := t.TempDir()
	db, err := database.Open(t.Context(), root, "example")
	require.NoError(t, err)
	defer db.Close()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestOpen_LeaseAcrossProcesses$")
	cmd.Env = append(os.Environ(), "DATABASE_LEASE_TEST_DIRECTORY="+root)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.NoError(t, db.Close())
	// The lock inode must remain in place; deleting it permits competing leases.
	_, err = os.Stat(filepath.Join(root, "example.db.lock"))
	require.NoError(t, err)
	db, err = database.Open(t.Context(), root, "example")
	require.NoError(t, err)
	require.NoError(t, db.Close())
}

func TestClose_WaitsForTransactionBeforeReleasingLease(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(t.Context(), root, "example")
	require.NoError(t, err)
	defer db.Close()
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	closed := make(chan error, 2)
	for range 2 {
		go func() { closed <- db.Close() }()
	}
	require.Eventually(t, func() bool {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()
		err := db.PingContext(ctx)
		return err != nil && !errors.Is(err, context.DeadlineExceeded)
	}, time.Second, time.Millisecond)
	other, leaseErr := database.Open(t.Context(), root, "example")
	if other != nil {
		defer other.Close()
	}
	require.ErrorIs(t, leaseErr, database.ErrInUse)
	select {
	case err := <-closed:
		t.Fatalf("Close returned with a transaction still active: %v", err)
	default:
	}
	require.NoError(t, tx.Rollback())
	for range 2 {
		select {
		case err := <-closed:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("Close did not finish after rollback")
		}
	}
	require.NoError(t, db.Close())
	other, err = database.Open(t.Context(), root, "example")
	require.NoError(t, err)
	defer func() { require.NoError(t, other.Close()) }()
	require.NoError(t, db.Close())
	_, err = database.Open(t.Context(), root, "example")
	require.ErrorIs(t, err, database.ErrInUse)
}

func TestWithTx_RollsBackFailedOperation(t *testing.T) {
	// Given
	db := openDB(t, "example")
	_, err := db.ExecContext(t.Context(), "CREATE TABLE entries (id INTEGER)")
	require.NoError(t, err)
	failure := errors.New("operation failed")
	// When
	err = db.WithTx(t.Context(), func(tx *sql.Tx) error {
		if _, writeErr := tx.ExecContext(t.Context(), "INSERT INTO entries VALUES (1)"); writeErr != nil {
			return writeErr
		}
		return failure
	})
	// Then
	require.ErrorIs(t, err, failure)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entries").Scan(&count))
	require.Zero(t, count)
}

func TestWithTx_RollsBackPanicAndCancellation(t *testing.T) {
	for _, kind := range []string{"panic", "cancellation"} {
		t.Run(kind, func(t *testing.T) {
			db := openDB(t, "example")
			_, err := db.ExecContext(t.Context(), "CREATE TABLE entries(id INTEGER)")
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			operation := func(tx *sql.Tx) error {
				if _, err := tx.ExecContext(ctx, "INSERT INTO entries VALUES(1)"); err != nil {
					return err
				}
				if kind == "panic" {
					panic("failed operation")
				}
				cancel()
				require.Eventually(t, func() bool {
					_, err := tx.ExecContext(t.Context(), "SELECT 1")
					return errors.Is(err, sql.ErrTxDone)
				}, time.Second, time.Millisecond)
				return nil
			}
			if kind == "panic" {
				require.PanicsWithValue(t, "failed operation", func() { _ = db.WithTx(ctx, operation) })
			} else {
				require.ErrorIs(t, db.WithTx(ctx, operation), context.Canceled)
			}
			var count int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entries").Scan(&count))
			require.Zero(t, count)
		})
	}
}

func TestWithTx_CommitFailureRollsBackAndConnectionRemainsUsable(t *testing.T) {
	db := openDB(t, "example")
	_, err := db.ExecContext(t.Context(), `CREATE TABLE parents(id INTEGER PRIMARY KEY);
		CREATE TABLE children(parent_id INTEGER REFERENCES parents(id) DEFERRABLE INITIALLY DEFERRED);`)
	require.NoError(t, err)
	err = db.WithTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "INSERT INTO children VALUES(1)")
		return err
	})
	require.Error(t, err)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM children").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.WithTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "INSERT INTO parents VALUES(1); INSERT INTO children VALUES(1)")
		return err
	}))
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM children").Scan(&count))
	require.Equal(t, 1, count)
}

func TestMigrate_IsIdempotentAndDetectsChangedHistory(t *testing.T) {
	// Given
	db := openDB(t, "example")
	migrations := fstest.MapFS{"001_init.sql": {Data: []byte("CREATE TABLE entries (id INTEGER);")}}
	require.NoError(t, db.Migrate(t.Context(), migrations))
	// When / Then
	require.NoError(t, db.Migrate(t.Context(), migrations))
	migrations["001_init.sql"].Data = []byte("CREATE TABLE changed (id INTEGER);")
	require.ErrorIs(t, db.Migrate(t.Context(), migrations), database.ErrMigration)
}

func TestMigrate_RollsBackInvalidBatch(t *testing.T) {
	// Given
	db := openDB(t, "example")
	migrations := fstest.MapFS{
		"001_init.sql": {Data: []byte("CREATE TABLE entries (id INTEGER);")},
		"002_bad.sql":  {Data: []byte("THIS IS NOT SQL;")},
	}
	// When
	err := db.Migrate(t.Context(), migrations)
	// Then
	require.Error(t, err)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE name IN ('entries', 'schema_migrations')").Scan(&count))
	require.Zero(t, count)
}

func TestMigrate_RejectsUnreadableDirectory(t *testing.T) {
	db := openDB(t, "example")
	err := db.Migrate(t.Context(), os.DirFS(filepath.Join(t.TempDir(), "missing")))
	require.ErrorIs(t, err, os.ErrNotExist)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE name='schema_migrations'").Scan(&count))
	require.Zero(t, count)
}

func TestMigrate_RequiresExactHistoryPrefix(t *testing.T) {
	for _, change := range []string{"removed", "renamed", "prepended", "duplicate version", "invalid name"} {
		t.Run(change, func(t *testing.T) {
			db := openDB(t, "example")
			migrations := fstest.MapFS{"002_init.sql": {Data: []byte("CREATE TABLE entries(id INTEGER);")}}
			require.NoError(t, db.Migrate(t.Context(), migrations))
			switch change {
			case "removed":
				delete(migrations, "002_init.sql")
			case "renamed":
				migrations["002_renamed.sql"] = migrations["002_init.sql"]
				delete(migrations, "002_init.sql")
			case "prepended":
				migrations["001_earlier.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
			case "duplicate version":
				migrations["002_duplicate.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
			case "invalid name":
				migrations["3_bad.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
			}
			require.ErrorIs(t, db.Migrate(t.Context(), migrations), database.ErrMigration)
			var count int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM schema_migrations").Scan(&count))
			require.Equal(t, 1, count)
		})
	}
}

func TestMigrate_FailedUpgradePreservesHistoryAndCanRetry(t *testing.T) {
	db := openDB(t, "example")
	migrations := fstest.MapFS{"001_init.sql": {Data: []byte("CREATE TABLE entries(id INTEGER); INSERT INTO entries VALUES(1);")}}
	require.NoError(t, db.Migrate(t.Context(), migrations))
	migrations["002_more.sql"] = &fstest.MapFile{Data: []byte("ALTER TABLE entries ADD COLUMN label TEXT; INSERT INTO entries(id) VALUES(2);")}
	migrations["003_bad.sql"] = &fstest.MapFile{Data: []byte("THIS IS NOT SQL;")}
	require.Error(t, db.Migrate(t.Context(), migrations))
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM schema_migrations").Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entries").Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM pragma_table_info('entries') WHERE name='label'").Scan(&count))
	require.Zero(t, count)
	migrations["003_bad.sql"].Data = []byte("UPDATE entries SET label='ready';")
	require.NoError(t, db.Migrate(t.Context(), migrations))
	require.NoError(t, db.Migrate(t.Context(), migrations))
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM schema_migrations").Scan(&count))
	require.Equal(t, 3, count)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entries WHERE label='ready'").Scan(&count))
	require.Equal(t, 2, count)
}

func TestMigrate_ScriptsCannotEscapeTransaction(t *testing.T) {
	for _, statement := range []string{"COMMIT", "END", "ROLLBACK"} {
		t.Run(statement, func(t *testing.T) {
			db := openDB(t, "example")
			migrations := fstest.MapFS{"001_init.sql": {Data: []byte("CREATE TABLE entries(id INTEGER); " + statement + "; CREATE TABLE escaped(id INTEGER); INVALID SQL;")}}
			require.Error(t, db.Migrate(t.Context(), migrations))
			var count int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE name IN ('entries', 'escaped', 'schema_migrations')").Scan(&count))
			require.Zero(t, count)
			// Authorization must be removed before rollback and connection reuse.
			migrations["001_init.sql"].Data = []byte("CREATE TABLE entries(value TEXT); INSERT INTO entries VALUES('COMMIT; ROLLBACK;');")
			require.NoError(t, db.Migrate(t.Context(), migrations))
		})
	}
}

func TestMigrate_CancellationRollsBackAndReleasesConnection(t *testing.T) {
	db := openDB(t, "example")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	require.NoError(t, conn.Raw(func(driverConn any) error {
		return driverConn.(*sqlite3.SQLiteConn).RegisterFunc("cancel_migration", func() int {
			cancel()
			return 1
		}, false)
	}))
	require.NoError(t, conn.Close())
	migrations := fstest.MapFS{"001_init.sql": {Data: []byte("CREATE TABLE entries(id INTEGER); SELECT cancel_migration(); INSERT INTO entries VALUES(1);")}}
	require.ErrorIs(t, db.Migrate(ctx, migrations), context.Canceled)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE name IN ('entries', 'schema_migrations')").Scan(&count))
	require.Zero(t, count)
	migrations["001_init.sql"].Data = []byte("CREATE TABLE entries(id INTEGER);")
	require.NoError(t, db.Migrate(t.Context(), migrations))
}

func TestMigrate_ScriptsCannotRewriteHistoryOrAttachDatabases(t *testing.T) {
	for _, statement := range []string{
		"DELETE FROM schema_migrations",
		"UPDATE schema_migrations SET checksum='changed'",
		"INSERT INTO schema_migrations VALUES('999_fake.sql', 'fake')",
		"DROP TABLE schema_migrations",
		"ALTER TABLE schema_migrations RENAME TO lost_history",
		"CREATE TEMP TABLE schema_migrations(name TEXT PRIMARY KEY, checksum TEXT NOT NULL)",
		"CREATE TEMP VIEW schema_migrations AS SELECT 'fake' AS name, 'fake' AS checksum",
		"CREATE TRIGGER corrupt_history AFTER INSERT ON schema_migrations BEGIN DELETE FROM schema_migrations; END",
		"ATTACH DATABASE ':memory:' AS other",
	} {
		t.Run(statement, func(t *testing.T) {
			db := openDB(t, "example")
			migrations := fstest.MapFS{"001_init.sql": {Data: []byte("CREATE TABLE entries(id INTEGER);")}}
			require.NoError(t, db.Migrate(t.Context(), migrations))
			migrations["002_bad.sql"] = &fstest.MapFile{Data: []byte("INSERT INTO entries VALUES(1); " + statement + ";")}
			require.Error(t, db.Migrate(t.Context(), migrations))
			delete(migrations, "002_bad.sql")
			require.NoError(t, db.Migrate(t.Context(), migrations))
			var count int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entries").Scan(&count))
			require.Zero(t, count)
		})
	}
}

func TestBackup_CapturesWALDataAndDoesNotOverwrite(t *testing.T) {
	// Given
	db := openDB(t, "example")
	_, err := db.ExecContext(t.Context(), "CREATE TABLE entries(id INTEGER); INSERT INTO entries VALUES(42);")
	require.NoError(t, err)
	var source string
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list() WHERE name='main'").Scan(&source))
	wal, err := os.Stat(source + "-wal")
	require.NoError(t, err)
	require.Positive(t, wal.Size())
	destination := filepath.Join(t.TempDir(), "snapshot.db")
	// When
	require.NoError(t, db.Backup(t.Context(), destination))
	// Then
	copyDB, err := sql.Open("sqlite3", destination)
	require.NoError(t, err)
	defer func() { require.NoError(t, copyDB.Close()) }()
	var id int
	require.NoError(t, copyDB.QueryRowContext(context.Background(), "SELECT id FROM entries").Scan(&id))
	require.Equal(t, 42, id)
	var integrity string
	require.NoError(t, copyDB.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity))
	require.Equal(t, "ok", integrity)
	before, err := os.ReadFile(destination)
	require.NoError(t, err)
	require.ErrorIs(t, db.Backup(t.Context(), destination), os.ErrExist)
	after, err := os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, before, after)
	info, err := os.Stat(destination)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestBackup_CancellationDoesNotPublishOrLeaveTemporaryFiles(t *testing.T) {
	db := openDB(t, "example")
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	defer conn.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	root := t.TempDir()
	destination := filepath.Join(root, "snapshot.db")
	waiting := db.Stats().WaitCount
	finished := make(chan error, 1)
	go func() { finished <- db.Backup(ctx, destination) }()
	require.Eventually(t, func() bool { return db.Stats().WaitCount > waiting }, time.Second, time.Millisecond)
	_, publishedErr := os.Lstat(destination)
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Backup did not stop while waiting for a connection")
	}
	require.ErrorIs(t, publishedErr, os.ErrNotExist)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestBackup_FailureCleansUpAndConcurrentBackupsDoNotOverwrite(t *testing.T) {
	db := openDB(t, "example")
	root := t.TempDir()
	destination := filepath.Join(root, "snapshot.db")
	_, err := db.ExecContext(t.Context(), "CREATE TABLE entries(id INTEGER); INSERT INTO entries VALUES(42)")
	require.NoError(t, err)
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	defer conn.Close()
	waiting := db.Stats().WaitCount
	finished := make(chan error, 2)
	for range 2 {
		go func() { finished <- db.Backup(t.Context(), destination) }()
	}
	// Both calls must get past the existence check before either can publish.
	require.Eventually(t, func() bool { return db.Stats().WaitCount >= waiting+2 }, time.Second, time.Millisecond)
	require.NoError(t, conn.Close())
	one, two := <-finished, <-finished
	if one != nil {
		one, two = two, one
	}
	require.NoError(t, one)
	require.ErrorIs(t, two, os.ErrExist)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "snapshot.db", entries[0].Name())
	require.NoError(t, db.Close())
	require.Error(t, db.Backup(t.Context(), filepath.Join(root, "failed.db")))
	entries, err = os.ReadDir(root)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestBackup_RejectsExistingEmptyFileDirectoryAndDanglingSymlink(t *testing.T) {
	db := openDB(t, "example")
	for _, kind := range []string{"empty file", "directory", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshot.db")
			switch kind {
			case "empty file":
				require.NoError(t, os.WriteFile(path, nil, 0o600))
			case "directory":
				require.NoError(t, os.Mkdir(path, 0o700))
			case "dangling symlink":
				require.NoError(t, os.Symlink("missing.db", path))
			}
			before, err := os.Lstat(path)
			require.NoError(t, err)
			require.ErrorIs(t, db.Backup(t.Context(), path), os.ErrExist)
			after, err := os.Lstat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
		})
	}
}
