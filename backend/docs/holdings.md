# Current Holdings and Security Transactions

All paths below use `/api/platform/ledger`. These APIs do not save quote
observations or insert, replace, or backfill account/asset records.

## Read Contract

- `GET /accounts/{id}/holdings` returns the agreed `account_id`, `currency`,
  `source`, `as_of`, `ledger_at`, opaque `revision`, `manual_version`,
  `trade_date_floor`, `configured`, `cash`, `complete`, `total_assets`, and `items` envelope.
- Unconfigured reported accounts return HTTP 200, `configured:false`,
  `manual_version:"0"`, `cash:null`, `complete:false`, and `items:[]`.
- Replay accounts have `manual_version:null`. `GET` and `HEAD` are supported;
  query parameters (including a bare `?`) are rejected for the holdings view.
- `trade_date_floor` is a `YYYY-MM-DD` string for configured manual accounts,
  taken from the same snapshot's current date fence. It advances with appended
  trades and direct snapshot adjustments. Unconfigured/replay accounts return
  `null`. Use it as the inclusive minimum transaction date; a concurrent write
  can still invalidate a previously loaded version/date fence.
- Each item contains `instrument:{id,name,market,code,currency}`, `quantity`,
  `market_value`, `account_market_value`, `price`, `holding_cost`, `diluted_cost`,
  `weight`, `cost_status`, `cycle_id`, `quote_status`, `quote`, and `fx`.
- `market_value`, `price`, and both cost prices use the security's currency.
  `account_market_value`, `cash`, and `total_assets` use the account currency.
  Decimal values are JSON strings; unknown values are JSON null.
- A single database read transaction captures cash, quantities, costs and
  identities. Existing Tencent providers and valuation network limits are reused
  after releasing the transaction. A concurrent write does not mix snapshots.
  `revision` identifies the financial input basis, not quote freshness.
- `weight` is `account_market_value / (cash + all account_market_values) * 100`,
  rounded half away from zero to two decimals, e.g. `"5.65"`. All weights are
  null if any required quote/FX is missing, or total assets are nonpositive.
  `complete` means valuation completeness, not known cost; no partial denominator
  is used. `total_assets` is null for an incomplete valuation.
- Zero-quantity cycles can remain visible with cost/quote status `closed`, zero
  market values, and null price/costs/quote/FX. They require no network lookup.
- When FX is missing but price is known, price and native market value remain
  available; account market value is null and quote status is `unavailable`.
  Otherwise quote status is `current` or `prior_date` using the existing quote
  and FX freshness rules.

`GET /accounts/{id}/holdings/{instrumentID}/transactions?limit=20&cursor=...`
returns `{items:[...],next_cursor?:string}`. The default limit is 20, maximum
100. Cursors are opaque and bound to account, instrument and source. The API
checks both identities; a registered but never-held instrument returns an empty
list, not a fabricated position. Removed manual positions retain their journal.

Rows contain `id`, `kind`, `date`, `quantity`, `price`, `amount`, `fee`, `note`,
`cycle_id`, and `source`. Replay rows also contain `operation_id`. Only buy,
sell and dividend rows are returned, newest business date first, then descending
same-day sequence. Replay maps `deposit_buy` to buy and `sell_withdraw` to sell;
the independent deposit/withdrawal amount is NOT the trade amount. Voids are
excluded, and dividends retain their explicit original-cycle attribution.
All cycles, including previously closed ones, are available through pagination.
An invalid/stale cursor returns `invalid_query`; restart pagination after replay
operation corrections. Pages are not a frozen export across concurrent edits.

## Declare a Baseline

`PUT /accounts/{id}/current-holdings` accepts optional `baseline_date` in addition
to its existing `expected_version`, `cash`, and `positions` fields:

```json
{
  "expected_version": "0",
  "cash": "1000.00",
  "positions": [],
  "baseline_date": "2026-09-01"
}
```

Before the account has **any** manual journal entries, this date may be between
the account opening date and today (inclusive, Beijing business date). This also
allows redeclaring an existing today's snapshot as a historical opening before
the first trade. After any manual journal entry, an explicit date must be today;
any other valid date returns HTTP 422 `unsafe_trade_date`, even after liquidation
or removal of all positions. Invalid date syntax, or an out-of-range date before
the first trade, returns HTTP 400 `invalid_operation` (wrong JSON types use
`invalid_body`). Omitting the date always means today, not the previous floor.

This is the user's declaration of actual opening cash and quantities on that
date, not a historical trade or an asset observation. Enter the true opening
state, **not the ending holdings**, before appending historical buys/sells in
chronological order, or quantities/cash will be counted twice. Supplied holdings
still have unknown cost. No account records are changed and `saved_at`/audit time
remain the actual save time. The server stores the date in optional internal
snapshot `trades.floor_date`; clients cannot submit `trades` or cost metadata.

Baseline selection is part of idempotency identity. Omission preserves the old
request/receipt shape. Exact retries return the original saved response even
after trades, later edits or a change of day, without applying the old baseline
again. There is no new database migration for these optional fields.

## Append Contract

`POST /accounts/{id}/holdings/{instrumentID}/transactions` requires the existing
`Idempotency-Key` header and JSON:

```json
{
  "expected_version": "1",
  "kind": "buy",
  "date": "2026-09-06",
  "quantity": "10",
  "price": "10",
  "fee": "1.00",
  "note": "Example only",
  "reason": "Enter actual trade"
}
```

Only configured manual/reported accounts may append. A registered security may
be bought even if absent from current holdings. Buy/sell require positive
quantity and price; fee is optional and nonnegative. Dividend requires positive
native-currency `amount` and forbids quantity, price and fee. Buy/sell forbid
`amount`. Unknown, duplicate, differently cased and irrelevant fields, including
explicit null optional values, are rejected. Money has 2 decimal places,
quantity/price 6 and FX rate 8; extra precision is rejected, never truncated.
`reason` must be nonblank (maximum 512 bytes); note can be empty (4096 bytes max).

Cross-currency writes require
`fx:{rate,date,source,fetched_at}` converting security currency to account
currency. Rate must be positive, FX date no later than trade date, fetched time
a valid timestamp no later than the server clock. Same-currency FX is rejected.
Native gross is rounded to minor units first, then fee is applied, then net is
converted to account cash. Buy cannot overdraw cash, sell cannot oversell, and
sell fee cannot exceed gross proceeds. Zero net sell proceeds are permitted.

HTTP 201 response, including an identical idempotent retry:

```json
{
  "account_id": "a",
  "instrument_id": "i",
  "version": "2",
  "transaction": {
    "id": "server-generated-id",
    "kind": "buy",
    "date": "2026-09-06",
    "quantity": "10.000000",
    "price": "10.000000",
    "amount": "101.00",
    "fee": "1.00",
    "note": "Example only",
    "cycle_id": "server-generated-cycle",
    "source": "manual"
  }
}
```

The response `version` is the new current-holdings version. An exact retry with
the original key returns the original response even after further writes. Keys
share the existing global receipt namespace; a changed request conflicts.
CAS, current holdings, immutable journal, audit and receipt commit atomically.
No update/delete journal API is provided.

## Costs and Dates

- Holding cost = current cycle's cumulative net buy outlay / cumulative bought
  quantity. Net buy outlay includes fees.
- Diluted cost = (net buy outlay - net sale proceeds - dividends) / current
  quantity. Net sale proceeds deduct fees. Negative diluted cost is valid.
- A cycle begins on buy from zero and ends at liquidation. A later buy creates
  a new cycle, even on the same day. Replay's existing remaining cost, moving
  average, account-currency accounting and saved valuations are unchanged.
- Example: buy 10 at 10 plus fee 1; sell 4 at 20 less fee 2; buy 4 at 30 plus
  fee 3. Current quantity is 10, bought quantity 14, spent 224, received 78.
  Holding cost is 16, diluted cost is 14.60. The original remaining-cost moving
  average is 18.36, not 16. A dividend of 200 makes diluted cost -5.40.
- Existing manual quantities and replay opening positions have unknown new cost
  basis. Old account-currency opening cost cannot prove cumulative native buys.
  Later buys/sales do not make that initial basis known. Liquidation followed
  by a fresh buy produces a known cycle.
- A direct current-holdings PUT resets quantity-changed/new securities to unknown
  basis, removes deleted open positions, and retains the immutable journal.
  A positive-to-positive quantity correction keeps the ongoing cycle ID, but
  discards its cost evidence; it does not invent a liquidation/reopening.
  Unchanged quantities retain their cost basis; cash-only edits do not reset
  security costs. Internal optional snapshot `trades` metadata is server-owned
  and is rejected in PUT input.
- Manual date fence: no trade may predate the latest declared snapshot baseline
  (the PUT's Beijing business date when omitted), or the last appended trade date
  **across the account**. The original saved date is the fence for legacy
  snapshots. Dates on or after that fence and no later than today support
  chronological backfill. Same-day trades
  append after existing trades and the baseline; they cannot be inserted ahead
  of another same-day event. Cash history before a snapshot is never inferred.
- Manual dividends apply to the latest retained cycle, including a closed cycle
  before reopening. The fixed append contract has no cycle selector: after a
  reopening it cannot express a late dividend for an older cycle. Do not enter
  such a dividend as if it belonged to the new cycle. Replay's operation API
  continues to support explicit old-cycle attribution.

## Errors and Migration

Only two new error codes are introduced, both HTTP 422:

- `manual_holdings_required`: configure manual holdings first; replay accounts
  must use their existing operation management.
- `unsafe_trade_date`: the date predates the manual baseline or last account
  trade, or a PUT specifies a non-today baseline after a manual journal entry.
  Enter eligible backfills in business-date order. There is no override that
  fabricates historical cash.

Existing codes include `invalid_query`, `invalid_body`, `invalid_operation`,
`invalid_precision`, `not_found`, `version_conflict`, `idempotency_conflict`,
`insufficient_cash`, `insufficient_position`, and `data_integrity`.

`002_manual_trades.sql` is an additive migration run by Go's existing checksummed
migration runner. It adds only an immutable journal table, index and integrity
triggers. No existing table, receipt, account record, valuation or snapshot is
rewritten. Optional snapshot metadata is introduced lazily on the first trade
or an explicit baseline declaration;
old snapshot JSON remains readable and old PUT receipts remain retryable.
Receipt kind `current_holdings` is reused with a distinct request envelope,
avoiding a rebuild of the persisted receipt-kind constraint.

Unit tests create a real schema-001 database, seed old snapshots/receipts and
operation-derived account records, upgrade using `Open`, and check retained
payloads, legacy retries, new trades and SQLite integrity. Tests use synthetic
data and fake quote/FX providers only; no E2E or production writes are needed.
