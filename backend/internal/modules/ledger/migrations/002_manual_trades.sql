-- Additive upgrade: existing snapshots, receipts and historical records are untouched.
CREATE TABLE manual_trades (
    account_id TEXT NOT NULL REFERENCES accounts(id),
    instrument_id TEXT NOT NULL REFERENCES instruments(id),
    version INTEGER NOT NULL CHECK (version > 1),
    business_date TEXT NOT NULL,
    audit_id INTEGER NOT NULL UNIQUE REFERENCES audit_log(id),
    holdings_audit_id INTEGER NOT NULL UNIQUE REFERENCES audit_log(id),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    PRIMARY KEY (account_id, version)
) STRICT;
CREATE INDEX manual_trades_page ON manual_trades(account_id, instrument_id, business_date DESC, version DESC);
CREATE TRIGGER manual_trades_insert BEFORE INSERT ON manual_trades
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM current_holdings c JOIN audit_log a ON a.id=NEW.audit_id
        JOIN audit_log h ON h.id=NEW.holdings_audit_id
        WHERE c.account_id=NEW.account_id AND c.version=NEW.version AND c.audit_id=h.id
        AND h.entity_type='current_holdings' AND h.after_json=c.payload
        AND a.entity_type='manual_trade' AND a.account_id=NEW.account_id
        AND a.entity_id=json_extract(NEW.payload,'$.transaction.id') AND a.version=1
        AND a.correlation_id=h.correlation_id AND a.after_json=NEW.payload
        AND json_extract(NEW.payload,'$.account_id') IS NEW.account_id
        AND json_extract(NEW.payload,'$.instrument_id') IS NEW.instrument_id
        AND json_extract(NEW.payload,'$.version') IS CAST(NEW.version AS TEXT)
        AND json_extract(NEW.payload,'$.transaction.date') IS NEW.business_date
    ) THEN RAISE(ABORT,'manual trade audit mismatch') END;
END;
CREATE TRIGGER manual_trades_no_update BEFORE UPDATE ON manual_trades
BEGIN SELECT RAISE(ABORT,'immutable manual trade'); END;
CREATE TRIGGER manual_trades_no_delete BEFORE DELETE ON manual_trades
BEGIN SELECT RAISE(ABORT,'retain manual trade'); END;
