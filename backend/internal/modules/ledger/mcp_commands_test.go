package ledger

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/stretchr/testify/require"
)

type mcpSearchFunc func(context.Context, string) ([]InstrumentSearchItem, error)

func (f mcpSearchFunc) Search(ctx context.Context, q string) ([]InstrumentSearchItem, error) {
	return f(ctx, q)
}

func mcpFixture(t *testing.T) (MCPService, *time.Time) {
	s, now := stockFixture(t)
	return MCPService{Store: s, Instruments: mcpSearchFunc(func(ctx context.Context, q string) ([]InstrumentSearchItem, error) {
		// A query here would deadlock if providers ran inside the one-connection transaction.
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&count); err != nil {
			return nil, err
		}
		if strings.HasPrefix(q, "hk") {
			return []InstrumentSearchItem{{"Synthetic HK", "HK", "00700", HKD}}, nil
		}
		return []InstrumentSearchItem{{"Synthetic SH", "SH", "600036", CNY}}, nil
	})}, now
}

func mcpTrade(key, version string) MCPTradeInput {
	return MCPTradeInput{MCPWriteFields: MCPWriteFields{RequestID: key, AccountID: "a", ExpectedVersion: version}, Instrument: MCPInstrumentSelector{"SH", "600036"}, Side: "buy", Date: "2026-09-06", Quantity: "10", Price: "10", Fee: "0", Currency: CNY}
}

func TestMCPCommandsReceiptsCashAndOfflineRecovery(t *testing.T) {
	s, now := mcpFixture(t)
	first := mcpTrade("first", "0")
	first.Date, first.Quantity = "2026-01-01", "100"
	a, err := s.Write(t.Context(), "ledger_record_trade", first)
	require.NoError(t, err)
	require.Equal(t, "1", a.VersionAfter)
	require.Equal(t, "no_anchor", a.CashEffectReason)
	require.Nil(t, a.CashAnchorAfter)
	require.Equal(t, Quantity(100000000), a.PositionsChanged[0].QuantityAfter)
	cash := MCPCashInput{MCPWriteFields: MCPWriteFields{RequestID: "cash", AccountID: "a", ExpectedVersion: "1"}, ObservedDate: "2026-09-06", Amount: "1000", Currency: CNY}
	b, err := s.Write(t.Context(), "ledger_set_cash_balance", cash)
	require.NoError(t, err)
	require.Equal(t, Money(100000), b.Cash.After)
	buy := mcpTrade("add", "2")
	buy.Price, buy.Fee = "12", "1"
	c, err := s.Write(t.Context(), "ledger_record_trade", buy)
	require.NoError(t, err)
	require.Equal(t, Money(87900), c.Cash.After)
	sell := mcpTrade("sell", "3")
	sell.Side, sell.Price, sell.Fee = "sell", "13", "1"
	d, err := s.Write(t.Context(), "ledger_record_trade", sell)
	require.NoError(t, err)
	require.Equal(t, Money(100800), d.Cash.After)
	count := auditCount(t, s.Store)
	s.Instruments = nil
	*now = now.Add(24 * time.Hour)
	again, err := s.Write(t.Context(), "ledger_record_trade", first)
	require.NoError(t, err)
	require.Equal(t, a, again)
	again, err = s.Write(t.Context(), "ledger_set_cash_balance", cash)
	require.NoError(t, err)
	require.Equal(t, b, again)
	require.Equal(t, count, auditCount(t, s.Store))
	r, err := s.WriteResult(t.Context(), MCPReceiptQuery{"a", "first"})
	require.NoError(t, err)
	require.Equal(t, "committed", r.Status)
	require.True(t, r.Definitive)
	require.Equal(t, a, *r.Result)
	r, err = s.WriteResult(t.Context(), MCPReceiptQuery{"a", "unknown"})
	require.NoError(t, err)
	require.Equal(t, "not_found", r.Status)
	require.False(t, r.Definitive)
	first.Price = "11"
	_, err = s.Write(t.Context(), "ledger_record_trade", first)
	require.Equal(t, "idempotency_conflict", mcpToolFailure(err).Code)
	first = mcpTrade("stale", "0")
	_, err = s.Write(t.Context(), "ledger_record_trade", first)
	require.Equal(t, "version_conflict", mcpToolFailure(err).Code)
	cash.RequestID, cash.ExpectedVersion = "old-cash", "4"
	_, err = s.Write(t.Context(), "ledger_set_cash_balance", cash)
	require.Equal(t, "invalid_observed_date", mcpToolFailure(err).Code)
	var records int
	require.NoError(t, s.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&records))
	require.Zero(t, records)
}

func TestMCPNormalizationAndBusinessRejections(t *testing.T) {
	s, _ := mcpFixture(t)
	for _, value := range []string{"-1", "-0", "1e2", "1,000", "1.0000001", "9223372036854.775808", "NaN", ""} {
		t.Run(value, func(t *testing.T) {
			in := mcpTrade("invalid", "0")
			in.Quantity = value
			before := auditCount(t, s.Store)
			_, err := s.Write(t.Context(), "ledger_record_trade", in)
			require.Error(t, err)
			require.Equal(t, before, auditCount(t, s.Store))
		})
	}
	in := mcpTrade("buy", "0")
	out, err := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	in.Quantity, in.Price, in.Fee, in.Mode = "10.000000", "10.000000", "0.00", "trade"
	again, err := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	require.Equal(t, out, again)
	in.RequestID, in.ExpectedVersion, in.Side, in.Quantity = "oversell", "1", "sell", "11"
	_, err = s.Write(t.Context(), "ledger_record_trade", in)
	require.Equal(t, "stock_quantity", mcpToolFailure(err).Code)
	require.NotEmpty(t, mcpToolFailure(err).Details["entry_id"])
	in.RequestID, in.Currency = "currency", HKD
	_, err = s.Write(t.Context(), "ledger_record_trade", in)
	require.Equal(t, "currency_mismatch", mcpToolFailure(err).Code)
	in.RequestID, in.Currency, in.Date = "future", CNY, "2027-01-01"
	_, err = s.Write(t.Context(), "ledger_record_trade", in)
	require.Equal(t, "invalid_input", mcpToolFailure(err).Code)
}

func TestMCPConcurrentKeysAndAtomicRollback(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(map[bool]string{true: "same", false: "different"}[same], func(t *testing.T) {
			s, _ := mcpFixture(t)
			// Avoid concurrent provider calls in this test; production providers are bounded.
			require.NoError(t, s.Store.AddInstrument(t.Context(), Instrument{"i", "SH", "600036", "Synthetic", CNY}))
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for _, key := range []string{"x", "y"} {
				if same {
					key = "x"
				}
				wg.Add(1)
				go func(key string) {
					defer wg.Done()
					_, err := s.Write(t.Context(), "ledger_record_trade", mcpTrade(key, "0"))
					results <- err
				}(key)
			}
			wg.Wait()
			close(results)
			success := 0
			for err := range results {
				if err == nil {
					success++
				} else {
					require.Equal(t, "version_conflict", mcpToolFailure(err).Code)
				}
			}
			if same {
				require.Equal(t, 2, success)
			} else {
				require.Equal(t, 1, success)
			}
			require.Len(t, readBook(t, s.Store).Entries, 1)
		})
	}
	for _, table := range []string{"instruments", "stock_entries", "stock_journals", "current_holdings", "audit_log", "idempotency_receipts"} {
		t.Run("rollback_"+table, func(t *testing.T) {
			s, _ := mcpFixture(t)
			before := auditCount(t, s.Store)
			when := ""
			if table == "audit_log" {
				when = " WHEN NEW.entity_type='mcp_stock_command'"
			}
			_, err := s.Store.db.ExecContext(t.Context(), `CREATE TRIGGER reject_mcp BEFORE INSERT ON `+table+when+` BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`)
			require.NoError(t, err)
			_, err = s.Write(t.Context(), "ledger_record_trade", mcpTrade("rollback", "0"))
			require.Error(t, err)
			require.Equal(t, before, auditCount(t, s.Store))
			var count int
			require.NoError(t, s.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM stock_entries`).Scan(&count))
			require.Zero(t, count)
			require.NoError(t, s.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM instruments`).Scan(&count))
			require.Zero(t, count)
		})
	}
}

func TestMCPMigrationPreservesLegacyBytesAndRestartRecovery(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(t.Context(), root, "ledger")
	require.NoError(t, err)
	prior := fstest.MapFS{}
	files, err := fs.ReadDir(migrations, "migrations")
	require.NoError(t, err)
	for _, f := range files {
		if f.Name() >= "008" {
			continue
		}
		raw, err := migrations.ReadFile("migrations/" + f.Name())
		require.NoError(t, err)
		prior[f.Name()] = &fstest.MapFile{Data: raw}
	}
	require.NoError(t, db.Migrate(t.Context(), prior))
	now := func() time.Time { return time.Date(2026, 9, 6, 5, 0, 0, 0, time.UTC) }
	store := NewStore(db, now)
	manualSourceAccount(t, store, "a")
	c := buyCommand("legacy", "0", "2026-01-01", 100, 10)
	c.Security = stockSecurity(CNY)
	legacy, err := store.WriteStock(t.Context(), "a", "legacy", c)
	require.NoError(t, err)
	var frozen string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT request_json||response_json||request_hash FROM idempotency_receipts WHERE key='legacy'`).Scan(&frozen))
	require.NoError(t, db.Close())
	db, err = Open(t.Context(), root)
	require.NoError(t, err)
	store = NewStore(db, now)
	var restored string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT request_json||response_json||request_hash FROM idempotency_receipts WHERE key='legacy'`).Scan(&restored))
	require.Equal(t, frozen, restored)
	again, err := store.WriteStock(t.Context(), "a", "legacy", c)
	require.NoError(t, err)
	require.Equal(t, string(legacy), string(again))
	svc := MCPService{Store: store}
	in := mcpTrade("durable", "1")
	out, err := svc.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = Open(t.Context(), root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	svc = MCPService{Store: NewStore(db, now)}
	replay, err := svc.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	a, _ := json.Marshal(out)
	b, _ := json.Marshal(replay)
	require.Equal(t, string(a), string(b))
	_, err = db.ExecContext(t.Context(), `UPDATE idempotency_receipts SET response_json='{}'`)
	require.Error(t, err)
	var violations int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations))
	require.Zero(t, violations)
}

func TestMCPRequestNamespaceSpansToolsButSeparatesAccounts(t *testing.T) {
	s, _ := mcpFixture(t)
	a, err := s.Write(t.Context(), "ledger_record_trade", mcpTrade("same", "0"))
	require.NoError(t, err)
	_, err = s.Write(t.Context(), "ledger_set_cash_balance", MCPCashInput{MCPWriteFields: MCPWriteFields{RequestID: "same", AccountID: "a", ExpectedVersion: "1"}, ObservedDate: "2026-09-06", Amount: "100", Currency: CNY})
	require.Equal(t, "idempotency_conflict", mcpToolFailure(err).Code)
	manualSourceAccount(t, s.Store, "b")
	in := mcpTrade("same", "0")
	in.AccountID = "b"
	b, err := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	require.NotEqual(t, a.ReceiptID, b.ReceiptID)
	require.NotEqual(t, a.Entry.ID, b.Entry.ID)
	result, err := s.WriteResult(t.Context(), MCPReceiptQuery{"b", "same"})
	require.NoError(t, err)
	require.Equal(t, b, *result.Result)
}
