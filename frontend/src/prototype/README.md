# Ledger Prototype

Local route: `/ledger-prototype` (also accepts a trailing slash).

The route lazily mounts only this prototype. It does not import the production
ledger, request APIs, obtain quotes, read file contents, or use browser storage.
All accounts, records, holdings and changes reset on page reload. Dates and the
current year are anchored to the visible example date, 2026-09-10.

The main page contains a return curve and one unified record list. Flow markers
locate the corresponding record, clear record filters and select the correct
page. Account management contains separate information, holdings and read-only
schedule examples. Import always creates a new account using the exact built-in
preview, including when a local filename was selected.

Amounts use integer cents. Demonstration returns use observed period endpoints,
post-flow assets and exclude the baseline day's flows. Personal returns use
Modified Dietz; manager returns compound observed subperiods only when each flow
date has assets. Missing asset observations are never fabricated. These are
illustrative calculations, not a production financial engine.

Verification: `npm test -- src/prototype` and `npm run build` from `frontend/`.
Native dialog focus trapping and responsive visual critique are reserved for the
parent's browser pass; unit tests polyfill only dialog open/close in jsdom.
