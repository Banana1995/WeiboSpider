package ledger

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/stretchr/testify/require"
)

func TestAccountDeleteUpdatesPortfoliosAndReplays(t *testing.T) {
	f := newHTTPFixture(t)
	portfolioAccounts(t, f, "a", "b", "c")
	portfolioRecord(t, f, "b", "opening", "2024-01-01", "100.00", "")
	portfolioRecord(t, f, "b", "closing", "2025-01-01", "110.00", "")
	family := createPortfolio(t, f, "family", "a", "b")
	createPortfolio(t, f, "only-a", "a")
	other := createPortfolio(t, f, "other", "b", "c")
	before := f.snapshot(t)
	stamp := "2026-09-07T00:00:00Z"
	f.store.now = func() time.Time {
		now, err := time.Parse(time.RFC3339Nano, stamp)
		require.NoError(t, err)
		return now
	}

	deleted := f.request(t, "DELETE", "/accounts/a", "delete-a", `{}`, 200).Body.String()
	require.JSONEq(t, `{"account_id":"a","deleted":true}`, deleted)
	updated, err := f.store.Portfolio(t.Context(), "family")
	require.NoError(t, err)
	require.Equal(t, Portfolio{
		ID: family.ID, Name: family.Name, Currency: family.Currency, AccountIDs: []string{"b"},
		Version: "2", CreatedAt: family.CreatedAt, UpdatedAt: stamp,
	}, updated)
	unchanged, err := f.store.Portfolio(t.Context(), "other")
	require.NoError(t, err)
	require.Equal(t, other, unchanged)
	f.request(t, "GET", "/portfolios/only-a", "", "", 404)
	f.request(t, "GET", "/portfolios/only-a/analysis-basis", "", "", 404)
	require.Len(t, httpItems(t, f.get(t, "/portfolios")), 2)

	analysis := portfolioRead(t, f, "/portfolios/family/analysis-basis")
	require.Equal(t, updated, analysis.Portfolio)
	require.Len(t, analysis.Members, 1)
	require.Equal(t, "b", analysis.Members[0].AccountID)
	require.Equal(t, Money(11000), *analysis.Closing.Assets)
	require.Equal(t, "10.00", *analysis.Returns.Profit.Value)
	f.get(t, "/portfolios/family/annual-returns")
	require.Len(t, httpItems(t, f.get(t, "/accounts/b/records")), 2)

	after := f.snapshot(t)
	require.Equal(t, before["audit_log"], after["audit_log"][:len(before["audit_log"])])
	require.Len(t, after["audit_log"], len(before["audit_log"])+3)
	for _, row := range before["idempotency_receipts"] {
		require.Contains(t, after["idempotency_receipts"], row)
	}
	var audits int
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log
		WHERE entity_type='portfolio' AND correlation_id='delete-a' AND source='system'
		AND json_extract(metadata_json,'$.reason')='account_deleted'
		AND json_extract(metadata_json,'$.account_id')='a'`).Scan(&audits))
	require.Equal(t, 2, audits)
	require.Equal(t, deleted, f.request(t, "DELETE", "/accounts/a", "delete-a", `{}`, 200).Body.String())
	require.Equal(t, after, f.snapshot(t))
	updatedAgain, err := f.store.Portfolio(t.Context(), "family")
	require.NoError(t, err)
	require.Equal(t, updated, updatedAgain)
	f.request(t, "PUT", "/portfolios/family", "stale-edit", `{"name":"Stale","account_ids":["b","c"],"expected_version":"1"}`, 409)
	f.request(t, "POST", "/portfolios", "reuse-only-a", `{"id":"only-a","name":"Reuse","account_ids":["b"]}`, 409)

	f.request(t, "DELETE", "/accounts/b", "delete-b", `{}`, 200)
	f.request(t, "GET", "/portfolios/family", "", "", 404)
	require.Equal(t, []any{"c"}, f.get(t, "/portfolios/other")["account_ids"])
	f.request(t, "DELETE", "/accounts/c", "delete-c", `{}`, 200)
	require.Empty(t, httpItems(t, f.get(t, "/portfolios")))
	require.Equal(t, deleted, f.request(t, "DELETE", "/accounts/a", "delete-a", `{}`, 200).Body.String())
}

func TestAccountDeletePortfolioRollback(t *testing.T) {
	for name, trigger := range map[string]string{
		"audit":   `BEFORE INSERT ON audit_log WHEN NEW.entity_type='portfolio'`,
		"replace": `BEFORE UPDATE ON portfolios`,
		"delete":  `BEFORE DELETE ON portfolios`,
		"receipt": `BEFORE INSERT ON idempotency_receipts WHEN NEW.kind='account_delete'`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newHTTPFixture(t)
			portfolioAccounts(t, f, "a", "b")
			portfolioRecord(t, f, "a", "opening", "2024-01-01", "100.00", "")
			family := createPortfolio(t, f, "family", "a", "b")
			only := createPortfolio(t, f, "only", "a")
			_, err := f.store.db.ExecContext(t.Context(), `CREATE TRIGGER synthetic_failure `+trigger+` BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
			require.NoError(t, err)
			before := f.snapshot(t)
			_, err = f.store.DeleteAccount(t.Context(), "a", "delete-a")
			require.Error(t, err)
			require.Equal(t, before, f.snapshot(t))
			groups, err := f.store.ListPortfolios(t.Context(), PageQuery{Limit: 100})
			require.NoError(t, err)
			require.Equal(t, []Portfolio{family, only}, groups)
		})
	}
}

// Reproduce the pre-fix persisted state, including its original immutable receipt.
// These synthetic accounts have no operational rows other than their identity.
func deleteLegacyPortfolioMember(t *testing.T, s *Store, id string) json.RawMessage {
	t.Helper()
	key := "legacy-delete-" + id
	raw, err := s.accountReceipt(t.Context(), key, "account_delete", accountDeleteIntent{id}, func(tx *sql.Tx) (any, int64, error) {
		info, err := scanAccountInfo(t.Context(), tx.QueryRowContext(t.Context(), accountInfoSelect+` WHERE id=?`, id))
		if err != nil {
			return nil, 0, err
		}
		_, stamp, err := s.cutoff()
		if err != nil {
			return nil, 0, err
		}
		result := AccountDeletion{AccountID: id, Deleted: true}
		auditID, err := appendAudit(t.Context(), tx, key, "delete", "account_delete", id, id, 1, stamp, "human", publicAccount(info), result, nil)
		if err != nil {
			return nil, 0, err
		}
		_, err = tx.ExecContext(t.Context(), `DELETE FROM accounts WHERE id=?`, id)
		return result, auditID, err
	})
	require.NoError(t, err)
	return raw
}

func TestPortfolioMembersReconciledOnOpen(t *testing.T) {
	root := t.TempDir()
	db, err := Open(t.Context(), root)
	require.NoError(t, err)
	f := &httpFixture{store: NewStore(db, nil), mux: http.NewServeMux()}
	Handler{Store: f.store}.Register(f.mux)
	portfolioAccounts(t, f, "a", "b", "c")
	family := createPortfolio(t, f, "family", "a", "b")
	createPortfolio(t, f, "empty", "a", "c")
	unchanged := createPortfolio(t, f, "unchanged", "b")
	receipts := map[string]json.RawMessage{}
	for _, id := range []string{"a", "c"} {
		receipts[id] = deleteLegacyPortfolioMember(t, f.store, id)
	}
	_, err = f.store.PortfolioAnalysis(t.Context(), "family", "", "", nil)
	require.ErrorIs(t, err, ErrPortfolioMember)
	before := f.snapshot(t)
	require.NoError(t, db.Close())

	db, err = Open(t.Context(), root)
	require.NoError(t, err)
	f.store = NewStore(db, nil)
	groups, err := f.store.ListPortfolios(t.Context(), PageQuery{Limit: 100})
	require.NoError(t, err)
	require.Len(t, groups, 2)
	require.Equal(t, family.ID, groups[0].ID)
	require.Equal(t, family.CreatedAt, groups[0].CreatedAt)
	require.Equal(t, family.Name, groups[0].Name)
	require.Equal(t, []string{"b"}, groups[0].AccountIDs)
	require.Equal(t, "2", groups[0].Version)
	require.Equal(t, unchanged, groups[1])
	_, err = f.store.Portfolio(t.Context(), "empty")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = f.store.PortfolioAnalysis(t.Context(), "family", "", "", nil)
	require.NoError(t, err)
	_, err = f.store.WritePortfolio(t.Context(), "reuse-empty", PortfolioCommand{Action: "create", ID: "empty", Name: "Reuse", AccountIDs: []string{"b"}})
	require.ErrorIs(t, err, ErrConflict)
	for id, receipt := range receipts {
		replayed, err := f.store.DeleteAccount(t.Context(), id, "legacy-delete-"+id)
		require.NoError(t, err)
		require.Equal(t, receipt, replayed)
	}
	after := f.snapshot(t)
	require.Equal(t, before["audit_log"], after["audit_log"][:len(before["audit_log"])])
	require.Len(t, after["audit_log"], len(before["audit_log"])+3)
	for table, rows := range before {
		if table != "audit_log" {
			require.Equal(t, rows, after[table], table)
		}
	}
	require.NoError(t, db.Close())

	db, err = Open(t.Context(), root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	f.store = NewStore(db, nil)
	require.Equal(t, after, f.snapshot(t))
	reopened, err := f.store.ListPortfolios(t.Context(), PageQuery{Limit: 100})
	require.NoError(t, err)
	require.Equal(t, groups, reopened)
	var violations int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations))
	require.Zero(t, violations)
}

func TestPortfolioMemberRepairRollback(t *testing.T) {
	root := t.TempDir()
	db, err := Open(t.Context(), root)
	require.NoError(t, err)
	f := &httpFixture{store: NewStore(db, nil)}
	portfolioAccounts(t, f, "a", "b")
	first := createPortfolio(t, f, "first", "a", "b")
	last := createPortfolio(t, f, "last", "a")
	deleteLegacyPortfolioMember(t, f.store, "a")
	before := f.snapshot(t)
	_, err = db.ExecContext(t.Context(), `CREATE TRIGGER synthetic_failure BEFORE DELETE ON portfolios BEGIN SELECT RAISE(ABORT,'synthetic'); END`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	failed, err := Open(t.Context(), root)
	require.Error(t, err)
	require.Nil(t, failed)
	// Opening directly also proves the failed startup released its database lease.
	db, err = database.Open(t.Context(), root, "ledger")
	require.NoError(t, err)
	f.store = NewStore(db, nil)
	require.Equal(t, before, f.snapshot(t))
	groups, err := f.store.ListPortfolios(t.Context(), PageQuery{Limit: 100})
	require.NoError(t, err)
	require.Equal(t, []Portfolio{first, last}, groups)
	_, err = db.ExecContext(t.Context(), `DROP TRIGGER synthetic_failure`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	db, err = Open(t.Context(), root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	groups, err = NewStore(db, nil).ListPortfolios(t.Context(), PageQuery{Limit: 100})
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, []string{"b"}, groups[0].AccountIDs)
}
