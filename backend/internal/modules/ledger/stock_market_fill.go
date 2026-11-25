package ledger

import (
	"context"
	"maps"
	"slices"
	"time"
)

func (s *Store) lockMarketUpdates(ctx context.Context) bool {
	select {
	case s.marketUpdates <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}
func (s *Store) unlockMarketUpdates() { <-s.marketUpdates }

// An explicit request samples every open position even outside trading hours.
func (h Handler) refreshHeldQuotes(ctx context.Context, held []Instrument, currency Currency) (int, error) {
	targets := missingMarketTargets(held, currency, nil, nil, true, false)
	if len(targets) == 0 || h.Quotes == nil {
		return len(held), nil
	}
	remote, cancel := context.WithTimeout(ctx, valuationTimeout-2*time.Second)
	defer cancel()
	if !h.Store.lockMarketUpdates(remote) {
		return len(held), ctx.Err()
	}
	defer h.Store.unlockMarketUpdates()
	active := map[string]bool{}
	for _, t := range targets {
		active[t.instrument.Market] = true
	}
	misses, err := h.Store.fetchMarketMarks(ctx, remote, targets, active, h.Quotes, nil)
	return len(held) - len(targets) + misses, err
}

func missingMarketTargets(held []Instrument, currency Currency, quotes savedStockQuotes, rates savedStockFX, wantQuotes, wantFX bool) []stockMarketTarget {
	var missing []stockMarketTarget
	for _, i := range held {
		symbol, code := quoteSymbol(i)
		if code != "" {
			continue
		}
		_, hasRate := rates[FXRequest{Base: i.Currency, Quote: currency, Mode: "latest"}]
		needQ, needFX := wantQuotes && quotes[symbol] == nil, wantFX && i.Currency != currency && !hasRate
		if needQ || needFX {
			// Use exchange identities, not account-specific instrument IDs or names.
			instrument := Instrument{ID: symbol, Market: i.Market, Code: i.Code, Currency: i.Currency}
			missing = append(missing, stockMarketTarget{instrument: instrument, accountCurrency: currency, missingQuote: needQ, missingFX: needFX})
		}
	}
	return missing
}

// Serve local prices immediately on a hit. A miss uses the same provider,
// validation and timestamp-guarded writes as the scheduled update but only
// for this book's missing prices/rates, including when the market is closed.
func (h Handler) stockBookPrices(ctx context.Context, held []Instrument, currency Currency) (savedStockQuotes, savedStockFX, error) {
	s := h.Store
	quotes, rates, err := s.readStockPrices(ctx, held, currency)
	if err != nil || len(missingMarketTargets(held, currency, quotes, rates, h.Quotes != nil, h.FX != nil)) == 0 {
		return quotes, rates, err
	}
	// Reserve time for persisting any successful legs and returning the book
	// even when the source is slow. Client cancellation still stops all I/O.
	end := time.Now().Add(valuationTimeout - 2*time.Second)
	if deadline, ok := ctx.Deadline(); ok && deadline.Add(-time.Second).Before(end) {
		end = deadline.Add(-time.Second)
	}
	remote, cancel := context.WithDeadline(ctx, end)
	defer cancel()
	if !s.lockMarketUpdates(remote) {
		return quotes, rates, ctx.Err()
	}
	defer s.unlockMarketUpdates()
	// Another reader or the scheduled worker may just have populated them.
	quotes, rates, err = s.readStockPrices(ctx, held, currency)
	if err != nil {
		return nil, nil, err
	}
	missing := missingMarketTargets(held, currency, quotes, rates, h.Quotes != nil, h.FX != nil)
	if len(missing) == 0 || remote.Err() != nil {
		return quotes, rates, ctx.Err()
	}
	if _, err = s.fetchMarketMarks(ctx, remote, missing, nil, h.Quotes, h.FX); err != nil {
		return nil, nil, err
	}
	return s.readStockPrices(ctx, held, currency)
}

// Caller holds marketUpdates, not a SQLite transaction. Published quotes
// and FX are persisted together; a failed leg never clears a successful one.
func (s *Store) fetchMarketMarks(ctx, remote context.Context, targets []stockMarketTarget, active map[string]bool, provider QuotesProvider, fx FXProvider) (int, error) {
	now := s.now()
	maps.DeleteFunc(s.marketRetryAfter, func(_ string, until time.Time) bool { return !until.After(now) })
	var instruments []Instrument
	seen := map[string]bool{}
	pairs := map[FXRequest]bool{}
	for _, t := range targets {
		i := t.instrument
		if provider != nil && !seen[i.ID] && (active[i.Market] || t.missingQuote && !s.marketRetryAfter[i.ID].After(now)) {
			seen[i.ID] = true
			instruments = append(instruments, i)
		}
		if fx != nil && i.Currency != t.accountCurrency && (active[i.Market] || t.missingFX && !s.marketRetryAfter[stockFXKey(i.Currency, t.accountCurrency)].After(now)) {
			pairs[FXRequest{Base: i.Currency, Quote: t.accountCurrency, Mode: "latest"}] = true
		}
	}
	quotes, rates := savedStockQuotes{}, savedStockFX{}
	for batch := range slices.Chunk(instruments, 50) {
		if remote.Err() != nil {
			break
		}
		got := provider.Fetch(remote, batch)
		at := s.now()
		today := at.In(fxBeijing).Format(time.DateOnly)
		for _, i := range batch {
			row := got[i.ID]
			if row.Quote != nil && row.ErrorCode == "" && validPositionQuote(row.Quote, i, today, at) {
				quotes[i.ID] = row.Quote
				delete(s.marketRetryAfter, i.ID)
			} else if ctx.Err() == nil {
				s.marketRetryAfter[i.ID] = at.Add(time.Minute)
			}
		}
	}
	for r := range pairs {
		if remote.Err() != nil {
			break
		}
		q, err := fx.Fetch(remote, r)
		at := s.now()
		key := stockFXKey(r.Base, r.Quote)
		if err == nil && validPositionFX(q, r.Base, r.Quote, at.In(fxBeijing).Format(time.DateOnly), at) {
			rates[r] = q
			delete(s.marketRetryAfter, key)
		} else if ctx.Err() == nil {
			s.marketRetryAfter[key] = at.Add(time.Minute)
		}
	}
	return len(instruments) + len(pairs) - len(quotes) - len(rates), s.saveStockPrices(ctx, quotes, rates)
}
