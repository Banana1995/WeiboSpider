-- Individual-stock books are independent of account_records and their returns.
CREATE TABLE stock_journals (
    account_id TEXT PRIMARY KEY REFERENCES accounts(id),
    version INTEGER NOT NULL CHECK(version > 0),
    audit_id INTEGER NOT NULL REFERENCES audit_log(id),
    payload TEXT NOT NULL CHECK(json_valid(payload))
) STRICT;
CREATE TABLE stock_entries (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK(version > 0),
    audit_id INTEGER NOT NULL REFERENCES audit_log(id),
    payload TEXT NOT NULL CHECK(json_valid(payload)),
    UNIQUE(account_id,id)
) STRICT;
CREATE INDEX stock_entries_account ON stock_entries(account_id,sequence);
CREATE TRIGGER stock_entry_insert BEFORE INSERT ON stock_entries BEGIN
    SELECT CASE WHEN NEW.version != 1 OR NOT EXISTS (
        SELECT 1 FROM audit_log WHERE id=NEW.audit_id AND entity_type='stock_entry'
        AND account_id=NEW.account_id AND entity_id=NEW.id AND version=NEW.version
        AND before_json IS NULL AND after_json=NEW.payload
    ) THEN RAISE(ABORT,'stock entry audit mismatch') END;
END;
CREATE TRIGGER stock_entry_update BEFORE UPDATE ON stock_entries BEGIN
    SELECT CASE WHEN NEW.sequence!=OLD.sequence OR NEW.account_id!=OLD.account_id OR NEW.id!=OLD.id
        OR NEW.version!=OLD.version+1 OR NOT EXISTS (
        SELECT 1 FROM audit_log WHERE id=NEW.audit_id AND entity_type='stock_entry'
        AND account_id=NEW.account_id AND entity_id=NEW.id AND version=NEW.version
        AND before_json=OLD.payload AND after_json=NEW.payload
    ) THEN RAISE(ABORT,'stock entry audit mismatch') END;
END;
CREATE TRIGGER stock_journal_insert BEFORE INSERT ON stock_journals BEGIN
    SELECT CASE WHEN NEW.version!=1 OR NOT EXISTS (
        SELECT 1 FROM audit_log WHERE id=NEW.audit_id AND entity_type='stock_journal'
        AND account_id=NEW.account_id AND entity_id=NEW.account_id AND version=NEW.version
        AND before_json IS NULL AND after_json=NEW.payload
    ) THEN RAISE(ABORT,'stock journal audit mismatch') END;
END;
CREATE TRIGGER stock_journal_update BEFORE UPDATE ON stock_journals BEGIN
    SELECT CASE WHEN NEW.account_id!=OLD.account_id OR NEW.version!=OLD.version+1 OR NOT EXISTS (
        SELECT 1 FROM audit_log WHERE id=NEW.audit_id AND entity_type='stock_journal'
        AND account_id=NEW.account_id AND entity_id=NEW.account_id AND version=NEW.version
        AND before_json=OLD.payload AND after_json=NEW.payload
    ) THEN RAISE(ABORT,'stock journal audit mismatch') END;
END;
CREATE TRIGGER stock_entry_delete BEFORE DELETE ON stock_entries
WHEN NOT EXISTS(SELECT 1 FROM audit_log WHERE entity_type='account_delete' AND account_id=OLD.account_id)
BEGIN SELECT RAISE(ABORT,'void stock entries instead'); END;
CREATE TRIGGER stock_journal_delete BEFORE DELETE ON stock_journals
WHEN NOT EXISTS(SELECT 1 FROM audit_log WHERE entity_type='account_delete' AND account_id=OLD.account_id)
BEGIN SELECT RAISE(ABORT,'retain stock journal'); END;

-- Public market observations, not account money. Entry audits freeze the applied terms.
CREATE TABLE stock_dividend_cache (
    market TEXT NOT NULL, code TEXT NOT NULL,
    payload TEXT NOT NULL CHECK(json_valid(payload)),
    PRIMARY KEY(market,code)
) STRICT;
CREATE TABLE stock_dividend_status (
    account_id TEXT PRIMARY KEY REFERENCES accounts(id),
    checked_at TEXT NOT NULL, message TEXT NOT NULL
) STRICT;

CREATE TABLE idempotency_receipts_new (
    key TEXT PRIMARY KEY CHECK(length(trim(key))>0),
    kind TEXT NOT NULL CHECK(kind IN ('account_record','reported_account','import','current_holdings','account_delete','portfolio','stock_command')),
    request_hash TEXT NOT NULL CHECK(length(request_hash)=64 AND request_hash NOT GLOB '*[^0-9a-f]*'),
    request_json TEXT NOT NULL CHECK(json_valid(request_json) AND json_type(request_json)='object'),
    response_json TEXT NOT NULL CHECK(json_valid(response_json) AND json_type(response_json)='object'),
    audit_id INTEGER NOT NULL REFERENCES audit_log(id), created_at TEXT NOT NULL CHECK(length(trim(created_at))>0)
) STRICT;
INSERT INTO idempotency_receipts_new SELECT * FROM idempotency_receipts;
DROP TABLE idempotency_receipts;
ALTER TABLE idempotency_receipts_new RENAME TO idempotency_receipts;
CREATE TRIGGER idempotency_receipts_no_update BEFORE UPDATE ON idempotency_receipts
BEGIN SELECT RAISE(ABORT,'immutable receipt'); END;
CREATE TRIGGER idempotency_receipts_no_delete BEFORE DELETE ON idempotency_receipts
BEGIN SELECT RAISE(ABORT,'immutable receipt'); END;
