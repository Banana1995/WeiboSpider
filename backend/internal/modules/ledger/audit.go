package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

// Every caller supplies its business transaction; there is no best-effort audit.
func appendAudit(ctx context.Context, tx *sql.Tx, correlation, action, entity, id, account string, version int64, stamp, source string, before, after, metadata any) (int64, error) {
	encode := func(value any) ([]byte, error) {
		if raw, ok := value.(json.RawMessage); ok {
			if !json.Valid(raw) {
				return nil, ErrCorrupt
			}
			return raw, nil
		}
		return json.Marshal(value)
	}
	var previous any
	if before != nil {
		b, err := encode(before)
		if err != nil {
			return 0, err
		}
		previous = string(b)
	}
	b, err := encode(after)
	if err != nil {
		return 0, err
	}
	m, err := json.Marshal(metadata)
	if err != nil {
		return 0, err
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO audit_log(correlation_id,action,entity_type,entity_id,account_id,version,recorded_at,source,before_json,after_json,metadata_json) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, correlation, action, entity, id, sql.NullString{String: account, Valid: account != ""}, version, stamp, source, previous, string(b), string(m))
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// putAccountRecord stores the latest state and its immutable audit version
// together. Source-managed records never use a second business projection.
func putAccountRecord(ctx context.Context, tx *sql.Tx, next *AccountRecord, previous *AccountRecord, correlation, reason, source string) (int64, error) {
	version, err := positiveInteger(next.Version)
	if err != nil || !next.AccountEntry.valid() || !next.validProvenance() {
		return 0, ErrCorrupt
	}
	var before any
	if previous == nil {
		if next.Sequence == "" {
			var sequence int64
			if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(sequence),0)+1 FROM account_records`).Scan(&sequence); err != nil {
				return 0, err
			}
			next.Sequence = strconv.FormatInt(sequence, 10)
		} else if _, err := positiveInteger(next.Sequence); err != nil {
			return 0, ErrCorrupt
		}
	} else {
		next.Sequence = previous.Sequence
		var payload string
		if err := tx.QueryRowContext(ctx, `SELECT payload FROM account_records WHERE account_id=? AND id=? AND version=?`, previous.AccountID, previous.ID, previous.Version).Scan(&payload); err != nil {
			return 0, ErrCorrupt
		}
		before = json.RawMessage(payload)
	}
	payload, err := json.Marshal(next)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO account_records(sequence,account_id,id,business_date,kind,flow_minor,total_assets_minor,note,origin,operation_id,quote_audit_id,manual_assertion,voided,version,created_at,updated_at,payload)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id,id) DO UPDATE SET business_date=excluded.business_date,kind=excluded.kind,flow_minor=excluded.flow_minor,total_assets_minor=excluded.total_assets_minor,note=excluded.note,origin=excluded.origin,operation_id=excluded.operation_id,quote_audit_id=excluded.quote_audit_id,manual_assertion=excluded.manual_assertion,voided=excluded.voided,version=excluded.version,updated_at=excluded.updated_at,payload=excluded.payload`,
		next.Sequence, next.AccountID, next.ID, next.Date, next.Kind, next.Flow, next.TotalAssets, next.Note, next.Origin, sql.NullString{String: next.OperationID, Valid: next.OperationID != ""}, sql.NullString{String: next.QuoteAuditID, Valid: next.QuoteAuditID != ""}, next.ManualAssertion, next.Voided, version, next.CreatedAt, next.UpdatedAt, string(payload))
	if err != nil {
		return 0, constraintError(err)
	}
	action := "create"
	from := next.Date
	if previous != nil {
		action = "replace"
		if previous.Date < from {
			from = previous.Date
		}
	}
	if next.Voided {
		action = "void"
	}
	return appendAudit(ctx, tx, correlation, action, "account_record", next.ID, next.AccountID, version, next.UpdatedAt, source, before, next, map[string]string{"reason": reason, "from_date": from})
}

// Operation IDs and account legs, not dates or amounts, establish identity.
func syncOperationRecords(ctx context.Context, tx *sql.Tx, record Record, correlation, reason, source string) error {
	o := record.Operation
	rows, err := tx.QueryContext(ctx, accountRecordSelect+` WHERE operation_id=? ORDER BY sequence`, o.ID)
	if err != nil {
		return err
	}
	previous := map[string]AccountRecord{}
	for rows.Next() {
		r, e := scanAccountRecord(rows)
		if e != nil {
			rows.Close()
			return e
		}
		previous[r.AccountID] = r
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	accounts := []string{o.AccountID}
	if o.ToAccountID != "" {
		accounts = append(accounts, o.ToAccountID)
	}
	for _, account := range accounts {
		old, exists := previous[account]
		delete(previous, account)
		next := AccountRecord{ID: "operation-" + o.ID, AccountID: account, AccountEntry: AccountEntry{Kind: "log", Date: o.Date, Note: record.Note}, Origin: "operation", OperationID: o.ID, Version: "1", Voided: o.Voided, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
		if len(next.ID) > 128 {
			sum := sha256.Sum256([]byte(o.ID))
			next.ID = "op-" + hex.EncodeToString(sum[:])
		}
		var prior *AccountRecord
		if exists {
			prior = &old
			v, e := positiveInteger(old.Version)
			if e != nil || v == 9223372036854775807 {
				return ErrCorrupt
			}
			next.ID = old.ID
			next.Version = strconv.FormatInt(v+1, 10)
			next.CreatedAt = old.CreatedAt
		}
		var flow Money
		switch o.Kind {
		case Deposit, DepositBuy:
			flow = o.Amount
		case Withdrawal, SellWithdraw:
			flow = -o.Amount
		case Transfer:
			flow = -o.Amount
			if account == o.ToAccountID {
				flow = o.Amount
			}
		}
		if flow != 0 {
			next.Kind = "cash_flow"
			next.Flow = &flow
		}
		if next.Note == "" && next.Kind == "log" {
			next.Note = string(o.Kind)
		}
		if _, err := putAccountRecord(ctx, tx, &next, prior, correlation, reason, source); err != nil {
			return err
		}
	}
	for _, old := range previous {
		if old.Voided {
			continue
		}
		next := old
		v, e := positiveInteger(old.Version)
		if e != nil || v == 9223372036854775807 {
			return ErrCorrupt
		}
		next.Version = strconv.FormatInt(v+1, 10)
		next.Voided = true
		next.UpdatedAt = record.UpdatedAt
		if _, err := putAccountRecord(ctx, tx, &next, &old, correlation, reason, source); err != nil {
			return err
		}
	}
	return nil
}
