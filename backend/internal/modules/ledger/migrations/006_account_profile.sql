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

-- Allow a versioned name update only: identity, currency, opening values and
-- version jumps stay immutable, and each change must have its audit row.
DROP TRIGGER accounts_no_update;
CREATE TRIGGER accounts_no_update BEFORE UPDATE ON accounts
WHEN NEW.id != OLD.id
  OR NEW.currency != OLD.currency
  OR NEW.opening_date != OLD.opening_date
  OR NEW.opening_cash_minor != OLD.opening_cash_minor
  OR NEW.version != OLD.version + 1
  OR NOT EXISTS (
      SELECT 1 FROM audit_log WHERE entity_type='account' AND entity_id=NEW.id
      AND account_id=NEW.id AND version=NEW.version AND action='replace'
  )
BEGIN SELECT RAISE(ABORT, 'immutable account'); END;
