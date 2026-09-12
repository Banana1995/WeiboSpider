package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

type HoldingItem struct {
	Instrument         instrumentJSON `json:"instrument"`
	Quantity           Quantity       `json:"quantity"`
	MarketValue        *Money         `json:"market_value"`
	AccountMarketValue *Money         `json:"account_market_value"`
	Price              *Price         `json:"price"`
	Weight             *string        `json:"weight"`
	QuoteStatus        string         `json:"quote_status"`
	Quote              *Quote         `json:"quote"`
	FX                 *FXQuote       `json:"fx"`
}

type HoldingsView struct {
	AccountID     string        `json:"account_id"`
	Currency      Currency      `json:"currency"`
	Source        string        `json:"source"`
	AsOf          string        `json:"as_of"`
	LedgerAt      string        `json:"ledger_at"`
	Revision      string        `json:"revision"`
	ManualVersion *string       `json:"manual_version"`
	Configured    bool          `json:"configured"`
	Cash          *Money        `json:"cash"`
	Complete      bool          `json:"complete"`
	TotalAssets   *Money        `json:"total_assets"`
	Items         []HoldingItem `json:"items"`
}

// Read account-local inputs in one snapshot; provider I/O happens after release.
func (s *Store) holdingsInputs(ctx context.Context, id string) (HoldingsView, []Instrument, error) {
	out := HoldingsView{AccountID: id, Source: "manual_snapshot", Items: []HoldingItem{}}
	var instruments []Instrument
	if !validID(id) {
		return out, nil, ErrQuery
	}
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		info, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		out.Currency = info.Currency
		out.AsOf, out.LedgerAt, err = s.cutoff()
		if err != nil {
			return err
		}
		version := "0"
		out.ManualVersion = &version
		current, err := readCurrentHoldings(ctx, tx, id)
		if err != nil {
			return err
		}
		basis, err := json.Marshal(current)
		if err != nil {
			return err
		}
		out.Revision = receiptDigest(string(basis))
		if current.Snapshot == nil {
			return nil
		}
		out.Configured = true
		out.Cash = copyMoney(&current.Snapshot.Cash)
		version = current.Snapshot.Version
		for _, p := range current.Snapshot.Positions {
			i, err := holdingInstrument(ctx, tx, p.InstrumentID)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, HoldingItem{Instrument: instrumentJSON{i.ID, i.Market, i.Code, i.Name, i.Currency}, Quantity: p.Quantity, QuoteStatus: "unavailable"})
			instruments = append(instruments, i)
		}
		return nil
	})
	return out, instruments, err
}

func (h Handler) valueHoldings(ctx context.Context, out *HoldingsView, instruments []Instrument) error {
	if !out.Configured {
		return nil
	}
	v := Valuation{Currency: out.Currency, AsOf: out.AsOf, Cash: *out.Cash, Complete: true, Items: make([]ValuationItem, len(out.Items))}
	for n, item := range out.Items {
		v.Items[n] = ValuationItem{InstrumentID: item.Instrument.ID, Quantity: item.Quantity, Status: "unavailable"}
	}
	if err := h.valuePositions(ctx, &v, instruments); err != nil {
		return err
	}
	out.Complete, out.TotalAssets = v.Complete, v.TotalAssets
	for n, q := range v.Items {
		item := &out.Items[n]
		item.Quote, item.FX, item.QuoteStatus, item.AccountMarketValue = q.Quote, q.FX, q.Status, q.MarketValue
		if q.Quote != nil {
			item.Price = &q.Quote.Price
			amount, err := TradeAmount(item.Quantity, q.Quote.Price)
			if err != nil {
				return err
			}
			item.MarketValue = &amount
		}
		if v.Complete && v.TotalAssets != nil && *v.TotalAssets > 0 && q.MarketValue != nil {
			weight, err := roundedProduct(int64(*q.MarketValue), 10000, int64(*v.TotalAssets))
			if err != nil {
				return err
			}
			text := formatFixed(weight, 2)
			item.Weight = &text
		}
	}
	return nil
}

func (h Handler) holdings(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), valuationTimeout)
	defer cancel()
	out, instruments, err := h.Store.holdingsInputs(ctx, r.PathValue("id"))
	if err == nil {
		err = h.valueHoldings(ctx, &out, instruments)
	}
	if r.Context().Err() != nil {
		err = r.Context().Err()
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, out)
}
