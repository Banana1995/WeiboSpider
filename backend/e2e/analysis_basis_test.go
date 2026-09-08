//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessAnalysisBasisSurvivesRestart(t *testing.T) {
	binary, dir := buildServer(t), t.TempDir()
	overrides := map[string]string{"LEDGER_ENABLED": "true", "LIQUOR_SOURCE_URL": "http://127.0.0.1:1"}
	p := startProcess(t, binary, dir, overrides)
	send := func(method, path, body, key string, status int) []byte {
		t.Helper()
		got, data := request(t, p, method, "/api/platform/ledger"+path, body, map[string]string{"Content-Type": "application/json", "Idempotency-Key": key})
		require.Equal(t, status, got, "%s", data)
		return data
	}
	send("POST", "/reported-accounts", `{"id":"synthetic","name":"Synthetic","currency":"CNY","opening_date":"2020-01-01"}`, "a", 201)
	send("POST", "/accounts/synthetic/records", `{"id":"manual-z","entry":{"kind":"asset","date":"2020-01-01","total_assets":"100"}}`, "z", 201)
	send("POST", "/accounts/synthetic/records", `{"id":"manual-a","entry":{"kind":"cash_flow","date":"2020-01-01","flow":"20","total_assets":null}}`, "b", 201)
	before := send("GET", "/accounts/synthetic/analysis-basis?to=2020-01-02", "", "", 200)
	p.stop(t, false)
	p = startProcess(t, binary, dir, overrides)
	require.JSONEq(t, string(before), string(send("GET", "/accounts/synthetic/analysis-basis?to=2020-01-02", "", "", 200)))
	var b map[string]any
	require.NoError(t, json.Unmarshal(before, &b))
	closing := b["closing"].(map[string]any)
	require.Equal(t, "100.00", closing["assets"])
	require.Equal(t, "carried", closing["status"])
	require.Equal(t, "manual-z", closing["source_id"])
	require.Equal(t, "manual-a", closing["record_id"])
	send("DELETE", "/accounts/synthetic/records/manual-z", `{"expected_version":"1","reason":"Synthetic"}`, "void", 200)
	changed := send("GET", "/accounts/synthetic/analysis-basis?to=2020-01-02&since_revision="+b["change_revision"].(string), "", "", 200)
	require.NoError(t, json.Unmarshal(changed, &b))
	require.True(t, b["previous_basis_affected"].(bool))
	require.Nil(t, b["closing"].(map[string]any)["assets"])
	p.stop(t, false)
}
