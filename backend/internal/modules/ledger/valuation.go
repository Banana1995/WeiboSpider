package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

type ValuationItem struct {
	InstrumentID string   `json:"instrument_id"`
	Quantity     Quantity `json:"quantity"`
	Quote        *Quote   `json:"quote"`
	FX           *FXQuote `json:"fx"`
	MarketValue  *Money   `json:"market_value"`
	Status       string   `json:"status"`
	ErrorCode    string   `json:"error_code,omitempty"`
}

type Valuation struct {
	Source              string           `json:"source"`
	CurrentHoldings     *CurrentHoldings `json:"current_holdings,omitempty"`
	weekly              bool
	correlation         string
	changeRevision      *int64
	LedgerRevision      string `json:"ledger_revision"`
	HistoryID           string `json:"history_id,omitempty"`
	accountName         string
	AccountID           string          `json:"account_id"`
	Currency            Currency        `json:"currency"`
	AsOf                string          `json:"as_of"`
	LedgerAt            string          `json:"ledger_at"`
	CalculatedAt        string          `json:"calculated_at"`
	Cash                Money           `json:"cash"`
	KnownPositionsValue Money           `json:"known_positions_value"`
	PositionsValue      *Money          `json:"positions_value"`
	TotalAssets         *Money          `json:"total_assets"`
	Complete            bool            `json:"complete"`
	Items               []ValuationItem `json:"items"`
}

// valuationInputs captures metadata, cash, quantities and instrument identities
// in one SQLite snapshot. Network I/O must only start after this returns.
// ledger_at identifies this read, not a promise of freshness after concurrent writes.
func (s *Store) valuationInputs(ctx context.Context, id string) (Valuation, []Instrument, error) {
	result := Valuation{AccountID: id, Source: "manual_snapshot", Complete: true, Items: make([]ValuationItem, 0)}
	if !validID(id) {
		return result, nil, ErrQuery
	}
	var held []Instrument
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		info, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		result.AsOf, result.LedgerAt, err = s.cutoff()
		if err != nil {
			return err
		}
		current, err := readCurrentHoldings(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Snapshot == nil {
			return ErrUnsupported
		}
		result.Source, result.CurrentHoldings = "manual_snapshot", &current
		result.accountName, result.Currency, result.Cash = info.Name, info.Currency, current.Snapshot.Cash
		basis, err := json.Marshal(current)
		if err != nil {
			return err
		}
		result.LedgerRevision = receiptDigest(string(basis))
		revision, err := positiveInteger(current.AuditID)
		if err != nil {
			return ErrCorrupt
		}
		result.changeRevision = &revision
		for _, p := range current.Snapshot.Positions {
			var i Instrument
			if err := tx.QueryRowContext(ctx, `SELECT id,market,code,name,currency FROM instruments WHERE id=?`, p.InstrumentID).Scan(&i.ID, &i.Market, &i.Code, &i.Name, &i.Currency); err != nil {
				return ErrCorrupt
			}
			result.Items = append(result.Items, ValuationItem{InstrumentID: p.InstrumentID, Quantity: p.Quantity, Status: "unavailable"})
			held = append(held, i)
		}
		return nil
	})
	return result, held, err
}

func (h Handler) valuation(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	// Current only, including rejection of empty query delimiters and parameters.
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), valuationTimeout)
	defer cancel()
	result, instruments, err := h.Store.valuationInputs(ctx, r.PathValue("id"))
	if err == nil {
		err = h.valuePositions(ctx, &result, instruments)
	}
	if r.Context().Err() != nil {
		err = r.Context().Err()
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, http.StatusOK, result)
}

func (h Handler) valuePositions(ctx context.Context, result *Valuation, instruments []Instrument) error {
	return valuePositions(ctx, result, instruments, h.Quotes, h.FX, h.Now)
}

func valuePositions(ctx context.Context, result *Valuation, instruments []Instrument, provider QuotesProvider, fxProvider FXProvider, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	requested := make([]Instrument, 0, len(instruments))
	for n, i := range instruments {
		item := &result.Items[n]
		if item.Quantity == 0 {
			zero := Money(0)
			item.MarketValue, item.Status = &zero, "closed"
			continue
		}
		_, item.ErrorCode = quoteSymbol(i)
		if item.ErrorCode == "" {
			requested = append(requested, i)
		}
	}
	quotes := map[string]QuoteResult{}
	if len(requested) > 0 && provider != nil {
		quotes = provider.Fetch(ctx, requested)
	}
	type fxResult struct {
		quote FXQuote
		err   error
	}
	fxs := make(map[Currency]fxResult)
	for n, i := range instruments {
		item := &result.Items[n]
		if item.Status == "closed" {
			continue
		}
		if item.ErrorCode == "" {
			row := quotes[i.ID]
			item.ErrorCode = row.ErrorCode
			if row.Quote == nil || item.ErrorCode != "" {
				if item.ErrorCode == "" {
					item.ErrorCode = "quote_unavailable"
					if errors.Is(ctx.Err(), context.DeadlineExceeded) {
						item.ErrorCode = "quote_timeout"
					}
				}
			} else {
				q := row.Quote
				symbol, _ := quoteSymbol(i)
				stamp, stampErr := time.Parse(time.RFC3339Nano, q.QuotedAt)
				_, fetchedErr := time.Parse(time.RFC3339Nano, q.FetchedAt)
				if q.Currency != i.Currency {
					item.ErrorCode = "currency_mismatch"
				} else if q.Symbol != symbol || q.Price <= 0 || q.Source != "Tencent" || !validDate(q.Date) || q.Date > result.AsOf || stampErr != nil || fetchedErr != nil || stamp.After(now()) || stamp.In(fxBeijing).Format(time.DateOnly) != q.Date {
					item.ErrorCode = "quote_unavailable"
				} else {
					item.Quote = q
				}
			}
		}
		if item.ErrorCode != "" {
			result.Complete = false
			continue
		}
		amount, err := TradeAmount(item.Quantity, item.Quote.Price)
		if err != nil {
			return err
		}
		if i.Currency != result.Currency {
			fx, exists := fxs[i.Currency]
			if !exists {
				fx.err = ErrFXUnavailable
				if fxProvider != nil {
					fxCtx, cancel := context.WithTimeout(ctx, quoteNetworkTimeout)
					fx.quote, fx.err = fxProvider.Fetch(fxCtx, FXRequest{Base: i.Currency, Quote: result.Currency, Mode: "latest"})
					cancel()
				}
				q := fx.quote
				stamp, stampErr := time.Parse(time.RFC3339Nano, q.QuotedAt)
				_, fetchedErr := time.Parse(time.RFC3339Nano, q.FetchedAt)
				if fx.err == nil && (q.Base != i.Currency || q.Quote != result.Currency || q.Mode != "latest" || q.Rate <= 0 || q.Source == "" || !validDate(q.Date) || q.Date > result.AsOf || stampErr != nil || fetchedErr != nil || stamp.After(now()) || q.Date > stamp.In(fxBeijing).Format(time.DateOnly)) {
					fx.err = ErrFXUnavailable
				}
				fxs[i.Currency] = fx
			}
			if fx.err != nil {
				item.ErrorCode = "fx_unavailable"
				if errors.Is(fx.err, ErrFXTimeout) || errors.Is(fx.err, context.DeadlineExceeded) {
					item.ErrorCode = "fx_timeout"
				}
				result.Complete = false
				continue
			}
			item.FX = &fx.quote
			amount, err = ConvertMoney(amount, fx.quote.Rate)
			if err != nil {
				return err
			}
		}
		item.MarketValue, item.Status = &amount, "current"
		if item.Quote.Date != result.AsOf || item.FX != nil && item.FX.Date != result.AsOf {
			item.Status = "prior_date"
		}
		result.KnownPositionsValue, err = AddMoney(result.KnownPositionsValue, amount)
		if err != nil {
			return err
		}
	}
	if result.Complete {
		total, err := AddMoney(result.Cash, result.KnownPositionsValue)
		if err != nil {
			return err
		}
		result.PositionsValue, result.TotalAssets = copyMoney(&result.KnownPositionsValue), &total
	}
	result.CalculatedAt = now().UTC().Format(time.RFC3339Nano)
	return nil
}
