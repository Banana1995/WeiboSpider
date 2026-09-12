package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
)

// Audit and business changes share a transaction; neither is best-effort.
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
	_, err = tx.ExecContext(ctx, `INSERT INTO account_records(sequence,account_id,id,business_date,kind,flow_minor,total_assets_minor,note,origin,quote_audit_id,manual_assertion,voided,version,created_at,updated_at,payload)
	 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id,id) DO UPDATE SET business_date=excluded.business_date,kind=excluded.kind,flow_minor=excluded.flow_minor,total_assets_minor=excluded.total_assets_minor,note=excluded.note,origin=excluded.origin,quote_audit_id=excluded.quote_audit_id,manual_assertion=excluded.manual_assertion,voided=excluded.voided,version=excluded.version,updated_at=excluded.updated_at,payload=excluded.payload`,
		next.Sequence, next.AccountID, next.ID, next.Date, next.Kind, next.Flow, next.TotalAssets, next.Note, next.Origin, sql.NullString{String: next.QuoteAuditID, Valid: next.QuoteAuditID != ""}, next.ManualAssertion, next.Voided, version, next.CreatedAt, next.UpdatedAt, string(payload))
	if err != nil {
		return 0, constraintError(err)
	}
	action, from := "create", next.Date
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
