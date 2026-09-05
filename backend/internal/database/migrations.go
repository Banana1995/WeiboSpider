package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/mattn/go-sqlite3"
)

var (
	ErrMigration  = errors.New("migration history does not match application")
	migrationName = regexp.MustCompile(`^[0-9]{3}_[a-z0-9_]+\.sql$`)
)

func (db *DB) Migrate(ctx context.Context, files fs.FS) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Unlike Glob, ReadDir reports missing or unreadable migration directories.
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	var names []string
	var scripts [][]byte
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		if !migrationName.MatchString(name) || (len(names) > 0 && names[len(names)-1][:3] == name[:3]) {
			return fmt.Errorf("%w: invalid migration name %s", ErrMigration, name)
		}
		script, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("read migration: %w", err)
		}
		names = append(names, name)
		scripts = append(scripts, script)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migration connection: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); !errors.Is(closeErr, sql.ErrConnDone) {
			err = errors.Join(err, closeErr)
		}
		if err != nil {
			err = errors.Join(err, ctx.Err())
		}
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, checksum TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("create migration history: %w", err)
	}
	var applied int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&applied); err != nil {
		return fmt.Errorf("read migration count: %w", err)
	}
	if applied > len(names) {
		return ErrMigration
	}
	for i, name := range names {
		checksum := fmt.Sprintf("%x", sha256.Sum256(scripts[i]))
		if i < applied {
			var savedName, savedChecksum string
			if err := tx.QueryRowContext(ctx, "SELECT name, checksum FROM schema_migrations ORDER BY name LIMIT 1 OFFSET ?", i).
				Scan(&savedName, &savedChecksum); err != nil {
				return fmt.Errorf("read migration history: %w", err)
			}
			if savedName != name || savedChecksum != checksum {
				return fmt.Errorf("%w: %s", ErrMigration, name)
			}
			continue
		}
		if err := conn.Raw(func(raw any) error {
			driverConn := raw.(*sqlite3.SQLiteConn)
			// Keep authorization and execution under Raw's connection lock so
			// cancellation can roll back only after the authorizer is removed.
			driverConn.RegisterAuthorizer(func(action int, arg1, arg2, _ string) int {
				switch action {
				case sqlite3.SQLITE_TRANSACTION, sqlite3.SQLITE_ATTACH, sqlite3.SQLITE_DETACH:
					return sqlite3.SQLITE_DENY
				case sqlite3.SQLITE_INSERT, sqlite3.SQLITE_UPDATE, sqlite3.SQLITE_DELETE, sqlite3.SQLITE_DROP_TABLE,
					sqlite3.SQLITE_CREATE_TABLE, sqlite3.SQLITE_CREATE_TEMP_TABLE, sqlite3.SQLITE_CREATE_VIEW, sqlite3.SQLITE_CREATE_TEMP_VIEW:
					if strings.EqualFold(arg1, "schema_migrations") {
						return sqlite3.SQLITE_DENY
					}
				case sqlite3.SQLITE_ALTER_TABLE, sqlite3.SQLITE_CREATE_TRIGGER, sqlite3.SQLITE_CREATE_TEMP_TRIGGER:
					if strings.EqualFold(arg2, "schema_migrations") {
						return sqlite3.SQLITE_DENY
					}
				}
				return sqlite3.SQLITE_OK
			})
			defer driverConn.RegisterAuthorizer(nil)
			if err := ctx.Err(); err != nil {
				return err
			}
			_, err := driverConn.ExecContext(ctx, string(scripts[i]), nil)
			return err
		}); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(name, checksum) VALUES (?, ?)", name, checksum); err != nil {
			return fmt.Errorf("record migration: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}
