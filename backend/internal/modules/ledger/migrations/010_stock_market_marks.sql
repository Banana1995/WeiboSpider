-- Shared market data only: one latest verified quote per exchange symbol or
-- FX pair. This table contains no holdings, account balances or timeline rows.
CREATE TABLE stock_market_marks (
    symbol TEXT PRIMARY KEY,
    quoted_ms INTEGER NOT NULL,
    payload TEXT NOT NULL CHECK (json_valid(payload) AND json_type(payload)='object')
) STRICT;

-- At most one attempt per fixed trading slot, including across service restarts.
CREATE TABLE stock_market_clock (
    id INTEGER PRIMARY KEY CHECK (id=1),
    slot INTEGER NOT NULL
) STRICT;

-- On upgrade, reuse only quote/FX observations from already verified, frozen
-- valuations. Never infer a price from a trade, a balance, or a subtotal.
WITH items AS (
    SELECT p.value FROM audit_log a, json_each(a.after_json,'$.valuation.items') p
    WHERE a.entity_type='valuation' AND a.action='observe' AND json_extract(a.after_json,'$.schema_version')=1
), marks(symbol,payload) AS (
    SELECT json_extract(value,'$.quote.symbol'),json_extract(value,'$.quote') FROM items
    WHERE json_extract(value,'$.quote.source')='Tencent'
    UNION ALL
    SELECT 'fx:' || json_extract(value,'$.fx.base') || '/' || json_extract(value,'$.fx.quote'),json_extract(value,'$.fx') FROM items
    WHERE json_extract(value,'$.fx.mode')='latest'
)
INSERT INTO stock_market_marks(symbol,quoted_ms,payload)
SELECT symbol,CAST(round(unixepoch(json_extract(payload,'$.quoted_at'),'subsec')*1000) AS INTEGER),payload FROM marks
WHERE symbol IS NOT NULL AND unixepoch(json_extract(payload,'$.quoted_at'),'subsec') IS NOT NULL
ON CONFLICT(symbol) DO UPDATE SET quoted_ms=excluded.quoted_ms,payload=excluded.payload
WHERE excluded.quoted_ms>=stock_market_marks.quoted_ms;
