package ledger

import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"strconv"
	"strings"
	"testing"
)

func basisRead(t *testing.T, f *httpFixture, account, _, extra string) AnalysisBasis {
	t.Helper()
	var b AnalysisBasis
	path := "/accounts/" + account + "/analysis-basis"
	if extra != "" {
		path += "?" + strings.TrimPrefix(extra, "&")
	}
	require.NoError(t, json.Unmarshal(f.request(t, "GET", path, "", "", 200).Body.Bytes(), &b))
	return b
}
func basisEntry(t *testing.T, f *httpFixture, id, date, kind, flow, assets string) {
	t.Helper()
	f.request(t, "POST", "/accounts/a/records", id, fmt.Sprintf(`{"id":%q,"entry":{"kind":%q,"date":%q,"flow":%s,"total_assets":%s,"note":"Synthetic"}}`, id, kind, date, flow, assets), 201)
}
func reportedFixture(t *testing.T) *httpFixture {
	t.Helper()
	f := newHTTPFixture(t)
	f.request(t, "POST", "/accounts", "a", `{"id":"a","name":"Synthetic","currency":"CNY","opening_date":"2020-01-01"}`, 201)
	return f
}
func mustInt(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	require.NoError(t, err)
	return n
}

func TestBasisMissingZeroCarryCombinedAndCutoff(t *testing.T) {
	f := reportedFixture(t)
	b := basisRead(t, f, "a", "", "")
	require.Nil(t, b.Opening)
	require.Nil(t, b.Closing)
	require.Empty(t, b.Points)
	require.Equal(t, "unavailable", b.Status)
	basisEntry(t, f, "manual-future", "2099-01-01", "asset", "null", `"999"`)
	basisEntry(t, f, "manual-first-flow", "2020-01-01", "cash_flow", `"20"`, "null")
	b = basisRead(t, f, "a", "", "")
	require.Len(t, b.Points, 1)
	require.Nil(t, b.Closing.Assets)
	basisEntry(t, f, "manual-zero", "2020-01-02", "asset", "null", `"0"`)
	basisEntry(t, f, "manual-carry", "2020-01-03", "cash_flow", `"50"`, "null")
	basisEntry(t, f, "manual-combined", "2020-01-04", "cash_flow", `"10"`, `"80"`)
	basisEntry(t, f, "manual-log", "2020-01-04", "log", "null", "null")
	b = basisRead(t, f, "a", "", "from=2020-01-03&to=2020-01-04")
	require.Equal(t, Money(0), *b.Opening.Assets)
	require.Equal(t, Money(5000), *b.Points[0].Assets)
	require.Nil(t, b.Points[0].Record.TotalAssets)
	require.Equal(t, "manual-zero", b.Points[0].SourceID)
	require.Equal(t, "carried", b.Points[0].Status)
	require.Equal(t, Money(8000), *b.Closing.Assets)
	require.Equal(t, "60.00", b.NetFlow)
	require.Equal(t, "manual-combined", b.Closing.RecordID)
	basisEntry(t, f, "manual-backdate", "2019-01-01", "cash_flow", `"1"`, "null")
	require.Nil(t, basisRead(t, f, "a", "", "to=2019-01-01").Closing.Assets)
}

func TestBasisStableOrderCorrectionsVoidAndInvalidation(t *testing.T) {
	f := reportedFixture(t)
	basisEntry(t, f, "manual-z", "2020-01-01", "asset", "null", `"100"`)
	basisEntry(t, f, "manual-a", "2020-01-01", "asset", "null", `"200"`)
	basisEntry(t, f, "manual-carry", "2020-01-02", "cash_flow", `"-5"`, "null")
	before := basisRead(t, f, "a", "", "")
	require.Equal(t, "manual-a", before.Closing.SourceID)
	f.request(t, "PUT", "/accounts/a/records/manual-z", "edit", `{"expected_version":"1","reason":"Synthetic","entry":{"kind":"asset","date":"2020-01-01","total_assets":"300"}}`, 200)
	b := basisRead(t, f, "a", "", "since_revision="+before.ChangeRevision)
	require.Equal(t, before.Points[0].Sequence, b.Points[0].Sequence)
	require.Equal(t, Money(19500), *b.Closing.Assets)
	require.True(t, b.PreviousBasisAffected)
	require.Len(t, b.Changes, 1)
	require.Equal(t, "2020-01-01", b.Changes[0].From)
	require.Nil(t, b.Changes[0].To)
	require.NotEqual(t, before.Revision, b.Revision)
	same := basisRead(t, f, "a", "", "since_revision="+b.ChangeRevision)
	require.False(t, same.PreviousBasisAffected)
	require.Empty(t, same.Changes)
	require.Equal(t, b.Revision, same.Revision)
	f.request(t, "DELETE", "/accounts/a/records/manual-a", "void", `{"expected_version":"1","reason":"Synthetic"}`, 200)
	b = basisRead(t, f, "a", "", "")
	require.Equal(t, Money(29500), *b.Closing.Assets)
	require.Equal(t, "manual-z", b.Closing.SourceID)
	f.request(t, "PUT", "/accounts/a/records/manual-z", "backdate", `{"expected_version":"2","reason":"Synthetic","entry":{"kind":"asset","date":"2018-01-01","total_assets":"300"}}`, 200)
	require.Equal(t, "2018-01-01", basisRead(t, f, "a", "", "since_revision="+b.ChangeRevision).Changes[0].From)
	p1 := f.get(t, "/accounts/a/records?limit=1")
	p2 := f.get(t, "/accounts/a/records?limit=1&cursor="+p1["next_cursor"].(string))
	require.NotEqual(t, httpItems(t, p1)[0]["id"], httpItems(t, p2)[0]["id"])
}

func TestBasisImportOrderExactValuesAndIndependentInputs(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "10.00", nil)
	data := syntheticImport(t)
	p := parseSynthetic(t, data)
	_, err := f.store.ConfirmAccountImport(t.Context(), "a", "import", p.Digest, false, data)
	require.NoError(t, err)
	b := basisRead(t, f, "a", "", "")
	require.Len(t, b.Points, 5)
	for i := 1; i < len(b.Points); i++ {
		if b.Points[i-1].Date == b.Points[i].Date {
			require.Less(t, b.Points[i-1].Record.Original.SourceRow, b.Points[i].Record.Original.SourceRow)
		}
	}
	for i := 0; i < 3; i++ {
		basisEntry(t, f, fmt.Sprint("manual-big-", i), "2020-01-01", "cash_flow", `"92233720368547758.07"`, "null")
	}
	b = basisRead(t, f, "a", "", "from=2020-01-01&to=2020-01-01")
	require.Equal(t, "276701161105643274.21", b.NetFlow)
	require.Equal(t, "10.00", f.get(t, "/accounts/a")["cash"])
	_, err = f.store.ConfirmAccountImport(t.Context(), "a", "reimport", p.Digest, false, data)
	require.NoError(t, err)
	after := basisRead(t, f, "a", "", "from=2020-01-01&to=2020-01-01")
	require.Equal(t, b.Revision, after.Revision)
	require.Equal(t, b.ChangeRevision, after.ChangeRevision)
}

func TestBasisCurrentInputAndUnrelatedAccountEditsDoNotInvalidate(t *testing.T) {
	f := reportedFixture(t)
	manualSourceAccount(t, f.store, "b")
	putSource(t, f.store, "a", "input", "0", 10000)
	v := sampleValuation(t, f.store, "a", Handler{Now: f.store.now})
	raw, err := f.store.ValuationHistory(t.Context(), "a", mustInt(t, v.HistoryID))
	require.NoError(t, err)
	before := basisRead(t, f, "a", "", "")
	putSource(t, f.store, "a", "edit", "1", 20000)
	putSource(t, f.store, "b", "other", "0", 99999)
	after := basisRead(t, f, "a", "", "since_revision="+before.ChangeRevision)
	require.Equal(t, before.Revision, after.Revision)
	require.False(t, after.PreviousBasisAffected)
	require.Empty(t, after.Changes)
	require.Equal(t, "observed", after.Closing.Status)
	history, err := f.store.ValuationHistory(t.Context(), "a", mustInt(t, v.HistoryID))
	require.NoError(t, err)
	require.Equal(t, raw, history)
}

func TestBasisValidationRollbackAndFutureOnlyChanges(t *testing.T) {
	f := reportedFixture(t)
	before := basisRead(t, f, "a", "", "")
	_, err := f.store.db.ExecContext(t.Context(), `CREATE TRIGGER synthetic_basis_failure BEFORE INSERT ON idempotency_receipts BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
	require.NoError(t, err)
	f.request(t, "POST", "/accounts/a/records", "failure", `{"id":"manual-fail","entry":{"kind":"asset","date":"2020-01-01","total_assets":"1"}}`, 500)
	require.Equal(t, before, basisRead(t, f, "a", "", ""))
	_, err = f.store.db.ExecContext(t.Context(), `DROP TRIGGER synthetic_basis_failure`)
	require.NoError(t, err)
	basisEntry(t, f, "manual-future", "2099-01-01", "asset", "null", `"1"`)
	b := basisRead(t, f, "a", "", "since_revision=0")
	require.False(t, b.PreviousBasisAffected)
	require.Len(t, b.Changes, 1)
	require.Equal(t, before.Revision, b.Revision)
	for _, q := range []string{"?", "?track=all", "?track=reported&track=holdings", "?to=2020-02-30", "?from=2021-01-01&to=2020-01-01", "?since_revision=-1", "?since_revision=01", "?since_revision=999", "?unknown=x"} {
		f.request(t, "GET", "/accounts/a/analysis-basis"+q, "", "", 400)
	}
	f.request(t, "GET", "/accounts/missing/analysis-basis", "", "", 404)
	require.Empty(t, f.request(t, "HEAD", "/accounts/a/analysis-basis", "", "", 200).Body.String())
}
