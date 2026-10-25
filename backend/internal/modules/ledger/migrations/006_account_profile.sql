-- Account profile updates (name) share the global receipt namespace.
CREATE TABLE idempotency_receipts_new (
    key TEXT PRIMARY KEY CHECK (length(trim(key)) > 0),
    kind TEXT NOT NULL CHECK (kind IN ('account_record', 'reported_account', 'import', 'current_holdings', 'account_delete', 'portfolio', 'stock_command', 'account_profile')),
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
