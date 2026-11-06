package ledger

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMCPBookStablePaginationFiltersAndReadOnly(t *testing.T) {
	s, now := mcpFixture(t)
	for i := range 5 {
		in := mcpTrade("page-"+strconv.Itoa(i), strconv.Itoa(i))
		if i < 2 {
			in.Date = "2026-01-01"
		}
		_, err := s.Write(t.Context(), "ledger_record_trade", in)
		require.NoError(t, err)
	}
	s.Instruments = nil
	before := auditCount(t, s.Store)
	q := MCPBookQuery{AccountID: "a", IncludeEntries: true, Limit: 2}
	first, err := s.StockBook(t.Context(), q)
	require.NoError(t, err)
	require.Len(t, first.Entries, 2)
	require.NotNil(t, first.EntriesNextCursor)
	require.Greater(t, first.Entries[0].Sequence, first.Entries[1].Sequence)
	q.Cursor = *first.EntriesNextCursor
	second, err := s.StockBook(t.Context(), q)
	require.NoError(t, err)
	require.Len(t, second.Entries, 2)
	require.NotNil(t, second.EntriesNextCursor)
	q.Cursor = *second.EntriesNextCursor
	third, err := s.StockBook(t.Context(), q)
	require.NoError(t, err)
	require.Len(t, third.Entries, 1)
	require.Nil(t, third.EntriesNextCursor)
	seen := map[string]bool{}
	for _, book := range []MCPBook{first, second, third} {
		for _, e := range book.Entries {
			require.False(t, seen[e.ID])
			seen[e.ID] = true
		}
	}
	require.Len(t, seen, 5)
	filtered, err := s.StockBook(t.Context(), MCPBookQuery{AccountID: "a", IncludeEntries: true, To: "2026-01-01"})
	require.NoError(t, err)
	require.Len(t, filtered.Entries, 2)
	one, err := s.StockBook(t.Context(), MCPBookQuery{AccountID: "a", EntryID: first.Entries[0].ID})
	require.NoError(t, err)
	require.Len(t, one.Entries, 1)
	book, err := s.StockBook(t.Context(), MCPBookQuery{AccountID: "a"})
	require.NoError(t, err)
	require.Empty(t, book.Entries)
	require.Nil(t, book.Positions[0].Metrics.Profit)
	require.NotNil(t, book.Positions[0].Metrics.Cost)
	require.Equal(t, before, auditCount(t, s.Store))
	q.Cursor = *first.EntriesNextCursor
	q.IncludeVoided = true
	_, err = s.StockBook(t.Context(), q)
	require.Equal(t, "stale_cursor", mcpToolFailure(err).Code)
	q.IncludeVoided = false
	*now = now.Add(24 * time.Hour)
	_, err = s.StockBook(t.Context(), q)
	require.Equal(t, "stale_cursor", mcpToolFailure(err).Code)
	*now = now.Add(-24 * time.Hour)
	_, err = s.Write(t.Context(), "ledger_record_trade", mcpTrade("page-new", "5"))
	require.NoError(t, err)
	_, err = s.StockBook(t.Context(), q)
	require.Equal(t, "stale_cursor", mcpToolFailure(err).Code)
	q.Cursor = "not-a-valid-cursor"
	_, err = s.StockBook(t.Context(), q)
	require.Equal(t, "invalid_input", mcpToolFailure(err).Code)
}

func TestMCPAccountFilteringBeforePaginationAndLiteralWildcards(t *testing.T) {
	s, _ := mcpFixture(t)
	for _, v := range []struct{ id, name string }{{"b", "Other"}, {"c", "港股 1"}, {"d", "港股 2"}, {"e", "literal_%"}} {
		_, err := s.Store.CreateReportedAccount(t.Context(), v.id, ReportedAccountInput{ID: v.id, Name: v.name, Currency: HKD, OpeningDate: "2026-01-01"})
		require.NoError(t, err)
	}
	first, err := s.Accounts(t.Context(), MCPAccountQuery{Query: "港股", Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	require.Equal(t, "c", first.Items[0].ID)
	require.NotNil(t, first.NextCursor)
	second, err := s.Accounts(t.Context(), MCPAccountQuery{Query: "港股", Limit: 1, Cursor: *first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.Equal(t, "d", second.Items[0].ID)
	require.Nil(t, second.NextCursor)
	literal, err := s.Accounts(t.Context(), MCPAccountQuery{Query: "_%"})
	require.NoError(t, err)
	require.Len(t, literal.Items, 1)
	_, err = s.Accounts(t.Context(), MCPAccountQuery{Limit: -1})
	require.Error(t, err)
}

func TestMCPInstrumentResolutionFailuresDoNotWriteAndCatalogWorksOffline(t *testing.T) {
	s, _ := mcpFixture(t)
	before := auditCount(t, s.Store)
	s.Instruments = mcpSearchFunc(func(context.Context, string) ([]InstrumentSearchItem, error) {
		return nil, errors.New("synthetic offline")
	})
	_, err := s.Write(t.Context(), "ledger_record_trade", mcpTrade("new", "0"))
	require.Equal(t, "instrument_unavailable", mcpToolFailure(err).Code)
	require.Equal(t, before, auditCount(t, s.Store))
	s.Instruments = mcpSearchFunc(func(context.Context, string) ([]InstrumentSearchItem, error) {
		return []InstrumentSearchItem{{"A", "SH", "600036", CNY}, {"B", "SH", "600036", CNY}}, nil
	})
	_, err = s.Write(t.Context(), "ledger_record_trade", mcpTrade("ambiguous", "0"))
	require.Equal(t, "instrument_ambiguous", mcpToolFailure(err).Code)
	require.Equal(t, before, auditCount(t, s.Store))
	require.NoError(t, s.Store.AddInstrument(t.Context(), Instrument{"i", "SH", "600036", "Synthetic", CNY}))
	require.NoError(t, s.Store.AddInstrument(t.Context(), Instrument{"other-id", "SH", "600036", "Another archived name", CNY}))
	s.Instruments = nil
	items, err := s.SearchInstruments(t.Context(), MCPInstrumentQuery{Query: "sh600036"})
	require.NoError(t, err)
	require.Equal(t, "ledger_catalog", items.Source)
	require.Len(t, items.Items, 1)
	_, err = s.Write(t.Context(), "ledger_record_trade", mcpTrade("offline", "0"))
	require.NoError(t, err)
	_, err = s.SearchInstruments(t.Context(), MCPInstrumentQuery{Query: "hk00700", Market: "SH"})
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.Write(ctx, "ledger_record_trade", mcpTrade("canceled", "1"))
	require.Equal(t, "request_timeout", mcpToolFailure(err).Code)
}
