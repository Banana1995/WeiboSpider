package ledger

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func basisRead(t *testing.T, f *httpFixture, account, track, extra string) AnalysisBasis {
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
	f := newHTTPFixture(t)
	f.request(t, "POST", "/reported-accounts", "a", `{"id":"a","name":"Synthetic","currency":"CNY","opening_date":"2020-01-01"}`, 201)
	return f
}
func TestBasisMissingZeroCarryCombinedAndCutoff(t *testing.T) {
	f := reportedFixture(t)
	b := basisRead(t, f, "a", "reported", "")
	require.Nil(t, b.Opening)
	require.Nil(t, b.Closing)
	require.Empty(t, b.Points)
	require.False(t, b.PreviousBasisAffected)
	require.Equal(t, "unavailable", b.Status)
	basisEntry(t, f, "manual-future", "2099-01-01", "asset", "null", `"999"`)
	basisEntry(t, f, "manual-first-flow", "2020-01-01", "cash_flow", `"20"`, "null")
	b = basisRead(t, f, "a", "reported", "")
	require.Len(t, b.Points, 1)
	require.Nil(t, b.Closing.Assets)
	require.Equal(t, "unavailable", b.Closing.Status)
	basisEntry(t, f, "manual-zero", "2020-01-02", "asset", "null", `"0"`)
	basisEntry(t, f, "manual-carry", "2020-01-03", "cash_flow", `"50"`, "null")
	basisEntry(t, f, "manual-combined", "2020-01-04", "cash_flow", `"10"`, `"80"`)
	basisEntry(t, f, "manual-log", "2020-01-04", "log", "null", "null")
	b = basisRead(t, f, "a", "reported", "&from=2020-01-03&to=2020-01-04")
	require.Equal(t, Money(0), *b.Opening.Assets)
	require.Equal(t, Money(0), *b.Points[0].Assets)
	require.Equal(t, "carried", b.Points[0].Status)
	require.Equal(t, "manual-zero", b.Points[0].SourceID)
	require.Nil(t, b.Points[0].Record.TotalAssets)
	require.Equal(t, Money(8000), *b.Closing.Assets) // post-flow, not 90
	require.Equal(t, "60.00", b.NetFlow)
	require.Equal(t, "manual-combined", b.Closing.RecordID)
	require.Equal(t, "current", b.Status)
	// A backdated flow must not use the most recently submitted (future) assets.
	basisEntry(t, f, "manual-backdate", "2019-01-01", "cash_flow", `"1"`, "null")
	b = basisRead(t, f, "a", "reported", "&to=2019-01-01")
	require.Nil(t, b.Closing.Assets)
	f.request(t, "GET", "/accounts/a/analysis-basis?track=reported&to=2099-01-01", "", "", 400)
}

func TestBasisStableOrderCorrectionsVoidAndInvalidation(t *testing.T) {
	f := reportedFixture(t)
	basisEntry(t, f, "manual-z", "2020-01-01", "asset", "null", `"100"`)
	basisEntry(t, f, "manual-a", "2020-01-01", "asset", "null", `"200"`)
	basisEntry(t, f, "manual-carry", "2020-01-02", "cash_flow", `"-5"`, "null")
	before := basisRead(t, f, "a", "reported", "")
	require.Equal(t, "manual-a", before.Closing.SourceID)
	sequence := before.Points[0].Sequence
	f.request(t, "PUT", "/accounts/a/records/manual-z", "edit", `{"expected_version":"1","reason":"Synthetic","entry":{"kind":"asset","date":"2020-01-01","total_assets":"300"}}`, 200)
	b := basisRead(t, f, "a", "reported", "&since_revision="+before.ChangeRevision)
	require.Equal(t, sequence, b.Points[0].Sequence)
	require.Equal(t, Money(20000), *b.Closing.Assets)
	require.True(t, b.PreviousBasisAffected)
	require.Len(t, b.Changes, 1)
	require.Equal(t, "2020-01-01", b.Changes[0].From)
	require.Nil(t, b.Changes[0].To)
	require.NotEqual(t, before.Revision, b.Revision)
	same := basisRead(t, f, "a", "reported", "&since_revision="+b.ChangeRevision)
	require.False(t, same.PreviousBasisAffected)
	require.Empty(t, same.Changes)
	require.Equal(t, b.Revision, same.Revision)
	f.request(t, "DELETE", "/accounts/a/records/manual-a", "void", `{"expected_version":"1","reason":"Synthetic"}`, 200)
	b = basisRead(t, f, "a", "reported", "")
	require.Equal(t, Money(30000), *b.Closing.Assets)
	require.Equal(t, "manual-z", b.Closing.SourceID)
	prior := b.ChangeRevision
	f.request(t, "PUT", "/accounts/a/records/manual-z", "backdate", `{"expected_version":"2","reason":"Synthetic","entry":{"kind":"asset","date":"2018-01-01","total_assets":"300"}}`, 200)
	b = basisRead(t, f, "a", "reported", "&since_revision="+prior)
	require.Equal(t, "2018-01-01", b.Changes[0].From)
	page1 := f.get(t, "/accounts/a/records?limit=1")
	page2 := f.get(t, "/accounts/a/records?limit=1&cursor="+page1["next_cursor"].(string))
	require.NotEqual(t, httpItems(t, page1)[0]["id"], httpItems(t, page2)[0]["id"])
	require.Equal(t, "manual-carry", httpItems(t, page1)[0]["id"])
}

func TestBasisImportOrderExactValuesAndMixedTracks(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "10.00", nil)
	data := syntheticImport(t)
	p := parseSynthetic(t, data)
	_, err := f.store.ConfirmAccountImport(t.Context(), "a", "import", p.Digest, false, data)
	require.NoError(t, err)
	b := basisRead(t, f, "a", "reported", "")
	require.Len(t, b.Points, 5)
	for i := 1; i < len(b.Points); i++ {
		prev, cur := b.Points[i-1], b.Points[i]
		if prev.Date == cur.Date {
			require.Less(t, prev.Record.Original.SourceRow, cur.Record.Original.SourceRow)
		}
	}
	holding := basisRead(t, f, "a", "holdings", "")
	require.Equal(t, b, holding)
	for i := 0; i < 3; i++ {
		basisEntry(t, f, fmt.Sprintf("manual-big-%d", i), "2020-01-01", "cash_flow", `"92233720368547758.07"`, "null")
	}
	b = basisRead(t, f, "a", "reported", "&from=2020-01-01&to=2020-01-01")
	require.Equal(t, "276701161105643274.21", b.NetFlow)
	require.Equal(t, "10.00", f.get(t, "/accounts/a")["cash"])
	_, err = f.store.ConfirmAccountImport(t.Context(), "a", "reimport", p.Digest, false, data)
	require.NoError(t, err)
	after := basisRead(t, f, "a", "reported", "&from=2020-01-01&to=2020-01-01")
	require.Equal(t, b.Revision, after.Revision)
	require.Equal(t, b.ChangeRevision, after.ChangeRevision)
}

func TestBasisHoldingsTrackedRevisionScopesAndLateSave(t *testing.T) {
	f := newHTTPFixture(t)
	for _, id := range []string{"a", "b", "c"} {
		f.account(t, id, "CNY", "100.00", nil)
	}
	// Capture before I/O, then mutate while an observation is in flight.
	v, instruments, err := f.store.valuationInputs(t.Context(), "a")
	require.NoError(t, err)
	h := Handler{Now: f.store.now}
	require.NoError(t, h.valuePositions(t.Context(), &v, instruments))
	f.request(t, "POST", "/operations", "deposit", httpMutation(t, httpCash("deposit", "a", "2026-01-02", "1", "deposit", "5"), ""), 201)
	id, err := f.store.RecordValuation(t.Context(), v, instruments)
	require.NoError(t, err)
	raw, err := f.store.ValuationHistory(t.Context(), "a", mustInt(t, id))
	require.NoError(t, err)
	b := basisRead(t, f, "a", "holdings", "")
	require.Equal(t, "pending_recalculation", b.Status)
	require.Equal(t, "stale", b.Closing.Status)
	require.Equal(t, "5.00", b.NetFlow)
	require.Equal(t, "1", b.Points[0].Version)
	require.Equal(t, "deposit", b.Points[0].Operation.Operation.ID)
	// A new observation is current; unrelated accounts and reported writes do not stale it.
	f.request(t, "POST", "/accounts/a/valuation", "save-a", "{}", 200)
	basisEntry(t, f, "manual-separate", "2026-01-01", "asset", "null", `"99999"`)
	f.request(t, "POST", "/operations", "unrelated", httpMutation(t, httpCash("other", "c", "2026-01-02", "2", "deposit", "1"), ""), 201)
	b = basisRead(t, f, "a", "holdings", "")
	require.Equal(t, "observed", b.Closing.Status)
	require.Equal(t, Money(10500), *b.Closing.Assets)
	require.Equal(t, "0", basisRead(t, f, "b", "holdings", "").ChangeRevision)
	op := httpCash("transfer", "a", "2026-01-03", "3", "transfer", "10")
	op["to_account_id"] = "b"
	f.request(t, "POST", "/operations", "transfer", httpMutation(t, op, ""), 201)
	b = basisRead(t, f, "a", "holdings", "")
	require.Equal(t, "stale", b.Closing.Status)
	require.Equal(t, "10.00", basisRead(t, f, "b", "holdings", "").NetFlow)
	unchanged, err := f.store.ValuationHistory(t.Context(), "a", mustInt(t, id))
	require.NoError(t, err)
	require.Equal(t, raw, unchanged)
	// A later business event must not invalidate an earlier observation.
	stamp, _ := time.Parse(time.RFC3339Nano, httpTestStamp)
	f.store.now = func() time.Time { return stamp.AddDate(0, 0, 1) }
	f.request(t, "POST", "/accounts/b/valuation", "save-b", "{}", 200)
	f.store.now = func() time.Time { return stamp.AddDate(0, 0, 2) }
	f.request(t, "POST", "/operations", "later", httpMutation(t, httpCash("later", "b", "2026-09-08", "1", "deposit", "1"), ""), 201)
	laterBasis := basisRead(t, f, "b", "holdings", "")
	require.Equal(t, "carried", laterBasis.Closing.Status)
	require.Equal(t, "2026-09-07", laterBasis.Closing.SourceDate)
}
func mustInt(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	require.NoError(t, err)
	return n
}

func TestBasisValidationRollbackAndFutureOnlyChanges(t *testing.T) {
	f := reportedFixture(t)
	before := basisRead(t, f, "a", "reported", "")
	_, err := f.store.db.ExecContext(t.Context(), `CREATE TRIGGER synthetic_basis_failure BEFORE INSERT ON idempotency_receipts BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
	require.NoError(t, err)
	f.request(t, "POST", "/accounts/a/records", "failure", `{"id":"manual-fail","entry":{"kind":"asset","date":"2020-01-01","total_assets":"1"}}`, 500)
	after := basisRead(t, f, "a", "reported", "")
	require.Equal(t, before, after)
	_, err = f.store.db.ExecContext(t.Context(), `DROP TRIGGER synthetic_basis_failure`)
	require.NoError(t, err)
	basisEntry(t, f, "manual-future", "2099-01-01", "asset", "null", `"1"`)
	b := basisRead(t, f, "a", "reported", "&since_revision=0")
	require.False(t, b.PreviousBasisAffected)
	require.Len(t, b.Changes, 1)
	require.Equal(t, before.Revision, b.Revision)
	for _, q := range []string{"?", "?track=all", "?track=reported&track=holdings", "?to=2020-02-30", "?from=2021-01-01&to=2020-01-01", "?since_revision=-1", "?since_revision=01", "?since_revision=999", "?unknown=x"} {
		f.request(t, "GET", "/accounts/a/analysis-basis"+q, "", "", 400)
	}
	f.request(t, "GET", "/accounts/a/analysis-basis?track=holdings", "", "", 400)
	f.request(t, "GET", "/accounts/missing/analysis-basis", "", "", 404)
	require.Empty(t, f.request(t, "HEAD", "/accounts/a/analysis-basis", "", "", 200).Body.String())
}

func TestBasisHoldingsCorrectionsVoidAndUntracked(t *testing.T) {
	f := newHTTPFixture(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		f.account(t, id, "CNY", "100.00", nil)
	}
	v, ins, err := f.store.valuationInputs(t.Context(), "a")
	require.NoError(t, err)
	h := Handler{Now: f.store.now}
	require.NoError(t, h.valuePositions(t.Context(), &v, ins))
	v.changeRevision = nil // A legacy/untracked observation must not be called current.
	_, err = f.store.RecordValuation(t.Context(), v, ins)
	require.NoError(t, err)
	require.Equal(t, "untracked_history", basisRead(t, f, "a", "holdings", "").Status)
	op := httpCash("transfer", "a", "2026-01-04", "1", "transfer", "10")
	op["to_account_id"] = "b"
	f.request(t, "POST", "/operations", "create", httpMutation(t, op, ""), 201)
	prior := basisRead(t, f, "a", "holdings", "").ChangeRevision
	op["account_id"] = "c"
	op["to_account_id"] = "d"
	op["date"] = "2026-01-02"
	f.request(t, "PUT", "/operations/transfer", "correct", httpMutation(t, op, "1"), 200)
	a := basisRead(t, f, "a", "holdings", "&since_revision="+prior)
	require.Equal(t, "2026-01-04", a.Changes[0].From)
	require.Equal(t, "0.00", a.NetFlow)
	for _, id := range []string{"c", "d"} {
		b := basisRead(t, f, id, "holdings", "")
		require.Equal(t, "2026-01-02", b.Changes[0].From)
	}
	before := basisRead(t, f, "c", "holdings", "")
	f.request(t, "DELETE", "/operations/transfer", "void", `{"expected_version":"2","reason":"Synthetic"}`, 200)
	after := basisRead(t, f, "c", "holdings", "&since_revision="+before.ChangeRevision)
	require.Equal(t, "Synthetic", after.Changes[0].Reason)
	require.Equal(t, "0.00", after.NetFlow)
	f.request(t, "DELETE", "/operations/transfer", "void", `{"expected_version":"2","reason":"Synthetic"}`, 200)
	same := basisRead(t, f, "c", "holdings", "&since_revision="+after.ChangeRevision)
	require.Empty(t, same.Changes)
}
