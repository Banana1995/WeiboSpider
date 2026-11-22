package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

type ImportResult struct {
	importReceipt
	// Only mixed imports need coordinates; all other rows in the same preview
	// were skipped. Do not store a second copy of the workbook in the receipt.
	AddedSourceRows []int `json:"added_source_rows,omitempty"`
}

// The original frozen receipt shape is independent of the UI's row projection.
type importReceipt struct {
	AccountID     string `json:"account_id"`
	BatchID       string `json:"batch_id"`
	ImportedCount int    `json:"imported_count"`
	SkippedCount  int    `json:"skipped_count,omitempty"`
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
	Digest        string    `json:"digest"`
	CreateAccount bool      `json:"create_account"`
	RecordIDs     *[]string `json:"record_ids,omitempty"` // legacy manifests, including explicit []
	SkippedCount  int       `json:"skipped_count,omitempty"`
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

// ConfirmAccountImport reparses caller bytes and adds missing source occurrences.
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
	var result importReceipt
	var added []ImportedRow
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		receipt, found, err := loadReceipt(ctx, tx, key, "import", string(request))
		if err != nil {
			return err
		}

		var deleted bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit_log WHERE entity_type='account_delete' AND account_id=?)`, id).Scan(&deleted); err != nil {
			return err
		}
		if deleted {
			if create {
				return ErrConflict
			}
			return ErrNotFound
		}
		history, err := readImportHistory(ctx, tx, id, p)
		if err != nil {
			return err
		}
		if found {
			if decodeReceipt(receipt.Response, &result) != nil {
				return ErrCorrupt
			}
			if err := validateImportReceipt(history.match, result, key, receipt.AuditID); err != nil {
				return err
			}
			if !result.Duplicate {
				added = history.matchAdded
			}
			return nil
		}
		if stored := history.match; stored != nil {
			if create && !stored.Metadata.CreateAccount {
				return ErrConflict
			}
			result = stored.result(key)
			response, _ := json.Marshal(result)
			return saveReceipt(ctx, tx, key, "import", string(request), string(response), stored.AuditID)
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
		action := "initialize"
		if history.latest != nil {
			action = "append"
		} else if exists {
			var occupied bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_records WHERE account_id=?)`, id).Scan(&occupied); err != nil {
				return err
			}
			if occupied {
				return importConflict("initialization_requires_empty_account")
			}
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
		if err := tx.QueryRowContext(ctx, nextAccountSequence).Scan(&nextSequence); err != nil {
			return err
		}
		added = history.counts.missing(p.Rows)
		for _, row := range added {
			if err := history.summary.add(row); err != nil {
				return err
			}
		}
		manifest := importManifest{Metadata: p.Metadata, Summary: p.Summary}
		manifestMetadata := importManifestMetadata{Digest: p.Digest, CreateAccount: create, SkippedCount: len(p.Rows) - len(added)}
		manifestAuditID, err := appendAudit(ctx, tx, key, action, "import", strconv.FormatInt(batch, 10), id, 1, stamp, "human", nil, manifest, manifestMetadata)
		if err != nil {
			return err
		}
		for i, row := range added {
			recordID := strconv.FormatInt(nextSequence+int64(i), 10)
			if _, err := appendAudit(ctx, tx, key, "import", "import_row", recordID, id, 1, stamp, "human", nil, row,
				map[string]any{"batch_id": batch, "source_row": row.SourceRow, "account_record_id": "import-" + recordID}); err != nil {
				return err
			}
			next := AccountRecord{ID: "import-" + recordID, AccountID: id,
				AccountEntry: AccountEntry{Kind: row.Kind, Date: row.Date, Flow: row.Flow, TotalAssets: row.TotalAssets, Note: row.Note, ChannelAssets: importChannelAssets(row.Detail, row.TotalAssets)},
				Sequence:     recordID, Origin: "import", Original: &row, Version: "1", CreatedAt: stamp, UpdatedAt: stamp}
			if _, err := putAccountRecord(ctx, tx, &next, nil, key, "Excel import", "human"); err != nil {
				return err
			}
		}
		result = importReceipt{AccountID: id, BatchID: strconv.FormatInt(batch, 10), ImportedCount: len(added), SkippedCount: manifestMetadata.SkippedCount, Duplicate: len(added) == 0}
		response, _ := json.Marshal(result)
		return saveReceipt(ctx, tx, key, "import", string(request), string(response), manifestAuditID)
	})
	if err != nil {
		return ImportResult{}, err
	}
	out := ImportResult{importReceipt: result}
	if !result.Duplicate && result.SkippedCount > 0 {
		out.AddedSourceRows = make([]int, len(added))
		for i, row := range added {
			out.AddedSourceRows[i] = row.SourceRow
		}
	}
	return out, nil
}

func (s *Store) ImportSummary(ctx context.Context, id string) (ImportedSummary, error) {
	if !validID(id) {
		return ImportedSummary{}, ErrQuery
	}
	var result ImportedSummary
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		history, err := readImportHistory(ctx, tx, id, nil)
		if err != nil {
			return err
		}
		stored := history.latest
		if stored == nil {
			return ErrNotFound
		}
		result = ImportedSummary{BatchID: stored.BatchID, Metadata: stored.Manifest.Metadata, Summary: history.summary, ImportedAt: stored.ImportedAt}
		return nil
	})
	return result, err
}

func (s *Store) ImportedRecords(ctx context.Context, id string, q OperationQuery) ([]ImportedRecord, error) {
	if !validID(id) || q.Limit < 1 || q.Limit > 100 || q.From != "" && !validDate(q.From) || q.To != "" && !validDate(q.To) || q.From != "" && q.To != "" && q.From > q.To || q.BeforeDate != "" && (!validDate(q.BeforeDate) || q.BeforeSourceRow < 5 || q.BeforeSourceRow > maxImportRows+4 || q.BeforeSequence < 0) || q.BeforeDate == "" && (q.BeforeSourceRow != 0 || q.BeforeSequence != 0) {
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
		where := ` WHERE a.account_id=? AND a.entity_type='import_row'`
		const day = `json_extract(a.after_json,'$.date')`
		const sourceRow = `CAST(json_extract(a.after_json,'$.source_row') AS INTEGER)`
		const sequence = `CAST(a.entity_id AS INTEGER)`
		args := []any{id}
		if q.From != "" {
			where += ` AND ` + day + `>=?`
			args = append(args, q.From)
		}
		if q.To != "" {
			where += ` AND ` + day + `<=?`
			args = append(args, q.To)
		}
		if q.BeforeDate != "" {
			where += ` AND (` + day + `,` + sourceRow + `,` + sequence + `) < (?,?,?)`
			args = append(args, q.BeforeDate, q.BeforeSourceRow, q.BeforeSequence)
		}
		args = append(args, q.Limit)
		rows, err := tx.QueryContext(ctx, importSourceSelect+where+` ORDER BY `+day+` DESC,`+sourceRow+` DESC,`+sequence+` DESC LIMIT ?`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, _, err := scanImportSource(rows)
			if err != nil {
				return err
			}
			result = append(result, r)
		}
		return rows.Err()
	})
	return result, err
}
