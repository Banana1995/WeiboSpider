package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/mattn/go-sqlite3"
)

func loadLedger(ctx context.Context, tx *sql.Tx) ([]Opening, []Instrument, []Record, error) {
	var openings []Opening
	accounts := make(map[string]int)
	rows, err := tx.QueryContext(ctx, `SELECT id,currency,opening_date,opening_cash_minor FROM accounts WHERE accounting_mode='holdings' ORDER BY id`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var opening Opening
		if err := rows.Scan(&opening.AccountID, &opening.Currency, &opening.Date, &opening.Cash); err != nil {
			return nil, nil, nil, errors.Join(ErrCorrupt, ctx.Err())
		}
		accounts[opening.AccountID] = len(openings)
		openings = append(openings, opening)
	}
	if err := errors.Join(rows.Err(), rows.Close(), ctx.Err()); err != nil {
		return nil, nil, nil, err
	}

	rows, err = tx.QueryContext(ctx, `SELECT account_id,instrument_id,quantity_micros,cost_minor,diluted_basis_minor
		FROM opening_positions ORDER BY account_id,instrument_id`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var accountID string
		var position OpeningPosition
		if err := rows.Scan(&accountID, &position.InstrumentID, &position.Quantity, &position.Cost, &position.DilutedBasis); err != nil {
			return nil, nil, nil, errors.Join(ErrCorrupt, ctx.Err())
		}
		index, exists := accounts[accountID]
		if !exists {
			return nil, nil, nil, fmt.Errorf("%w: opening account missing", ErrCorrupt)
		}
		openings[index].Positions = append(openings[index].Positions, position)
	}
	if err := errors.Join(rows.Err(), rows.Close(), ctx.Err()); err != nil {
		return nil, nil, nil, err
	}

	var instruments []Instrument
	securities := make(map[string]bool)
	rows, err = tx.QueryContext(ctx, `SELECT id,market,code,name,currency FROM instruments ORDER BY id`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var instrument Instrument
		if err := rows.Scan(&instrument.ID, &instrument.Market, &instrument.Code, &instrument.Name, &instrument.Currency); err != nil {
			return nil, nil, nil, errors.Join(ErrCorrupt, ctx.Err())
		}
		securities[instrument.ID] = true
		instruments = append(instruments, instrument)
	}
	if err := errors.Join(rows.Err(), rows.Close(), ctx.Err()); err != nil {
		return nil, nil, nil, err
	}
	for _, opening := range openings {
		for _, position := range opening.Positions {
			if err := ctx.Err(); err != nil {
				return nil, nil, nil, err
			}
			if !securities[position.InstrumentID] {
				return nil, nil, nil, fmt.Errorf("%w: opening instrument missing", ErrCorrupt)
			}
		}
	}

	// Read all dates and statuses: truncating history could let a rewrite bypass
	// later cash/position conflicts. Rows are closed before the next query.
	var records []Record
	rows, err = tx.QueryContext(ctx, recordSelect+` ORDER BY o.business_date,o.sequence`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		record, err := scanRecord(ctx, rows)
		if err != nil {
			return nil, nil, nil, err
		}
		records = append(records, record)
	}
	if err := errors.Join(rows.Err(), rows.Close(), ctx.Err()); err != nil {
		return nil, nil, nil, err
	}
	return openings, instruments, records, nil
}

// LEFT JOIN preserves operations with missing current audits or parents so they fail
// closed instead of silently disappearing from replay.
const recordSelect = `SELECT o.id,o.kind,o.business_date,o.sequence,o.account_id,o.to_account_id,o.instrument_id,
	o.amount_minor,o.quantity_micros,o.price_micros,o.fee_minor,
	o.fx_rate_1e8,o.fx_date,o.fx_source,o.fx_fetched_at,o.cycle_id,o.note,o.status,o.version,o.created_at,
	o.updated_at,a.after_json,
	(a.id IS NOT NULL AND parent.id IS NOT NULL AND (o.to_account_id IS NULL OR t.id IS NOT NULL)
	 AND (o.instrument_id IS NULL OR i.id IS NOT NULL))
	FROM operations o
	LEFT JOIN audit_log a ON a.entity_type='operation' AND a.entity_id=o.id
	 AND a.account_id=o.account_id AND a.version=o.version
	LEFT JOIN accounts parent ON parent.id=o.account_id
	LEFT JOIN accounts t ON t.id=o.to_account_id
	LEFT JOIN instruments i ON i.id=o.instrument_id`

func scanRecord(ctx context.Context, row interface{ Scan(...any) error }) (Record, error) {
	var record Record
	o := &record.Operation
	var target, instrument, cycle, fxDate, fxSource, fxFetched, updated, payload sql.NullString
	var rate sql.NullInt64
	var status string
	var parents bool
	err := row.Scan(&o.ID, &o.Kind, &o.Date, &o.Sequence, &o.AccountID, &target, &instrument,
		&o.Amount, &o.Quantity, &o.Price, &o.Fee, &rate, &fxDate, &fxSource, &fxFetched,
		&cycle, &record.Note, &status, &record.Version, &record.CreatedAt, &updated, &payload, &parents)
	if ctx.Err() != nil {
		return Record{}, ctx.Err()
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, err
	}
	if err != nil {
		// Scan errors can embed the offending private SQL value.
		return Record{}, fmt.Errorf("%w: operation columns", ErrCorrupt)
	}
	if !parents || !updated.Valid || !payload.Valid {
		return Record{}, fmt.Errorf("%w: operation parent or current audit missing", ErrCorrupt)
	}
	switch status {
	case "active":
	case "voided":
		o.Voided = true
	default:
		return Record{}, fmt.Errorf("%w: operation status", ErrCorrupt)
	}
	for _, value := range []sql.NullString{target, instrument, cycle} {
		if value.Valid && value.String == "" {
			return Record{}, fmt.Errorf("%w: empty optional identity", ErrCorrupt)
		}
	}
	o.ToAccountID, o.InstrumentID, o.CycleID = target.String, instrument.String, cycle.String
	if rate.Valid != fxDate.Valid || rate.Valid != fxSource.Valid || rate.Valid != fxFetched.Valid {
		return Record{}, fmt.Errorf("%w: partial FX snapshot", ErrCorrupt)
	}
	if rate.Valid {
		o.FX = &FXSnapshot{Rate: Rate(rate.Int64), Date: fxDate.String, Source: fxSource.String, FetchedAt: fxFetched.String}
	}
	record.UpdatedAt = updated.String
	revision, err := decodeStoredRecord(payload.String)
	if err != nil {
		return Record{}, err
	}
	if !reflect.DeepEqual(record, revision) {
		return Record{}, fmt.Errorf("%w: operation differs from current revision", ErrCorrupt)
	}
	return record, nil
}

func selectRecord(ctx context.Context, tx *sql.Tx, id string) (Record, error) {
	record, err := scanRecord(ctx, tx.QueryRowContext(ctx, recordSelect+` WHERE o.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return record, err
}

func decodeStoredRecord(payload string) (Record, error) {
	var record Record
	if json.Unmarshal([]byte(payload), &record) != nil {
		return Record{}, fmt.Errorf("%w: invalid record JSON", ErrCorrupt)
	}
	// Our writer stores the exact Record encoding. Requiring that shape also
	// rejects omitted zero/null fields, unknown fields and lossy numeric encodings.
	encoded, err := json.Marshal(record)
	var compact bytes.Buffer
	if err != nil || json.Compact(&compact, []byte(payload)) != nil || !bytes.Equal(encoded, compact.Bytes()) {
		return Record{}, fmt.Errorf("%w: record JSON shape", ErrCorrupt)
	}
	o := record.Operation
	if !validID(o.ID) || !validID(o.AccountID) || !validDate(o.Date) || o.Sequence <= 0 || record.Version <= 0 {
		return Record{}, fmt.Errorf("%w: record identity or version", ErrCorrupt)
	}
	switch o.Kind {
	case Deposit, Withdrawal, Buy, Sell, DepositBuy, SellWithdraw, Dividend, Transfer:
	default:
		return Record{}, fmt.Errorf("%w: record kind", ErrCorrupt)
	}
	for _, stamp := range []string{record.CreatedAt, record.UpdatedAt} {
		if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
			return Record{}, fmt.Errorf("%w: record timestamp", ErrCorrupt)
		}
	}
	return record, nil
}

func readReceipt(ctx context.Context, tx *sql.Tx, key, request string, command Command) (bool, Record, error) {
	receipt, found, err := loadReceipt(ctx, tx, key, "operation", request)
	if err != nil || !found {
		return found, Record{}, err
	}
	record, err := decodeStoredRecord(receipt.Response)
	if err != nil {
		return true, Record{}, err
	}
	var action, entity, id, account, stamp, source, correlation, payload string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT action,entity_type,entity_id,account_id,version,recorded_at,source,correlation_id,after_json
		FROM audit_log WHERE id=?`, receipt.AuditID).
		Scan(&action, &entity, &id, &account, &version, &stamp, &source, &correlation, &payload); err != nil {
		return true, Record{}, ErrCorrupt
	}
	if action != string(command.Action) || entity != "operation" || source != "human" || correlation != key ||
		id != record.Operation.ID || account != record.Operation.AccountID || version != record.Version ||
		stamp != record.UpdatedAt || payload != receipt.Response {
		return true, Record{}, fmt.Errorf("%w: receipt audit differs from result", ErrCorrupt)
	}
	current, err := selectRecord(ctx, tx, record.Operation.ID)
	if err != nil || current.Version < record.Version {
		return true, Record{}, fmt.Errorf("%w: receipt operation missing", ErrCorrupt)
	}
	return true, record, nil
}

func saveRecord(ctx context.Context, tx *sql.Tx, record Record, command Command, request string) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	o := record.Operation
	status := "active"
	if o.Voided {
		status = "voided"
	}
	var rate, fxDate, fxSource, fxFetched any
	if o.FX != nil {
		rate, fxDate, fxSource, fxFetched = o.FX.Rate, o.FX.Date, o.FX.Source, o.FX.FetchedAt
	}
	args := []any{o.Kind, o.Date, o.Sequence, o.AccountID,
		sql.NullString{String: o.ToAccountID, Valid: o.ToAccountID != ""},
		sql.NullString{String: o.InstrumentID, Valid: o.InstrumentID != ""},
		o.Amount, o.Quantity, o.Price, o.Fee, rate, fxDate, fxSource, fxFetched,
		sql.NullString{String: o.CycleID, Valid: o.CycleID != ""}, record.Note, status, record.Version}
	switch command.Action {
	case CreateOperation:
		args = append(args, o.ID, record.CreatedAt, record.UpdatedAt)
		_, err = tx.ExecContext(ctx, `INSERT INTO operations
			(kind,business_date,sequence,account_id,to_account_id,instrument_id,amount_minor,quantity_micros,price_micros,
			 fee_minor,fx_rate_1e8,fx_date,fx_source,fx_fetched_at,cycle_id,note,status,version,id,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
	case ReplaceOperation, VoidOperation:
		args = append(args, record.UpdatedAt, o.ID, command.ExpectedVersion)
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE operations SET
			kind=?,business_date=?,sequence=?,account_id=?,to_account_id=?,instrument_id=?,amount_minor=?,quantity_micros=?,price_micros=?,
			fee_minor=?,fx_rate_1e8=?,fx_date=?,fx_source=?,fx_fetched_at=?,cycle_id=?,note=?,status=?,version=?,updated_at=?
			WHERE id=? AND version=? AND status='active'`, args...)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count != 1 {
				return ErrVersion
			}
		}
	default:
		return ErrOperation
	}
	if err != nil {
		return constraintError(err)
	}
	var before any
	if record.Version > 1 {
		var old string
		if err := tx.QueryRowContext(ctx, `SELECT after_json FROM audit_log
			WHERE entity_type='operation' AND entity_id=? AND version=?`, o.ID, record.Version-1).Scan(&old); err != nil {
			return ErrCorrupt
		}
		before = json.RawMessage(old)
	}
	auditID, err := appendAudit(ctx, tx, command.Key, string(command.Action), "operation", o.ID, o.AccountID, record.Version, record.UpdatedAt, "human", before, json.RawMessage(payload), map[string]string{"reason": command.Reason, "from_date": o.Date})
	if err != nil {
		return constraintError(err)
	}
	return saveReceipt(ctx, tx, command.Key, "operation", request, string(payload), auditID)
}

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
	// Drop SQLite's message (including trigger-supplied private values), but keep
	// the codes available to errors.As for descriptive non-conflict failures.
	return fmt.Errorf("ledger %s: %w", kind, sqlite3.Error{
		Code: sqliteErr.Code, ExtendedCode: sqliteErr.ExtendedCode, SystemErrno: sqliteErr.SystemErrno,
	})
}
