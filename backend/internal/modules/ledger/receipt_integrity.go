package ledger

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
)

// Frozen Go-writer JSON must have its exact shape: no missing/unknown fields,
// alternate numeric representations or identity substitutions.
func decodeReceipt[T any](payload string, out *T) error {
	shape := json.NewDecoder(bytes.NewReader([]byte(payload)))
	shape.UseNumber()
	if uniqueJSON(shape, 0, true) != nil {
		return ErrCorrupt
	}
	if json.Unmarshal([]byte(payload), out) != nil {
		return ErrCorrupt
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return ErrCorrupt
	}
	// SQLite's JSON writer and Go's writer escape HTML differently. Compare
	// exact JSON shapes/values with json.Number, never float64, without rewriting
	// either frozen byte string. Missing and unknown fields remain distinguishable.
	var original, canonical any
	a := json.NewDecoder(bytes.NewReader([]byte(payload)))
	a.UseNumber()
	b := json.NewDecoder(bytes.NewReader(encoded))
	b.UseNumber()
	if a.Decode(&original) != nil || b.Decode(&canonical) != nil || !reflect.DeepEqual(original, canonical) {
		return ErrCorrupt
	}
	return nil
}

type reportedAccountIntent struct {
	Action string
	Input  ReportedAccountInput
}

type storedReceipt struct {
	Response string
	AuditID  int64
}

func receiptDigest(request string) string {
	digest := sha256.Sum256([]byte(request))
	return hex.EncodeToString(digest[:])
}

func loadReceipt(ctx context.Context, tx *sql.Tx, key, kind, request string) (storedReceipt, bool, error) {
	var result storedReceipt
	var storedKind, storedHash, storedRequest string
	err := tx.QueryRowContext(ctx, `SELECT kind,request_hash,request_json,response_json,audit_id
		FROM idempotency_receipts WHERE key=?`, key).
		Scan(&storedKind, &storedHash, &storedRequest, &result.Response, &result.AuditID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if storedHash != receiptDigest(storedRequest) || !json.Valid([]byte(storedRequest)) || !json.Valid([]byte(result.Response)) {
		return result, true, ErrCorrupt
	}
	if storedKind != kind || storedRequest != request {
		return result, true, ErrIdempotency
	}
	var auditExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit_log WHERE id=?)`, result.AuditID).Scan(&auditExists); err != nil {
		return result, true, err
	}
	if !auditExists {
		return result, true, ErrCorrupt
	}
	if kind != "account_delete" {
		var deleted bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit_log a JOIN audit_log d
			ON d.account_id=a.account_id AND d.entity_type='account_delete' WHERE a.id=?)`, result.AuditID).Scan(&deleted); err != nil {
			return result, true, err
		}
		if deleted {
			return result, true, ErrNotFound
		}
	}
	return result, true, nil
}

func saveReceipt(ctx context.Context, tx *sql.Tx, key, kind, request, response string, auditID int64) error {
	var stamp string
	if err := tx.QueryRowContext(ctx, `SELECT recorded_at FROM audit_log WHERE id=?`, auditID).Scan(&stamp); err != nil {
		return ErrCorrupt
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO idempotency_receipts
		(key,kind,request_hash,request_json,response_json,audit_id,created_at) VALUES(?,?,?,?,?,?,?)`,
		key, kind, receiptDigest(request), request, response, auditID, stamp)
	return constraintError(err)
}

func validateAccountReceipt(ctx context.Context, tx *sql.Tx, intent any, payload, key string, auditID int64) error {
	switch c := intent.(type) {
	case accountDeleteIntent:
		var result AccountDeletion
		if decodeReceipt(payload, &result) != nil || result.AccountID != c.AccountID || !result.Deleted {
			return ErrCorrupt
		}
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit_log WHERE id=?
			AND entity_type='account_delete' AND entity_id=? AND account_id=? AND action='delete'
			AND version=1 AND source='human' AND correlation_id=? AND after_json=?)
			AND NOT EXISTS(SELECT 1 FROM accounts WHERE id=?)`, auditID, c.AccountID, c.AccountID, key, payload, c.AccountID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrCorrupt
		}
		return nil
	case AccountRecordCommand:
		var r AccountRecord
		if err := decodeReceipt(payload, &r); err != nil {
			return err
		}
		version := int64(1)
		if c.Action != CreateOperation {
			v, err := positiveInteger(c.ExpectedVersion)
			if err != nil || v == 9223372036854775807 {
				return ErrCorrupt
			}
			version = v + 1
		}
		if r.ID != c.ID || r.AccountID != c.AccountID || r.Version != strconv.FormatInt(version, 10) || !r.AccountEntry.valid() || r.Voided != (c.Action == VoidOperation) {
			return ErrCorrupt
		}
		if c.Entry != nil && !reflect.DeepEqual(r.AccountEntry, *c.Entry) {
			return ErrCorrupt
		}
		var committed, metadata string
		if err := tx.QueryRowContext(ctx, `SELECT after_json,metadata_json FROM audit_log
			WHERE id=? AND entity_type='account_record' AND entity_id=? AND account_id=?
			AND version=? AND action=? AND recorded_at=? AND correlation_id=? AND source='human'`,
			auditID, r.ID, r.AccountID, version, string(c.Action), r.UpdatedAt, key).Scan(&committed, &metadata); err != nil || committed != payload {
			return ErrCorrupt
		}
		var reason struct {
			Reason   string `json:"reason"`
			FromDate string `json:"from_date"`
		}
		if decodeReceipt(metadata, &reason) != nil || reason.Reason != c.Reason || !validDate(reason.FromDate) {
			return ErrCorrupt
		}
		current, err := scanAccountRecord(tx.QueryRowContext(ctx, accountRecordSelect+` WHERE account_id=? AND id=?`, r.AccountID, r.ID))
		currentVersion, versionErr := positiveInteger(current.Version)
		if err != nil || versionErr != nil || currentVersion < version {
			return ErrCorrupt
		}
		return nil
	case reportedAccountIntent:
		var result accountJSON
		if err := decodeReceipt(payload, &result); err != nil {
			return err
		}
		want := accountJSON{ID: c.Input.ID, Name: c.Input.Name, Currency: c.Input.Currency, OpeningDate: c.Input.OpeningDate, Version: "1"}
		if !reflect.DeepEqual(result, want) {
			return ErrCorrupt
		}
		var action, raw, correlation string
		if err := tx.QueryRowContext(ctx, `SELECT action,after_json,correlation_id FROM audit_log
			WHERE id=? AND entity_type='account' AND entity_id=? AND account_id=? AND version=1 AND source='human'`,
			auditID, want.ID, want.ID).Scan(&action, &raw, &correlation); err != nil {
			return ErrCorrupt
		}
		var audited accountJSON
		if action != "create" || correlation != key || decodeReceipt(raw, &audited) != nil || !reflect.DeepEqual(audited, want) {
			return ErrCorrupt
		}
		info, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, want.ID))
		if err != nil || !reflect.DeepEqual(publicAccount(info), want) {
			return ErrCorrupt
		}
		return nil
	default:
		return ErrCorrupt
	}
}

func validateImportReceipt(ctx context.Context, tx *sql.Tx, result ImportResult, p *ImportPreview, key string, auditID int64) error {
	if result.AccountID == "" || result.BatchID == "" || result.ImportedCount != len(p.Rows) {
		return ErrCorrupt
	}
	stored, err := readImport(ctx, tx, result.AccountID)
	if err != nil || stored.AuditID != auditID || stored.BatchID != result.BatchID || stored.Metadata.Digest != p.Digest ||
		!reflect.DeepEqual(stored.Manifest.Metadata, p.Metadata) || !reflect.DeepEqual(stored.Manifest.Summary, p.Summary) ||
		stored.Correlation == "" || result.Duplicate == (stored.Correlation == key) {
		return ErrCorrupt
	}
	if err := validateStoredImport(ctx, tx, stored, p.Rows); err != nil {
		return err
	}
	for i, id := range stored.Metadata.RecordIDs {
		var raw, metadata, stamp, correlation, source string
		if err := tx.QueryRowContext(ctx, `SELECT after_json,metadata_json,recorded_at,correlation_id,source
			FROM audit_log WHERE entity_type='import_row' AND entity_id=? AND account_id=?
			AND version=1 AND action='import'`, id, result.AccountID).
			Scan(&raw, &metadata, &stamp, &correlation, &source); err != nil {
			return ErrCorrupt
		}
		var row ImportedRow
		var detail struct {
			BatchID         int64  `json:"batch_id"`
			SourceRow       int    `json:"source_row"`
			AccountRecordID string `json:"account_record_id"`
		}
		if decodeReceipt(raw, &row) != nil || decodeReceipt(metadata, &detail) != nil ||
			!reflect.DeepEqual(row, p.Rows[i]) || strconv.FormatInt(detail.BatchID, 10) != result.BatchID ||
			detail.SourceRow != row.SourceRow || detail.AccountRecordID != "import-"+id ||
			stamp != stored.ImportedAt || correlation != stored.Correlation || source != "human" {
			return ErrCorrupt
		}
	}
	return nil
}
