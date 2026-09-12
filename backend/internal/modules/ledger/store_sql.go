package ledger

import (
	"errors"
	"fmt"

	"github.com/mattn/go-sqlite3"
)

func constraintError(err error) error {
	var sqliteErr sqlite3.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code != sqlite3.ErrConstraint {
		return err
	}
	if sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique || sqliteErr.ExtendedCode == sqlite3.ErrConstraintPrimaryKey {
		return ErrConflict
	}
	kind := "constraint"
	switch sqliteErr.ExtendedCode {
	case sqlite3.ErrConstraintForeignKey:
		kind = "foreign key constraint"
	case sqlite3.ErrConstraintCheck:
		kind = "check constraint"
	case sqlite3.ErrConstraintNotNull:
		kind = "not-null constraint"
	}
	// Do not expose SQL/trigger messages containing private input values.
	return fmt.Errorf("ledger %s: %w", kind, sqlite3.Error{Code: sqliteErr.Code, ExtendedCode: sqliteErr.ExtendedCode, SystemErrno: sqliteErr.SystemErrno})
}
