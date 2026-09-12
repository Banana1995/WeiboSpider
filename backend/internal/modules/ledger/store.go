package ledger

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
)

var (
	ErrNotFound    = errors.New("ledger resource not found")
	ErrConflict    = errors.New("ledger identity or uniqueness conflict")
	ErrIdempotency = errors.New("idempotency key already used for another request")
	ErrVersion     = errors.New("ledger version conflict")
	ErrVoided      = errors.New("ledger record is voided")
	ErrCorrupt     = errors.New("inconsistent ledger persistence")
)

type WriteAction string

const (
	CreateOperation  WriteAction = "create"
	ReplaceOperation WriteAction = "replace"
	VoidOperation    WriteAction = "void"
)

// Store owns neither the database nor authentication. The HTTP boundary applies
// the configured public-ledger same-origin and request-size protections.
type Store struct {
	db  *database.DB
	now func() time.Time
}

func NewStore(db *database.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}
}

func (s *Store) cutoff() (string, string, error) {
	now := s.now()
	date := now.In(time.FixedZone("Beijing", 8*60*60)).Format(time.DateOnly)
	if !validDate(date) || now.Year() < 1 || now.Year() > 9999 {
		return "", "", ErrOperation
	}
	return date, now.UTC().Format(time.RFC3339Nano), nil
}

// Immutable identity entries may be shared; uniqueness belongs to each account's
// current snapshot, not the catalog. Changes create a new identity entry.
func (s *Store) AddInstrument(ctx context.Context, i Instrument) error {
	_, stamp, err := s.cutoff()
	if err != nil {
		return err
	}
	if !validID(i.ID) || !validText(i.Market) || !validText(i.Code) || !validText(i.Name) || !i.Currency.valid() {
		return ErrOperation
	}
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		stored, err := holdingInstrument(ctx, tx, i.ID)
		if err == nil {
			if stored != i {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO instruments(id,market,code,name,currency) VALUES(?,?,?,?,?)`, i.ID, i.Market, i.Code, i.Name, i.Currency); err != nil {
			return constraintError(err)
		}
		_, err = appendAudit(ctx, tx, "", "create", "instrument", i.ID, "", 1, stamp, "human", nil, instrumentJSON{i.ID, i.Market, i.Code, i.Name, i.Currency}, nil)
		return err
	})
}
