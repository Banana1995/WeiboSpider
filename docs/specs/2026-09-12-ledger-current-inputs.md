# Ledger: Current Inputs and Fixed History

This redesign is for a fresh database. The initial schema is edited in place;
there is no old-schema upgrade migration or compatibility layer.
Do not apply it to an existing database without a separate deployment decision.
No production data or deployment was touched during implementation.

## Model

- An account has one model, without a persisted accounting mode.
- `current_holdings` is an account-local, versioned snapshot of current cash and
  security quantities. Replacing it never creates transactions, flows or assets.
- Security identities are immutable catalog entries. Adding/editing a holding
  atomically supplies new identities with the snapshot, without a registration
  step. Changing an identity creates a new identity, not a mutation of another
  account's security. Historical identity entries and audit events are retained.
- A snapshot may contain each market/code only once, enforced in the writer and
  database audit trigger. Different accounts may hold the same market/code.
- `account_records` alone supplies historical assets, external flows and income.
  Holdings edits do not invalidate fixed valuations or return revisions.

## Writes and Evidence

`POST /accounts` creates metadata with an idempotency key. The current holding
editor uses `PUT /accounts/{id}/current-holdings` with `expected_version`, `cash`,
`positions` and optional `securities` (the identities newly selected in the draft).
The identity inserts, audit events, snapshot and idempotency receipt commit in
one transaction. Concurrent edits cannot silently overwrite one another. A
committed retry returns the original receipt even after a later edit.

Current `/holdings` and `/valuation` reads are read-only. There is no valuation
POST handler, standalone registration screen, transaction editor or historical
replay route. The old operation/transfer/position HTTP routes are unregistered.
The replay engine, operation Store methods, unreachable HTTP handlers, operation
DTOs, opening-position/operation tables and transaction-generated record paths
have been removed. The browser no longer has transaction navigation or the old
account-retry fallback that inferred a successful write from a GET. Account
creation retries use the same immutable POST body and idempotency key.

Weekly jobs capture cash, quantities, security identities, quotes and FX in an
immutable audit snapshot and save a fixed asset record. The writer compares the
captured current-input version only while the save is in flight. It never
rechecks that version to invalidate a previously saved record. Manual creation,
correction and voiding of asset records remain available; corrections do not
rewrite the original valuation audit inputs.

## Asset Projection

Ledger display and return endpoints use the last explicit valid asset amount
plus net external flows on strictly later dates. An explicit daily amount is
post all of that day's flows, independent of row ordering. Raw null
`total_assets` values remain null. Projection is derived, not persisted into
those rows. Profit, Dietz, XIRR and the TWR curve use the same projected amounts.
Reference status is retained where actual market changes are unknown.

No-source weekly jobs retain their existing carry records and frozen source
amount/date. Those records are not new observations and do not reset the flow
projection. Missing source amounts are not fabricated as zero. Unsupported or
incomplete quote/FX inputs cannot produce a partial asset record.

Voiding the only explicit source leaves its old carry/audit evidence intact, but
does not turn that carry into a new anchor. The derived asset becomes unavailable,
consistent with the ledger summary. Correcting a source updates derived amounts
and source versions, not the frozen carry payload. Already projected amounts are
never reanchored or incremented a second time by TWR.

## Scope

No old-schema upgrade, production access, E2E, live provider testing, commits or
deployment is part of this change. Local tests use temporary databases and mocked quotes.
Amounts retain the existing fixed-point Money range and six-decimal quantities.
The catalog and audit history are intentionally retained on holding deletion.

## Local Verification

Commands run from `backend/`:

```bash
go test -race -shuffle=on -count=1 -timeout=5m ./...
go vet ./...
```

Commands run from `frontend/`:

```bash
npm test
npm run build
```

All Go unit packages pass, including race detection and shuffled order. Frontend:
23 files, 227 tests pass; Vue/TypeScript production build passes. No `e2e` build
tag or live-source flag is enabled. `git diff --check` passes. This is local
automated verification, not a browser/mobile or production acceptance claim.

## Coverage Changes

Retired trade-cycle/cost-replay expectations were replaced with the direct-input
contract, not retained via compatibility shims or skipped tests. Useful coverage
was moved to the actual write/read boundaries:

- Store/HTTP: exact numbers, strict JSON and Unicode-key rejection, account
  defaults, complete request identity, CAS, durable original receipts, concurrent
  same-key writes, cancellation, and rollback at data/audit/receipt/commit stages.
- Integrity: missing or malformed old/current snapshots, absent revisions,
  contiguous version history and payload/SQL sequence mismatch fail closed.
- Valuation: foreign currency rounding and immutable prices/FX/identities after
  security replacement, quantity edits, deletion and manual asset correction/void.
- Weekly: retries, cancellation, leases, midnight fences, missing/partial inputs,
  source changes during I/O and immutable terminal jobs. Carries with flows,
  source corrections and source voiding retain raw evidence without double counts.
- Returns: existing exact TWR percentages remain asserted on projected input;
  repeated calculations are nonmutating, including custom opening boundaries.
- Frontend: import preview precision/paging, caller-draft freezing, pending-write
  locks, same-key retries and stale-response cancellation remain covered. A real
  import recovery bug was fixed: the selected target survives a failed post-commit
  account refresh. Prototype tests now use its actual record/edit controls.
