//go:build e2e

package e2e

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessValuationHistorySurvivesRestart(t *testing.T) {
	binary, directory := buildServer(t), t.TempDir()
	override := map[string]string{"LEDGER_ENABLED": "true", "LIQUOR_SOURCE_URL": "http://127.0.0.1:1"}
	p := startProcess(t, binary, directory, override)
	saves := 0
	call := func(method, path, body string, status int) []byte {
		t.Helper()
		headers := map[string]string{"Content-Type": "application/json"}
		if method == "POST" && strings.HasSuffix(path, "/valuation") {
			saves++
			headers["Idempotency-Key"] = "history-" + strconv.Itoa(saves)
		}
		code, data := request(t, p, method, "/api/platform/ledger"+path, body, headers)
		require.Equal(t, status, code, string(data))
		return data
	}
	call("POST", "/accounts", `{"id":"cash","name":"Cash only","currency":"CNY","opening_date":"2020-01-01","opening_cash":"1234.56"}`, 201)
	require.JSONEq(t, `{"items":[]}`, string(call("GET", "/accounts/cash/valuations", "", 200)))
	call("HEAD", "/accounts/cash/valuation", "", 200)
	require.JSONEq(t, `{"items":[]}`, string(call("GET", "/accounts/cash/valuations", "", 200)))
	var first map[string]json.RawMessage
	call("GET", "/accounts/cash/valuation", "", 200)
	require.JSONEq(t, `{"items":[]}`, string(call("GET", "/accounts/cash/valuations", "", 200)))
	firstReceipt := call("POST", "/accounts/cash/valuation", "{}", 200)
	require.NoError(t, json.Unmarshal(firstReceipt, &first))
	require.JSONEq(t, `"1"`, string(first["history_id"]))
	require.JSONEq(t, `"1234.56"`, string(first["total_assets"]))
	detail := call("GET", "/accounts/cash/valuations/1", "", 200)
	page := call("GET", "/accounts/cash/valuations", "", 200)
	for range 2 {
		p.stop(t, false)
		p = startProcess(t, binary, directory, override)
		require.JSONEq(t, string(detail), string(call("GET", "/accounts/cash/valuations/1", "", 200)))
		require.JSONEq(t, string(page), string(call("GET", "/accounts/cash/valuations", "", 200)))
		call("HEAD", "/accounts/cash/valuation", "", 200)
		call("HEAD", "/accounts/cash/valuations/1", "", 200)
		code, retried := request(t, p, "POST", "/api/platform/ledger/accounts/cash/valuation", "{}", map[string]string{"Content-Type": "application/json", "Idempotency-Key": "history-1"})
		require.Equal(t, 200, code)
		require.Equal(t, string(firstReceipt), string(retried))
	}
	var second map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(call("POST", "/accounts/cash/valuation", "{}", 200), &second))
	require.JSONEq(t, `"2"`, string(second["history_id"]))
	require.Equal(t, string(first["ledger_revision"]), string(second["ledger_revision"]))
	call("GET", "/accounts/missing/valuations/1", "", 404)
	p.stop(t, false)
}
