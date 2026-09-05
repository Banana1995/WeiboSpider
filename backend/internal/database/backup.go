package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Backup publishes a synced snapshot without replacing any existing path.
// Errors after publication may leave a complete snapshot at the destination.
// Cancellation is checked before publication; filesystem sync is not interruptible.
func (db *DB) Backup(ctx context.Context, destination string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("backup path: %w", err)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("backup destination: %w", os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("backup destination: %w", err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open backup directory: %w", err)
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	file, err := os.CreateTemp(filepath.Dir(path), ".sqlite-backup-*")
	if err != nil {
		return fmt.Errorf("create temporary backup: %w", err)
	}
	temporary := file.Name()
	defer func() {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
		if removeErr := os.Remove(temporary); !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	// VACUUM INTO includes committed WAL data without copying live database files.
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", temporary); err != nil {
		return fmt.Errorf("backup sqlite: %w", errors.Join(err, ctx.Err()))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	syncErr := errors.Join(file.Sync(), file.Close())
	file = nil
	if syncErr != nil {
		return fmt.Errorf("sync backup: %w", syncErr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// A same-directory hard link publishes only a complete, synced file and
	// atomically refuses an existing destination, including concurrent backups.
	if err := os.Link(temporary, path); err != nil {
		return fmt.Errorf("publish backup: %w", err)
	}
	if err := os.Remove(temporary); err != nil {
		return fmt.Errorf("remove temporary backup: %w", err)
	}
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync backup directory: %w", err)
	}
	return nil
}
