//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessAccountRecordReceipts(t *testing.T) {
	binary, dir := buildServer(t), t.TempDir()
	overrides := map[string]string{"LEDGER_ENABLED": "true", "LIQUOR_SOURCE_URL": "http://127.0.0.1:1"}
	p := startProcess(t, binary, dir, overrides)
	send := func(method, path, body, key string, want int) []byte {
		t.Helper()
		status, data := request(t, p, method, "/api/platform/ledger"+path, body, map[string]string{"Content-Type": "application/json", "Idempotency-Key": key})
		require.Equal(t, want, status, "%s", data)
		return data
	}
	account := `{"id":"synthetic","name":"Synthetic","currency":"USD","opening_date":"2020-01-01"}`
	accountReceipt := send("POST", "/accounts", account, "account", 201)
	create := `{"id":"manual-synthetic","entry":{"kind":"cash_flow","date":"2099-01-01","flow":"-90071992547409.01","total_assets":null,"note":"Synthetic"}}`
	receipt := send("POST", "/accounts/synthetic/records", create, "create", 201)
	// Keep the original future-dated receipt, but correct to a past asset date:
	// current summary amounts intentionally exclude future asset assertions.
	replace := `{"expected_version":"1","reason":"Synthetic correction","entry":{"kind":"asset","date":"2020-01-02","total_assets":"0"}}`
	send("PUT", "/accounts/synthetic/records/manual-synthetic", replace, "replace", 200)
	for i := 0; i < 2; i++ {
		if i == 1 {
			p.stop(t, false)
			p = startProcess(t, binary, dir, overrides)
		}
		require.JSONEq(t, string(accountReceipt), string(send("POST", "/accounts", account, "account", 201)))
		require.JSONEq(t, string(receipt), string(send("POST", "/accounts/synthetic/records", create, "create", 201)))
		var summary map[string]any
		require.NoError(t, json.Unmarshal(send("GET", "/accounts/synthetic/effective-summary", "", "", 200), &summary))
		require.Equal(t, "0.00", summary["latest_assets"])
		require.Equal(t, float64(1), summary["row_count"])
		send("PUT", "/accounts/synthetic/records/manual-synthetic", replace, "stale", 409)
		send("GET", "/accounts/synthetic/records/manual-synthetic/revisions", "", "", 200)
		var detail map[string]any
		require.NoError(t, json.Unmarshal(send("GET", "/accounts/synthetic", "", "", 200), &detail))
		require.Nil(t, detail["cash"])
	}
	send("DELETE", "/accounts/synthetic/records/manual-synthetic", `{"expected_version":"2","reason":"Synthetic void"}`, "void", 200)
	p.stop(t, false)
}
