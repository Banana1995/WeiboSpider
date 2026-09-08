package ledger

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreRejectsInconsistentPersistence(t *testing.T) {
	for _, scenario := range []string{"missing_revision", "changed_column", "changed_payload", "receipt_link", "receipt_payload", "old_revision_shape", "missing_old_revision"} {
		t.Run(scenario, func(t *testing.T) {
			db, err := Open(t.Context(), t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			s := NewStore(db, func() time.Time { return time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC) })
			require.NoError(t, s.InitializeAccount(t.Context(), "Synthetic", Opening{AccountID: "a", Currency: CNY, Date: "2026-01-01"}))
			create := Command{Action: CreateOperation, Key: "create", Reason: "Synthetic initial record", Operation: Operation{
				ID: "op", Kind: Deposit, AccountID: "a", Date: "2026-01-02", Sequence: 1, Amount: 100}}
			_, err = s.Write(t.Context(), create)
			require.NoError(t, err)
			sql := ""
			check := "state"
			switch scenario {
			case "missing_revision":
				// Remove the dependent receipt before its referenced audit event.
				sql = `DELETE FROM idempotency_receipts; DELETE FROM audit_log WHERE entity_type='operation'`
			case "changed_column":
				sql = `UPDATE operations SET amount_minor=200`
			case "changed_payload":
				sql = `UPDATE audit_log SET after_json='{}' WHERE entity_type='operation'`
			case "receipt_link":
				sql = `UPDATE idempotency_receipts SET audit_id=(SELECT id FROM audit_log WHERE entity_type='account')`
				check = "receipt"
			case "receipt_payload":
				sql = `UPDATE idempotency_receipts SET response_json='{}'`
				check = "receipt"
			case "old_revision_shape", "missing_old_revision":
				change := create
				change.Action, change.Key, change.ExpectedVersion, change.Operation.Amount = ReplaceOperation, "change", 1, 200
				_, err = s.Write(t.Context(), change)
				require.NoError(t, err)
				check = "revisions"
				if scenario == "old_revision_shape" {
					// A missing zero-valued field must not pass permissive Unmarshal.
					sql = `UPDATE audit_log SET after_json=json_remove(after_json,'$.operation.Voided') WHERE entity_type='operation' AND version=1`
				} else {
					sql = `DELETE FROM idempotency_receipts WHERE key='create'; DELETE FROM audit_log WHERE entity_type='operation' AND version=1`
				}
			}
			allowAuditCorruption(t, db)
			_, err = db.ExecContext(t.Context(), sql)
			require.NoError(t, err)
			switch check {
			case "state":
				book, err := s.State(t.Context())
				require.ErrorIs(t, err, ErrCorrupt)
				require.Nil(t, book)
				create.Key, create.Operation.ID, create.Operation.Sequence = "another", "another", 2
				record, err := s.Write(t.Context(), create)
				require.ErrorIs(t, err, ErrCorrupt)
				require.Equal(t, Record{}, record)
			case "receipt":
				record, err := s.Write(t.Context(), create)
				require.ErrorIs(t, err, ErrCorrupt)
				require.Equal(t, Record{}, record)
			case "revisions":
				revisions, err := s.Revisions(t.Context(), "op")
				require.ErrorIs(t, err, ErrCorrupt)
				require.Nil(t, revisions)
			}
		})
	}
}
