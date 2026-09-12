package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
)

type ImportResult struct {
	AccountID     string `json:"account_id"`
	BatchID       string `json:"batch_id"`
	ImportedCount int    `json:"imported_count"`
	Duplicate     bool   `json:"duplicate"`
}

type ImportedRecord struct {
	ID string `json:"id"`
	ImportedRow
}

type ImportedSummary struct {
	BatchID    string         `json:"batch_id"`
	Metadata   ImportMetadata `json:"metadata"`
	Summary    ImportSummary  `json:"summary"`
	ImportedAt string         `json:"imported_at"`
}

type importIntent struct {
	AccountID string `json:"account_id"`
	Digest    string `json:"digest"`
	Create    bool   `json:"create_account"`
}

type importManifest struct {
	Metadata ImportMetadata `json:"metadata"`
	Summary  ImportSummary  `json:"summary"`
}

type importManifestMetadata struct {
	Digest        string   `json:"digest"`
	CreateAccount bool     `json:"create_account"`
	RecordIDs     []string `json:"record_ids"`
}

type storedImport struct {
	AuditID     int64
	BatchID     string
	AccountID   string
	ImportedAt  string
	Correlation string
	Manifest    importManifest
	Metadata    importManifestMetadata
}

func readImport(ctx context.Context, tx *sql.Tx, accountID string) (storedImport, error) {
	var result storedImport
	var manifest, metadata string
	err := tx.QueryRowContext(ctx, `SELECT id,entity_id,account_id,recorded_at,correlation_id,after_json,metadata_json
		FROM audit_log WHERE entity_type='import' AND account_id=? AND action='initialize'`, accountID).
		Scan(&result.AuditID, &result.BatchID, &result.AccountID, &result.ImportedAt, &result.Correlation, &manifest, &metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if _, err := positiveInteger(result.BatchID); err != nil || decodeReceipt(manifest, &result.Manifest) != nil || decodeReceipt(metadata, &result.Metadata) != nil ||
		result.Metadata.Digest == "" || len(result.Metadata.RecordIDs) != result.Manifest.Summary.RowCount {
		return storedImport{}, ErrCorrupt
	}
	return result, nil
}

// ConfirmAccountImport reparses caller bytes and initializes one account timeline.
// Canonical rows are account_records; the manifest and exact source rows are audit.
func (s *Store) ConfirmAccountImport(ctx context.Context, id, key, digest string, create bool, data []byte) (ImportResult, error) {
	if !validID(id) || !validID(key) {
		return ImportResult{}, invalidImport("identity", 0, "")
	}
	p, err := ParseAccountImport(ctx, data)
	if err != nil {
		return ImportResult{}, err
	}
	if digest != p.Digest {
		return ImportResult{}, &importError{Code: "preview_mismatch", Status: 400}
	}
	intent := importIntent{AccountID: id, Digest: p.Digest, Create: create}
	request, _ := json.Marshal(intent)
	var result ImportResult
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		receipt, found, err := loadReceipt(ctx, tx, key, "import", string(request))
		if err != nil {
			return err
		}
		if found {
			if decodeReceipt(receipt.Response, &result) != nil {
				return ErrCorrupt
			}
			return validateImportReceipt(ctx, tx, result, p, key, receipt.AuditID)
		}

		stored, importErr := readImport(ctx, tx, id)
		if importErr == nil {
			if stored.Metadata.Digest != p.Digest {
				return importConflict("import_already_exists")
			}
			if create && !stored.Metadata.CreateAccount {
				return ErrConflict
			}
			result = ImportResult{AccountID: id, BatchID: stored.BatchID, ImportedCount: len(p.Rows), Duplicate: true}
			if err := validateImportReceipt(ctx, tx, result, p, key, stored.AuditID); err != nil {
				return err
			}
			response, _ := json.Marshal(result)
			return saveReceipt(ctx, tx, key, "import", string(request), string(response), stored.AuditID)
		}
		if !errors.Is(importErr, ErrNotFound) {
			return importErr
		}

		var currency Currency
		err = tx.QueryRowContext(ctx, `SELECT currency FROM accounts WHERE id=?`, id).Scan(&currency)
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if exists && create {
			return ErrConflict
		}
		if !exists && !create {
			return ErrNotFound
		}
		if exists && currency != p.Metadata.Currency {
			return importConflict("currency_mismatch")
		}
		var occupied bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_records WHERE account_id=?)`, id).Scan(&occupied); err != nil {
			return err
		}
		if occupied {
			return importConflict("initialization_requires_empty_account")
		}
		_, stamp, err := s.cutoff()
		if err != nil {
			return err
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, `INSERT INTO accounts
				(id,name,currency,opening_date,opening_cash_minor,version)
				VALUES(?,?,?,?,0,1)`, id, p.Metadata.Name, p.Metadata.Currency, p.Summary.From); err != nil {
				return constraintError(err)
			}
			account := accountJSON{ID: id, Name: p.Metadata.Name, Currency: p.Metadata.Currency, OpeningDate: p.Summary.From, Version: "1"}
			if _, err := appendAudit(ctx, tx, key, "create", "account", id, id, 1, stamp, "human", nil, account, nil); err != nil {
				return err
			}
		}

		var batch, nextSequence int64
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(CAST(entity_id AS INTEGER)),0)+1
			FROM audit_log WHERE entity_type='import'`).Scan(&batch); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(sequence),0)+1 FROM account_records`).Scan(&nextSequence); err != nil {
			return err
		}
		recordIDs := make([]string, len(p.Rows))
		for i := range recordIDs {
			recordIDs[i] = strconv.FormatInt(nextSequence+int64(i), 10)
		}
		manifest := importManifest{Metadata: p.Metadata, Summary: p.Summary}
		manifestMetadata := importManifestMetadata{Digest: p.Digest, CreateAccount: create, RecordIDs: recordIDs}
		manifestAuditID, err := appendAudit(ctx, tx, key, "initialize", "import", strconv.FormatInt(batch, 10), id, 1, stamp, "human", nil, manifest, manifestMetadata)
		if err != nil {
			return err
		}
		for i, row := range p.Rows {
			recordID := recordIDs[i]
			if _, err := appendAudit(ctx, tx, key, "import", "import_row", recordID, id, 1, stamp, "human", nil, row,
				map[string]any{"batch_id": batch, "source_row": row.SourceRow, "account_record_id": "import-" + recordID}); err != nil {
				return err
			}
			next := AccountRecord{ID: "import-" + recordID, AccountID: id,
				AccountEntry: AccountEntry{Kind: row.Kind, Date: row.Date, Flow: row.Flow, TotalAssets: row.TotalAssets, Note: row.Note},
				Sequence:     recordID, Origin: "import", Original: &row, Version: "1", CreatedAt: stamp, UpdatedAt: stamp}
			if _, err := putAccountRecord(ctx, tx, &next, nil, key, "initialization", "human"); err != nil {
				return err
			}
		}
		result = ImportResult{AccountID: id, BatchID: strconv.FormatInt(batch, 10), ImportedCount: len(p.Rows)}
		response, _ := json.Marshal(result)
		return saveReceipt(ctx, tx, key, "import", string(request), string(response), manifestAuditID)
	})
	if err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

func (s *Store) ImportSummary(ctx context.Context, id string) (ImportedSummary, error) {
	if !validID(id) {
		return ImportedSummary{}, ErrQuery
	}
	var result ImportedSummary
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		stored, err := readImport(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := validateStoredImport(ctx, tx, stored, nil); err != nil {
			return err
		}
		result = ImportedSummary{BatchID: stored.BatchID, Metadata: stored.Manifest.Metadata, Summary: stored.Manifest.Summary, ImportedAt: stored.ImportedAt}
		return nil
	})
	return result, err
}

func validateStoredImport(ctx context.Context, tx *sql.Tx, stored storedImport, rows []ImportedRow) error {
	if len(stored.Metadata.RecordIDs) != stored.Manifest.Summary.RowCount {
		return ErrCorrupt
	}
	for i, id := range stored.Metadata.RecordIDs {
		r, err := scanAccountRecord(tx.QueryRowContext(ctx, accountRecordSelect+` WHERE account_id=? AND id=?`,
			stored.AccountID, "import-"+id))
		if err != nil || r.Origin != "import" || r.Original == nil || r.Sequence != id {
			return ErrCorrupt
		}
		if rows != nil && (i >= len(rows) || !reflect.DeepEqual(*r.Original, rows[i])) {
			return ErrCorrupt
		}
	}
	return nil
}

func (s *Store) ImportedRecords(ctx context.Context, id string, q OperationQuery) ([]ImportedRecord, error) {
	if !validID(id) || q.Limit < 1 || q.Limit > 100 || q.From != "" && !validDate(q.From) || q.To != "" && !validDate(q.To) || q.From != "" && q.To != "" && q.From > q.To || q.BeforeDate != "" && (!validDate(q.BeforeDate) || q.BeforeSequence < 5) || q.BeforeDate == "" && q.BeforeSequence != 0 {
		return nil, ErrQuery
	}
	result := make([]ImportedRecord, 0)
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=?)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		where := ` WHERE account_id=? AND origin='import'`
		args := []any{id}
		if q.From != "" {
			where += ` AND business_date>=?`
			args = append(args, q.From)
		}
		if q.To != "" {
			where += ` AND business_date<=?`
			args = append(args, q.To)
		}
		if q.BeforeDate != "" {
			where += ` AND (business_date<? OR (business_date=? AND CAST(json_extract(payload,'$.original.source_row') AS INTEGER)<?))`
			args = append(args, q.BeforeDate, q.BeforeDate, q.BeforeSequence)
		}
		args = append(args, q.Limit)
		rows, err := tx.QueryContext(ctx, accountRecordSelect+where+` ORDER BY business_date DESC,CAST(json_extract(payload,'$.original.source_row') AS INTEGER) DESC LIMIT ?`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanAccountRecord(rows)
			if err != nil || r.Original == nil || !strings.HasPrefix(r.ID, "import-") || strings.TrimPrefix(r.ID, "import-") != r.Sequence {
				return ErrCorrupt
			}
			result = append(result, ImportedRecord{ID: r.Sequence, ImportedRow: *r.Original})
		}
		return rows.Err()
	})
	return result, err
}
