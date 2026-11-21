package ledger

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/stretchr/testify/require"
)

func TestStockMarketUpgradeStartsFromLatestFrozenEvidence(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	old, err := database.Open(ctx, root, "ledger")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, old.Close()) })
	files, err := fs.Glob(migrations, "migrations/00*.sql")
	require.NoError(t, err)
	previous := fstest.MapFS{}
	for _, f := range files {
		contents, err := migrations.ReadFile(f)
		require.NoError(t, err)
		previous[filepath.Base(f)] = &fstest.MapFile{Data: contents}
	}
	require.NoError(t, old.Migrate(ctx, previous))
	s := NewStore(old, func() time.Time { return stockNow })
	manualSourceAccount(t, s, "a")
	require.NoError(t, s.AddInstrument(ctx, Instrument{ID: "hk", Market: "HK", Code: "00700", Name: "Synthetic", Currency: HKD}))
	putSource(t, s, "a", "snapshot-a", "0", 10000, CurrentPosition{"hk", 1_000_000})
	// The older observation was saved last. Order by market time, not audit ID.
	at := stockNow
	h := Handler{Quotes: valuationQuotes(func(ctx context.Context, instruments []Instrument) map[string]QuoteResult {
		rows := validValuationQuotes(ctx, instruments)
		q := rows["hk"].Quote
		q.Date = at.In(fxBeijing).Format(time.DateOnly)
		q.QuotedAt = at.Format(time.RFC3339)
		return rows
	}), FX: valuationFX(func(context.Context, FXRequest) (FXQuote, error) {
		return FXQuote{Base: HKD, Quote: CNY, Mode: "latest", Rate: 50_000_000, Date: at.In(fxBeijing).Format(time.DateOnly), QuotedAt: at.Format(time.RFC3339), FetchedAt: stockNow.Format(time.RFC3339), Source: "Tencent/spot/HKDCNY"}, nil
	})}
	latest := sampleValuation(t, s, "a", h)
	at = at.AddDate(0, 0, -1)
	sampleValuation(t, s, "a", h)
	count := auditCount(t, s)
	require.NoError(t, old.Close())
	db, err := Open(ctx, root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	s = NewStore(db, func() time.Time { return stockNow })
	book := readBook(t, s)
	require.True(t, book.Complete)
	require.Equal(t, latest.Items[0].Quote, book.Items[0].Quote)
	require.Equal(t, latest.Items[0].FX, book.Items[0].FX)
	require.Equal(t, count, auditCount(t, s))
	historyCount(t, s, 2)
}

func TestStockMarketWorkerSharesPricesAndKeepsPreviousGoodEvidence(t *testing.T) {
	ctx := t.Context()
	const shares Quantity = 1_000_000
	const ten Price = 10_000_000
	securities := []Instrument{
		{ID: "a-hk", Market: "HK", Code: "00700", Name: "One", Currency: HKD},
		{ID: "b-hk", Market: "HK", Code: "00700", Name: "Other identity", Currency: HKD},
		{ID: "cn", Market: "SH", Code: "600000", Name: "Local", Currency: CNY},
		{ID: "unheld", Market: "SZ", Code: "000001", Name: "Unheld", Currency: CNY},
	}
	s := valuationFixture(t, []CurrentPosition{{"a-hk", shares}, {"cn", shares}}, securities)
	_, err := s.CreateReportedAccount(ctx, "make-b", ReportedAccountInput{ID: "b", Name: "Other account", Currency: CNY, OpeningDate: "2026-01-01"})
	require.NoError(t, err)
	putSource(t, s, "b", "snapshot-b", "0", 10000, CurrentPosition{"b-hk", 2 * shares})
	now := stockMarketTime(t, "2026-11-20 10:10")
	s.now = func() time.Time { return now }
	price, stamp := ten, now
	var blocked, invalid bool
	var checked, requested []string
	var fxCalls int
	w := StockMarketWorker{store: s, marketOpen: func(_ context.Context, m string, _ time.Time) (bool, error) {
		checked = append(checked, m)
		return !(blocked && m == "SH"), nil
	},
		quotes: valuationQuotes(func(_ context.Context, batch []Instrument) map[string]QuoteResult {
			out := map[string]QuoteResult{}
			for _, i := range batch {
				requested = append(requested, i.ID)
				q := &Quote{Symbol: i.ID, Price: price, Currency: i.Currency, Source: "Tencent", Date: stamp.Format(time.DateOnly), QuotedAt: stamp.Format(time.RFC3339), FetchedAt: now.Format(time.RFC3339)}
				if invalid {
					q.Currency = USD
				}
				out[i.ID] = QuoteResult{Quote: q}
			}
			return out
		}), fx: valuationFX(func(_ context.Context, r FXRequest) (FXQuote, error) {
			fxCalls++
			require.Equal(t, FXRequest{Base: HKD, Quote: CNY, Mode: "latest"}, r)
			if invalid {
				return FXQuote{}, ErrFXTimeout
			}
			return FXQuote{Base: HKD, Quote: CNY, Mode: "latest", Rate: 50_000_000, Date: stamp.Format(time.DateOnly), QuotedAt: stamp.Format(time.RFC3339), FetchedAt: now.Format(time.RFC3339), Source: "Tencent/spot/HKDCNY"}, nil
		})}
	// Without a quote source missing prices are unknown, not zeros.
	reads := Handler{Store: s}
	current := func(id string) StockBook { b, err := reads.stockBookView(ctx, id); require.NoError(t, err); return b }
	cold := current("a")
	require.False(t, cold.Complete)
	require.Nil(t, cold.TotalAssets)
	require.Nil(t, cold.PositionsValue)
	require.Nil(t, cold.Items[0].Price)
	count := auditCount(t, s)
	require.NoError(t, w.refreshSlot(ctx, now, stockMarkets[:]))
	reads.Quotes = valuationQuotes(func(context.Context, []Instrument) map[string]QuoteResult {
		panic("stock-book refetched a cached quote")
	})
	reads.FX = valuationFX(func(context.Context, FXRequest) (FXQuote, error) { panic("stock-book refetched a cached FX rate") })
	require.ElementsMatch(t, []string{"SH", "HK"}, checked)
	require.ElementsMatch(t, []string{"hk00700", "sh600000"}, requested)
	require.Equal(t, 1, fxCalls)
	require.Equal(t, Money(11500), *current("a").TotalAssets)
	require.Equal(t, Money(11000), *current("b").TotalAssets)
	require.Equal(t, count, auditCount(t, s))
	// An independent worker (same DB / restarted service) cannot claim twice.
	again := w
	require.NoError(t, again.refreshSlot(ctx, now, stockMarkets[:]))
	require.Len(t, requested, 2)
	require.Len(t, checked, 2)
	// One exchange is closed; the other can advance. A delayed older quote,
	// a wrong-currency quote and a failed FX lookup cannot replace valid ones.
	now = now.Add(20 * time.Minute)
	stamp = now
	price = 2 * ten
	blocked = true
	requested = nil
	require.NoError(t, w.refreshSlot(ctx, now, stockMarkets[:]))
	require.Equal(t, []string{"hk00700"}, requested)
	require.Equal(t, Money(12000), *current("a").TotalAssets)
	now = now.Add(20 * time.Minute)
	stamp = stamp.Add(-time.Hour)
	price = 3 * ten
	require.NoError(t, w.refreshSlot(ctx, now, stockMarkets[:]))
	require.Equal(t, Money(12000), *current("a").TotalAssets)
	now = now.Add(20 * time.Minute)
	stamp = now
	invalid = true
	require.Error(t, w.refreshSlot(ctx, now, stockMarkets[:]))
	require.Equal(t, Money(12000), *current("a").TotalAssets)
	// A write changes quantity and cash immediately, without refetching prices.
	putSource(t, s, "b", "update-b", "1", 50000, CurrentPosition{"b-hk", 3 * shares})
	b := current("b")
	require.Equal(t, "2", b.Version)
	require.Equal(t, Money(53000), *b.TotalAssets)
	// Open the file again off-hours: all price/FX evidence survives restart.
	var path string
	require.NoError(t, s.db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path))
	require.NoError(t, s.db.Close())
	db, err := Open(ctx, filepath.Dir(path))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	reads.Store = NewStore(db, func() time.Time { return now.AddDate(0, 0, 1) })
	b = current("b")
	require.Equal(t, Money(53000), *b.TotalAssets)
	require.Equal(t, "prior_date", b.Items[0].QuoteStatus)
}

func TestStockMarketWorkerDoesNotBlockReadersAndJoinsOnCancel(t *testing.T) {
	s, clock := stockFixture(t)
	*clock = stockMarketTime(t, "2026-11-20 10:10")
	buy := buyCommand("buy", "0", "2026-11-19", 1, 10)
	buy.Security = stockSecurity(CNY)
	stockWrite(t, s, "buy", buy)
	started := make(chan struct{})
	done := make(chan error, 1)
	w := StockMarketWorker{store: s, marketOpen: func(context.Context, string, time.Time) (bool, error) { return true, nil }, quotes: valuationQuotes(func(ctx context.Context, _ []Instrument) map[string]QuoteResult {
		close(started)
		<-ctx.Done()
		return nil
	})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { done <- w.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not fetch without a page")
	}
	// SQLite has one connection. All readers must finish while the upstream hangs.
	readCtx, stop := context.WithTimeout(t.Context(), 2*time.Second)
	defer stop()
	var group sync.WaitGroup
	errs := make(chan error, 8)
	for range cap(errs) {
		group.Go(func() {
			book, err := (Handler{Store: s}).stockBookView(readCtx, "a")
			if err == nil && (book.Complete || book.Version != "1") {
				err = fmt.Errorf("unexpected live book")
			}
			errs <- err
		})
	}
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not cancel its network I/O and timer")
	}
}
