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
