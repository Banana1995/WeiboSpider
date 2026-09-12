//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
	"github.com/stretchr/testify/require"
)

func TestProcessWeeklyFreshSchemaReadOnlyAndInterruptedExpiry(t *testing.T) {
	binary, root := buildServer(t), t.TempDir()
	db, err := ledger.Open(t.Context(), root)
	require.NoError(t, err)
	store := ledger.NewStore(db, func() time.Time { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) })
	_, err = store.CreateReportedAccount(t.Context(), "create-cash", ledger.ReportedAccountInput{ID: "cash", Name: "Synthetic", Currency: ledger.CNY, OpeningDate: "2020-01-01"})
	require.NoError(t, err)
	cash := ledger.Money(12345)
	_, err = store.PutCurrentHoldings(t.Context(), "cash", "current-cash", ledger.CurrentHoldingsInput{ExpectedVersion: "0", Cash: &cash, Positions: []ledger.CurrentPosition{}})
	require.NoError(t, err)
	var checksum string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT group_concat(name||checksum) FROM (SELECT * FROM schema_migrations ORDER BY name)`).Scan(&checksum))
	require.NoError(t, db.Close())
	override := map[string]string{"LEDGER_ENABLED": "true", "LIQUOR_SOURCE_URL": "http://127.0.0.1:1"}
	p := startProcess(t, binary, root, override)
	call := func(method, path string, status int) []byte {
		t.Helper()
		code, data := request(t, p, method, "/api/platform/ledger"+path, "", nil)
		require.Equal(t, status, code, string(data))
		return data
	}
	var status map[string]any
	require.NoError(t, json.Unmarshal(call("GET", "/weekly-status", 200), &status))
	require.Equal(t, false, status["enabled"])
	require.Equal(t, "08:00", status["time"])
	for range 2 {
		require.Empty(t, call("HEAD", "/accounts/cash/weekly-jobs", 200))
		require.JSONEq(t, `{"items":[]}`, string(call("GET", "/accounts/cash/weekly-jobs", 200)))
		require.JSONEq(t, `{"items":[]}`, string(call("GET", "/accounts/cash/valuations", 200)))
	}
	// Seed a real weekly observation through the current worker API, not the
	// removed public valuation-write endpoint or a fabricated audit snapshot.
	p.stop(t, false)
	historyID := seedWeeklyValuation(t, root, "cash", "2020-01-04")
	p = startProcess(t, binary, root, override)
	frozenPath := "/accounts/cash/valuations/" + historyID
	frozen := call("GET", frozenPath, 200)
	p.stop(t, false)
	db, err = ledger.Open(t.Context(), root)
	require.NoError(t, err)
	var current string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT group_concat(name||checksum) FROM (SELECT * FROM schema_migrations ORDER BY name)`).Scan(&current))
	require.Equal(t, checksum, current)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM schema_migrations`).Scan(&count))
	require.Equal(t, 1, count)
	result, err := db.ExecContext(t.Context(), `INSERT INTO weekly_jobs(account_id,scheduled_business_date,source,status,attempts,created_at,started_at,lease_until) VALUES('cash','2020-01-11','holdings_current','running',1,'2020-01-11T00:00:00.000000000Z','2020-01-11T00:00:00.000000000Z','2020-01-11T00:01:00.000000000Z')`)
	require.NoError(t, err)
	jobID, err := result.LastInsertId()
	require.NoError(t, err)
	jobPath := "/accounts/cash/weekly-jobs/" + strconv.FormatInt(jobID, 10)
	require.NoError(t, db.Close())
	// Disabled restart leaves even interrupted claims untouched.
	p = startProcess(t, binary, root, override)
	var job ledger.WeeklyJob
	require.NoError(t, json.Unmarshal(call("GET", jobPath, 200), &job))
	require.Equal(t, "running", job.Status)
	require.Empty(t, call("HEAD", jobPath, 200))
	p.stop(t, true)
	override["LEDGER_WEEKLY_ENABLED"] = "true"
	p = startProcess(t, binary, root, override)
	require.Eventually(t, func() bool {
		data := call("GET", jobPath, 200)
		require.NoError(t, json.Unmarshal(data, &job))
		return job.Status == "skipped"
	}, 3*time.Second, 10*time.Millisecond)
	require.Equal(t, "expired", job.ErrorCode)
	require.Nil(t, job.HistoryID)
	require.Equal(t, 1, job.Attempts)
	require.JSONEq(t, string(frozen), string(call("GET", frozenPath, 200)))
	require.Empty(t, call("HEAD", "/weekly-status", 200))
	call("GET", "/accounts/wrong/weekly-jobs/"+strconv.FormatInt(jobID, 10), 404)
	completed := call("GET", jobPath, 200)
	p.stop(t, false)
	p = startProcess(t, binary, root, override)
	require.JSONEq(t, string(completed), string(call("GET", jobPath, 200)))
	require.JSONEq(t, string(frozen), string(call("GET", frozenPath, 200)))
	p.stop(t, false)
	// No fake clock or provider URL is exposed in production configuration.
	t.Setenv("LEDGER_WEEKLY_ENABLED", "true")
	t.Setenv("LEDGER_WEEKLY_TIME", "broken")
	p = startProcess(t, binary, t.TempDir(), nil)
	p.stop(t, false) // cleanEnvironment overrides caller state.
	for _, bad := range []map[string]string{{"LEDGER_WEEKLY_ENABLED": "true"}, {"LEDGER_WEEKLY_TIME": "8:00"}, {"LEDGER_WEEKLY_ENABLED": "1"}} {
		dir := filepath.Join(t.TempDir(), "not-created")
		cmd := exec.CommandContext(t.Context(), binary)
		cmd.Env = cleanEnvironment(dir, bad)
		output, err := cmd.CombinedOutput()
		require.Error(t, err)
		require.Contains(t, string(output), "LEDGER_WEEKLY")
		_, err = os.Stat(dir)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}
