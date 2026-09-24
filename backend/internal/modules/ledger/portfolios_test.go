package ledger

import (
	"encoding/json"
	"io/fs"
	"math"
	"math/big"
	"strconv"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/stretchr/testify/require"
)

func portfolioAccounts(t *testing.T, f *httpFixture, ids ...string) {
	t.Helper()
	for _, id := range ids {
		_, err := f.store.CreateReportedAccount(t.Context(), "create-"+id, ReportedAccountInput{ID: id, Name: "Synthetic " + id, Currency: CNY, OpeningDate: "2020-01-01"})
		require.NoError(t, err)
	}
}

func portfolioRecord(t *testing.T, f *httpFixture, account, id, date, assets, flow string) AccountRecord {
	t.Helper()
	id = "manual-" + id
	entry := AccountEntry{Kind: "asset", Date: date}
	if assets != "" {
		value, err := ParseMoney(assets)
		require.NoError(t, err)
		entry.TotalAssets = &value
	}
	if flow != "" {
		value, err := ParseMoney(flow)
		require.NoError(t, err)
		entry.Kind, entry.Flow = "cash_flow", &value
	}
	raw, err := f.store.WriteAccountRecord(t.Context(), "record-"+account+"-"+id, AccountRecordCommand{Action: CreateOperation, AccountID: account, ID: id, Entry: &entry})
	require.NoError(t, err)
	var r AccountRecord
	require.NoError(t, json.Unmarshal(raw, &r))
	return r
}

func createPortfolio(t *testing.T, f *httpFixture, id string, accounts ...string) Portfolio {
	t.Helper()
	raw, err := f.store.WritePortfolio(t.Context(), "portfolio-"+id, PortfolioCommand{Action: "create", ID: id, Name: "Synthetic " + id, AccountIDs: accounts})
	require.NoError(t, err)
	var p Portfolio
	require.NoError(t, json.Unmarshal(raw, &p))
	return p
}

func portfolioRead(t *testing.T, f *httpFixture, path string) PortfolioBasis {
	t.Helper()
	var b PortfolioBasis
	require.NoError(t, json.Unmarshal(f.request(t, "GET", path, "", "", 200).Body.Bytes(), &b))
	return b
}

func TestPortfolioHTTPPersistenceReceiptsAndMembership(t *testing.T) {
	f := newHTTPFixture(t)
	portfolioAccounts(t, f, "a", "b", "c")
	portfolioRecord(t, f, "a", "opening", "2021-01-01", "100.00", "")
	create := `{"id":"family","name":"家庭股票","account_ids":["b","a"]}`
	first := f.request(t, "POST", "/portfolios", "new-portfolio", create, 201).Body.String()
	require.Equal(t, first, f.request(t, "POST", "/portfolios", "new-portfolio", create, 201).Body.String())
	require.Equal(t, []any{"a", "b"}, f.get(t, "/portfolios/family")["account_ids"])
	createPortfolio(t, f, "other", "a", "c") // account membership is not exclusive
	page := f.get(t, "/portfolios?limit=1")
	require.Len(t, httpItems(t, page), 1)
	require.Equal(t, "family", page["next_cursor"])
	require.Len(t, httpItems(t, f.get(t, "/portfolios?cursor=family")), 1)
	require.Empty(t, f.request(t, "HEAD", "/portfolios/family", "", "", 200).Body.String())

	replace := `{"name":"家庭投资","account_ids":["a","c"],"expected_version":"1"}`
	f.request(t, "PUT", "/portfolios/family", "edit-portfolio", replace, 200)
	f.request(t, "PUT", "/portfolios/family", "stale-portfolio", replace, 409)
	require.Equal(t, first, f.request(t, "POST", "/portfolios", "new-portfolio", create, 201).Body.String())
	require.Equal(t, "2", f.get(t, "/portfolios/family")["version"])
	f.request(t, "POST", "/portfolios", "new-portfolio", `{"id":"different","name":"X","account_ids":["a"]}`, 409)
	f.request(t, "POST", "/portfolios", "duplicate-members", `{"id":"bad","name":"X","account_ids":["a","a"]}`, 400)
	f.request(t, "POST", "/portfolios", "empty-members", `{"id":"bad","name":"X","account_ids":[]}`, 400)
	f.request(t, "POST", "/portfolios", "missing-members", `{"id":"bad","name":"X","account_ids":["missing"]}`, 409)
	f.request(t, "POST", "/portfolios", "unknown-field", `{"id":"bad","name":"X","account_ids":["a"],"currency":"CNY"}`, 400)
	f.request(t, "GET", "/portfolios/family/analysis-basis?unknown=x", "", "", 400)
	f.request(t, "POST", "/portfolios", "duplicate-field", `{"id":"bad","id":"other","name":"X","account_ids":["a"]}`, 400)
	_, err := f.store.CreateReportedAccount(t.Context(), "usd", ReportedAccountInput{ID: "usd", Name: "USD", Currency: USD, OpeningDate: "2020-01-01"})
	require.NoError(t, err)
	httpError(t, f.request(t, "POST", "/portfolios", "mixed", `{"id":"mixed","name":"X","account_ids":["a","usd"]}`, 422), "portfolio_currency_mismatch")

	_, err = f.store.db.ExecContext(t.Context(), `CREATE TRIGGER portfolio_test_failure BEFORE INSERT ON idempotency_receipts WHEN NEW.key='rollback' BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
	require.NoError(t, err)
	f.request(t, "PUT", "/portfolios/family", "rollback", `{"name":"rolled back","account_ids":["a"],"expected_version":"2"}`, 500)
	require.Equal(t, "家庭投资", f.get(t, "/portfolios/family")["name"])
	deleted := f.request(t, "DELETE", "/portfolios/family", "delete-portfolio", `{"expected_version":"2"}`, 200).Body.String()
	require.Equal(t, deleted, f.request(t, "DELETE", "/portfolios/family", "delete-portfolio", `{"expected_version":"2"}`, 200).Body.String())
	f.request(t, "GET", "/portfolios/family", "", "", 404)
	f.request(t, "POST", "/portfolios", "new-portfolio", create, 404)
	f.request(t, "POST", "/portfolios", "reuse-deleted", create, 409)
	require.Len(t, httpItems(t, f.get(t, "/accounts/a/records")), 1)
	require.Len(t, httpItems(t, f.get(t, "/accounts")), 4)
	f.request(t, "DELETE", "/accounts/c", "delete-member", `{}`, 200)
	httpError(t, f.request(t, "GET", "/portfolios/other/analysis-basis", "", "", 409), "portfolio_member_missing")
	require.Equal(t, []any{"a", "c"}, f.get(t, "/portfolios/other")["account_ids"])
}

func TestPortfolioDelayedMemberCarryAndIndependentReturns(t *testing.T) {
	f := newHTTPFixture(t)
	portfolioAccounts(t, f, "a", "b")
	portfolioRecord(t, f, "a", "opening", "2021-01-01", "10000.00", "")
	portfolioRecord(t, f, "a", "middle", "2021-06-01", "10500.00", "")
	portfolioRecord(t, f, "a", "year", "2022-01-01", "11000.00", "")
	portfolioRecord(t, f, "a", "closing", "2022-01-03", "12100.00", "")
	portfolioRecord(t, f, "b", "opening", "2022-01-01", "20000.00", "")
	portfolioRecord(t, f, "b", "deposit", "2022-01-02", "", "5000.00")
	createPortfolio(t, f, "p", "a", "b")
	early := portfolioRead(t, f, "/portfolios/p/analysis-basis?to=2021-06-01")
	single, err := f.store.AnalysisBasis(t.Context(), "a", "", "2021-06-01", 0)
	require.NoError(t, err)
	require.Equal(t, single.Returns.Dietz.Value, early.Returns.Dietz.Value)
	require.Equal(t, "2021-01-01", early.Returns.EffectiveFrom)
	require.Equal(t, Money(0), early.Members[1].Assets)

	b := portfolioRead(t, f, "/portfolios/p/analysis-basis?to=2022-01-03")
	require.Equal(t, Money(3710000), *b.Closing.Assets)
	require.Equal(t, "2100.00", *b.Returns.Profit.Value)
	require.Equal(t, "25000.00", b.Returns.NetFlow)
	require.Equal(t, 368, b.Returns.PeriodDays)
	// Independently specified cash-flow weights: 20k for two days, 5k for one.
	expected := new(big.Rat).SetFrac(big.NewInt(2100*368), big.NewInt(10000*368+20000*2+5000))
	require.Equal(t, expected.FloatString(12), *b.Returns.Dietz.Value)
	xirr, err := strconv.ParseFloat(*b.Returns.XIRR.Value, 64)
	require.NoError(t, err)
	require.InDelta(t, 0, -10000-20000/math.Pow(1+xirr, 365.0/365)-5000/math.Pow(1+xirr, 366.0/365)+37100/math.Pow(1+xirr, 367.0/365), 1e-6)
	require.Equal(t, b.Returns.Dietz, b.Returns.Curve[len(b.Returns.Curve)-1].Dietz)
	require.Equal(t, "2100.00", *b.Members[0].Profit.Value)
	require.Equal(t, "0.00", *b.Members[1].Profit.Value)
	require.Equal(t, Money(2500000), b.Members[1].Assets)
	require.Equal(t, "2022-01-01", b.Members[1].FirstDate)
	require.True(t, b.Carried)
	require.Equal(t, *b.Closing.Assets, b.Members[0].Assets+b.Members[1].Assets)

	period := portfolioRead(t, f, "/portfolios/p/analysis-basis?from=2022-01-01&to=2022-01-03")
	require.Equal(t, "1600.00", *period.Returns.Profit.Value)
	require.Equal(t, "1600.00", *period.Members[0].Profit.Value)
	require.Equal(t, "0.00", *period.Members[1].Profit.Value)
	require.Equal(t, "2021-12-31", period.Returns.EffectiveFrom)
	annual := f.get(t, "/portfolios/p/annual-returns")
	years := annual["years"].([]any)
	require.Len(t, years, 2)
	require.Equal(t, "2021-12-31", years[0].(map[string]any)["to"])
	require.Equal(t, "5.00", years[0].(map[string]any)["money_weighted"].(map[string]any)["percentage"])
	yearEnd := portfolioRead(t, f, "/portfolios/p/analysis-basis?to=2021-12-31")
	require.Equal(t, "2021-12-31", yearEnd.Returns.EffectiveTo)
	require.Equal(t, "5.00", *yearEnd.Returns.Dietz.Percentage)
	f.request(t, "HEAD", "/portfolios/p/analysis-basis", "", "", 200)
}

func TestPortfolioFirstDayLossBelongsToMemberContribution(t *testing.T) {
	f := newHTTPFixture(t)
	portfolioAccounts(t, f, "a", "b")
	portfolioRecord(t, f, "a", "opening", "2024-01-01", "100.00", "")
	portfolioRecord(t, f, "b", "opening", "2024-01-02", "99.00", "100.00")
	createPortfolio(t, f, "p", "a", "b")
	b := portfolioRead(t, f, "/portfolios/p/analysis-basis")
	require.Equal(t, "-1.00", *b.Returns.Profit.Value)
	require.Equal(t, "-1.00", *b.Members[1].Profit.Value)
	require.Equal(t, "0.00", *b.Members[0].Profit.Value)
	require.Equal(t, "100.00", b.Returns.NetFlow)
	for _, e := range b.Entries {
		require.False(t, e.AccountID == "b" && e.Kind == "opening")
	}
}

func TestPortfolioSameDayOpeningDoesNotDoubleCountFlowsAndCorrections(t *testing.T) {
	f := newHTTPFixture(t)
	portfolioAccounts(t, f, "a", "b", "empty")
	portfolioRecord(t, f, "a", "opening", "2024-01-01", "100.00", "")
	portfolioRecord(t, f, "a", "closing", "2024-01-03", "111.00", "")
	portfolioRecord(t, f, "b", "opening", "2024-01-02", "1000.00", "500.00")
	portfolioRecord(t, f, "b", "later-same-day", "2024-01-02", "", "100.00")
	closing := portfolioRecord(t, f, "b", "closing", "2024-01-03", "1200.00", "")
	createPortfolio(t, f, "p", "a", "b", "empty")
	b := portfolioRead(t, f, "/portfolios/p/analysis-basis")
	require.Equal(t, "211.00", *b.Returns.Profit.Value)
	require.Equal(t, "1000.00", b.Returns.NetFlow)
	var brought Money
	for _, entry := range b.Entries {
		if entry.Kind == "opening" && entry.AccountID == "b" {
			brought += entry.Amount
		}
	}
	require.Equal(t, Money(40000), brought)
	require.Equal(t, "empty", b.Members[2].State)
	require.Equal(t, "0.00", *b.Members[2].Profit.Value)
	newAssets := Money(125000)
	entry := closing.AccountEntry
	entry.TotalAssets = &newAssets
	_, err := f.store.WriteAccountRecord(t.Context(), "correction", AccountRecordCommand{Action: ReplaceOperation, AccountID: "b", ID: closing.ID,
		ExpectedVersion: closing.Version, Reason: "Synthetic correction", Entry: &entry})
	require.NoError(t, err)
	updated := portfolioRead(t, f, "/portfolios/p/analysis-basis")
	require.NotEqual(t, b.Revision, updated.Revision)
	require.Equal(t, "261.00", *updated.Returns.Profit.Value)
	_, err = f.store.WriteAccountRecord(t.Context(), "void", AccountRecordCommand{Action: VoidOperation, AccountID: "b", ID: closing.ID, ExpectedVersion: "2", Reason: "Synthetic void"})
	require.NoError(t, err)
	voided := portfolioRead(t, f, "/portfolios/p/analysis-basis")
	require.Equal(t, "11.00", *voided.Returns.Profit.Value)
	require.Equal(t, "0.00", *voided.Members[1].Profit.Value)
}

func TestPortfolioFlowOnlyStartPrecisionAndEmpty(t *testing.T) {
	f := newHTTPFixture(t)
	portfolioAccounts(t, f, "a", "b", "empty")
	portfolioRecord(t, f, "a", "deposit", "2024-01-01", "", "100.00")
	portfolioRecord(t, f, "a", "balance", "2025-01-01", "110.00", "")
	createPortfolio(t, f, "p", "a", "empty")
	b := portfolioRead(t, f, "/portfolios/p/analysis-basis")
	require.Equal(t, "10.00", *b.Returns.Profit.Value)
	require.Equal(t, "10.00", *b.Returns.Dietz.Percentage)
	createPortfolio(t, f, "empty-p", "empty")
	empty := portfolioRead(t, f, "/portfolios/empty-p/analysis-basis")
	require.Equal(t, "missing_opening", empty.Returns.Profit.Reason)
	require.Empty(t, empty.Points)
	portfolioRecord(t, f, "b", "huge", "2024-01-01", "92233720368547758.07", "")
	createPortfolio(t, f, "overflow", "a", "b")
	httpError(t, f.request(t, "GET", "/portfolios/overflow/analysis-basis", "", "", 400), "invalid_precision")
}

func TestPortfolioMigrationPreservesExistingRecordsAndReceipts(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(t.Context(), root, "ledger")
	require.NoError(t, err)
	old := fstest.MapFS{}
	for _, name := range []string{"001_init.sql", "002_account_deletion.sql"} {
		data, err := fs.ReadFile(migrations, "migrations/"+name)
		require.NoError(t, err)
		old[name] = &fstest.MapFile{Data: data}
	}
	require.NoError(t, db.Migrate(t.Context(), old))
	now := func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }
	s := NewStore(db, now)
	input := ReportedAccountInput{ID: "a", Name: "Synthetic", Currency: CNY, OpeningDate: "2024-01-01"}
	original, err := s.CreateReportedAccount(t.Context(), "original", input)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = Open(t.Context(), root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	s = NewStore(db, now)
	replayed, err := s.CreateReportedAccount(t.Context(), "original", input)
	require.NoError(t, err)
	require.Equal(t, original, replayed)
	_, err = s.WritePortfolio(t.Context(), "p", PortfolioCommand{Action: "create", ID: "p", Name: "Synthetic", AccountIDs: []string{"a"}})
	require.NoError(t, err)
	var violations int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations))
	require.Zero(t, violations)
}
