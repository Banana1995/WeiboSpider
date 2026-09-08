//go:build e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessLedgerDisabledByDefault(t *testing.T) {
	t.Setenv("LEDGER_ENABLED", "true")
	t.Setenv("LEDGER_TEST_SENTINEL", "must-not-inherit")
	binary, directory := buildServer(t), t.TempDir()
	environment := cleanEnvironment(directory, nil)
	require.Contains(t, environment, "LEDGER_ENABLED=false")
	require.NotContains(t, environment, "LEDGER_ENABLED=true")
	require.NotContains(t, environment, "LEDGER_TEST_SENTINEL=must-not-inherit")
	environment = cleanEnvironment(directory, map[string]string{"LEDGER_ENABLED": "true"})
	require.Contains(t, environment, "LEDGER_ENABLED=true")
	require.NotContains(t, environment, "LEDGER_ENABLED=false")

	p := startProcess(t, binary, directory, nil)
	code, body := request(t, p, "GET", "/api/platform/ledger/accounts", "", nil)
	require.Equal(t, 404, code, "%s", body)
	require.NoFileExists(t, filepath.Join(directory, "ledger.db"))
	p.stop(t, false)
	files, err := filepath.Glob(filepath.Join(directory, "ledger*"))
	require.NoError(t, err)
	require.Empty(t, files, "disabled ledger must not create a database or lease")
}

func TestProcessLedgerReceiptsSurviveRestart(t *testing.T) {
	binary := buildServer(t)
	for _, token := range []string{"", strings.Repeat("synthetic-e2e-token", 2)} {
		t.Run("token="+strconv.FormatBool(token != ""), func(t *testing.T) {
			directory := t.TempDir()
			override := map[string]string{
				"LEDGER_ENABLED": "true", "BACKEND_API_TOKEN": token,
				"LIQUOR_SOURCE_URL": "http://127.0.0.1:1", "TZ": "Asia/Shanghai",
			}
			p := startProcess(t, binary, directory, override)
			require.FileExists(t, filepath.Join(directory, "ledger.db"))
			ledgerRequest := func(method, path, body, key string, want int) []byte {
				t.Helper()
				headers := map[string]string{"Content-Type": "application/json"}
				if token != "" {
					headers["Authorization"] = "Bearer " + token
				}
				if key != "" {
					headers["Idempotency-Key"] = key
				}
				code, data := request(t, p, method, "/api/platform/ledger"+path, body, headers)
				require.Equal(t, want, code, "%s %s: %s", method, path, data)
				return data
			}
			if token != "" {
				code, _ := request(t, p, "GET", "/api/platform/ledger/accounts", "", nil)
				require.Equal(t, 401, code)
			}
			ledgerRequest("GET", "/accounts", "", "", 200)
			const instrument = `{"id":"synthetic-instrument","market":"TEST","code":"SYNTH","name":"Synthetic instrument","currency":"CNY"}`
			require.JSONEq(t, instrument, string(ledgerRequest("POST", "/instruments", instrument, "", 201)))
			ledgerRequest("POST", "/accounts", `{"id":"acct","name":"Synthetic account","currency":"CNY","opening_date":"2020-01-01","opening_cash":"1000.00"}`, "", 201)
			writes := []struct {
				method, path, body, key, amount string
				status                          int
			}{
				{"POST", "/operations", `{"operation":{"id":"deposit","account_id":"acct","date":"2020-01-02","sequence":"1","kind":"deposit","amount":"100.00"},"reason":"Synthetic deposit"}`, "create-deposit", "100.00", 201},
				{"PUT", "/operations/deposit", `{"operation":{"id":"deposit","account_id":"acct","date":"2020-01-02","sequence":"1","kind":"deposit","amount":"200.00"},"expected_version":"1","reason":"Synthetic correction"}`, "replace-deposit", "200.00", 200},
				{"DELETE", "/operations/deposit", `{"expected_version":"2","reason":"Synthetic void"}`, "void-deposit", "200.00", 200},
			}
			type record struct {
				Version   string `json:"version"`
				CreatedAt string `json:"created_at"`
				UpdatedAt string `json:"updated_at"`
				Operation struct {
					ID        string `json:"id"`
					AccountID string `json:"account_id"`
					Date      string `json:"date"`
					Sequence  string `json:"sequence"`
					Kind      string `json:"kind"`
					Amount    string `json:"amount"`
					Voided    bool   `json:"voided"`
				} `json:"operation"`
			}
			var receipts [][]byte
			started := time.Now()
			beijing := time.FixedZone("Beijing", 8*60*60)
			var createdAt string
			for i, write := range writes {
				before := time.Now()
				data := ledgerRequest(write.method, write.path, write.body, write.key, write.status)
				after := time.Now()
				var result record
				require.NoError(t, json.Unmarshal(data, &result))
				require.Equal(t, strconv.Itoa(i+1), result.Version)
				require.Equal(t, "deposit", result.Operation.ID)
				require.Equal(t, "acct", result.Operation.AccountID)
				require.Equal(t, "2020-01-02", result.Operation.Date)
				require.Equal(t, "1", result.Operation.Sequence)
				require.Equal(t, "deposit", result.Operation.Kind)
				require.Equal(t, write.amount, result.Operation.Amount)
				require.Equal(t, i == 2, result.Operation.Voided)
				if i == 0 {
					createdAt = result.CreatedAt
				}
				require.Equal(t, createdAt, result.CreatedAt)
				for _, stamp := range []string{result.CreatedAt, result.UpdatedAt} {
					parsed, err := time.Parse(time.RFC3339Nano, stamp)
					require.NoError(t, err)
					require.True(t, strings.HasSuffix(stamp, "Z"), "wire timestamps stay UTC under Asia/Shanghai TZ")
					require.False(t, parsed.Before(started))
					require.False(t, parsed.After(after))
					require.Contains(t, []string{started.In(beijing).Format(time.DateOnly), after.In(beijing).Format(time.DateOnly)}, parsed.In(beijing).Format(time.DateOnly))
				}
				updated, err := time.Parse(time.RFC3339Nano, result.UpdatedAt)
				require.NoError(t, err)
				require.False(t, updated.Before(before), "mutation uses the actual process clock")
				receipts = append(receipts, data)
			}

			for _, phase := range []string{"before restart", "after restart"} {
				if phase == "after restart" {
					p.stop(t, false)
					p = startProcess(t, binary, directory, override)
				}
				for i, write := range writes {
					retry := ledgerRequest(write.method, write.path, write.body, write.key, write.status)
					require.JSONEq(t, string(receipts[i]), string(retry), phase+": return original receipt, not current v3")
				}
				latest := ledgerRequest("GET", "/operations/deposit", "", "", 200)
				require.JSONEq(t, string(receipts[2]), string(latest), phase)
				var revisions struct {
					Items []struct {
						Record json.RawMessage `json:"record"`
					} `json:"items"`
				}
				require.NoError(t, json.Unmarshal(ledgerRequest("GET", "/operations/deposit/revisions", "", "", 200), &revisions))
				require.Len(t, revisions.Items, 3, phase+": retries must not create revisions")
				for i, revision := range revisions.Items {
					require.JSONEq(t, string(receipts[i]), string(revision.Record), phase)
				}
				var account struct {
					Cash string `json:"cash"`
				}
				require.NoError(t, json.Unmarshal(ledgerRequest("GET", "/accounts/acct", "", "", 200), &account))
				require.Equal(t, "1000.00", account.Cash, phase)
				var instruments struct {
					Items []json.RawMessage `json:"items"`
				}
				require.NoError(t, json.Unmarshal(ledgerRequest("GET", "/instruments", "", "", 200), &instruments))
				require.Len(t, instruments.Items, 1)
				require.JSONEq(t, instrument, string(instruments.Items[0]), phase)
			}
			p.stop(t, false)
		})
	}
}
