package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
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

// Read just the batch needed for compatibility (by digest), or the latest
// contributing batch for the summary. Old record_ids are not row identities.
func readImportBatch(ctx context.Context, tx *sql.Tx, id, digest string) (*storedImport, error) {
	query := `SELECT id,entity_id,recorded_at,correlation_id,action,after_json,metadata_json FROM audit_log
	 WHERE entity_type='import' AND account_id=? AND EXISTS(SELECT 1 FROM accounts WHERE id=?)`
	args := []any{id, id}
	if digest != "" {
		query += ` AND json_extract(metadata_json,'$.digest')=?`
		args = append(args, digest)
	} else {
		query += ` AND coalesce(json_extract(metadata_json,'$.skipped_count'),0)<json_extract(after_json,'$.summary.row_count')`
	}
	b := &storedImport{AccountID: id}
	var action, manifest, metadata string
	err := tx.QueryRowContext(ctx, query+` ORDER BY id DESC LIMIT 1`, args...).Scan(&b.AuditID, &b.BatchID, &b.ImportedAt, &b.Correlation, &action, &manifest, &metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := positiveInteger(b.BatchID); err != nil || b.Correlation == "" || decodeReceipt(manifest, &b.Manifest) != nil || decodeReceipt(metadata, &b.Metadata) != nil || len(b.Metadata.Digest) != 64 ||
		b.Manifest.Summary.RowCount < 1 || b.Manifest.Summary.RowCount > maxImportRows || b.Metadata.SkippedCount < 0 || b.Metadata.SkippedCount > b.Manifest.Summary.RowCount ||
		b.Metadata.RecordIDs != nil && len(*b.Metadata.RecordIDs) != b.addedCount() || action != "initialize" && action != "append" ||
		action == "initialize" && b.Metadata.SkippedCount != 0 || action == "append" && b.Metadata.CreateAccount {
		return nil, ErrCorrupt
	}
	return b, nil
}

// The import_row is the source. Its effective record may have changed or been
// deliberately removed; only an authenticated removal can explain its absence.
const importSourceSelect = `SELECT a.entity_id,a.after_json,a.metadata_json,
 CASE WHEN r.id IS NULL THEN json_extract(d.before_json,'$.original')
 WHEN r.origin='import' AND r.sequence=CAST(a.entity_id AS INTEGER) AND EXISTS(SELECT 1 FROM audit_log c WHERE c.entity_type='account_record' AND c.entity_id=r.id AND c.account_id=r.account_id AND c.version=r.version AND c.after_json=r.payload) THEN json_extract(r.payload,'$.original') END,
 (a.action='import' AND a.version=1 AND a.source='human')
 FROM audit_log a LEFT JOIN account_records r ON r.account_id=a.account_id AND r.id='import-'||a.entity_id
 LEFT JOIN audit_log d ON d.entity_type='account_record' AND d.action='remove_duplicate' AND d.account_id=a.account_id AND d.entity_id='import-'||a.entity_id`

func scanImportSource(rows *sql.Rows) (r ImportedRecord, batch string, err error) {
	var raw, meta string
	var original sql.NullString
	var verified bool
	if err = rows.Scan(&r.ID, &raw, &meta, &original, &verified); err != nil {
		return
	}
	var detail struct {
		BatchID         int64  `json:"batch_id"`
		SourceRow       int    `json:"source_row"`
		AccountRecordID string `json:"account_record_id"`
	}
	if _, e := positiveInteger(r.ID); e != nil || !verified || !original.Valid || decodeReceipt(raw, &r.ImportedRow) != nil || decodeReceipt(meta, &detail) != nil || detail.AccountRecordID != "import-"+r.ID || detail.SourceRow != r.SourceRow || r.SourceRow < 5 || r.SourceRow > maxImportRows+4 || detail.BatchID < 1 {
		return r, "", ErrCorrupt
	}
	if original.String != raw {
		var other ImportedRow
		if decodeReceipt(original.String, &other) != nil || !reflect.DeepEqual(other, r.ImportedRow) {
			return r, "", ErrCorrupt
		}
	}
	return r, strconv.FormatInt(detail.BatchID, 10), nil
}

func readImportHistory(ctx context.Context, tx *sql.Tx, id string, p *ImportPreview) (importHistory, error) {
	h := importHistory{counts: make(importCounts)}
	var err error
	h.latest, err = readImportBatch(ctx, tx, id, "")
	if err != nil || h.latest == nil {
		return h, err
	}
	var coords map[int]ImportedRow
	if p != nil {
		h.match, err = readImportBatch(ctx, tx, id, p.Digest)
		if err != nil {
			return h, err
		}
		if b := h.match; b != nil {
			if p.Metadata != b.Manifest.Metadata || !reflect.DeepEqual(p.Summary, b.Manifest.Summary) {
				return h, ErrCorrupt
			}
			coords = make(map[int]ImportedRow, len(p.Rows))
			for _, r := range p.Rows {
				coords[r.SourceRow] = r
			}
		}
	}
	rows, err := tx.QueryContext(ctx, importSourceSelect+` WHERE a.account_id=? AND a.entity_type='import_row' ORDER BY a.id`, id)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		r, b, err := scanImportSource(rows)
		if err != nil {
			return h, err
		}
		if err := h.summary.add(r.ImportedRow); err != nil {
			return h, ErrCorrupt
		}
		if p != nil {
			h.counts[importRowKey(r.ImportedRow)]++
		}
		if h.match != nil && b == h.match.BatchID {
			if !reflect.DeepEqual(r.ImportedRow, coords[r.SourceRow]) {
				return h, ErrCorrupt
			}
			delete(coords, r.SourceRow)
			h.matchAdded = append(h.matchAdded, r.ImportedRow)
		}
	}
	if err := rows.Err(); err != nil {
		return h, err
	}
	if h.match != nil && (len(h.matchAdded) != h.match.addedCount() || len(h.counts.missing(p.Rows)) != 0) {
		return h, ErrCorrupt
	}
	return h, nil
}

func (b storedImport) addedCount() int { return b.Manifest.Summary.RowCount - b.Metadata.SkippedCount }

func (b storedImport) result(key string) importReceipt {
	added := 0
	if key == b.Correlation {
		added = b.addedCount()
	}
	return importReceipt{AccountID: b.AccountID, BatchID: b.BatchID, ImportedCount: added, SkippedCount: b.Manifest.Summary.RowCount - added, Duplicate: added == 0}
}
