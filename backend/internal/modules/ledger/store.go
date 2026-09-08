package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
)

var (
	ErrNotFound      = errors.New("ledger operation not found")
	ErrConflict      = errors.New("ledger identity or uniqueness conflict")
	ErrIdempotency   = errors.New("idempotency key already used for another request")
	ErrVersion       = errors.New("ledger version conflict")
	ErrVoided        = errors.New("ledger operation is voided")
	ErrCorrupt       = errors.New("inconsistent ledger persistence")
	ErrManagedRecord = errors.New("correct the linked operation instead")
)

type WriteAction string

const (
	CreateOperation  WriteAction = "create"
	ReplaceOperation WriteAction = "replace"
	VoidOperation    WriteAction = "void"
)

// Command describes one atomic mutation. Key is unique across all write actions.
// For void, Operation must contain only ID; for replace it is a full replacement.
type Command struct {
	Action          WriteAction `json:"action"`
	Key             string      `json:"key"`
	Operation       Operation   `json:"operation"`
	ExpectedVersion int64       `json:"expected_version"`
	Note            string      `json:"note"`
	Reason          string      `json:"reason"`
}

// Record is a committed version, also stored in revisions and idempotency receipts.
type Record struct {
	Operation Operation `json:"operation"`
	Note      string    `json:"note"`
	Version   int64     `json:"version"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}

type Revision struct {
	Record Record `json:"record"`
	Reason string `json:"reason"`
}

// Store does not own db and does not provide user authentication. It must remain
// behind a trusted service boundary until private HTTP authorization is added.
type Store struct {
	db  *database.DB
	now func() time.Time
}

// NewStore expects a database opened with ledger.Open. The clock is injectable
// for deterministic historical validation; nil uses time.Now.
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

// AddInstrument is insert-only: changing identity or currency could reinterpret
// every historical amount. Identical retries are harmless; changes conflict.
func (s *Store) AddInstrument(ctx context.Context, instrument Instrument) error {
	date, stamp, err := s.cutoff()
	if err != nil {
		return err
	}
	if _, err := Replay(nil, []Instrument{instrument}, nil, date); err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var existing Instrument
		err := tx.QueryRowContext(ctx, `SELECT id,market,code,name,currency FROM instruments WHERE id=?`, instrument.ID).
			Scan(&existing.ID, &existing.Market, &existing.Code, &existing.Name, &existing.Currency)
		if err == nil {
			if existing != instrument {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO instruments(id,market,code,name,currency) VALUES(?,?,?,?,?)`,
			instrument.ID, instrument.Market, instrument.Code, instrument.Name, instrument.Currency); err != nil {
			return constraintError(err)
		}
		_, err = appendAudit(ctx, tx, "", "create", "instrument", instrument.ID, "", 1, stamp, "human", nil,
			instrumentJSON{ID: instrument.ID, Market: instrument.Market, Code: instrument.Code, Name: instrument.Name, Currency: instrument.Currency}, nil)
		return err
	})
}

// InitializeAccount atomically creates an immutable opening, not an external
// deposit. Duplicate IDs conflict; no upsert may reset an established account.
func (s *Store) InitializeAccount(ctx context.Context, name string, opening Opening) error {
	date, stamp, err := s.cutoff()
	if err != nil || !validText(name) || !utf8.ValidString(name) {
		return ErrOperation
	}
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		openings, instruments, records, err := loadLedger(ctx, tx)
		if err != nil {
			return err
		}
		for _, existing := range openings {
			if existing.AccountID == opening.AccountID {
				return ErrConflict
			}
		}
		if _, err := replayRecords(append(openings, opening), instruments, records, date); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO accounts(id,name,currency,opening_date,opening_cash_minor,version,accounting_mode) VALUES(?,?,?,?,?,1,'holdings')`,
			opening.AccountID, name, opening.Currency, opening.Date, opening.Cash); err != nil {
			return constraintError(err)
		}
		cash := opening.Cash
		if _, err := appendAudit(ctx, tx, "", "create", "account", opening.AccountID, opening.AccountID, 1, stamp, "human", nil,
			accountJSON{ID: opening.AccountID, Name: name, Currency: opening.Currency, OpeningDate: opening.Date, OpeningCash: &cash, Version: "1", AccountingMode: "holdings"}, nil); err != nil {
			return err
		}
		for _, p := range opening.Positions {
			if _, err := tx.ExecContext(ctx, `INSERT INTO opening_positions(account_id,instrument_id,quantity_micros,cost_minor,diluted_basis_minor) VALUES(?,?,?,?,?)`,
				opening.AccountID, p.InstrumentID, p.Quantity, p.Cost, p.DilutedBasis); err != nil {
				return constraintError(err)
			}
			if _, err := appendAudit(ctx, tx, "", "create", "opening_position", p.InstrumentID, opening.AccountID, 1, stamp, "human", nil,
				openingPositionJSON{InstrumentID: p.InstrumentID, Quantity: p.Quantity, Cost: p.Cost, DilutedBasis: p.DilutedBasis}, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// Write validates the complete resulting history and commits the operation,
// revision and replayable receipt together. No partial result escapes on error.
func (s *Store) Write(ctx context.Context, command Command) (Record, error) {
	if !validID(command.Key) || !validID(command.Operation.ID) || !validText(command.Reason) ||
		!utf8.ValidString(command.Note) || len(command.Note) > 8192 || command.Operation.Voided {
		return Record{}, ErrOperation
	}
	command.Operation.Fee = copyMoney(command.Operation.Fee)
	if command.Operation.FX != nil {
		fx := *command.Operation.FX
		command.Operation.FX = &fx
	}
	switch command.Action {
	case CreateOperation:
		if command.ExpectedVersion != 0 {
			return Record{}, ErrOperation
		}
	case ReplaceOperation:
		if command.ExpectedVersion <= 0 {
			return Record{}, ErrOperation
		}
	case VoidOperation:
		if command.ExpectedVersion <= 0 || command.Note != "" || command.Operation != (Operation{ID: command.Operation.ID}) {
			return Record{}, ErrOperation
		}
	default:
		return Record{}, ErrOperation
	}
	request, err := json.Marshal(command)
	if err != nil {
		return Record{}, err
	}
	var result Record
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		found, record, err := readReceipt(ctx, tx, command.Key, string(request), command)
		if err != nil || found {
			result = record
			return err
		}
		date, stamp, err := s.cutoff()
		if err != nil {
			return err
		}
		for _, id := range []string{command.Operation.AccountID, command.Operation.ToAccountID} {
			if id != "" {
				if err := requireHoldings(ctx, tx, id); err != nil {
					return err
				}
			}
		}
		openings, instruments, records, err := loadLedger(ctx, tx)
		if err != nil {
			return err
		}
		index := -1
		for i := range records {
			if records[i].Operation.ID == command.Operation.ID {
				index = i
				break
			}
		}
		record = Record{Operation: command.Operation, Note: command.Note, Version: 1, CreatedAt: stamp, UpdatedAt: stamp}
		if command.Action == CreateOperation {
			if index >= 0 {
				return ErrConflict
			}
			records = append(records, record)
		} else {
			if index < 0 {
				return ErrNotFound
			}
			old := records[index]
			if old.Version != command.ExpectedVersion || old.Version == math.MaxInt64 {
				return ErrVersion
			}
			if old.Operation.Voided {
				return ErrVoided
			}
			record.Version, record.CreatedAt = old.Version+1, old.CreatedAt
			if command.Action == VoidOperation {
				record.Operation, record.Note = old.Operation, old.Note
				record.Operation.Voided = true
			}
			records[index] = record
		}
		if _, err := replayRecords(openings, instruments, records, date); err != nil {
			return err
		}
		if err := saveRecord(ctx, tx, record, command, string(request)); err != nil {
			return err
		}
		if err := syncOperationRecords(ctx, tx, record, command.Key, command.Reason, "human"); err != nil {
			return err
		}
		result = record
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return result, nil
}

func replayRecords(openings []Opening, instruments []Instrument, records []Record, date string) (*Book, error) {
	operations := make([]Operation, len(records))
	for i := range records {
		operations[i] = records[i].Operation
	}
	return Replay(openings, instruments, operations, date)
}

// State returns one consistent replay under the same transaction/connection.
// It is not a historical valuation or a market-price query.
func (s *Store) State(ctx context.Context) (*Book, error) {
	date, _, err := s.cutoff()
	if err != nil {
		return nil, err
	}
	var book *Book
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		openings, instruments, records, err := loadLedger(ctx, tx)
		if err != nil {
			return err
		}
		book, err = replayRecords(openings, instruments, records, date)
		return err
	})
	if err != nil {
		return nil, err
	}
	return book, nil
}

func (s *Store) GetOperation(ctx context.Context, id string) (Record, error) {
	if !validID(id) {
		return Record{}, ErrOperation
	}
	var record Record
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		record, err = selectRecord(ctx, tx, id)
		return err
	})
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *Store) Revisions(ctx context.Context, id string) ([]Revision, error) {
	if !validID(id) {
		return nil, ErrOperation
	}
	var result []Revision
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		current, err := selectRecord(ctx, tx, id)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT version,after_json,recorded_at,metadata_json
			FROM audit_log WHERE entity_type='operation' AND entity_id=? ORDER BY version`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var version int64
			var payload, stamp, metadata string
			var revision Revision
			if err := rows.Scan(&version, &payload, &stamp, &metadata); err != nil {
				return err
			}
			var detail struct {
				Reason   string `json:"reason"`
				FromDate string `json:"from_date"`
			}
			if decodeReceipt(metadata, &detail) != nil || !validText(detail.Reason) || !validDate(detail.FromDate) {
				return ErrCorrupt
			}
			revision.Reason = detail.Reason
			revision.Record, err = decodeStoredRecord(payload)
			if err != nil || revision.Record.Operation.ID != id || revision.Record.UpdatedAt != stamp ||
				revision.Record.Version != version || version != int64(len(result))+1 {
				return ErrCorrupt
			}
			result = append(result, revision)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if int64(len(result)) != current.Version {
			return fmt.Errorf("%w: incomplete revisions", ErrCorrupt)
		}
		return rows.Close()
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
