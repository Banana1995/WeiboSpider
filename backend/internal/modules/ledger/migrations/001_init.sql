-- Fresh-install schema: current_holdings contains mutable current inputs;
-- account_records contains independent fixed assets and external flows.
CREATE TABLE accounts (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    currency TEXT NOT NULL CHECK (currency IN ('CNY', 'HKD', 'USD')),
    opening_date TEXT NOT NULL CHECK (
        length(opening_date) = 10
        AND opening_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        AND substr(opening_date, 1, 4) >= '0001'
        AND substr(opening_date, 6, 2) BETWEEN '01' AND '12'
        AND substr(opening_date, 9, 2) BETWEEN '01' AND '31'
    ),
    opening_cash_minor INTEGER NOT NULL CHECK (opening_cash_minor >= 0),
    version INTEGER NOT NULL CHECK (version >= 1)
) STRICT;

CREATE TABLE instruments (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    market TEXT NOT NULL CHECK (length(trim(market)) > 0),
    code TEXT NOT NULL CHECK (length(trim(code)) > 0),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    currency TEXT NOT NULL CHECK (currency IN ('CNY', 'HKD', 'USD'))
) STRICT;

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    correlation_id TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL CHECK (length(trim(action)) > 0),
    entity_type TEXT NOT NULL CHECK (length(trim(entity_type)) > 0),
    entity_id TEXT NOT NULL CHECK (length(trim(entity_id)) > 0),
    account_id TEXT REFERENCES accounts(id),
    version INTEGER NOT NULL CHECK (version > 0),
    recorded_at TEXT NOT NULL CHECK (length(trim(recorded_at)) > 0),
    source TEXT NOT NULL CHECK (source IN ('human', 'system')),
    before_json TEXT CHECK (before_json IS NULL OR json_valid(before_json)),
    after_json TEXT NOT NULL CHECK (json_valid(after_json)),
    metadata_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata_json))
) STRICT;
CREATE UNIQUE INDEX audit_entity_version
    ON audit_log(entity_type, entity_id, ifnull(account_id, ''), version);
CREATE INDEX audit_account_page ON audit_log(account_id, id);
CREATE INDEX audit_correlation ON audit_log(correlation_id, id);
CREATE UNIQUE INDEX audit_single_account_import ON audit_log(account_id)
    WHERE entity_type = 'import';

-- The one business timeline for imported/manual amounts and fixed valuations.
CREATE TABLE account_records (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    id TEXT NOT NULL,
    business_date TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('asset', 'cash_flow', 'log')),
    flow_minor INTEGER,
    total_assets_minor INTEGER CHECK (total_assets_minor >= 0),
    note TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('import', 'manual', 'currentrefresh', 'weekly', 'weekly_carry')),
    quote_audit_id INTEGER REFERENCES audit_log(id),
    manual_assertion INTEGER NOT NULL DEFAULT 0 CHECK (manual_assertion IN (0, 1)),
    voided INTEGER NOT NULL CHECK (voided IN (0, 1)),
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    payload TEXT NOT NULL CHECK (
        json_valid(payload)
        AND json_type(payload) = 'object'
        AND json_extract(payload, '$.id') = id
        AND json_extract(payload, '$.account_id') = account_id
        AND json_extract(payload, '$.date') = business_date
        AND json_extract(payload, '$.version') = CAST(version AS TEXT)
    ),
    UNIQUE (account_id, id),
    CHECK ((origin IN ('currentrefresh', 'weekly', 'weekly_carry')) = (quote_audit_id IS NOT NULL)),
    CHECK (
        (kind = 'asset' AND total_assets_minor IS NOT NULL AND flow_minor IS NULL)
        OR (kind = 'cash_flow' AND flow_minor IS NOT NULL)
        OR (kind = 'log' AND flow_minor IS NULL AND total_assets_minor IS NULL)
    )
) STRICT;
CREATE INDEX account_records_timeline ON account_records(account_id, business_date, sequence);
CREATE INDEX account_records_valuation ON account_records(account_id, sequence DESC)
    WHERE origin IN ('currentrefresh', 'weekly');

-- One global key namespace for every idempotent ledger write.
CREATE TABLE idempotency_receipts (
    key TEXT PRIMARY KEY CHECK (length(trim(key)) > 0),
    kind TEXT NOT NULL CHECK (kind IN ('account_record', 'reported_account', 'import', 'current_holdings')),
    request_hash TEXT NOT NULL CHECK (
        length(request_hash) = 64 AND request_hash NOT GLOB '*[^0-9a-f]*'
    ),
    request_json TEXT NOT NULL CHECK (json_valid(request_json) AND json_type(request_json) = 'object'),
    response_json TEXT NOT NULL CHECK (json_valid(response_json) AND json_type(response_json) = 'object'),
    audit_id INTEGER NOT NULL REFERENCES audit_log(id),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0)
) STRICT;

-- Manual current inputs are not trades, opening balances or timeline observations.
-- One atomic JSON snapshot; immutable versions live in audit_log.
CREATE TABLE current_holdings (
    account_id TEXT PRIMARY KEY REFERENCES accounts(id),
    version INTEGER NOT NULL CHECK (version > 0),
    audit_id INTEGER NOT NULL UNIQUE REFERENCES audit_log(id),
    payload TEXT NOT NULL CHECK (json_valid(payload) AND json_type(payload) = 'object'
        AND json_extract(payload,'$.version') IS CAST(version AS TEXT)
        AND json_type(payload,'$.cash') IS 'text'
        AND json_extract(payload,'$.cash') GLOB '[0-9]*.[0-9][0-9]'
        AND json_extract(payload,'$.cash') NOT GLOB '*[^0-9.]*'
        AND instr(json_extract(payload,'$.cash'),'.') = length(json_extract(payload,'$.cash'))-2
        AND length(json_extract(payload,'$.cash')) <= 20
        AND (length(json_extract(payload,'$.cash')) < 20 OR json_extract(payload,'$.cash') <= '92233720368547758.07')
        AND json_type(payload,'$.saved_at') IS 'text'
        AND length(json_extract(payload,'$.saved_at')) >= 20
        AND json_type(payload,'$.positions') IS 'array'
        AND json_array_length(payload,'$.positions') <= 200)
) STRICT;
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
CREATE TRIGGER current_holdings_insert BEFORE INSERT ON current_holdings
BEGIN
    SELECT CASE WHEN NEW.version != 1 OR NOT EXISTS (
        SELECT 1 FROM accounts WHERE id=NEW.account_id
    ) THEN RAISE(ABORT, 'invalid manual source') END;
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM audit_log WHERE id=NEW.audit_id AND entity_type='current_holdings'
        AND account_id=NEW.account_id AND entity_id=NEW.account_id AND version=NEW.version
        AND after_json=NEW.payload AND before_json IS NULL
    ) THEN RAISE(ABORT, 'source audit mismatch') END;
END;
CREATE TRIGGER current_holdings_update BEFORE UPDATE ON current_holdings
BEGIN
    SELECT CASE WHEN NEW.account_id != OLD.account_id OR NEW.version != OLD.version+1
        THEN RAISE(ABORT, 'source version mismatch') END;
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM audit_log WHERE id=NEW.audit_id AND entity_type='current_holdings'
        AND account_id=NEW.account_id AND entity_id=NEW.account_id AND version=NEW.version
        AND after_json=NEW.payload AND before_json=OLD.payload
    ) THEN RAISE(ABORT, 'source audit mismatch') END;
END;
CREATE TRIGGER current_holdings_no_delete BEFORE DELETE ON current_holdings
BEGIN SELECT RAISE(ABORT, 'replace current source instead'); END;

CREATE TABLE weekly_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    scheduled_business_date TEXT NOT NULL CHECK (
        length(scheduled_business_date) = 10
        AND coalesce(date(scheduled_business_date, '+0 days'), '') = scheduled_business_date
        AND coalesce(strftime('%w', scheduled_business_date), '') = '6'
    ),
    source TEXT NOT NULL CHECK (source IN ('holdings_current', 'account_record_carry')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 3),
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    next_attempt_at TEXT,
    lease_until TEXT,
    error_code TEXT NOT NULL DEFAULT '' CHECK (error_code IN (
        '', 'no_source', 'expired', 'interrupted', 'canceled', 'timeout',
        'basis_changed', 'incomplete_valuation', 'invalid_valuation', 'storage_error'
    )),
    history_id INTEGER UNIQUE REFERENCES account_records(sequence),
    UNIQUE (account_id, scheduled_business_date),
    CHECK ((status = 'succeeded') = (history_id IS NOT NULL)),
    CHECK (status != 'succeeded' OR (attempts > 0 AND finished_at IS NOT NULL AND error_code = '')),
    CHECK (error_code != 'no_source' OR (source = 'account_record_carry' AND status = 'skipped')),
    CHECK ((status = 'running') = (lease_until IS NOT NULL)),
    CHECK (status != 'running' OR (attempts > 0 AND started_at IS NOT NULL)),
    CHECK (status NOT IN ('succeeded', 'skipped') OR next_attempt_at IS NULL)
) STRICT;
CREATE INDEX weekly_jobs_due ON weekly_jobs(scheduled_business_date, status, next_attempt_at, id);
CREATE INDEX weekly_jobs_account ON weekly_jobs(account_id, id DESC);

CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN SELECT RAISE(ABORT, 'immutable audit'); END;
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
BEGIN SELECT RAISE(ABORT, 'immutable audit'); END;
CREATE TRIGGER idempotency_receipts_no_update BEFORE UPDATE ON idempotency_receipts
BEGIN SELECT RAISE(ABORT, 'immutable receipt'); END;
CREATE TRIGGER idempotency_receipts_no_delete BEFORE DELETE ON idempotency_receipts
BEGIN SELECT RAISE(ABORT, 'immutable receipt'); END;
CREATE TRIGGER accounts_no_update BEFORE UPDATE ON accounts
BEGIN SELECT RAISE(ABORT, 'immutable account'); END;
CREATE TRIGGER accounts_no_delete BEFORE DELETE ON accounts
BEGIN SELECT RAISE(ABORT, 'retain account'); END;
CREATE TRIGGER instruments_no_update BEFORE UPDATE ON instruments
BEGIN SELECT RAISE(ABORT, 'immutable instrument'); END;
CREATE TRIGGER instruments_no_delete BEFORE DELETE ON instruments
BEGIN SELECT RAISE(ABORT, 'retain instrument'); END;
CREATE TRIGGER account_records_no_delete BEFORE DELETE ON account_records
BEGIN SELECT RAISE(ABORT, 'void records instead'); END;
CREATE TRIGGER account_records_identity BEFORE UPDATE ON account_records
WHEN NEW.sequence != OLD.sequence OR NEW.id != OLD.id OR NEW.account_id != OLD.account_id OR NEW.created_at != OLD.created_at
BEGIN SELECT RAISE(ABORT, 'immutable record identity'); END;
CREATE TRIGGER weekly_jobs_identity BEFORE UPDATE ON weekly_jobs
WHEN NEW.id != OLD.id OR NEW.account_id != OLD.account_id
  OR NEW.scheduled_business_date != OLD.scheduled_business_date
  OR NEW.created_at != OLD.created_at
  OR (NEW.source != OLD.source AND NOT (
      OLD.status IN ('pending','failed') AND NEW.status='running'
      AND OLD.source='account_record_carry' AND NEW.source='holdings_current'
      AND EXISTS(SELECT 1 FROM current_holdings WHERE account_id=OLD.account_id)))
BEGIN SELECT RAISE(ABORT, 'immutable weekly identity'); END;
CREATE TRIGGER weekly_jobs_terminal BEFORE UPDATE ON weekly_jobs
WHEN OLD.status IN ('succeeded', 'skipped')
BEGIN SELECT RAISE(ABORT, 'terminal weekly job'); END;
CREATE TRIGGER weekly_jobs_no_delete BEFORE DELETE ON weekly_jobs
BEGIN SELECT RAISE(ABORT, 'retain weekly job'); END;
CREATE TRIGGER weekly_history_insert BEFORE INSERT ON weekly_jobs WHEN NEW.history_id IS NOT NULL
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM account_records r
        JOIN audit_log a ON a.id = r.quote_audit_id
          AND a.account_id = r.account_id AND a.entity_id = CAST(r.sequence AS TEXT)
        WHERE r.sequence = NEW.history_id AND r.account_id = NEW.account_id
          AND r.business_date = NEW.scheduled_business_date
          AND r.kind = 'asset' AND r.voided = 0
          AND (
              NEW.source = 'holdings_current' AND r.origin = 'weekly' AND a.entity_type = 'valuation'
              OR NEW.source = 'account_record_carry' AND r.origin = 'weekly_carry' AND a.entity_type = 'weekly_carry'
          )
    ) THEN RAISE(ABORT, 'weekly history mismatch') END;
END;
CREATE TRIGGER weekly_history_update BEFORE UPDATE ON weekly_jobs WHEN NEW.history_id IS NOT NULL
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM account_records r
        JOIN audit_log a ON a.id = r.quote_audit_id
          AND a.account_id = r.account_id AND a.entity_id = CAST(r.sequence AS TEXT)
        WHERE r.sequence = NEW.history_id AND r.account_id = NEW.account_id
          AND r.business_date = NEW.scheduled_business_date
          AND r.kind = 'asset' AND r.voided = 0
          AND (
              NEW.source = 'holdings_current' AND r.origin = 'weekly' AND a.entity_type = 'valuation'
              OR NEW.source = 'account_record_carry' AND r.origin = 'weekly_carry' AND a.entity_type = 'weekly_carry'
          )
    ) THEN RAISE(ABORT, 'weekly history mismatch') END;
END;

-- Weekly state transitions are business events. Triggers cover bulk expiration,
-- restart recovery and normal claim/completion paths in the same transaction.
CREATE TRIGGER weekly_audit_insert AFTER INSERT ON weekly_jobs
BEGIN
    INSERT INTO audit_log(correlation_id, action, entity_type, entity_id, account_id,
                          version, recorded_at, source, after_json)
    VALUES(
        'weekly-' || NEW.id, 'create', 'weekly_job', CAST(NEW.id AS TEXT), NEW.account_id,
        1, NEW.created_at, 'system',
        json_object(
            'id', CAST(NEW.id AS TEXT), 'account_id', NEW.account_id,
            'scheduled_business_date', NEW.scheduled_business_date, 'source', NEW.source,
            'status', NEW.status, 'attempts', CAST(NEW.attempts AS TEXT),
            'created_at', NEW.created_at, 'started_at', NEW.started_at,
            'finished_at', NEW.finished_at, 'next_attempt_at', NEW.next_attempt_at,
            'lease_until', NEW.lease_until, 'error_code', NEW.error_code,
            'history_id', CAST(NEW.history_id AS TEXT)
        )
    );
END;
CREATE TRIGGER weekly_audit_update AFTER UPDATE ON weekly_jobs
BEGIN
    INSERT INTO audit_log(correlation_id, action, entity_type, entity_id, account_id,
                          version, recorded_at, source, before_json, after_json)
    VALUES(
        'weekly-' || NEW.id, NEW.status, 'weekly_job', CAST(NEW.id AS TEXT), NEW.account_id,
        (SELECT coalesce(max(version), 0) + 1 FROM audit_log
         WHERE entity_type = 'weekly_job' AND entity_id = CAST(NEW.id AS TEXT)),
        coalesce(NEW.finished_at, NEW.started_at, NEW.created_at), 'system',
        json_object(
            'id', CAST(OLD.id AS TEXT), 'account_id', OLD.account_id,
            'scheduled_business_date', OLD.scheduled_business_date, 'source', OLD.source,
            'status', OLD.status, 'attempts', CAST(OLD.attempts AS TEXT),
            'created_at', OLD.created_at, 'started_at', OLD.started_at,
            'finished_at', OLD.finished_at, 'next_attempt_at', OLD.next_attempt_at,
            'lease_until', OLD.lease_until, 'error_code', OLD.error_code,
            'history_id', CAST(OLD.history_id AS TEXT)
        ),
        json_object(
            'id', CAST(NEW.id AS TEXT), 'account_id', NEW.account_id,
            'scheduled_business_date', NEW.scheduled_business_date, 'source', NEW.source,
            'status', NEW.status, 'attempts', CAST(NEW.attempts AS TEXT),
            'created_at', NEW.created_at, 'started_at', NEW.started_at,
            'finished_at', NEW.finished_at, 'next_attempt_at', NEW.next_attempt_at,
            'lease_until', NEW.lease_until, 'error_code', NEW.error_code,
            'history_id', CAST(NEW.history_id AS TEXT)
        )
    );
END;
