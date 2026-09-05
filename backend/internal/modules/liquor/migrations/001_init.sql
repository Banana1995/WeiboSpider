CREATE TABLE products (
    source TEXT NOT NULL,
    id INTEGER NOT NULL CHECK (id > 0),
    name TEXT NOT NULL,
    specifications TEXT NOT NULL,
    unit TEXT NOT NULL,
    sort_order INTEGER NOT NULL,
    PRIMARY KEY (source, id)
);

CREATE TABLE prices (
    source TEXT NOT NULL,
    product_id INTEGER NOT NULL,
    price_date TEXT NOT NULL CHECK (length(price_date) = 10),
    price_cents INTEGER NOT NULL CHECK (price_cents > 0 AND price_cents <= 100000000000),
    change_cents INTEGER NOT NULL CHECK (abs(change_cents) <= 100000000000),
    fetched_at TEXT NOT NULL,
    PRIMARY KEY (source, product_id, price_date),
    FOREIGN KEY (source, product_id) REFERENCES products(source, id)
);
CREATE INDEX prices_by_date ON prices(source, price_date);

CREATE TABLE sync_status (
    source TEXT PRIMARY KEY,
    state TEXT NOT NULL CHECK (state IN ('idle', 'running', 'succeeded', 'failed', 'interrupted')),
    run_id TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL DEFAULT '',
    finished_at TEXT NOT NULL DEFAULT '',
    last_success_at TEXT NOT NULL DEFAULT '',
    last_price_date TEXT NOT NULL DEFAULT '',
    records INTEGER NOT NULL DEFAULT 0,
    error_code TEXT NOT NULL DEFAULT ''
);
INSERT INTO sync_status(source, state) VALUES ('sina_jiujia', 'idle');
