-- Check the legacy manifests once, while all original business rows still exist.
-- Deleted accounts intentionally retain only their immutable evidence.
CREATE TEMP TABLE verify_import_source (ok INTEGER NOT NULL CHECK(ok=1));
INSERT INTO verify_import_source SELECT NOT EXISTS (
    SELECT 1 FROM audit_log b JOIN accounts ON accounts.id=b.account_id
    WHERE b.entity_type='import' AND json_type(b.metadata_json,'$.record_ids')='array' AND (
        json_array_length(b.metadata_json,'$.record_ids') + coalesce(json_extract(b.metadata_json,'$.skipped_count'),0) IS NOT json_extract(b.after_json,'$.summary.row_count') OR
        EXISTS (SELECT 1 FROM json_each(b.metadata_json,'$.record_ids') j WHERE NOT EXISTS (
            SELECT 1 FROM audit_log a JOIN account_records r ON r.account_id=a.account_id AND r.id='import-'||a.entity_id
            WHERE a.entity_type='import_row' AND a.account_id=b.account_id AND a.entity_id=j.value AND a.version=1
              AND a.correlation_id=b.correlation_id AND json_extract(a.metadata_json,'$.batch_id')=CAST(b.entity_id AS INTEGER)
              AND r.origin='import' AND r.sequence=CAST(a.entity_id AS INTEGER)))));
INSERT INTO verify_import_source SELECT NOT EXISTS (
    SELECT 1 FROM audit_log a JOIN accounts ON accounts.id=a.account_id
    WHERE a.entity_type='import_row' AND NOT EXISTS (
        SELECT 1 FROM audit_log b WHERE b.entity_type='import' AND b.account_id=a.account_id
          AND b.entity_id=CAST(json_extract(a.metadata_json,'$.batch_id') AS TEXT)
          AND b.correlation_id=a.correlation_id AND b.recorded_at=a.recorded_at));
DROP TABLE verify_import_source;

-- Source-to-batch identity is immutable. Verify it when evidence is created,
-- rather than looking up historical batches for every row of every comparison.
CREATE TRIGGER import_row_batch_proof BEFORE INSERT ON audit_log
WHEN NEW.entity_type='import_row' AND NOT EXISTS (
    SELECT 1 FROM audit_log b WHERE b.entity_type='import' AND b.account_id=NEW.account_id
      AND b.entity_id=CAST(json_extract(NEW.metadata_json,'$.batch_id') AS TEXT)
      AND b.correlation_id=NEW.correlation_id AND b.recorded_at=NEW.recorded_at)
BEGIN SELECT RAISE(ABORT,'invalid source batch'); END;

CREATE INDEX audit_original_rows ON audit_log(account_id, id) WHERE entity_type='import_row';
CREATE UNIQUE INDEX audit_duplicate_removed ON audit_log(account_id,entity_id) WHERE entity_type='account_record' AND action='remove_duplicate';

-- Maintenance only. The removal audit authenticates both sides at their current
-- versions; its AFTER trigger makes the evidence and the physical delete atomic.
CREATE TRIGGER validate_duplicate_removal BEFORE INSERT ON audit_log
WHEN NEW.entity_type='account_record' AND NEW.action='remove_duplicate'
BEGIN
    SELECT CASE WHEN NEW.source!='human' OR NEW.correlation_id='' OR NEW.after_json!='null' OR
        coalesce(length(trim(json_extract(NEW.metadata_json,'$.reason'))),0)=0 OR NOT EXISTS (
        SELECT 1 FROM account_records r JOIN account_records k ON k.account_id=r.account_id AND k.id!=r.id
        WHERE r.account_id=NEW.account_id AND r.id=NEW.entity_id AND r.origin='import' AND r.kind='cash_flow' AND r.voided=0
          AND r.version+1=NEW.version AND r.payload=NEW.before_json AND r.business_date=json_extract(NEW.metadata_json,'$.from_date')
          AND k.id=json_extract(NEW.metadata_json,'$.kept_id') AND k.version=json_extract(NEW.metadata_json,'$.kept_version')
          AND k.origin='import' AND k.kind='cash_flow' AND k.voided=0
          AND EXISTS(SELECT 1 FROM audit_log a WHERE a.entity_type='account_record' AND a.account_id=r.account_id AND a.entity_id=r.id AND a.version=r.version AND a.after_json=r.payload)
          AND EXISTS(SELECT 1 FROM audit_log a WHERE a.entity_type='account_record' AND a.account_id=k.account_id AND a.entity_id=k.id AND a.version=k.version AND a.after_json=k.payload)
    ) THEN RAISE(ABORT,'invalid duplicate removal') END;
END;
DROP TRIGGER account_records_no_delete;
CREATE TRIGGER account_records_no_delete BEFORE DELETE ON account_records
WHEN NOT EXISTS(SELECT 1 FROM audit_log WHERE entity_type='account_delete' AND account_id=OLD.account_id)
 AND NOT EXISTS(SELECT 1 FROM audit_log WHERE entity_type='account_record' AND action='remove_duplicate' AND account_id=OLD.account_id AND entity_id=OLD.id AND version=OLD.version+1 AND before_json=OLD.payload)
BEGIN SELECT RAISE(ABORT,'retain account record'); END;
CREATE TRIGGER apply_duplicate_removal AFTER INSERT ON audit_log
WHEN NEW.entity_type='account_record' AND NEW.action='remove_duplicate'
BEGIN DELETE FROM account_records WHERE account_id=NEW.account_id AND id=NEW.entity_id; END;
CREATE TRIGGER account_records_no_retired_id BEFORE INSERT ON account_records
WHEN EXISTS(SELECT 1 FROM audit_log WHERE entity_type='account_record' AND action='remove_duplicate' AND account_id=NEW.account_id AND entity_id=NEW.id)
BEGIN SELECT RAISE(ABORT,'account record id retired'); END;
