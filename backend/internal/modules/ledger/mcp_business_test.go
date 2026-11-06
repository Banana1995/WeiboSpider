package ledger

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMCPFXAndExplicitZeroCashAnchor(t *testing.T) {
	s, _ := mcpFixture(t)
	in := mcpTrade("hk-old", "0")
	in.Instrument = MCPInstrumentSelector{"HK", "00700"}
	in.Currency = HKD
	in.Date = "2026-01-01"
	old, err := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	require.Nil(t, old.CashAnchorAfter)
	require.Zero(t, old.Entry.FX)
	cash := MCPCashInput{MCPWriteFields: MCPWriteFields{RequestID: "zero", AccountID: "a", ExpectedVersion: "1"}, ObservedDate: "2026-09-06", Amount: "0", Currency: CNY}
	zero, err := s.Write(t.Context(), "ledger_set_cash_balance", cash)
	require.NoError(t, err)
	require.NotNil(t, zero.CashAnchorAfter)
	require.Zero(t, zero.CashAnchorAfter.Amount)
	in.RequestID, in.ExpectedVersion, in.Date = "needs-fx", "2", "2026-09-06"
	_, err = s.Write(t.Context(), "ledger_record_trade", in)
	require.Equal(t, "fx_required", mcpToolFailure(err).Code)
	in.FX = "0.9"
	_, err = s.Write(t.Context(), "ledger_record_trade", in)
	require.Equal(t, "stock_cash", mcpToolFailure(err).Code)
	cash.RequestID, cash.ExpectedVersion, cash.Amount = "balance", "2", "1000"
	_, err = s.Write(t.Context(), "ledger_set_cash_balance", cash)
	require.NoError(t, err)
	in.RequestID, in.ExpectedVersion = "settled", "3"
	settled, err := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	require.Equal(t, Money(91000), settled.Cash.After)
	require.Equal(t, Money(-9000), settled.Cash.Delta)
	in.RequestID, in.ExpectedVersion, in.Date, in.FX = "covered", "4", "2026-01-02", ""
	covered, err := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	require.Zero(t, covered.Cash.Delta)
	require.Equal(t, "covered_by_anchor", covered.CashEffectReason)
	same := mcpTrade("bad-same-fx", "5")
	same.FX = "0.9"
	_, err = s.Write(t.Context(), "ledger_record_trade", same)
	require.Equal(t, "invalid_input", mcpToolFailure(err).Code)
}

func TestMCPOpeningCompletionAndHistoricalCorrections(t *testing.T) {
	s, _ := mcpFixture(t)
	require.NoError(t, s.Store.AddInstrument(t.Context(), Instrument{"i", "SH", "600036", "Synthetic", CNY}))
	putSource(t, s.Store, "a", "opening", "0", 100000, CurrentPosition{"i", 10000000})
	book, err := s.StockBook(t.Context(), MCPBookQuery{AccountID: "a"})
	require.NoError(t, err)
	require.True(t, book.Positions[0].Opening)
	require.Nil(t, book.Positions[0].Metrics.Cost)
	in := mcpTrade("complete", "1")
	in.Mode = "complete_opening"
	in.Date = "2026-01-01"
	completed, err := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	require.Equal(t, Quantity(10000000), completed.PositionsChanged[0].QuantityAfter)
	require.Zero(t, completed.PositionsChanged[0].Delta)
	require.Zero(t, completed.Cash.Delta)
	require.False(t, completed.PositionsChanged[0].OpeningAfter)
	require.NotNil(t, completed.PositionsChanged[0].CostAfter)
	sell := mcpTrade("sell-history", "2")
	sell.Side, sell.Quantity = "sell", "8"
	sold, err := s.Write(t.Context(), "ledger_record_trade", sell)
	require.NoError(t, err)
	edit := MCPEditInput{MCPWriteFields: MCPWriteFields{RequestID: "bad-correction", AccountID: "a", ExpectedVersion: "3"}, EntryID: completed.Entry.ID, Action: "replace", Reason: "核对原始数量", Replacement: &MCPReplacement{Kind: "buy", Date: "2026-01-01", Quantity: "5", Price: "10", Fee: "0", Currency: CNY}}
	before := auditCount(t, s.Store)
	_, err = s.Write(t.Context(), "ledger_edit_entry", edit)
	require.Equal(t, "stock_quantity", mcpToolFailure(err).Code)
	require.Equal(t, sold.Entry.ID, mcpToolFailure(err).Details["entry_id"])
	require.Equal(t, before, auditCount(t, s.Store))
	edit.RequestID, edit.EntryID, edit.Action, edit.Replacement = "void-sell", sold.Entry.ID, "void", nil
	voided, err := s.Write(t.Context(), "ledger_edit_entry", edit)
	require.NoError(t, err)
	require.True(t, voided.Entry.Voided)
	book, err = s.StockBook(t.Context(), MCPBookQuery{AccountID: "a", IncludeEntries: true})
	require.NoError(t, err)
	require.Len(t, book.Entries, 1)
	book, err = s.StockBook(t.Context(), MCPBookQuery{AccountID: "a", EntryID: sold.Entry.ID, IncludeVoided: true})
	require.NoError(t, err)
	require.Len(t, book.Entries, 1)
	replay, err := s.Write(t.Context(), "ledger_edit_entry", edit)
	require.NoError(t, err)
	require.Equal(t, voided, replay)
	edit.RequestID, edit.ExpectedVersion = "void-again", "4"
	_, err = s.Write(t.Context(), "ledger_edit_entry", edit)
	require.Equal(t, "operation_voided", mcpToolFailure(err).Code)
	edit.RequestID, edit.EntryID = "wrong-entry", "does-not-exist"
	_, err = s.Write(t.Context(), "ledger_edit_entry", edit)
	require.Equal(t, "entry_not_found", mcpToolFailure(err).Code)
}

func TestMCPAutomaticDividendConflictCorrectionAndSuppression(t *testing.T) {
	s, now := mcpFixture(t)
	*now = time.Date(2026, 9, 1, 5, 0, 0, 0, time.UTC)
	in := mcpTrade("entitled", "0")
	in.Date = "2026-09-01"
	in.Quantity = "100"
	_, err := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	_, err = s.Write(t.Context(), "ledger_set_cash_balance", MCPCashInput{MCPWriteFields: MCPWriteFields{RequestID: "cash", AccountID: "a", ExpectedVersion: "1"}, ObservedDate: "2026-09-01", Amount: "1000", Currency: CNY})
	require.NoError(t, err)
	*now = time.Date(2026, 9, 8, 5, 0, 0, 0, time.UTC)
	event := testDividend("SH", "600036", "2026-09-02", "2026-09-03", "2026-09-08", 2000000)
	worker := NewDividendWorker(s.Store, &stockDivFixture{events: []DividendEvent{event}}, nil, nil)
	require.NoError(t, worker.Tick(t.Context()))
	book := readBook(t, s.Store)
	require.Len(t, book.Entries, 2)
	div := book.Entries[1]
	// Correcting entitlement quantities also changes the automatic dividend in
	// this transaction; the receipt must include that collateral cash movement.
	eligible := MCPEditInput{MCPWriteFields: MCPWriteFields{RequestID: "eligible-fix", AccountID: "a", ExpectedVersion: book.Version}, EntryID: book.Entries[0].ID, Action: "replace", Reason: "核对权益日持股", Replacement: &MCPReplacement{Kind: "buy", Date: "2026-09-01", Quantity: "200", Price: "10", Fee: "0", Currency: CNY}}
	changed, err := s.Write(t.Context(), "ledger_edit_entry", eligible)
	require.NoError(t, err)
	require.Equal(t, 1, changed.RelatedChanges.Count)
	require.Equal(t, []string{div.ID}, changed.RelatedChanges.EntryIDs)
	require.Equal(t, Money(140000), changed.Cash.After)
	book = readBook(t, s.Store)
	manual := MCPDividendInput{MCPWriteFields: MCPWriteFields{RequestID: "duplicate-div", AccountID: "a", ExpectedVersion: book.Version}, Instrument: MCPInstrumentSelector{"SH", "600036"}, Date: "2026-09-08", Amount: "150", Currency: CNY}
	_, err = s.Write(t.Context(), "ledger_record_dividend", manual)
	require.Equal(t, "dividend_conflict", mcpToolFailure(err).Code)
	require.Equal(t, div.ID, mcpToolFailure(err).Details["entry_id"])
	edit := MCPEditInput{MCPWriteFields: MCPWriteFields{RequestID: "actual-div", AccountID: "a", ExpectedVersion: book.Version}, EntryID: div.ID, Action: "replace", Reason: "实际到账金额", Replacement: &MCPReplacement{Kind: "dividend", Date: event.ExDate, Amount: "150", Currency: CNY}}
	actual, err := s.Write(t.Context(), "ledger_edit_entry", edit)
	require.NoError(t, err)
	require.Equal(t, Money(115000), actual.Cash.After)
	require.True(t, actual.Entry.Override)
	require.NoError(t, worker.Tick(t.Context()))
	require.Equal(t, Money(115000), readBook(t, s.Store).Cash)
	edit.RequestID, edit.ExpectedVersion, edit.Action, edit.Replacement = "void-auto", actual.VersionAfter, "void", nil
	voided, err := s.Write(t.Context(), "ledger_edit_entry", edit)
	require.NoError(t, err)
	require.Equal(t, Money(100000), voided.Cash.After)
	require.NoError(t, worker.Tick(t.Context()))
	require.Equal(t, Money(100000), readBook(t, s.Store).Cash)
}

func TestMCPFractionalTradesClearAndReopen(t *testing.T) {
	s, _ := mcpFixture(t)
	in := mcpTrade("fractional", "0")
	in.Quantity, in.Price, in.Fee = "1.234567", "98.765432", "0.07"
	bought, err := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	require.Equal(t, Money(12200), bought.Entry.Amount)
	in.RequestID, in.ExpectedVersion, in.Side, in.Price, in.Fee = "close", "1", "sell", "100", "0"
	sold, err := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	require.Equal(t, Money(12346), sold.Entry.Amount)
	book, err := s.StockBook(t.Context(), MCPBookQuery{AccountID: "a", IncludeClosed: true})
	require.NoError(t, err)
	require.Zero(t, book.Positions[0].Quantity)
	require.Equal(t, Money(146), *book.Positions[0].Metrics.TotalProfit)
	in.RequestID, in.ExpectedVersion, in.Side, in.Quantity, in.Price = "reopen", "2", "buy", "1", "10"
	_, err = s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	book, err = s.StockBook(t.Context(), MCPBookQuery{AccountID: "a"})
	require.NoError(t, err)
	require.Equal(t, Price(10000000), *book.Positions[0].Metrics.Cost)
}

func TestMCPMissingReceiptWhileOriginalIsInFlight(t *testing.T) {
	s, _ := mcpFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s.Instruments = mcpSearchFunc(func(ctx context.Context, _ string) ([]InstrumentSearchItem, error) {
		close(started)
		select {
		case <-release:
			return []InstrumentSearchItem{{"Synthetic", "SH", "600036", CNY}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	done := make(chan error, 1)
	go func() { _, err := s.Write(t.Context(), "ledger_record_trade", mcpTrade("in-flight", "0")); done <- err }()
	<-started
	missing, err := s.WriteResult(t.Context(), MCPReceiptQuery{"a", "in-flight"})
	require.NoError(t, err)
	require.Equal(t, "not_found", missing.Status)
	require.False(t, missing.Definitive)
	once.Do(func() { close(release) })
	require.NoError(t, <-done)
	saved, err := s.WriteResult(t.Context(), MCPReceiptQuery{"a", "in-flight"})
	require.NoError(t, err)
	require.Equal(t, "committed", saved.Status)
}

func TestMCPTradeAndDividendSettlementConcurrency(t *testing.T) {
	s, now := mcpFixture(t)
	*now = time.Date(2026, 9, 1, 5, 0, 0, 0, time.UTC)
	in := mcpTrade("initial", "0")
	in.Date = "2026-09-01"
	in.Quantity = "100"
	_, err := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, err)
	_, err = s.Write(t.Context(), "ledger_set_cash_balance", MCPCashInput{MCPWriteFields: MCPWriteFields{RequestID: "cash", AccountID: "a", ExpectedVersion: "1"}, ObservedDate: "2026-09-01", Amount: "1000", Currency: CNY})
	require.NoError(t, err)
	*now = time.Date(2026, 9, 6, 5, 0, 0, 0, time.UTC)
	worker := NewDividendWorker(s.Store, &stockDivFixture{events: []DividendEvent{testDividend("SH", "600036", "2026-09-02", "2026-09-03", "2026-09-08", 2000000)}}, nil, nil)
	require.NoError(t, worker.Tick(t.Context()))
	version := readBook(t, s.Store).Version
	*now = time.Date(2026, 9, 8, 5, 0, 0, 0, time.UTC)
	in = mcpTrade("concurrent-buy", version)
	in.Date = "2026-09-08"
	done := make(chan error, 1)
	go func() { done <- worker.Tick(t.Context()) }()
	_, writeErr := s.Write(t.Context(), "ledger_record_trade", in)
	require.NoError(t, <-done)
	want := Money(110000)
	if writeErr != nil {
		require.Equal(t, "version_conflict", mcpToolFailure(writeErr).Code)
		want = 120000
	}
	require.NoError(t, worker.Tick(t.Context()))
	book := readBook(t, s.Store)
	require.Equal(t, want, book.Cash)
	dividends := 0
	for _, entry := range book.Entries {
		if entry.Kind == "dividend" && !entry.Voided {
			dividends++
		}
	}
	require.Equal(t, 1, dividends)
}

func TestMCPReceiptTamperingAndDeletedAccountFailClosed(t *testing.T) {
	for _, change := range []string{
		`UPDATE idempotency_receipts SET request_hash=printf('%064d',0) WHERE kind='mcp_stock_command'`,
		`UPDATE idempotency_receipts SET audit_id=(SELECT min(id) FROM audit_log) WHERE kind='mcp_stock_command'`,
		`UPDATE idempotency_receipts SET response_json=json_set(response_json,'$.cash.after','999.00') WHERE kind='mcp_stock_command'`,
	} {
		t.Run(change, func(t *testing.T) {
			s, _ := mcpFixture(t)
			_, err := s.Write(t.Context(), "ledger_record_trade", mcpTrade("proof", "0"))
			require.NoError(t, err)
			_, err = s.Store.db.ExecContext(t.Context(), `DROP TRIGGER idempotency_receipts_no_update`)
			require.NoError(t, err)
			_, err = s.Store.db.ExecContext(t.Context(), change)
			require.NoError(t, err)
			_, err = s.WriteResult(t.Context(), MCPReceiptQuery{"a", "proof"})
			require.Equal(t, "data_integrity", mcpToolFailure(err).Code)
			_, err = s.Write(t.Context(), "ledger_record_trade", mcpTrade("proof", "0"))
			require.Equal(t, "data_integrity", mcpToolFailure(err).Code)
		})
	}
	s, _ := mcpFixture(t)
	_, err := s.Write(t.Context(), "ledger_record_trade", mcpTrade("deleted", "0"))
	require.NoError(t, err)
	_, err = s.Store.DeleteAccount(t.Context(), "a", "delete")
	require.NoError(t, err)
	_, err = s.WriteResult(t.Context(), MCPReceiptQuery{"a", "deleted"})
	require.Equal(t, "account_not_found", mcpToolFailure(err).Code)
	_, err = s.Write(t.Context(), "ledger_record_trade", mcpTrade("deleted", "0"))
	require.Equal(t, "account_not_found", mcpToolFailure(err).Code)
}

func TestMCPEditsEnforceInstrumentRangeForLegacyEntries(t *testing.T) {
	s, _ := mcpFixture(t)
	require.NoError(t, s.Store.AddInstrument(t.Context(), Instrument{"legacy", "TEST", "001", "Synthetic", CNY}))
	putSource(t, s.Store, "a", "opening", "0", 10000, CurrentPosition{"legacy", 10000000})
	c := buyCommand("legacy-entry", "1", "2026-01-01", 1, 10)
	c.Entry.InstrumentID = "legacy"
	stockWrite(t, s.Store, "legacy-entry", c)
	before := auditCount(t, s.Store)
	_, err := s.Write(t.Context(), "ledger_edit_entry", MCPEditInput{MCPWriteFields: MCPWriteFields{RequestID: "unsupported", AccountID: "a", ExpectedVersion: "2"}, EntryID: "legacy-entry", Action: "void", Reason: "Synthetic"})
	require.Equal(t, "unsupported_instrument", mcpToolFailure(err).Code)
	require.Equal(t, before, auditCount(t, s.Store))
}
