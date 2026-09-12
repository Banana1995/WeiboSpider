package ledger

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreRejectsInconsistentPersistence(t *testing.T) {
	for _, scenario := range []string{"missing_revision", "changed_column", "changed_payload", "missing_current_field", "changed_sequence", "receipt_link", "receipt_payload", "old_revision_shape", "missing_old_revision"} {
		t.Run(scenario, func(t *testing.T) {
			f := reportedFixture(t)
			manualSourceAccount(t, f.store, "b")
			payload := `{"id":"manual-a","entry":{"kind":"cash_flow","date":"2020-01-01","flow":"1.00","total_assets":null,"note":"original"}}`
			f.request(t, "POST", "/accounts/a/records", "create", payload, 201)
			check := "read"
			statement := ""
			switch scenario {
			case "missing_revision":
				statement = `DELETE FROM idempotency_receipts WHERE key='create'; DELETE FROM audit_log WHERE entity_type='account_record'`
			case "changed_column":
				statement = `UPDATE account_records SET flow_minor=200`
			case "changed_payload":
				statement = `UPDATE audit_log SET after_json='{}' WHERE entity_type='account_record'`
			case "missing_current_field":
				statement = `UPDATE account_records SET payload=json_remove(payload,'$.voided'); UPDATE audit_log SET after_json=json_remove(after_json,'$.voided') WHERE entity_type='account_record'`
			case "changed_sequence":
				statement = `UPDATE account_records SET payload=json_set(payload,'$.sequence','999'); UPDATE audit_log SET after_json=json_set(after_json,'$.sequence','999') WHERE entity_type='account_record'`
			case "receipt_link":
				statement = `UPDATE idempotency_receipts SET audit_id=(SELECT min(id) FROM audit_log WHERE entity_type='account') WHERE key='create'`
				check = "receipt"
			case "receipt_payload":
				statement = `UPDATE idempotency_receipts SET response_json='{}' WHERE key='create'`
				check = "receipt"
			default:
				f.request(t, "PUT", "/accounts/a/records/manual-a", "edit", `{"expected_version":"1","reason":"correct","entry":{"kind":"cash_flow","date":"2020-01-01","flow":"2.00"}}`, 200)
				check = "revisions"
				if scenario == "old_revision_shape" {
					statement = `UPDATE audit_log SET after_json=json_remove(after_json,'$.voided') WHERE entity_type='account_record' AND version=1`
				} else {
					statement = `DELETE FROM idempotency_receipts WHERE key='create'; DELETE FROM audit_log WHERE entity_type='account_record' AND version=1`
				}
			}
			allowAuditCorruption(t, f.store.db)
			_, err := f.store.db.ExecContext(t.Context(), statement)
			require.NoError(t, err)
			before := f.snapshot(t)
			switch check {
			case "receipt":
				f.request(t, "POST", "/accounts/a/records", "create", payload, 500)
			case "revisions":
				f.request(t, "GET", "/accounts/a/records/manual-a/revisions", "", "", 500)
			default:
				f.request(t, "GET", "/accounts/a/records", "", "", 500)
				f.request(t, "GET", "/accounts/a/analysis-basis", "", "", 500)
			}
			require.Equal(t, before, f.snapshot(t))
			f.request(t, "GET", "/accounts/b/analysis-basis", "", "", 200)
		})
	}
}

func TestRecordWriterFreezesCallerInputBeforeItsAuditTransaction(t *testing.T) {
	f := reportedFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	writer := NewStore(f.store.db, func() time.Time { close(entered); <-release; return f.store.now() })
	amount := Money(123)
	command := AccountRecordCommand{Action: CreateOperation, AccountID: "a", ID: "manual-frozen", Entry: &AccountEntry{Kind: "cash_flow", Date: "2020-01-01", Flow: &amount, Note: "Original"}}
	type result struct {
		data json.RawMessage
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := writer.WriteAccountRecord(t.Context(), "frozen-input", command)
		done <- result{data, err}
	}()
	<-entered
	amount = 999
	command.Entry.Note = "Changed by caller"
	close(release)
	saved := <-done
	require.NoError(t, saved.err)
	var record AccountRecord
	require.NoError(t, json.Unmarshal(saved.data, &record))
	require.Equal(t, Money(123), *record.Flow)
	require.Equal(t, "Original", record.Note)
	command.Entry = &AccountEntry{Kind: "cash_flow", Date: "2020-01-01", Flow: replayMoney(123), Note: "Original"}
	retry, err := f.store.WriteAccountRecord(t.Context(), "frozen-input", command)
	require.NoError(t, err)
	require.Equal(t, saved.data, retry)
}
