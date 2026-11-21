package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type savedStockQuotes map[string]*Quote // by exchange symbol, independent of account/instrument ID

func (quotes savedStockQuotes) Fetch(_ context.Context, instruments []Instrument) map[string]QuoteResult {
	out := make(map[string]QuoteResult, len(instruments))
	for _, i := range instruments {
		symbol, _ := quoteSymbol(i)
		out[i.ID] = QuoteResult{Quote: quotes[symbol]}
	}
	return out
}

type savedStockFX map[FXRequest]FXQuote

func (rates savedStockFX) Fetch(_ context.Context, r FXRequest) (FXQuote, error) {
	if q, ok := rates[r]; ok {
		return q, nil
	}
	return FXQuote{}, ErrFXUnavailable
}

func stockFXKey(base, quote Currency) string { return "fx:" + string(base) + "/" + string(quote) }

// One SQLite snapshot for all requested prices and rates. Holdings GETs use
// the live journal for quantities, cash and versions, never a cached account.
func (s *Store) readStockPrices(ctx context.Context, instruments []Instrument, currency Currency) (savedStockQuotes, savedStockFX, error) {
	quotes, rates := savedStockQuotes{}, savedStockFX{}
	var keys []string
	for _, i := range instruments {
		if symbol, code := quoteSymbol(i); code == "" {
			keys = append(keys, symbol)
			if i.Currency != currency {
				keys = append(keys, stockFXKey(i.Currency, currency))
			}
		}
	}
	if len(keys) == 0 {
		return quotes, rates, nil
	}
	arg, _ := json.Marshal(keys)
	rows, err := s.db.QueryContext(ctx, `SELECT symbol,payload FROM stock_market_marks WHERE symbol IN (SELECT value FROM json_each(?))`, string(arg))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, payload string
		if err := rows.Scan(&key, &payload); err != nil {
			return nil, nil, err
		}
		if strings.HasPrefix(key, "fx:") {
			var q FXQuote
			if json.Unmarshal([]byte(payload), &q) != nil || stockFXKey(q.Base, q.Quote) != key {
				return nil, nil, ErrCorrupt
			}
			rates[FXRequest{Base: q.Base, Quote: q.Quote, Mode: "latest"}] = q
		} else {
			var q Quote
			if json.Unmarshal([]byte(payload), &q) != nil || q.Symbol != key {
				return nil, nil, ErrCorrupt
			}
			quotes[key] = &q
		}
	}
	return quotes, rates, rows.Err()
}

func (s *Store) saveStockPrices(ctx context.Context, quotes savedStockQuotes, rates savedStockFX) error {
	if len(quotes)+len(rates) == 0 {
		return nil
	}
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		put := func(key, asOf string, v any) error {
			t, err := time.Parse(time.RFC3339Nano, asOf)
			if err != nil {
				return err
			}
			b, err := json.Marshal(v)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO stock_market_marks(symbol,quoted_ms,payload) VALUES(?,?,?)
				ON CONFLICT(symbol) DO UPDATE SET quoted_ms=excluded.quoted_ms,payload=excluded.payload
				WHERE excluded.quoted_ms>=stock_market_marks.quoted_ms`, key, t.UnixMilli(), string(b))
			return err
		}
		for key, q := range quotes {
			if err := put(key, q.QuotedAt, q); err != nil {
				return err
			}
		}
		for r, q := range rates {
			if err := put(stockFXKey(r.Base, r.Quote), q.QuotedAt, q); err != nil {
				return err
			}
		}
		return nil
	})
}
