package ledger

import (
	"context"
	"database/sql"
	"errors"
)

var ErrQuery = errors.New("invalid ledger query")

// PageQuery uses an exclusive ID cursor. Callers supply the default limit (30).
type PageQuery struct {
	Limit int
	After string
}

type OperationQuery struct {
	AccountID      string
	From           string
	To             string
	Status         string
	Limit          int
	BeforeDate     string
	BeforeSequence int64
}

// AccountInfo describes the immutable opening, not a valuation. Version is the
// account metadata version (currently 1), not a version of its derived balance.
type AccountInfo struct {
	AccountingMode string
	ID             string
	Name           string
	Currency       Currency
	OpeningDate    string
	OpeningCash    Money
	Version        int64
}

const accountInfoSelect = `SELECT id,name,currency,opening_date,opening_cash_minor,version,accounting_mode FROM accounts`

func scanAccountInfo(ctx context.Context, row interface{ Scan(...any) error }) (AccountInfo, error) {
	var info AccountInfo
	err := row.Scan(&info.ID, &info.Name, &info.Currency, &info.OpeningDate, &info.OpeningCash, &info.Version, &info.AccountingMode)
	if ctx.Err() != nil {
		return AccountInfo{}, ctx.Err()
	}
	if errors.Is(err, sql.ErrNoRows) {
		return AccountInfo{}, ErrNotFound
	}
	if err != nil || !validID(info.ID) || !validText(info.Name) || !info.Currency.valid() ||
		!validDate(info.OpeningDate) || info.OpeningCash < 0 || info.Version <= 0 || (info.AccountingMode != "holdings" && info.AccountingMode != "reported") {
		return AccountInfo{}, ErrCorrupt
	}
	return info, nil
}

func (s *Store) ListAccounts(ctx context.Context, query PageQuery) ([]AccountInfo, error) {
	if query.Limit < 1 || query.Limit > 100 || query.After != "" && !validID(query.After) {
		return nil, ErrQuery
	}
	result := make([]AccountInfo, 0)
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, accountInfoSelect+` WHERE id>? ORDER BY id LIMIT ?`, query.After, query.Limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			info, err := scanAccountInfo(ctx, rows)
			if err != nil {
				return err
			}
			result = append(result, info)
		}
		return errors.Join(rows.Err(), rows.Close(), ctx.Err())
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) ListInstruments(ctx context.Context, query PageQuery) ([]Instrument, error) {
	if query.Limit < 1 || query.Limit > 100 || query.After != "" && !validID(query.After) {
		return nil, ErrQuery
	}
	result := make([]Instrument, 0)
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,market,code,name,currency FROM instruments WHERE id>? ORDER BY id LIMIT ?`, query.After, query.Limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var instrument Instrument
			if err := rows.Scan(&instrument.ID, &instrument.Market, &instrument.Code, &instrument.Name, &instrument.Currency); err != nil {
				return errors.Join(ErrCorrupt, ctx.Err())
			}
			if !validID(instrument.ID) || !validText(instrument.Market) || !validText(instrument.Code) ||
				!validText(instrument.Name) || !instrument.Currency.valid() {
				return ErrCorrupt
			}
			result = append(result, instrument)
		}
		return errors.Join(rows.Err(), rows.Close(), ctx.Err())
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Account reads metadata and the full replay in one snapshot. Filtering records
// to this account or an earlier date would hide global ordering/history errors.
func (s *Store) Account(ctx context.Context, id string) (AccountInfo, *AccountState, error) {
	if !validID(id) {
		return AccountInfo{}, nil, ErrQuery
	}
	var info AccountInfo
	var state *AccountState
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		info, err = scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		if info.AccountingMode == "reported" {
			return nil
		}
		date, _, err := s.cutoff()
		if err != nil {
			return err
		}
		openings, instruments, records, err := loadLedger(ctx, tx)
		if err != nil {
			return err
		}
		book, err := replayRecords(openings, instruments, records, date)
		if err != nil {
			return err
		}
		state = book.Accounts[id]
		if state == nil {
			return ErrCorrupt
		}
		return nil
	})
	if err != nil {
		return AccountInfo{}, nil, err
	}
	return info, state, nil
}

// ListOperations returns current committed records, including voided records by
// default. Date bounds are inclusive; the date/sequence cursor is exclusive.
func (s *Store) ListOperations(ctx context.Context, query OperationQuery) ([]Record, error) {
	if query.Limit < 1 || query.Limit > 100 || query.AccountID != "" && !validID(query.AccountID) ||
		query.From != "" && !validDate(query.From) || query.To != "" && !validDate(query.To) ||
		query.From != "" && query.To != "" && query.From > query.To {
		return nil, ErrQuery
	}
	if query.BeforeDate == "" {
		if query.BeforeSequence != 0 {
			return nil, ErrQuery
		}
	} else if !validDate(query.BeforeDate) || query.BeforeSequence <= 0 {
		return nil, ErrQuery
	}
	switch query.Status {
	case "", "all", "active", "voided":
	default:
		return nil, ErrQuery
	}
	where := ` WHERE 1=1`
	args := make([]any, 0)
	if query.AccountID != "" {
		where += ` AND (o.account_id=? OR o.to_account_id=?)`
		args = append(args, query.AccountID, query.AccountID)
	}
	if query.From != "" {
		where += ` AND o.business_date>=?`
		args = append(args, query.From)
	}
	if query.To != "" {
		where += ` AND o.business_date<=?`
		args = append(args, query.To)
	}
	if query.Status == "active" || query.Status == "voided" {
		where += ` AND o.status=?`
		args = append(args, query.Status)
	}
	if query.BeforeDate != "" {
		where += ` AND (o.business_date<? OR (o.business_date=? AND o.sequence<?))`
		args = append(args, query.BeforeDate, query.BeforeDate, query.BeforeSequence)
	}
	args = append(args, query.Limit)
	result := make([]Record, 0)
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		if query.AccountID != "" {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=?)`, query.AccountID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
		}
		rows, err := tx.QueryContext(ctx, recordSelect+where+` ORDER BY o.business_date DESC,o.sequence DESC LIMIT ?`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			record, err := scanRecord(ctx, rows)
			if err != nil {
				return err
			}
			result = append(result, record)
		}
		return errors.Join(rows.Err(), rows.Close(), ctx.Err())
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) RevisionPage(ctx context.Context, id string, afterVersion int64, limit int) ([]Revision, error) {
	if !validID(id) || afterVersion < 0 || limit < 1 || limit > 100 {
		return nil, ErrQuery
	}
	result := make([]Revision, 0)
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		current, err := selectRecord(ctx, tx, id)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT entity_id,version,after_json,recorded_at,metadata_json
			FROM audit_log WHERE entity_type='operation' AND entity_id=? AND version>? ORDER BY version LIMIT ?`, id, afterVersion, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		previous := afterVersion
		for rows.Next() {
			var storedID, payload, stamp, metadata string
			var version int64
			var revision Revision
			if err := rows.Scan(&storedID, &version, &payload, &stamp, &metadata); err != nil {
				return errors.Join(ErrCorrupt, ctx.Err())
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
			if err != nil || storedID != id || revision.Record.Operation.ID != storedID ||
				revision.Record.Version != version || revision.Record.UpdatedAt != stamp ||
				version <= previous || version-previous != 1 || version > current.Version {
				return ErrCorrupt
			}
			previous = version
			result = append(result, revision)
		}
		if err := errors.Join(rows.Err(), rows.Close(), ctx.Err()); err != nil {
			return err
		}
		if len(result) < limit && previous < current.Version {
			return ErrCorrupt
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
