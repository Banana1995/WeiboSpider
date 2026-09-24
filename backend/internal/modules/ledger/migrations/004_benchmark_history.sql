-- Market observations are shared by all accounts and never replace ledger facts.
CREATE TABLE benchmark_closes (
    code TEXT NOT NULL CHECK (code IN ('H00300', 'H00922', 'usINX')),
    business_date TEXT NOT NULL,
    close TEXT NOT NULL,
    fetched_at TEXT NOT NULL,
    PRIMARY KEY (code, business_date)
) STRICT;

-- A successful empty window is still covered (holidays or pre-listing dates).
CREATE TABLE benchmark_coverage (
    code TEXT NOT NULL CHECK (code IN ('H00300', 'H00922', 'usINX')),
    from_date TEXT NOT NULL,
    to_date TEXT NOT NULL CHECK (to_date >= from_date),
    synced_at TEXT NOT NULL,
    PRIMARY KEY (code, from_date, to_date)
) STRICT;

CREATE TABLE benchmark_sync_status (
    code TEXT PRIMARY KEY CHECK (code IN ('H00300', 'H00922', 'usINX')),
    last_attempt_at TEXT NOT NULL DEFAULT '',
    last_success_at TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '' CHECK (error_code IN ('', 'source_unavailable', 'timeout', 'storage_error'))
) STRICT;
