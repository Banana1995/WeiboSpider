//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
	"github.com/stretchr/testify/require"
)

// The server's valuation endpoint is read-only. Prepare persisted observations
// with the real worker and an injected Store clock while no process owns the DB.
// These fixtures hold cash only, so neither quotes nor FX can access the network.
func seedWeeklyValuation(t *testing.T, directory, accountID, date string) string {
	t.Helper()
	now, err := time.Parse(time.RFC3339, date+"T08:00:00+08:00")
	require.NoError(t, err)
	db, err := ledger.Open(t.Context(), directory)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	store := ledger.NewStore(db, func() time.Time { return now })
	current, err := store.CurrentHoldings(t.Context(), accountID)
	require.NoError(t, err)
	require.NotNil(t, current.Snapshot)
	require.Empty(t, current.Snapshot.Positions, "fixture must not need external market data")
	worker, err := ledger.NewWeeklyWorker(store, nil, nil, ledger.WeeklyConfig{Enabled: true, Time: "08:00"}, nil)
	require.NoError(t, err)
	require.NoError(t, worker.Tick(t.Context()))
	jobs, err := store.ListWeeklyJobs(t.Context(), accountID, "", 0, 30)
	require.NoError(t, err)
	require.NotEmpty(t, jobs.Items)
	job := jobs.Items[0]
	require.Equal(t, date, job.ScheduledBusinessDate)
	require.Equal(t, "holdings_current", job.Source)
	require.Equal(t, "succeeded", job.Status)
	require.Equal(t, 1, job.Attempts)
	require.NotNil(t, job.HistoryID)
	return *job.HistoryID
}

func TestProcessValuationHistorySurvivesRestart(t *testing.T) {
	binary, directory := buildServer(t), t.TempDir()
	db, err := ledger.Open(t.Context(), directory)
	require.NoError(t, err)
	store := ledger.NewStore(db, func() time.Time { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) })
	_, err = store.CreateReportedAccount(t.Context(), "create-cash", ledger.ReportedAccountInput{ID: "cash", Name: "Cash only", Currency: ledger.CNY, OpeningDate: "2020-01-01"})
	require.NoError(t, err)
	cash := ledger.Money(123456)
	_, err = store.PutCurrentHoldings(t.Context(), "cash", "current-cash", ledger.CurrentHoldingsInput{ExpectedVersion: "0", Cash: &cash, Positions: []ledger.CurrentPosition{}})
	require.NoError(t, err)
	require.NoError(t, db.Close())
	override := map[string]string{"LEDGER_ENABLED": "true", "LIQUOR_SOURCE_URL": "http://127.0.0.1:1"}
	p := startProcess(t, binary, directory, override)
	call := func(method, path, body string, status int) []byte {
		t.Helper()
		headers := map[string]string{"Content-Type": "application/json", "Idempotency-Key": "history-read-only"}
		code, data := request(t, p, method, "/api/platform/ledger"+path, body, headers)
		require.Equal(t, status, code, string(data))
		return data
	}
	require.JSONEq(t, `{"items":[]}`, string(call("GET", "/accounts/cash/valuations", "", 200)))
	call("HEAD", "/accounts/cash/valuation", "", 200)
	require.JSONEq(t, `{"items":[]}`, string(call("GET", "/accounts/cash/valuations", "", 200)))
	var current ledger.Valuation
	require.NoError(t, json.Unmarshal(call("GET", "/accounts/cash/valuation", "", 200), &current))
	require.Equal(t, "manual_snapshot", current.Source)
	require.NotNil(t, current.TotalAssets)
	require.Equal(t, cash, *current.TotalAssets)
	call("POST", "/accounts/cash/valuation", "{}", 405)
	require.JSONEq(t, `{"items":[]}`, string(call("GET", "/accounts/cash/valuations", "", 200)))
	p.stop(t, false)
	firstID := seedWeeklyValuation(t, directory, "cash", "2020-01-04")
	require.Equal(t, "1", firstID)
	p = startProcess(t, binary, directory, override)
	detail := call("GET", "/accounts/cash/valuations/1", "", 200)
	var first ledger.ValuationHistory
	require.NoError(t, json.Unmarshal(detail, &first))
	require.Equal(t, firstID, first.ID)
	require.NotNil(t, first.Valuation.TotalAssets)
	require.Equal(t, cash, *first.Valuation.TotalAssets)
	page := call("GET", "/accounts/cash/valuations", "", 200)
	var historyPage struct {
		Items []ledger.ValuationSummary `json:"items"`
	}
	require.NoError(t, json.Unmarshal(page, &historyPage))
	require.Len(t, historyPage.Items, 1)
	for range 2 {
		p.stop(t, false)
		require.Equal(t, firstID, seedWeeklyValuation(t, directory, "cash", "2020-01-04"), "repeated weekly ticks do not duplicate a committed observation")
		p = startProcess(t, binary, directory, override)
		require.JSONEq(t, string(detail), string(call("GET", "/accounts/cash/valuations/1", "", 200)))
		require.JSONEq(t, string(page), string(call("GET", "/accounts/cash/valuations", "", 200)))
		call("HEAD", "/accounts/cash/valuation", "", 200)
		call("HEAD", "/accounts/cash/valuations/1", "", 200)
		call("POST", "/accounts/cash/valuation", "{}", 405)
		require.JSONEq(t, string(page), string(call("GET", "/accounts/cash/valuations", "", 200)))
	}
	p.stop(t, false)
	require.Equal(t, "2", seedWeeklyValuation(t, directory, "cash", "2020-01-11"))
	p = startProcess(t, binary, directory, override)
	var second ledger.ValuationHistory
	require.NoError(t, json.Unmarshal(call("GET", "/accounts/cash/valuations/2", "", 200), &second))
	require.Equal(t, "2", second.ID)
	require.Equal(t, first.Valuation.LedgerRevision, second.Valuation.LedgerRevision)
	require.NoError(t, json.Unmarshal(call("GET", "/accounts/cash/valuations", "", 200), &historyPage))
	require.Len(t, historyPage.Items, 2)
	call("PUT", "/accounts/cash/current-holdings", `{"expected_version":"1","cash":"2000.00","positions":[]}`, 200)
	require.NoError(t, json.Unmarshal(call("GET", "/accounts/cash/valuation", "", 200), &current))
	require.NotNil(t, current.TotalAssets)
	require.Equal(t, ledger.Money(200000), *current.TotalAssets)
	require.JSONEq(t, string(detail), string(call("GET", "/accounts/cash/valuations/1", "", 200)), "current holdings edits cannot change frozen history")
	call("GET", "/accounts/missing/valuations/1", "", 404)
	p.stop(t, false)
}
