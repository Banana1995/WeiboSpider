-- Audit outlives the account. Rebuild only this FK boundary; all operational
-- account references and immutable audit/receipt contents remain intact.
PRAGMA defer_foreign_keys = ON;
PRAGMA legacy_alter_table = ON;
CREATE TABLE audit_log_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    correlation_id TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL CHECK (length(trim(action)) > 0),
    entity_type TEXT NOT NULL CHECK (length(trim(entity_type)) > 0),
    entity_id TEXT NOT NULL CHECK (length(trim(entity_id)) > 0),
    account_id TEXT,
    version INTEGER NOT NULL CHECK (version > 0),
    recorded_at TEXT NOT NULL CHECK (length(trim(recorded_at)) > 0),
    source TEXT NOT NULL CHECK (source IN ('human', 'system')),
    before_json TEXT CHECK (before_json IS NULL OR json_valid(before_json)),
    after_json TEXT NOT NULL CHECK (json_valid(after_json)),
    metadata_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata_json))
) STRICT;
INSERT INTO audit_log_new SELECT * FROM audit_log;
DROP TABLE audit_log;
ALTER TABLE audit_log_new RENAME TO audit_log;
PRAGMA legacy_alter_table = OFF;
CREATE UNIQUE INDEX audit_entity_version ON audit_log(entity_type, entity_id, ifnull(account_id, ''), version);
CREATE INDEX audit_account_page ON audit_log(account_id, id);
CREATE INDEX audit_correlation ON audit_log(correlation_id, id);
CREATE UNIQUE INDEX audit_single_account_import ON audit_log(account_id) WHERE entity_type = 'import';
CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN SELECT RAISE(ABORT, 'immutable audit'); END;
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
BEGIN SELECT RAISE(ABORT, 'immutable audit'); END;
CREATE TRIGGER audit_account_exists BEFORE INSERT ON audit_log
WHEN NEW.account_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM accounts WHERE id=NEW.account_id)
BEGIN SELECT RAISE(ABORT, 'missing account'); END;
CREATE TRIGGER current_holdings_audit_shape BEFORE INSERT ON audit_log
WHEN NEW.entity_type='current_holdings'
BEGIN
    SELECT CASE WHEN EXISTS (
        SELECT 1 FROM json_each(NEW.after_json,'$.positions') p
        WHERE json_type(p.value,'$.instrument_id') IS NOT 'text'
        OR json_type(p.value,'$.quantity') IS NOT 'text'
        OR json_extract(p.value,'$.quantity') NOT GLOB '[0-9]*.[0-9][0-9][0-9][0-9][0-9][0-9]'
        OR json_extract(p.value,'$.quantity') GLOB '*[^0-9.]*'
        OR instr(json_extract(p.value,'$.quantity'),'.') != length(json_extract(p.value,'$.quantity'))-6
        OR json_extract(p.value,'$.quantity') NOT GLOB '*[1-9]*'
        OR length(json_extract(p.value,'$.quantity')) > 20
        OR (length(json_extract(p.value,'$.quantity')) = 20 AND json_extract(p.value,'$.quantity') > '9223372036854.775807')
        OR NOT EXISTS(SELECT 1 FROM instruments WHERE id=json_extract(p.value,'$.instrument_id'))
    ) OR (SELECT count(*) FROM json_each(NEW.after_json,'$.positions')) !=
        (SELECT count(DISTINCT json_extract(value,'$.instrument_id')) FROM json_each(NEW.after_json,'$.positions'))
    OR (SELECT count(*) FROM json_each(NEW.after_json,'$.positions')) !=
        (SELECT count(DISTINCT i.market || '/' || i.code) FROM json_each(NEW.after_json,'$.positions') p
         JOIN instruments i ON i.id=json_extract(p.value,'$.instrument_id'))
    THEN RAISE(ABORT,'invalid current positions') END;
END;

CREATE TABLE idempotency_receipts_new (
    key TEXT PRIMARY KEY CHECK (length(trim(key)) > 0),
    kind TEXT NOT NULL CHECK (kind IN ('account_record', 'reported_account', 'import', 'current_holdings', 'account_delete')),
    request_hash TEXT NOT NULL CHECK (length(request_hash) = 64 AND request_hash NOT GLOB '*[^0-9a-f]*'),
    request_json TEXT NOT NULL CHECK (json_valid(request_json) AND json_type(request_json) = 'object'),
    response_json TEXT NOT NULL CHECK (json_valid(response_json) AND json_type(response_json) = 'object'),
    audit_id INTEGER NOT NULL REFERENCES audit_log(id),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0)
) STRICT;
INSERT INTO idempotency_receipts_new SELECT * FROM idempotency_receipts;
DROP TABLE idempotency_receipts;
ALTER TABLE idempotency_receipts_new RENAME TO idempotency_receipts;
CREATE TRIGGER idempotency_receipts_no_update BEFORE UPDATE ON idempotency_receipts
BEGIN SELECT RAISE(ABORT, 'immutable receipt'); END;
CREATE TRIGGER idempotency_receipts_no_delete BEFORE DELETE ON idempotency_receipts
BEGIN SELECT RAISE(ABORT, 'immutable receipt'); END;

-- The immutable deletion audit is also the permanent ID reservation and the
-- authorization for physical cleanup. Ordinary row deletion stays prohibited.
CREATE UNIQUE INDEX audit_account_deleted ON audit_log(account_id) WHERE entity_type='account_delete';
CREATE TRIGGER accounts_no_reuse BEFORE INSERT ON accounts
WHEN EXISTS(SELECT 1 FROM audit_log WHERE entity_type='account_delete' AND account_id=NEW.id)
BEGIN SELECT RAISE(ABORT, 'account id retired'); END;
DROP TRIGGER accounts_no_delete;
CREATE TRIGGER accounts_no_delete BEFORE DELETE ON accounts
WHEN NOT EXISTS(SELECT 1 FROM audit_log WHERE entity_type='account_delete' AND account_id=OLD.id)
BEGIN SELECT RAISE(ABORT, 'retain account'); END;
DROP TRIGGER current_holdings_no_delete;
CREATE TRIGGER current_holdings_no_delete BEFORE DELETE ON current_holdings
WHEN NOT EXISTS(SELECT 1 FROM audit_log WHERE entity_type='account_delete' AND account_id=OLD.account_id)
BEGIN SELECT RAISE(ABORT, 'replace current source instead'); END;
DROP TRIGGER account_records_no_delete;
CREATE TRIGGER account_records_no_delete BEFORE DELETE ON account_records
WHEN NOT EXISTS(SELECT 1 FROM audit_log WHERE entity_type='account_delete' AND account_id=OLD.account_id)
BEGIN SELECT RAISE(ABORT, 'void records instead'); END;
DROP TRIGGER weekly_jobs_no_delete;
CREATE TRIGGER weekly_jobs_no_delete BEFORE DELETE ON weekly_jobs
WHEN NOT EXISTS(SELECT 1 FROM audit_log WHERE entity_type='account_delete' AND account_id=OLD.account_id)
BEGIN SELECT RAISE(ABORT, 'retain weekly job'); END;

-- Rebuilding a referenced table leaves SQLite's deferred violation counter set
-- even after the same parent IDs have been restored. Validate the actual graph
-- before clearing that counter; never mask a real dangling reference.
CREATE TEMP TABLE ledger_deletion_fk_check (violations INTEGER CHECK (violations=0));
INSERT INTO ledger_deletion_fk_check SELECT count(*) FROM pragma_foreign_key_check;
DROP TABLE ledger_deletion_fk_check;
PRAGMA defer_foreign_keys = OFF;
