package ledger

import (
	"context"
	"database/sql"
	"errors"
)

var ErrQuery = errors.New("invalid ledger query")

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
type AccountInfo struct {
	ID          string
	Name        string
	Currency    Currency
	OpeningDate string
	OpeningCash Money
	Version     int64
}

const accountInfoSelect = `SELECT id,name,currency,opening_date,opening_cash_minor,version FROM accounts`

func scanAccountInfo(ctx context.Context, row interface{ Scan(...any) error }) (AccountInfo, error) {
	var a AccountInfo
	err := row.Scan(&a.ID, &a.Name, &a.Currency, &a.OpeningDate, &a.OpeningCash, &a.Version)
	if ctx.Err() != nil {
		return AccountInfo{}, ctx.Err()
	}
	if errors.Is(err, sql.ErrNoRows) {
		return AccountInfo{}, ErrNotFound
	}
	if err != nil || !validID(a.ID) || !validText(a.Name) || !a.Currency.valid() || !validDate(a.OpeningDate) || a.OpeningCash < 0 || a.Version <= 0 {
		return AccountInfo{}, ErrCorrupt
	}
	return a, nil
}
func (s *Store) ListAccounts(ctx context.Context, q PageQuery) ([]AccountInfo, error) {
	if q.Limit < 1 || q.Limit > 100 || q.After != "" && !validID(q.After) {
		return nil, ErrQuery
	}
	out := make([]AccountInfo, 0)
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, accountInfoSelect+` WHERE id>? ORDER BY id LIMIT ?`, q.After, q.Limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAccountInfo(ctx, rows)
			if err != nil {
				return err
			}
			out = append(out, a)
		}
		return errors.Join(rows.Err(), rows.Close(), ctx.Err())
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Store) ListInstruments(ctx context.Context, q PageQuery) ([]Instrument, error) {
	if q.Limit < 1 || q.Limit > 100 || q.After != "" && !validID(q.After) {
		return nil, ErrQuery
	}
	out := make([]Instrument, 0)
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,market,code,name,currency FROM instruments WHERE id>? ORDER BY id LIMIT ?`, q.After, q.Limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var i Instrument
			if err := rows.Scan(&i.ID, &i.Market, &i.Code, &i.Name, &i.Currency); err != nil {
				return errors.Join(ErrCorrupt, ctx.Err())
			}
			if !validID(i.ID) || !validText(i.Market) || !validText(i.Code) || !validText(i.Name) || !i.Currency.valid() {
				return ErrCorrupt
			}
			out = append(out, i)
		}
		return errors.Join(rows.Err(), rows.Close(), ctx.Err())
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Metadata and current input are captured in the same account-local snapshot.
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
		current, err := readCurrentHoldings(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Snapshot != nil {
			state = &AccountState{Currency: info.Currency, Cash: current.Snapshot.Cash}
		}
		return nil
	})
	if err != nil {
		return AccountInfo{}, nil, err
	}
	return info, state, nil
}
