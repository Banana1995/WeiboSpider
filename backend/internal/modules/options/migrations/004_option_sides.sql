-- Preserve old PUT payloads and frozen favorites byte-for-byte.
ALTER TABLE snapshots ADD COLUMN option_type TEXT NOT NULL DEFAULT 'PUT' CHECK(option_type IN ('PUT','CALL'));
CREATE INDEX snapshots_side_time ON snapshots(symbol,option_type,captured_at);

ALTER TABLE latest RENAME TO latest_put_only;
CREATE TABLE latest(
    symbol TEXT NOT NULL,
    option_type TEXT NOT NULL CHECK(option_type IN ('PUT','CALL')),
    snapshot_id TEXT NOT NULL REFERENCES snapshots(id),
    PRIMARY KEY(symbol,option_type)
);
INSERT INTO latest SELECT symbol,'PUT',snapshot_id FROM latest_put_only;
DROP TABLE latest_put_only;

ALTER TABLE directories RENAME TO directories_put_only;
CREATE TABLE directories(
    symbol TEXT NOT NULL,
    option_type TEXT NOT NULL CHECK(option_type IN ('PUT','CALL')),
    from_date TEXT NOT NULL,
    to_date TEXT NOT NULL,
    captured_at TEXT NOT NULL,
    data TEXT NOT NULL,
    PRIMARY KEY(symbol,option_type)
);
INSERT INTO directories SELECT symbol,'PUT',from_date,to_date,captured_at,data FROM directories_put_only;
DROP TABLE directories_put_only;

ALTER TABLE daily_history RENAME TO daily_history_put_only;
CREATE TABLE daily_history(
    symbol TEXT NOT NULL,
    option_type TEXT NOT NULL CHECK(option_type IN ('PUT','CALL')),
    day TEXT NOT NULL,
    captured_at TEXT NOT NULL,
    data TEXT NOT NULL,
    PRIMARY KEY(symbol,option_type,day)
);
INSERT INTO daily_history SELECT symbol,'PUT',day,captured_at,data FROM daily_history_put_only;
DROP TABLE daily_history_put_only;
