//go:build e2e

package e2e

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

type point struct {
	Date   string `json:"price_date"`
	Price  int64  `json:"price_cents"`
	Change int64  `json:"change_cents"`
}

type quote struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Unit string `json:"unit"`
	point
}

type latest struct {
	Source string  `json:"source"`
	Basis  string  `json:"price_basis"`
	Date   string  `json:"price_date"`
	Items  []quote `json:"items"`
}

func latestPrices(t *testing.T, p *process) latest {
	t.Helper()
	code, body := request(t, p, "GET", prefix+"/latest", "", nil)
	require.Equal(t, 200, code)
	var result latest
	require.NoError(t, json.Unmarshal(body, &result))
	return result
}

func databaseCount(t *testing.T, directory string) int {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(directory, "liquor.db"), RawQuery: "mode=ro&_query_only=1"}
	db, err := sql.Open("sqlite3", u.String())
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM prices").Scan(&count))
	var integrity string
	require.NoError(t, db.QueryRowContext(t.Context(), "PRAGMA quick_check").Scan(&integrity))
	require.Equal(t, "ok", integrity)
	return count
}

func TestProcessLiveUserWorkflow(t *testing.T) {
	if os.Getenv("LIQUOR_E2E_LIVE") != "1" {
		t.Skip("set LIQUOR_E2E_LIVE=1 to test the real source with an actual server process")
	}
	binary, directory := buildServer(t), t.TempDir()
	p := startProcess(t, binary, directory, nil)
	code, _ := request(t, p, "GET", "/healthz", "", nil)
	require.Equal(t, 200, code)
	require.Equal(t, "idle", status(t, p).State)
	require.Empty(t, latestPrices(t, p).Items)
	t.Log("PASS fresh install: health=200, empty list, automatic collection disabled")

	started := time.Now()
	id := trigger(t, p)
	code, _ = request(t, p, "POST", prefix+"/sync", `{}`, nil)
	require.Equal(t, 409, code)
	first := waitSync(t, p, id)
	require.Equal(t, "succeeded", first.State, "%+v\n%s", first, p.log.String())
	prices := latestPrices(t, p)
	require.NotEmpty(t, prices.Items)
	require.Equal(t, first.Date, prices.Date)
	require.Equal(t, "sina_jiujia", prices.Source)
	require.Equal(t, "terminal_retail_weighted_mean", prices.Basis)
	count := databaseCount(t, directory)
	require.Equal(t, first.Records, count)
	t.Logf("PASS real sync: duration=%s date=%s products=%d quotes=%d duplicate-trigger=409", time.Since(started).Round(time.Millisecond), prices.Date, len(prices.Items), count)

	for _, q := range prices.Items {
		code, body := request(t, p, "GET", prefix+"/products/"+strconv.FormatInt(q.ID, 10)+"/history?limit=366", "", nil)
		require.Equal(t, 200, code)
		var history struct {
			Items []point `json:"items"`
		}
		require.NoError(t, json.Unmarshal(body, &history))
		require.NotEmpty(t, history.Items)
		require.Equal(t, q.point, history.Items[0])
		for i := 1; i < len(history.Items); i++ {
			require.Greater(t, history.Items[i-1].Date, history.Items[i].Date)
		}
		t.Logf("PASS history: product=%s price_cents=%d points=%d", q.Name, q.Price, len(history.Items))
	}
	productID := strconv.FormatInt(prices.Items[0].ID, 10)
	code, body := request(t, p, "GET", prefix+"/products/"+productID+"/history?from="+prices.Date+"&to="+prices.Date+"&limit=1", "", nil)
	require.Equal(t, 200, code)
	var oneDay struct {
		Items []point `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &oneDay))
	require.Len(t, oneDay.Items, 1)
	t.Log("PASS date filter: inclusive same-day range returns exactly one quote")

	second := waitSync(t, p, trigger(t, p))
	require.Equal(t, "succeeded", second.State, "%+v\n%s", second, p.log.String())
	require.Equal(t, first.Date, second.Date)
	require.Equal(t, count, databaseCount(t, directory))
	require.NotEqual(t, first.RunID, second.RunID)
	t.Logf("PASS repeat sync: new run id, unchanged stored row count=%d", count)
	if rss, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(p.cmd.Process.Pid)).Output(); err == nil {
		t.Logf("OBSERVED local native process RSS KiB=%s (not a Linux/container capacity measurement)", strings.TrimSpace(string(rss)))
	}
	p.stop(t, false)
	t.Log("PASS SIGTERM: server exited successfully")
	p = startProcess(t, binary, directory, nil)
	require.Equal(t, second.RunID, status(t, p).RunID)
	require.Equal(t, prices.Date, latestPrices(t, p).Date)
	require.Equal(t, len(prices.Items), len(latestPrices(t, p).Items))
	require.Equal(t, count, databaseCount(t, directory))
	t.Log("PASS process restart: same data directory retains prices and successful sync status")

	ctxCmd := exec.CommandContext(t.Context(), binary)
	ctxCmd.Env = cleanEnvironment(directory, nil)
	output, err := ctxCmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "database already owned")
	code, _ = request(t, p, "GET", "/healthz", "", nil)
	require.Equal(t, 200, code)
	t.Log("PASS duplicate service: second process refused the database lease; original service stayed healthy")
}
