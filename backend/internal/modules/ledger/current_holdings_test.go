package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func manualSourceAccount(t *testing.T, s *Store, id string) {
	t.Helper()
	_, err := s.CreateReportedAccount(t.Context(), "create-"+id, ReportedAccountInput{ID: id, Name: "Synthetic", Currency: CNY, OpeningDate: "2020-01-01"})
	require.NoError(t, err)
}

func putSource(t *testing.T, s *Store, id, key, version string, cash Money, positions ...CurrentPosition) CurrentHoldings {
	t.Helper()
	out, err := s.PutCurrentHoldings(t.Context(), id, key, CurrentHoldingsInput{ExpectedVersion: version, Cash: &cash, Positions: append([]CurrentPosition{}, positions...)})
	require.NoError(t, err)
	return out
}

func TestCurrentHoldingsReplaceCASReceiptsAndAtomicity(t *testing.T) {
	s := importStoreFixture(t)
	manualSourceAccount(t, s, "a")
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Synthetic", Currency: CNY}))
	absent, err := s.CurrentHoldings(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, CurrentHoldings{AccountID: "a"}, absent)
	first := putSource(t, s, "a", "first", "0", 123, CurrentPosition{"i", 1234567})
	require.Equal(t, "1", first.Snapshot.Version)
	second := putSource(t, s, "a", "second", "1", 0)
	require.Empty(t, second.Snapshot.Positions)
	require.NotNil(t, second.Snapshot)
	count := auditCount(t, s)
	require.Equal(t, first, putSource(t, s, "a", "first", "0", 123, CurrentPosition{"i", 1234567}))
	require.Equal(t, count, auditCount(t, s))
	current, err := s.CurrentHoldings(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, second, current)
	cash := Money(0)
	input := CurrentHoldingsInput{ExpectedVersion: "1", Cash: &cash, Positions: []CurrentPosition{}}
	_, err = s.PutCurrentHoldings(t.Context(), "a", "stale", input)
	require.ErrorIs(t, err, ErrVersion)
	input.ExpectedVersion = "2"
	_, err = s.PutCurrentHoldings(t.Context(), "a", "first", input)
	require.ErrorIs(t, err, ErrIdempotency)
	_, err = s.PutCurrentHoldings(t.Context(), "a", "create-a", input)
	require.ErrorIs(t, err, ErrIdempotency)
	_, err = s.CreateReportedAccount(t.Context(), "second", ReportedAccountInput{ID: "b", Name: "Synthetic", Currency: CNY, OpeningDate: "2020-01-01"})
	require.ErrorIs(t, err, ErrIdempotency)
	for _, rows := range [][]CurrentPosition{nil, {{"missing", 1}}, {{"i", 0}}, {{"i", -1}}, {{"i", 1}, {"i", 2}}} {
		input.Positions = rows
		_, err = s.PutCurrentHoldings(t.Context(), "a", "invalid", input)
		require.Error(t, err)
	}
	require.Equal(t, count, auditCount(t, s))
	_, err = s.db.ExecContext(t.Context(), `CREATE TRIGGER synthetic_receipt_failure BEFORE INSERT ON idempotency_receipts WHEN NEW.kind='current_holdings' BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
	require.NoError(t, err)
	input.Positions = []CurrentPosition{}
	_, err = s.PutCurrentHoldings(t.Context(), "a", "rollback", input)
	require.Error(t, err)
	require.Equal(t, count, auditCount(t, s))
	current, err = s.CurrentHoldings(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, second, current)
	var n int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&n))
	require.Zero(t, n)
	for _, q := range []string{`DELETE FROM current_holdings`, `UPDATE current_holdings SET version=version+1`, `UPDATE current_holdings SET payload='{}'`, `UPDATE current_holdings SET account_id='other'`} {
		_, err = s.db.ExecContext(t.Context(), q)
		require.Error(t, err)
	}
}

func TestCurrentHoldingsHTTPStrictAndReplaySourceProtected(t *testing.T) {
	f := newHTTPFixture(t)
	manualSourceAccount(t, f.store, "a")
	f.account(t, "replay", "CNY", "10.00", nil)
	require.Equal(t, "manual_snapshot", f.get(t, "/accounts/a")["current_holdings_input"])
	require.Equal(t, "transaction_replay", f.get(t, "/accounts/replay")["current_holdings_input"])
	require.JSONEq(t, `{"account_id":"a","audit_id":"","snapshot":null}`, f.request(t, "GET", "/accounts/a/current-holdings", "", "", 200).Body.String())
	f.request(t, "HEAD", "/accounts/a/current-holdings", "", "", 200)
	f.request(t, "GET", "/accounts/missing/current-holdings", "", "", 404)
	for _, body := range []string{
		`{}`, `{"expected_version":"0","positions":[]}`, `{"expected_version":0,"cash":"0","positions":[]}`,
		`{"expected_version":"00","cash":"0","positions":[]}`, `{"expected_version":"0","cash":0,"positions":[]}`,
		`{"expected_version":"0","cash":"0","positions":null}`, `{"expected_version":"0","cash":"0.001","positions":[]}`,
		`{"expected_version":"0","cash":"-1","positions":[]}`, `{"expected_version":"0","cash":"0","cash":"1","positions":[]}`,
		`{"expected_version":"0","Cash":"0","positions":[]}`, `{"expected_version":"0","cash":"0","positions":[],"date":"2020-01-01"}`,
	} {
		f.request(t, "PUT", "/accounts/a/current-holdings", "invalid", body, 400)
	}
	payload := `{"expected_version":"0","cash":"0.00","positions":[]}`
	f.request(t, "PUT", "/accounts/replay/current-holdings", "replay", payload, 422)
	f.request(t, "GET", "/accounts/replay/current-holdings", "", "", 422)
	f.request(t, "PUT", "/accounts/a/current-holdings?", "invalid", payload, 400)
	first := f.request(t, "PUT", "/accounts/a/current-holdings", "valid", payload, 200).Body.String()
	require.Equal(t, first, f.request(t, "PUT", "/accounts/a/current-holdings", "valid", payload, 200).Body.String())
	f.request(t, "DELETE", "/accounts/a/current-holdings", "delete", "{}", 405)
}

func TestImportedCurrentHoldingsValuationEvidenceAndDateScope(t *testing.T) {
	s := importStoreFixture(t)
	data := syntheticImport(t)
	preview := parseSynthetic(t, data)
	_, err := s.ConfirmAccountImport(t.Context(), "a", "import", preview.Digest, true, data)
	require.NoError(t, err)
	original, err := s.ImportedRecords(t.Context(), "a", OperationQuery{Limit: 100})
	require.NoError(t, err)
	before, err := s.AnalysisBasis(t.Context(), "a", "", "", 0)
	require.NoError(t, err)
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Synthetic", Currency: CNY}))
	source := putSource(t, s, "a", "source", "0", 100, CurrentPosition{"i", 1000000})
	after, err := s.AnalysisBasis(t.Context(), "a", "", "", mustInt(t, before.ChangeRevision))
	require.NoError(t, err)
	require.False(t, after.PreviousBasisAffected)
	require.Equal(t, before.Revision, after.Revision)
	require.Equal(t, before.NetFlow, after.NetFlow)
	save := func() string {
		v, is, err := s.valuationInputs(t.Context(), "a")
		require.NoError(t, err)
		require.NoError(t, valuePositions(t.Context(), &v, is, valuationQuotes(validValuationQuotes), nil, s.now))
		id, err := s.RecordValuation(t.Context(), v, is)
		require.NoError(t, err)
		return id
	}
	id := save()
	history, err := s.ValuationHistory(t.Context(), "a", mustInt(t, id))
	require.NoError(t, err)
	require.Equal(t, source, *history.Valuation.CurrentHoldings)
	require.Equal(t, Money(1335), *history.Valuation.TotalAssets)
	var flow sql.NullInt64
	var origin string
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT flow_minor,origin FROM account_records WHERE sequence=?`, id).Scan(&flow, &origin))
	require.False(t, flow.Valid)
	require.Equal(t, "currentrefresh", origin)
	sampled, err := s.AnalysisBasis(t.Context(), "a", "", "", 0)
	require.NoError(t, err)
	putSource(t, s, "a", "edit", "1", 200, CurrentPosition{"i", 2000000})
	stale, err := s.AnalysisBasis(t.Context(), "a", "", "", mustInt(t, sampled.ChangeRevision))
	require.NoError(t, err)
	require.True(t, stale.PreviousBasisAffected)
	require.Equal(t, "stale", stale.Closing.Status)
	today := save()
	s.now = func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }
	putSource(t, s, "a", "tomorrow", "2", 0)
	old, err := s.ValuationHistory(t.Context(), "a", mustInt(t, id))
	require.NoError(t, err)
	require.Equal(t, history, old)
	basis, err := s.AnalysisBasis(t.Context(), "a", "2026-09-06", "2026-09-06", 0)
	require.NoError(t, err)
	require.Equal(t, "valuation-"+today, basis.Closing.RecordID)
	require.Equal(t, "observed", basis.Closing.Status)
	for _, p := range basis.Points {
		if p.RecordID == "valuation-"+today {
			require.Equal(t, "observed", p.Status)
		}
	}
	originalAfter, err := s.ImportedRecords(t.Context(), "a", OperationQuery{Limit: 100})
	require.NoError(t, err)
	require.Equal(t, original, originalAfter)
}

func TestCurrentHoldingsQuoteIOFenceAndValuationReceipt(t *testing.T) {
	f := newHTTPFixture(t)
	manualSourceAccount(t, f.store, "a")
	require.NoError(t, f.store.AddInstrument(t.Context(), Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Synthetic", Currency: CNY}))
	putSource(t, f.store, "a", "source", "0", 0, CurrentPosition{"i", 1000000})
	calls := 0
	provider := valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
		calls++
		if calls == 1 {
			putSource(t, f.store, "a", "during-io", "1", 300, CurrentPosition{"i", 2000000})
		}
		return validValuationQuotes(ctx, is)
	})
	f.mux = http.NewServeMux()
	Handler{Store: f.store, Quotes: provider, Now: f.store.now}.Register(f.mux)
	failed := f.request(t, "POST", "/accounts/a/valuation", "save", "{}", 409)
	require.Contains(t, failed.Body.String(), "basis_changed")
	var n int
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&n))
	require.Zero(t, n)
	saved := f.request(t, "POST", "/accounts/a/valuation", "save", "{}", 200).Body.String()
	putSource(t, f.store, "a", "later", "2", 0)
	count := auditCount(t, f.store)
	require.Equal(t, saved, f.request(t, "POST", "/accounts/a/valuation", "save", "{}", 200).Body.String())
	require.Equal(t, 2, calls)
	require.Equal(t, count, auditCount(t, f.store))
	var v Valuation
	require.NoError(t, json.Unmarshal([]byte(saved), &v))
	require.Equal(t, "2", v.CurrentHoldings.Snapshot.Version)
}

func TestWeeklyManualSourceEnrollmentPendingRefreshAndFence(t *testing.T) {
	w, clock := weeklyFixture(t, false)
	s := w.store
	for _, id := range []string{"manual", "carry", "pending", "incomplete"} {
		manualSourceAccount(t, s, id)
	}
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "unsupported", Market: "TEST", Code: "X", Name: "Synthetic", Currency: CNY}))
	putSource(t, s, "manual", "manual", "0", 123)
	putSource(t, s, "incomplete", "incomplete", "0", 200, CurrentPosition{"unsupported", 1000000})
	_, err := s.db.ExecContext(t.Context(), `INSERT INTO weekly_jobs(account_id,scheduled_business_date,source,status,created_at) VALUES('pending','2026-09-12','account_record_carry','pending',?)`, clock.now().UTC().Format(weeklyStamp))
	require.NoError(t, err)
	putSource(t, s, "pending", "pending", "0", 0)
	require.NoError(t, w.Tick(t.Context()))
	for _, id := range []string{"manual", "pending", "a"} {
		job := weeklyJobFor(t, w, id)
		require.Equal(t, "holdings_current", job.Source)
		require.Equal(t, "succeeded", job.Status)
	}
	require.Equal(t, "account_record_carry", weeklyJobFor(t, w, "carry").Source)
	require.Equal(t, "incomplete_valuation", weeklyJobFor(t, w, "incomplete").ErrorCode)
	require.Nil(t, weeklyJobFor(t, w, "incomplete").HistoryID)
	done := weeklyJobFor(t, w, "manual")
	putSource(t, s, "manual", "manual-edit", "1", 456)
	require.NoError(t, w.Tick(t.Context()))
	require.Equal(t, done, weeklyJobFor(t, w, "manual"))
	_, err = s.db.ExecContext(t.Context(), `UPDATE weekly_jobs SET source='account_record_carry' WHERE account_id='manual'`)
	require.Error(t, err)
	// A source revision during provider I/O fails instead of freezing a subtotal or old source.
	manualSourceAccount(t, s, "race")
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "quoted", Market: "SH", Code: "600000", Name: "Synthetic", Currency: CNY}))
	putSource(t, s, "race", "race", "0", 0, CurrentPosition{"quoted", 1000000})
	original := w.quotes
	w.quotes = valuationQuotes(func(ctx context.Context, is []Instrument) map[string]QuoteResult {
		putSource(t, s, "race", "race-edit", "1", 0)
		return original.Fetch(ctx, is)
	})
	require.NoError(t, w.Tick(t.Context()))
	require.Equal(t, "basis_changed", weeklyJobFor(t, w, "race").ErrorCode)
	require.Nil(t, weeklyJobFor(t, w, "race").HistoryID)
}
