package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var (
	ErrName    = errors.New("invalid database module name")
	ErrInUse   = errors.New("database already owned by another service")
	moduleName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

type DB struct {
	*sql.DB
	lease     *os.File
	closeOnce sync.Once
	closeErr  error
}

func Open(ctx context.Context, directory, module string) (*DB, error) {
	if !moduleName.MatchString(module) {
		return nil, ErrName
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("database directory: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	path := filepath.Join(root, module+".db")
	lease, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open database lease: %w", err)
	}
	if err := syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			err = errors.Join(ErrInUse, err)
		}
		return nil, errors.Join(fmt.Errorf("lock database lease: %w", err), lease.Close())
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create database: %w", err), lease.Close())
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Nlink != 1) {
		// Hard-linked database names would use different leases and WAL files.
		err = errors.New("database must be a regular file with a single link")
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return nil, errors.Join(err, lease.Close())
	}
	dsn := url.URL{Scheme: "file", Path: path}
	params := url.Values{
		"_foreign_keys": {"on"}, "_busy_timeout": {"5000"},
		"_journal_mode": {"WAL"}, "_synchronous": {"FULL"}, "_txlock": {"immediate"},
	}
	dsn.RawQuery = params.Encode()
	pool, err := sql.Open("sqlite3", dsn.String())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open sqlite: %w", err), lease.Close())
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	db := &DB{DB: pool, lease: lease}
	if err := errors.Join(pool.PingContext(ctx), ctx.Err()); err != nil {
		return nil, errors.Join(fmt.Errorf("connect sqlite: %w", err), db.Close())
	}
	return db, nil
}

// Close waits for checked-out connections, rows, and transactions to be released.
func (db *DB) Close() error {
	db.closeOnce.Do(func() {
		err := db.DB.Close()
		// sql.DB.Close does not wait for checked-out connections or transactions.
		// Keep the lease until they drain, and never unlink its shared lock inode.
		for db.DB.Stats().OpenConnections > 0 {
			time.Sleep(time.Millisecond)
		}
		db.closeErr = errors.Join(err, db.lease.Close())
	})
	return db.closeErr
}

// WithTx owns the transaction; operation must not commit or roll it back.
func (db *DB) WithTx(ctx context.Context, operation func(*sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", errors.Join(err, ctx.Err()))
	}
	defer func() {
		rollbackErr := tx.Rollback()
		if !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	if err := operation(tx); err != nil {
		return fmt.Errorf("transaction operation: %w", errors.Join(err, ctx.Err()))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", errors.Join(err, ctx.Err()))
	}
	return nil
}
