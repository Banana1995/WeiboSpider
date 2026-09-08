-- Fresh-install ledger schema. Current business state lives in operations and
-- account_records; every historical version and source artifact lives in audit_log.
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
    version INTEGER NOT NULL CHECK (version >= 1),
    accounting_mode TEXT NOT NULL CHECK (accounting_mode IN ('holdings', 'reported'))
) STRICT;

CREATE TABLE instruments (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    market TEXT NOT NULL CHECK (length(trim(market)) > 0),
    code TEXT NOT NULL CHECK (length(trim(code)) > 0),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    currency TEXT NOT NULL CHECK (currency IN ('CNY', 'HKD', 'USD')),
    UNIQUE (market, code)
) STRICT;

CREATE TABLE opening_positions (
    account_id TEXT NOT NULL REFERENCES accounts(id),
    instrument_id TEXT NOT NULL REFERENCES instruments(id),
    quantity_micros INTEGER NOT NULL CHECK (quantity_micros > 0),
    cost_minor INTEGER CHECK (cost_minor >= 0),
    diluted_basis_minor INTEGER,
    PRIMARY KEY (account_id, instrument_id)
) STRICT;

-- Balances and positions are replayed. This table contains only each operation's
-- latest committed state; prior versions are immutable audit_log rows.
CREATE TABLE operations (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    kind TEXT NOT NULL CHECK (kind IN (
        'deposit', 'withdrawal', 'buy', 'sell', 'deposit_buy',
        'sell_withdraw', 'dividend', 'transfer'
    )),
    business_date TEXT NOT NULL CHECK (
        length(business_date) = 10
        AND business_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        AND substr(business_date, 1, 4) >= '0001'
        AND substr(business_date, 6, 2) BETWEEN '01' AND '12'
        AND substr(business_date, 9, 2) BETWEEN '01' AND '31'
    ),
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    account_id TEXT NOT NULL REFERENCES accounts(id),
    to_account_id TEXT REFERENCES accounts(id) CHECK (to_account_id <> account_id),
    instrument_id TEXT REFERENCES instruments(id),
    amount_minor INTEGER NOT NULL DEFAULT 0 CHECK (amount_minor >= 0),
    quantity_micros INTEGER NOT NULL DEFAULT 0 CHECK (quantity_micros >= 0),
    price_micros INTEGER NOT NULL DEFAULT 0 CHECK (price_micros >= 0),
    fee_minor INTEGER CHECK (fee_minor >= 0),
    fx_rate_1e8 INTEGER CHECK (fx_rate_1e8 > 0),
    fx_date TEXT CHECK (
        length(fx_date) = 10
        AND fx_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
        AND substr(fx_date, 1, 4) >= '0001'
        AND substr(fx_date, 6, 2) BETWEEN '01' AND '12'
        AND substr(fx_date, 9, 2) BETWEEN '01' AND '31'
        AND fx_date <= business_date
    ),
    fx_source TEXT CHECK (length(trim(fx_source)) > 0),
    fx_fetched_at TEXT CHECK (length(trim(fx_fetched_at)) > 0),
    cycle_id TEXT CHECK (length(trim(cycle_id)) > 0),
    note TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('active', 'voided')),
    version INTEGER NOT NULL CHECK (version >= 1),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0),
    UNIQUE (business_date, sequence),
    CHECK (
        (fx_rate_1e8 IS NULL AND fx_date IS NULL AND fx_source IS NULL AND fx_fetched_at IS NULL)
        OR (fx_rate_1e8 IS NOT NULL AND fx_date IS NOT NULL AND fx_source IS NOT NULL AND fx_fetched_at IS NOT NULL)
    ),
    CHECK (
        (kind IN ('deposit', 'withdrawal') AND amount_minor > 0
            AND to_account_id IS NULL AND instrument_id IS NULL AND cycle_id IS NULL
            AND quantity_micros = 0 AND price_micros = 0 AND fee_minor IS NULL
            AND fx_rate_1e8 IS NULL)
        OR (kind = 'transfer' AND amount_minor > 0 AND to_account_id IS NOT NULL
            AND instrument_id IS NULL AND cycle_id IS NULL
            AND quantity_micros = 0 AND price_micros = 0 AND fee_minor IS NULL
            AND fx_rate_1e8 IS NULL)
        OR (kind IN ('buy', 'sell', 'deposit_buy', 'sell_withdraw')
            AND to_account_id IS NULL AND instrument_id IS NOT NULL AND cycle_id IS NULL
            AND quantity_micros > 0 AND price_micros > 0
            AND (kind IN ('buy', 'sell') AND amount_minor = 0
                OR kind IN ('deposit_buy', 'sell_withdraw') AND amount_minor > 0))
        OR (kind = 'dividend' AND amount_minor > 0
            AND to_account_id IS NULL AND instrument_id IS NOT NULL AND cycle_id IS NOT NULL
            AND quantity_micros = 0 AND price_micros = 0 AND fee_minor IS NULL)
    )
) STRICT;
CREATE INDEX operation_account_date_sequence ON operations(account_id, business_date, sequence);
CREATE INDEX operation_to_account_date_sequence ON operations(to_account_id, business_date, sequence)
    WHERE to_account_id IS NOT NULL;

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

-- The one physical business timeline. Imported, manually entered, operation-derived
-- and saved valuation rows all use this table and retain only their latest state.
CREATE TABLE account_records (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    id TEXT NOT NULL,
    business_date TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('asset', 'cash_flow', 'log')),
    flow_minor INTEGER,
    total_assets_minor INTEGER CHECK (total_assets_minor >= 0),
    note TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('import', 'manual', 'currentrefresh', 'weekly', 'weekly_carry', 'operation')),
    operation_id TEXT REFERENCES operations(id),
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
    UNIQUE (account_id, operation_id),
    CHECK ((origin = 'operation') = (operation_id IS NOT NULL)),
    CHECK ((origin IN ('currentrefresh', 'weekly', 'weekly_carry')) = (quote_audit_id IS NOT NULL)),
    CHECK (
        (kind = 'asset' AND total_assets_minor IS NOT NULL AND flow_minor IS NULL)
        OR (kind = 'cash_flow' AND flow_minor IS NOT NULL)
        OR (kind = 'log' AND flow_minor IS NULL AND total_assets_minor IS NULL)
    )
) STRICT;
CREATE INDEX account_records_timeline ON account_records(account_id, business_date, sequence);
CREATE INDEX account_records_operation ON account_records(operation_id) WHERE operation_id IS NOT NULL;
CREATE INDEX account_records_valuation ON account_records(account_id, sequence DESC)
    WHERE origin IN ('currentrefresh', 'weekly');

-- One global key namespace for every idempotent ledger write.
CREATE TABLE idempotency_receipts (
    key TEXT PRIMARY KEY CHECK (length(trim(key)) > 0),
    kind TEXT NOT NULL CHECK (kind IN ('operation', 'account_record', 'reported_account', 'import', 'valuation')),
    request_hash TEXT NOT NULL CHECK (
        length(request_hash) = 64 AND request_hash NOT GLOB '*[^0-9a-f]*'
    ),
    request_json TEXT NOT NULL CHECK (json_valid(request_json) AND json_type(request_json) = 'object'),
    response_json TEXT NOT NULL CHECK (json_valid(response_json) AND json_type(response_json) = 'object'),
    audit_id INTEGER NOT NULL REFERENCES audit_log(id),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0)
) STRICT;

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
CREATE TRIGGER opening_positions_no_update BEFORE UPDATE ON opening_positions
BEGIN SELECT RAISE(ABORT, 'immutable opening position'); END;
CREATE TRIGGER opening_positions_no_delete BEFORE DELETE ON opening_positions
BEGIN SELECT RAISE(ABORT, 'retain opening position'); END;
CREATE TRIGGER operations_no_delete BEFORE DELETE ON operations
BEGIN SELECT RAISE(ABORT, 'void operations instead'); END;
CREATE TRIGGER operations_identity BEFORE UPDATE ON operations
WHEN NEW.id != OLD.id OR NEW.created_at != OLD.created_at
BEGIN SELECT RAISE(ABORT, 'immutable operation identity'); END;
CREATE TRIGGER account_records_no_delete BEFORE DELETE ON account_records
BEGIN SELECT RAISE(ABORT, 'void records instead'); END;
CREATE TRIGGER account_records_identity BEFORE UPDATE ON account_records
WHEN NEW.sequence != OLD.sequence OR NEW.id != OLD.id OR NEW.account_id != OLD.account_id OR NEW.created_at != OLD.created_at
BEGIN SELECT RAISE(ABORT, 'immutable record identity'); END;
CREATE TRIGGER weekly_jobs_identity BEFORE UPDATE ON weekly_jobs
WHEN NEW.id != OLD.id OR NEW.account_id != OLD.account_id
  OR NEW.scheduled_business_date != OLD.scheduled_business_date
  OR NEW.source != OLD.source OR NEW.created_at != OLD.created_at
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
