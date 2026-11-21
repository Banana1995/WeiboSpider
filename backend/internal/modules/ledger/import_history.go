package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"reflect"
	"strconv"
)

// Row numbers and Excel date spellings are evidence, not business identity.
// Keep null amounts distinct from zero, and use the normalized creation time.
func importRowKey(row ImportedRow) [32]byte {
	row.SourceRow, row.DateRaw, row.CreatedRaw = 0, "", ""
	b, _ := json.Marshal(row)
	return sha256.Sum256(b)
}

type importCounts map[[32]byte]int

// Match occurrences one-for-one against earlier imports only. Two otherwise
// identical transfers in the same workbook can both be real.
func (prior importCounts) missing(rows []ImportedRow) []ImportedRow {
	seen := make(importCounts)
	var added []ImportedRow
	for _, row := range rows {
		k := importRowKey(row)
		seen[k]++
		if seen[k] > prior[k] {
			added = append(added, row)
		}
	}
	return added
}

type importHistory struct {
	counts     importCounts
	summary    ImportSummary
	latest     *storedImport // last batch that actually added records
	match      *storedImport // batch for the exact requested file, if any
	matchAdded []ImportedRow // added at the matching batch, before later imports
}

func readImportBatches(ctx context.Context, tx *sql.Tx, id string) ([]storedImport, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,entity_id,recorded_at,correlation_id,action,after_json,metadata_json
		FROM audit_log WHERE entity_type='import' AND account_id=? AND EXISTS(SELECT 1 FROM accounts WHERE id=?) ORDER BY id`, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batches []storedImport
	for rows.Next() {
		b := storedImport{AccountID: id}
		var action, manifest, metadata string
		if err := rows.Scan(&b.AuditID, &b.BatchID, &b.ImportedAt, &b.Correlation, &action, &manifest, &metadata); err != nil {
			return nil, err
		}
		if _, err := positiveInteger(b.BatchID); err != nil || b.Correlation == "" ||
			decodeReceipt(manifest, &b.Manifest) != nil || decodeReceipt(metadata, &b.Metadata) != nil || len(b.Metadata.Digest) != 64 ||
			b.Manifest.Summary.RowCount < 1 || b.Manifest.Summary.RowCount > maxImportRows ||
			b.Metadata.SkippedCount < 0 || b.Metadata.SkippedCount != b.Manifest.Summary.RowCount-len(b.Metadata.RecordIDs) ||
			len(batches) == 0 && (action != "initialize" || b.Metadata.SkippedCount != 0) || len(batches) > 0 && (action != "append" || b.Metadata.CreateAccount) {
			return nil, ErrCorrupt
		}
		batches = append(batches, b)
	}
	return batches, rows.Err()
}

// Stream only this account's original Excel evidence, including rows whose
// effective record has since been edited or voided. The manifest stores just
// the IDs introduced by that batch (all rows for pre-incremental manifests).
func readImportHistory(ctx context.Context, tx *sql.Tx, id string, p *ImportPreview) (importHistory, error) {
	h := importHistory{counts: make(importCounts)}
	batches, err := readImportBatches(ctx, tx, id)
	if err != nil || len(batches) == 0 {
		return h, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.entity_id,a.after_json,a.metadata_json,a.correlation_id,a.recorded_at,json_extract(r.payload,'$.original')
		FROM audit_log a JOIN account_records r ON r.account_id=a.account_id AND r.id='import-'||a.entity_id AND r.sequence=CAST(a.entity_id AS INTEGER)
		WHERE a.account_id=? AND a.entity_type='import_row' AND a.action='import' AND a.version=1 AND a.source='human' AND r.origin='import'
		AND EXISTS(SELECT 1 FROM audit_log c WHERE c.account_id=r.account_id AND c.entity_type='account_record' AND c.entity_id=r.id AND c.version=r.version AND c.after_json=r.payload)
		ORDER BY a.id`, id)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for i := range batches {
		b := &batches[i]
		var expected []ImportedRow
		if p != nil && p.Digest == b.Metadata.Digest {
			h.match = b
			expected = h.counts.missing(p.Rows)
			if p.Metadata != b.Manifest.Metadata || !reflect.DeepEqual(p.Summary, b.Manifest.Summary) || len(expected) != len(b.Metadata.RecordIDs) {
				return h, ErrCorrupt
			}
			h.matchAdded = expected
		}
		for j, rowID := range b.Metadata.RecordIDs {
			if !rows.Next() {
				if err := rows.Err(); err != nil {
					return h, err
				}
				return h, ErrCorrupt
			}
			var gotID, raw, meta, key, stamp, original string
			if err := rows.Scan(&gotID, &raw, &meta, &key, &stamp, &original); err != nil {
				return h, err
			}
			var row ImportedRow
			var detail struct {
				BatchID         int64  `json:"batch_id"`
				SourceRow       int    `json:"source_row"`
				AccountRecordID string `json:"account_record_id"`
			}
			if gotID != rowID || key != b.Correlation || stamp != b.ImportedAt || decodeReceipt(raw, &row) != nil || decodeReceipt(meta, &detail) != nil ||
				strconv.FormatInt(detail.BatchID, 10) != b.BatchID || detail.AccountRecordID != "import-"+rowID || detail.SourceRow != row.SourceRow ||
				h.match == b && !reflect.DeepEqual(row, expected[j]) {
				return h, ErrCorrupt
			}
			if original != raw { // equivalent JSON writers may use different escaping
				var r ImportedRow
				if decodeReceipt(original, &r) != nil || !reflect.DeepEqual(r, row) {
					return h, ErrCorrupt
				}
			}
			if err := h.summary.add(row); err != nil {
				return h, ErrCorrupt
			}
			if p != nil {
				h.counts[importRowKey(row)]++
			}
			h.latest = b
		}
	}
	if rows.Next() {
		return h, ErrCorrupt
	}
	return h, rows.Err()
}

func (b storedImport) result(key string) importReceipt {
	added := 0
	if key == b.Correlation {
		added = len(b.Metadata.RecordIDs)
	}
	return importReceipt{AccountID: b.AccountID, BatchID: b.BatchID, ImportedCount: added, SkippedCount: b.Manifest.Summary.RowCount - added, Duplicate: added == 0}
}
