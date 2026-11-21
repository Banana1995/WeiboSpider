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
		if len(markets) > 0 {
			if err := w.refreshSlot(ctx, slot, markets); err != nil && ctx.Err() == nil && w.logger != nil {
				w.logger.Warn("ledger holdings price update", "error", err)
			}
		}
		timer := time.NewTimer(next.Sub(w.store.now()))
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
}

// Only open positions are projected into current_holdings. Do not send
// portfolio IDs, quantities, names or balances to the market-data source.
func (s *Store) stockMarketTargets(ctx context.Context) ([]stockMarketTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT i.market, i.code, i.currency, a.currency
		FROM current_holdings h JOIN accounts a ON a.id=h.account_id
		JOIN json_each(h.payload,'$.positions') p JOIN instruments i ON i.id=json_extract(p.value,'$.instrument_id')
		ORDER BY i.market, i.code, i.currency, a.currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []stockMarketTarget
	for rows.Next() {
		var t stockMarketTarget
		i := &t.instrument
		if err := rows.Scan(&i.Market, &i.Code, &i.Currency, &t.accountCurrency); err != nil {
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
	// Claim before provider I/O. A restart in the same slot does not repeat
	// requests, including an unsuccessful attempt. The next slot can retry.
	res, err := w.store.db.ExecContext(ctx, `INSERT INTO stock_market_clock(id,slot) VALUES(1,?)
		ON CONFLICT(id) DO UPDATE SET slot=excluded.slot WHERE excluded.slot>stock_market_clock.slot`, slot.Unix())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
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
	var instruments []Instrument
	seen := map[string]bool{}
	pairs := map[FXRequest]bool{}
	for _, t := range targets {
		i := t.instrument
		if !active[i.Market] {
			continue
		}
		if !seen[i.ID] {
			seen[i.ID] = true
			instruments = append(instruments, i)
		}
		if i.Currency != t.accountCurrency {
			pairs[FXRequest{Base: i.Currency, Quote: t.accountCurrency, Mode: "latest"}] = true
		}
	}
	quotes, rates := savedStockQuotes{}, savedStockFX{}
	for batch := range slices.Chunk(instruments, 50) {
		if remote.Err() != nil {
			break
		}
		got := w.quotes.Fetch(remote, batch)
		now := w.store.now()
		today := now.In(fxBeijing).Format(time.DateOnly)
		for _, i := range batch {
			row := got[i.ID]
			if row.Quote != nil && row.ErrorCode == "" && validPositionQuote(row.Quote, i, today, now) {
				quotes[i.ID] = row.Quote
			}
		}
	}
	for r := range pairs {
		if remote.Err() != nil {
			break
		}
		q, err := w.fx.Fetch(remote, r)
		now := w.store.now()
		if err == nil && validPositionFX(q, r.Base, r.Quote, now.In(fxBeijing).Format(time.DateOnly), now) {
			rates[r] = q
		}
	}
	if missing := len(instruments) + len(pairs) - len(quotes) - len(rates); missing > 0 {
		issues = errors.Join(issues, fmt.Errorf("%d market observations unavailable", missing))
	}
	return errors.Join(issues, w.store.saveStockPrices(ctx, quotes, rates))
}
