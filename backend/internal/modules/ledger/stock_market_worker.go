package ledger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

type StockMarketWorker struct {
	store      *Store
	quotes     QuotesProvider
	fx         FXProvider
	marketOpen func(context.Context, string, time.Time) (bool, error)
	logger     *slog.Logger
}

func NewStockMarketWorker(store *Store, quotes *TencentQuotes, fx FXProvider, logger *slog.Logger) *StockMarketWorker {
	return &StockMarketWorker{store: store, quotes: quotes, fx: fx, marketOpen: quotes.stockMarketOpen, logger: logger}
}

func (w *StockMarketWorker) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		now := w.store.now()
		slot, markets, next := stockQuoteSchedule(now)
		if err := w.refreshSlot(ctx, slot, markets); err != nil && ctx.Err() == nil && w.logger != nil {
			w.logger.Warn("ledger holdings price update", "error", err)
		}
		// Check for never-priced holdings on startup and between sessions. A
		// fully populated weekend book entails only a local database read.
		nextCheck := now.Add(stockIntervalMinutes * time.Minute)
		if next.Before(nextCheck) {
			nextCheck = next
		}
		timer := time.NewTimer(nextCheck.Sub(w.store.now()))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}

type stockMarketTarget struct {
	instrument      Instrument
	accountCurrency Currency
	missingQuote    bool
	missingFX       bool
}

// Only open positions are projected into current_holdings. Do not send
// portfolio IDs, quantities, names or balances to the market-data source.
func (s *Store) stockMarketTargets(ctx context.Context) ([]stockMarketTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT i.market, i.code, i.currency, a.currency, q.symbol IS NULL, f.symbol IS NULL AND i.currency<>a.currency
		FROM current_holdings h JOIN accounts a ON a.id=h.account_id
		JOIN json_each(h.payload,'$.positions') p JOIN instruments i ON i.id=json_extract(p.value,'$.instrument_id')
		LEFT JOIN stock_market_marks q ON q.symbol=lower(i.market)||i.code
		LEFT JOIN stock_market_marks f ON f.symbol='fx:'||i.currency||'/'||a.currency
		ORDER BY i.market, i.code, i.currency, a.currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []stockMarketTarget
	for rows.Next() {
		var t stockMarketTarget
		i := &t.instrument
		if err := rows.Scan(&i.Market, &i.Code, &i.Currency, &t.accountCurrency, &t.missingQuote, &t.missingFX); err != nil {
			return nil, err
		}
		if symbol, code := quoteSymbol(*i); code == "" {
			i.ID = symbol
			targets = append(targets, t)
		}
	}
	return targets, rows.Err()
}

func (w *StockMarketWorker) refreshSlot(ctx context.Context, slot time.Time, markets []string) error {
	// Only scheduled trading refreshes advance the trading clock. Without a
	// slot (e.g. Saturday), bootstrap only the missing legs from the source's
	// latest observation, keeping its original date and timestamp.
	if !slot.IsZero() {
		res, err := w.store.db.ExecContext(ctx, `INSERT INTO stock_market_clock(id,slot) VALUES(1,?)
		ON CONFLICT(id) DO UPDATE SET slot=excluded.slot WHERE excluded.slot>stock_market_clock.slot`, slot.Unix())
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
	}
	if !w.store.lockMarketUpdates(ctx) {
		return ctx.Err()
	}
	defer w.store.unlockMarketUpdates()
	targets, err := w.store.stockMarketTargets(ctx)
	if err != nil || len(targets) == 0 {
		return err
	}
	// Every provider request also has a short individual deadline. Keep this
	// pass bounded, and persist completed observations if later fetches time out.
	remote, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	active := make(map[string]bool)
	var issues error
	for _, m := range markets {
		if !slices.ContainsFunc(targets, func(t stockMarketTarget) bool { return t.instrument.Market == m }) {
			continue
		}
		open, err := w.marketOpen(remote, m, w.store.now())
		if err != nil {
			issues = errors.Join(issues, fmt.Errorf("%s session: %w", m, err))
			continue
		}
		active[m] = open
	}
	missing, err := w.store.fetchMarketMarks(ctx, remote, targets, active, w.quotes, w.fx)
	if missing > 0 {
		issues = errors.Join(issues, fmt.Errorf("%d market observations unavailable", missing))
	}
	return errors.Join(issues, err)
}
