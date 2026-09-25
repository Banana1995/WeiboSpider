package ledger

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func channelMoney(t *testing.T, raw string) *Money {
	t.Helper()
	n, err := ParseMoney(raw)
	require.NoError(t, err)
	return &n
}

func TestImportChannelSnapshotParsing(t *testing.T) {
	items := importChannelAssets("平台甲：12.34\r\n平台乙: 5e1\n钱包：0\r", channelMoney(t, "62.34"))
	require.Len(t, items, 3)
	require.Equal(t, "平台甲", items[0].Name)
	require.Equal(t, Money(1234), *items[0].Amount)
	require.Zero(t, *items[2].Amount)
	for _, detail := range []string{
		"平台甲：12.34\n平台乙：49.99",    // Does not reconcile to D.
		"平台甲：62.34\n平台甲：0",        // Duplicate names are ambiguous.
		"平台甲：62.34\ninvalid text", // Never retain a partial parse.
		"平台甲：62.34\n平台乙：",         // Empty is not zero.
		"平台甲：62.34\n平台乙：-1",
		"平台甲：62.341",
		strings.Repeat("X", 129) + "：62.34",
	} {
		require.Nil(t, importChannelAssets(detail, channelMoney(t, "62.34")), detail)
	}
	require.Nil(t, importChannelAssets("平台甲：0", nil))
	require.Nil(t, importChannelAssets("平台甲：92233720368547758.07\n平台乙：0.01", channelMoney(t, "0")))
	large := importChannelAssets("平台甲：90071992547409.01\n平台乙：0.02", channelMoney(t, "90071992547409.03"))
	require.Len(t, large, 2)
}

func TestChannelImportAndLegacyReadsKeepFinancialFactsAndReceipts(t *testing.T) {
	f := newHTTPFixture(t)
	data := syntheticImportZip(t, syntheticImportParts(t, [][7]string{
		{"转入转出", "2020-01-01", "100", "", "", "", ""},
		{"记总资产", "2020-01-02", "", "101.01", "", "", "平台甲：101.01\r\n钱包：0"},
		{"记总资产", "2020-01-02", "", "103.03", "", "", "平台甲：101.01\r\n钱包：0\r\n平台乙：2.02"},
		{"转入转出", "2020-01-03", "-1.01", "102.02", "", "", "平台甲：100\r\n钱包：0\r\n平台乙：2.02"},
		{"记总资产", "2020-01-04", "", "104", "", "", "原始非渠道说明"},
	}))
	p := parseSynthetic(t, data)
	require.Equal(t, []string{"平台甲", "钱包", "平台乙"}, p.Channels.Names)
	require.Equal(t, 3, p.Channels.SnapshotCount)
	require.Equal(t, []int{9}, p.Channels.UnresolvedRows)
	result, err := f.store.ConfirmAccountImport(t.Context(), "channel-account", "channel-import", p.Digest, true, data)
	require.NoError(t, err)
	summary, err := f.store.EffectiveSummary(t.Context(), "channel-account")
	require.NoError(t, err)
	require.Equal(t, 5, summary.RowCount)
	require.Equal(t, "100.00", summary.TotalIn)
	require.Equal(t, "1.01", summary.TotalOut)
	require.Equal(t, Money(10400), *summary.LatestAssets)

	latest, err := f.store.AccountChannels(t.Context(), "channel-account", "2020-01-04")
	require.NoError(t, err)
	require.Equal(t, "2020-01-03", latest.SourceDate) // Not the later total-only date.
	require.Len(t, latest.Items, 3)
	require.Equal(t, "平台甲", latest.Items[0].Name)
	require.Equal(t, Money(10000), *latest.Items[0].Amount)
	dayTwo, err := f.store.AccountChannels(t.Context(), "channel-account", "2020-01-02")
	require.NoError(t, err)
	require.Len(t, dayTwo.Items, 3) // The second same-day snapshot wins.
	before, err := f.store.AccountChannels(t.Context(), "channel-account", "2020-01-01")
	require.NoError(t, err)
	require.Empty(t, before.Items)
	record := f.get(t, "/accounts/channel-account/records/"+latest.SourceRecordID)
	require.NotContains(t, record, "flow_channel") // G is not a transfer destination.
	require.Len(t, record["channel_assets"], 3)

	// Simulate records frozen by the pre-channel writer; source rows and manifest
	// remain exactly as imported. Merely reading must not rewrite old journal data.
	_, err = f.store.db.ExecContext(t.Context(), `UPDATE account_records SET payload=json_remove(payload,'$.channel_assets') WHERE account_id='channel-account'`)
	require.NoError(t, err)
	var auditTrigger string
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master WHERE type='trigger' AND name='audit_log_no_update'`).Scan(&auditTrigger))
	_, err = f.store.db.ExecContext(t.Context(), `DROP TRIGGER audit_log_no_update`)
	require.NoError(t, err)
	_, err = f.store.db.ExecContext(t.Context(), `UPDATE audit_log SET after_json=json_remove(after_json,'$.channel_assets') WHERE account_id='channel-account' AND entity_type='account_record'`)
	require.NoError(t, err)
	_, err = f.store.db.ExecContext(t.Context(), auditTrigger)
	require.NoError(t, err)
	var frozen string
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT payload FROM account_records WHERE id=? AND account_id=?`, latest.SourceRecordID, "channel-account").Scan(&frozen))
	legacy, err := f.store.AccountChannels(t.Context(), "channel-account", "2020-01-04")
	require.NoError(t, err)
	require.Equal(t, latest, legacy)
	record = f.get(t, "/accounts/channel-account/records/"+latest.SourceRecordID)
	require.Len(t, record["channel_assets"], 3)
	f.request(t, "GET", "/accounts/channel-account/records/"+latest.SourceRecordID+"/revisions", "", "", 200)
	var unchanged string
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT payload FROM account_records WHERE id=? AND account_id=?`, latest.SourceRecordID, "channel-account").Scan(&unchanged))
	require.Equal(t, frozen, unchanged)
	replay, err := f.store.ConfirmAccountImport(t.Context(), "channel-account", "channel-import", p.Digest, true, data)
	require.NoError(t, err)
	require.Equal(t, result, replay)
	duplicate, err := f.store.ConfirmAccountImport(t.Context(), "channel-account", "channel-import-again", p.Digest, true, data)
	require.NoError(t, err)
	require.True(t, duplicate.Duplicate)

	// Changing a legacy total without a breakdown must not revive old channel data.
	f.request(t, "PUT", "/accounts/channel-account/records/"+latest.SourceRecordID, "legacy-edit",
		`{"expected_version":"1","reason":"Synthetic correction","entry":{"kind":"cash_flow","date":"2020-01-03","flow":"-1.01","total_assets":"200.00","note":""}}`, 200)
	require.NotContains(t, f.get(t, "/accounts/channel-account/records/"+latest.SourceRecordID), "channel_assets")
}

func TestChannelRecordsHTTPValidationHistoryAndAtomicRetry(t *testing.T) {
	f := newHTTPFixture(t)
	f.request(t, "POST", "/accounts", "channel-account-create", `{"id":"channels","name":"Synthetic","currency":"CNY","opening_date":"2020-01-01"}`, 201)
	for _, entry := range []string{
		`{"kind":"asset","date":"2020-01-01","total_assets":"1.00","channel_assets":[{"name":"A","amount":"2.00"}]}`,
		`{"kind":"asset","date":"2020-01-01","total_assets":"0.00","channel_assets":[{"name":"A"}]}`,
		`{"kind":"asset","date":"2020-01-01","total_assets":"0.00","channel_assets":[{"name":"A","amount":"0"},{"name":"A","amount":"0"}]}`,
		`{"kind":"cash_flow","date":"2020-01-01","flow":"1.00","channel_assets":[{"name":"A","amount":"0"}]}`,
		`{"kind":"asset","date":"2020-01-01","total_assets":"0.00","flow_channel":"A"}`,
	} {
		f.request(t, "POST", "/accounts/channels/records", "bad-channel", `{"id":"manual-bad","entry":`+entry+`}`, 400)
	}
	create := `{"id":"manual-channel","entry":{"kind":"asset","date":"2020-01-02","flow":null,"total_assets":"12.34","note":"","channel_assets":[{"name":"A","amount":"12.34"},{"name":"B","amount":"0"}]}}`
	first := f.request(t, "POST", "/accounts/channels/records", "new-channel", create, 201).Body.String()
	edit := `{"expected_version":"1","reason":"Synthetic correction","entry":{"kind":"asset","date":"2020-01-02","flow":null,"total_assets":"12.35","note":"","channel_assets":[{"name":"A","amount":"12.34"},{"name":"B","amount":"0.01"}]}}`
	f.request(t, "PUT", "/accounts/channels/records/manual-channel", "edit-channel", edit, 200)
	require.Equal(t, first, f.request(t, "POST", "/accounts/channels/records", "new-channel", create, 201).Body.String())
	f.request(t, "POST", "/accounts/channels/records", "channel-out", `{"id":"manual-out","entry":{"kind":"cash_flow","date":"2020-01-03","flow":"-1.00","total_assets":null,"note":"","flow_channel":"B"}}`, 201)
	current := f.get(t, "/accounts/channels/channels?to=2020-01-03")
	var context AccountChannels
	encoded, err := json.Marshal(current)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, &context))
	require.Equal(t, Money(1), *context.Items[1].Amount) // A flow tag is not an asset observation.
	revisions := httpItems(t, f.get(t, "/accounts/channels/records/manual-channel/revisions"))
	require.Len(t, revisions, 2)
	require.Equal(t, "0.00", revisions[0]["record"].(map[string]any)["channel_assets"].([]any)[1].(map[string]any)["amount"])
	f.request(t, "DELETE", "/accounts/channels/records/manual-channel", "void-channel", `{"expected_version":"2","reason":"Synthetic void"}`, 200)
	require.Empty(t, f.get(t, "/accounts/channels/channels?to=2020-01-03")["items"])
	f.request(t, "HEAD", "/accounts/channels/channels", "", "", 200)
	f.request(t, "GET", "/accounts/missing/channels", "", "", 404)
	for _, path := range []string{"/accounts/channels/channels?", "/accounts/channels/channels?to=bad", "/accounts/channels/channels?bad=1"} {
		f.request(t, "GET", path, "", "", 400)
	}
}
