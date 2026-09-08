//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessReturnsSnapshotAndRestart(t *testing.T) {
	binary, dir := buildServer(t), t.TempDir()
	overrides := map[string]string{"LEDGER_ENABLED": "true", "LIQUOR_SOURCE_URL": "http://127.0.0.1:1", "LIQUOR_AUTO_SYNC": "false"}
	p := startProcess(t, binary, dir, overrides)
	send := func(method, path, body, key string, status int) []byte {
		t.Helper()
		got, data := request(t, p, method, "/api/platform/ledger"+path, body, map[string]string{"Content-Type": "application/json", "Idempotency-Key": key})
		require.Equal(t, status, got, "%s", data)
		return data
	}
	send("POST", "/reported-accounts", `{"id":"synthetic-returns","name":"Synthetic returns","currency":"CNY","opening_date":"2021-01-01"}`, "account", 201)
	send("POST", "/accounts/synthetic-returns/records", `{"id":"manual-base","entry":{"kind":"cash_flow","date":"2021-01-01","flow":"100","total_assets":"100"}}`, "base", 201)
	send("POST", "/accounts/synthetic-returns/records", `{"id":"manual-close","entry":{"kind":"asset","date":"2022-01-01","total_assets":"110"}}`, "close", 201)
	path := "/accounts/synthetic-returns/analysis-basis?to=2022-02-01"
	before := send("GET", path, "", "", 200)
	var b struct {
		Revision string `json:"revision"`
		Returns  struct {
			Revision string `json:"revision"`
			To       string `json:"effective_to"`
			Profit   struct {
				Value string `json:"value"`
			} `json:"profit"`
			Dietz struct {
				Value string `json:"value"`
			} `json:"modified_dietz"`
			XIRR struct {
				Value string `json:"value"`
			} `json:"xirr"`
			TWR struct {
				Value string `json:"value"`
			} `json:"twr"`
			Annualized struct {
				Value string `json:"value"`
			} `json:"twr_annualized"`
			Curve []struct {
				Baseline bool `json:"baseline"`
				TWR      struct {
					Value string `json:"value"`
				} `json:"twr"`
			} `json:"curve"`
		} `json:"returns"`
	}
	require.NoError(t, json.Unmarshal(before, &b))
	require.Equal(t, b.Revision, b.Returns.Revision)
	require.Equal(t, "2022-01-01", b.Returns.To)
	require.Equal(t, "10.00", b.Returns.Profit.Value)
	require.Equal(t, "0.100000000000", b.Returns.Dietz.Value)
	require.Equal(t, b.Returns.Dietz.Value, b.Returns.XIRR.Value)
	require.Equal(t, b.Returns.Dietz.Value, b.Returns.TWR.Value)
	require.Equal(t, b.Returns.TWR.Value, b.Returns.Annualized.Value)
	require.Len(t, b.Returns.Curve, 2)
	require.True(t, b.Returns.Curve[0].Baseline)
	require.Equal(t, "0.000000000000", b.Returns.Curve[0].TWR.Value)
	require.Equal(t, b.Returns.TWR.Value, b.Returns.Curve[1].TWR.Value)
	require.Empty(t, send("HEAD", path, "", "", 200))
	require.JSONEq(t, string(before), string(send("GET", path, "", "", 200)))
	p.stop(t, false)
	p = startProcess(t, binary, dir, overrides)
	require.JSONEq(t, string(before), string(send("GET", path, "", "", 200)))
	send("PUT", "/accounts/synthetic-returns/records/manual-close", `{"expected_version":"1","reason":"Synthetic correction","entry":{"kind":"asset","date":"2022-01-01","total_assets":"90"}}`, "edit", 200)
	changed := send("GET", path, "", "", 200)
	oldRevision := b.Revision
	require.NoError(t, json.Unmarshal(changed, &b))
	require.NotEqual(t, oldRevision, b.Revision)
	require.Equal(t, b.Revision, b.Returns.Revision)
	require.Equal(t, "-10.00", b.Returns.Profit.Value)
	require.Equal(t, "-0.100000000000", b.Returns.XIRR.Value)
	require.Equal(t, b.Returns.XIRR.Value, b.Returns.TWR.Value)
	require.Equal(t, b.Returns.TWR.Value, b.Returns.Curve[1].TWR.Value)
	p.stop(t, false)
}
