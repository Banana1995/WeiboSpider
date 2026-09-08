package ledger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Administrative SQL fault injection, not an HTTP audit-mutation capability.
func TestReviewReceiptPayloadAndBatchIndexCorruption(t *testing.T) {
	for _, damage := range []string{"audit-index", "unknown-field", "original-flag", "duplicate-flag"} {
		t.Run("import-"+damage, func(t *testing.T) {
			f := newHTTPFixture(t)
			data := syntheticImport(t)
			p := parseSynthetic(t, data)
			_, err := f.store.ConfirmAccountImport(t.Context(), "a", "original", p.Digest, true, data)
			require.NoError(t, err)
			key := "original"
			allowAuditCorruption(t, f.store.db)
			statement := `UPDATE idempotency_receipts SET audit_id=(SELECT min(id) FROM audit_log) WHERE key=?`
			switch damage {
			case "unknown-field":
				statement = `UPDATE idempotency_receipts SET response_json=json_set(response_json,'$.unknown','synthetic') WHERE key=?`
			case "original-flag":
				statement = `UPDATE idempotency_receipts SET response_json=json_set(response_json,'$.duplicate',json('true')) WHERE key=?`
			case "duplicate-flag":
				key = "duplicate"
				before := auditCount(t, f.store)
				result, err := f.store.ConfirmAccountImport(t.Context(), "a", key, p.Digest, true, data)
				require.NoError(t, err)
				require.True(t, result.Duplicate)
				require.Equal(t, before, auditCount(t, f.store))
				statement = `UPDATE idempotency_receipts SET response_json=json_set(response_json,'$.duplicate',json('false')) WHERE key=?`
			}
			_, err = f.store.db.ExecContext(t.Context(), statement, key)
			require.NoError(t, err)
			before := auditCount(t, f.store)
			_, err = f.store.ConfirmAccountImport(t.Context(), "a", key, p.Digest, true, data)
			require.ErrorIs(t, err, ErrCorrupt)
			require.Equal(t, before, auditCount(t, f.store))
		})
	}
	for _, damage := range []string{"unknown-field", "identity", "duplicate-json-key"} {
		t.Run("manual-"+damage, func(t *testing.T) {
			f := reportedFixture(t)
			m := Money(100)
			c := AccountRecordCommand{Action: CreateOperation, AccountID: "a", ID: "manual-a", Entry: &AccountEntry{Kind: "asset", Date: "2020-01-01", TotalAssets: &m}}
			_, err := f.store.WriteAccountRecord(t.Context(), "original", c)
			require.NoError(t, err)
			allowAuditCorruption(t, f.store.db)
			statement := `UPDATE idempotency_receipts SET response_json=json_set(response_json,'$.unknown','synthetic') WHERE key='original'`
			if damage == "identity" {
				statement = `UPDATE idempotency_receipts SET response_json=json_set(response_json,'$.id','manual-other') WHERE key='original'`
			}
			if damage == "duplicate-json-key" {
				statement = `UPDATE idempotency_receipts SET response_json='{"id":"manual-a",'||substr(response_json,2) WHERE key='original'`
			}
			_, err = f.store.db.ExecContext(t.Context(), statement)
			require.NoError(t, err)
			before := auditCount(t, f.store)
			_, err = f.store.WriteAccountRecord(t.Context(), "original", c)
			require.ErrorIs(t, err, ErrCorrupt)
			require.Equal(t, before, auditCount(t, f.store))
		})
	}
}
