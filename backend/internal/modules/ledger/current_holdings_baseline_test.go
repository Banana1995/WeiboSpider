package ledger

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCurrentHoldingsHistoricalBaselineAndPastTradesHTTP(t *testing.T) {
	f := newHTTPFixture(t)
	_, err := f.store.CreateReportedAccount(t.Context(), "account", ReportedAccountInput{ID: "a", Name: "Synthetic", Currency: CNY, OpeningDate: "2026-09-01"})
	require.NoError(t, err)
	require.NoError(t, f.store.AddInstrument(t.Context(), Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Synthetic", Currency: CNY}))
	_, err = f.store.WriteAccountRecord(t.Context(), "historical-asset", AccountRecordCommand{Action: CreateOperation, AccountID: "a", ID: "manual-asset", Entry: &AccountEntry{Kind: "asset", Date: "2026-09-01", TotalAssets: replayMoney(100000)}})
	require.NoError(t, err)
	var originalRecord string
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT payload FROM account_records WHERE account_id='a' AND id='manual-asset'`).Scan(&originalRecord))
	before := f.get(t, "/accounts/a/holdings")
	require.Contains(t, before, "trade_date_floor")
	require.Nil(t, before["trade_date_floor"])
	input := `{"expected_version":"0","cash":"1000","positions":[],"baseline_date":"2026-09-01"}`
	first := f.request(t, "PUT", "/accounts/a/current-holdings", "historical-baseline", input, 200).Body.String()
	var saved CurrentHoldings
	require.NoError(t, json.Unmarshal([]byte(first), &saved))
	require.Equal(t, "2026-09-01", saved.Snapshot.Trades.FloorDate)
	require.Equal(t, httpTestStamp, saved.Snapshot.SavedAt, "the declaration does not backdate the audit/saved timestamp")
	require.Equal(t, "2026-09-01", f.get(t, "/accounts/a/holdings")["trade_date_floor"])
	f.request(t, "POST", "/accounts/a/holdings/i/transactions", "past-buy", `{"expected_version":"1","kind":"buy","date":"2026-09-02","quantity":"10","price":"10","fee":"1","note":"Synthetic","reason":"Actual historical buy"}`, 201)
	f.request(t, "POST", "/accounts/a/holdings/i/transactions", "past-sell", `{"expected_version":"2","kind":"sell","date":"2026-09-03","quantity":"4","price":"20","fee":"2","note":"Synthetic","reason":"Actual historical sell"}`, 201)
	v := f.get(t, "/accounts/a/holdings")
	require.Equal(t, "2026-09-03", v["trade_date_floor"])
	require.Equal(t, "977.00", v["cash"])
	require.Equal(t, "3", v["manual_version"])
	items := httpItems(t, v)
	require.Len(t, items, 1)
	require.Equal(t, "6.000000", items[0]["quantity"])
	require.Equal(t, "10.100000", items[0]["holding_cost"])
	require.Equal(t, "3.833333", items[0]["diluted_cost"])
	rows := httpItems(t, f.get(t, "/accounts/a/holdings/i/transactions"))
	require.Len(t, rows, 2)
	require.Equal(t, "2026-09-03", rows[0]["date"])
	require.Equal(t, "2026-09-02", rows[1]["date"])
	count := auditCount(t, f.store)
	// A receipt retries the original declaration, not today's mutation rules.
	f.store.now = func() time.Time { return time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC) }
	require.Equal(t, first, f.request(t, "PUT", "/accounts/a/current-holdings", "historical-baseline", input, 200).Body.String())
	require.Equal(t, count, auditCount(t, f.store))
	require.Equal(t, "3", f.get(t, "/accounts/a/holdings")["manual_version"])
	var currentRecord string
	var recordCount int
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT payload FROM account_records WHERE account_id='a' AND id='manual-asset'`).Scan(&currentRecord))
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records WHERE account_id='a'`).Scan(&recordCount))
	require.Equal(t, originalRecord, currentRecord)
	require.Equal(t, 1, recordCount)
}

func TestCurrentHoldingsBaselineValidationAndJournalFence(t *testing.T) {
	f := newHTTPFixture(t)
	_, err := f.store.CreateReportedAccount(t.Context(), "account", ReportedAccountInput{ID: "a", Name: "Synthetic", Currency: CNY, OpeningDate: "2026-09-01"})
	require.NoError(t, err)
	require.NoError(t, f.store.AddInstrument(t.Context(), Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Synthetic", Currency: CNY}))
	count := auditCount(t, f.store)
	for _, day := range []string{"2026-09-07", "2026-08-31", "2026-02-30", ""} {
		payload := `{"expected_version":"0","cash":"1000","positions":[],"baseline_date":"` + day + `"}`
		f.request(t, "PUT", "/accounts/a/current-holdings", "invalid", payload, 400)
	}
	require.Equal(t, count, auditCount(t, f.store))
	// Today's legacy-style snapshot may still be redeclared as a historical
	// opening before any journal entry. Supplied quantities remain unknown-cost.
	putSource(t, f.store, "a", "today", "0", 100000, CurrentPosition{"i", 1_000_000})
	require.Equal(t, "2026-09-06", f.get(t, "/accounts/a/holdings")["trade_date_floor"])
	f.request(t, "PUT", "/accounts/a/current-holdings", "redeclaration", `{"expected_version":"1","cash":"1000","positions":[{"instrument_id":"i","quantity":"1"}],"baseline_date":"2026-09-01"}`, 200)
	v := f.get(t, "/accounts/a/holdings")
	require.Equal(t, "2026-09-01", v["trade_date_floor"])
	require.Equal(t, "unknown", httpItems(t, v)[0]["cost_status"])
	f.request(t, "POST", "/accounts/a/holdings/i/transactions", "close", `{"expected_version":"2","kind":"sell","date":"2026-09-02","quantity":"1","price":"10","reason":"Synthetic"}`, 201)
	count = auditCount(t, f.store)
	for _, day := range []string{"2026-09-01", "2026-09-02", "2026-09-03", "2026-09-07"} {
		payload := `{"expected_version":"3","cash":"1010","positions":[],"baseline_date":"` + day + `"}`
		response := f.request(t, "PUT", "/accounts/a/current-holdings", "no-rewind", payload, 422)
		require.Equal(t, "unsafe_trade_date", httpObject(t, response.Body.String())["code"])
	}
	require.Equal(t, count, auditCount(t, f.store))
	f.request(t, "PUT", "/accounts/a/current-holdings", "today-adjustment", `{"expected_version":"3","cash":"1010","positions":[],"baseline_date":"2026-09-06"}`, 200)
	require.Equal(t, "2026-09-06", f.get(t, "/accounts/a/holdings")["trade_date_floor"])
	// Closing/removing holdings never removes the account's historical journal fence.
	f.request(t, "PUT", "/accounts/a/current-holdings", "still-no-rewind", `{"expected_version":"4","cash":"1010","positions":[],"baseline_date":"2026-09-01"}`, 422)
	f.request(t, "POST", "/accounts/a/holdings/i/transactions", "before-adjustment", `{"expected_version":"4","kind":"buy","date":"2026-09-05","quantity":"1","price":"10","reason":"Synthetic"}`, 422)
	rows := httpItems(t, f.get(t, "/accounts/a/holdings/i/transactions"))
	require.Len(t, rows, 1)
	// Omitting the optional date retains the original today-default behavior.
	f.store.now = func() time.Time { return time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC) }
	putSource(t, f.store, "a", "default-tomorrow", "4", 101000)
	require.Equal(t, "2026-09-07", f.get(t, "/accounts/a/holdings")["trade_date_floor"])
}

func TestCurrentHoldingsBaselineOmissionKeepsLegacyReceipts(t *testing.T) {
	f := newHTTPFixture(t)
	manualSourceAccount(t, f.store, "a")
	payload := `{"expected_version":"0","cash":"100","positions":[]}`
	first := f.request(t, "PUT", "/accounts/a/current-holdings", "old-client", payload, 200).Body.String()
	var request string
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT request_json FROM idempotency_receipts WHERE key='old-client'`).Scan(&request))
	require.JSONEq(t, `{"AccountID":"a","Input":{"expected_version":"0","cash":"100.00","positions":[]}}`, request)
	var saved CurrentHoldings
	require.NoError(t, json.Unmarshal([]byte(first), &saved))
	require.Nil(t, saved.Snapshot.Trades, "omitting the date keeps the old snapshot shape")
	f.request(t, "PUT", "/accounts/a/current-holdings", "historical", `{"expected_version":"1","cash":"200","positions":[],"baseline_date":"2020-01-01"}`, 200)
	require.Equal(t, "2020-01-01", f.get(t, "/accounts/a/holdings")["trade_date_floor"])
	// Omitting the date replaces a previously explicit floor with today as well.
	putSource(t, f.store, "a", "reset-today", "2", 20000)
	require.Equal(t, "2026-09-06", f.get(t, "/accounts/a/holdings")["trade_date_floor"])
	f.store.now = func() time.Time { return time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC) }
	count := auditCount(t, f.store)
	require.Equal(t, first, f.request(t, "PUT", "/accounts/a/current-holdings", "old-client", payload, 200).Body.String())
	require.Equal(t, count, auditCount(t, f.store))
	f.request(t, "PUT", "/accounts/a/current-holdings", "old-client", `{"expected_version":"0","cash":"100","positions":[],"baseline_date":"2026-09-06"}`, 409)
	f.account(t, "replay", "CNY", "100.00", nil)
	v := f.get(t, "/accounts/replay/holdings")
	require.Contains(t, v, "trade_date_floor")
	require.Nil(t, v["trade_date_floor"])
}
