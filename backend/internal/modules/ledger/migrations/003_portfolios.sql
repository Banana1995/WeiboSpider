-- Portfolios reference account identities, not copies of their financial records.
-- Missing/deleted members remain visible in the definition and block analysis
-- until explicitly removed; they must never silently disappear from a return.
CREATE TABLE portfolios (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    version INTEGER NOT NULL CHECK (version > 0),
    audit_id INTEGER NOT NULL UNIQUE REFERENCES audit_log(id),
    payload TEXT NOT NULL CHECK (
        json_valid(payload) AND json_type(payload) = 'object'
        AND json_extract(payload, '$.id') IS id
        AND json_extract(payload, '$.version') IS CAST(version AS TEXT)
        AND json_type(payload, '$.account_ids') IS 'array'
        AND json_array_length(payload, '$.account_ids') BETWEEN 1 AND 50
        AND json_extract(payload, '$.currency') IN ('CNY', 'HKD', 'USD')
    )
) STRICT;

CREATE TRIGGER portfolios_insert BEFORE INSERT ON portfolios
BEGIN
    SELECT CASE WHEN NEW.version != 1 OR EXISTS (
        SELECT 1 FROM audit_log WHERE entity_type='portfolio' AND entity_id=NEW.id AND action='delete'
    ) THEN RAISE(ABORT, 'portfolio identity retired') END;
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM audit_log WHERE id=NEW.audit_id AND entity_type='portfolio'
        AND entity_id=NEW.id AND account_id IS NULL AND version=NEW.version
        AND action='create' AND before_json IS NULL AND after_json=NEW.payload
    ) THEN RAISE(ABORT, 'portfolio audit mismatch') END;
END;
CREATE TRIGGER portfolios_update BEFORE UPDATE ON portfolios
BEGIN
    SELECT CASE WHEN NEW.id != OLD.id OR NEW.version != OLD.version+1
        THEN RAISE(ABORT, 'portfolio version mismatch') END;
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM audit_log WHERE id=NEW.audit_id AND entity_type='portfolio'
        AND entity_id=NEW.id AND account_id IS NULL AND version=NEW.version
        AND action='replace' AND before_json=OLD.payload AND after_json=NEW.payload
    ) THEN RAISE(ABORT, 'portfolio audit mismatch') END;
END;
CREATE TRIGGER portfolios_delete BEFORE DELETE ON portfolios
WHEN NOT EXISTS (
    SELECT 1 FROM audit_log WHERE entity_type='portfolio' AND entity_id=OLD.id
    AND action='delete' AND version=OLD.version+1 AND before_json=OLD.payload
)
BEGIN SELECT RAISE(ABORT, 'portfolio deletion requires audit'); END;

CREATE TABLE idempotency_receipts_new (
    key TEXT PRIMARY KEY CHECK (length(trim(key)) > 0),
    kind TEXT NOT NULL CHECK (kind IN ('account_record', 'reported_account', 'import', 'current_holdings', 'account_delete', 'portfolio')),
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
