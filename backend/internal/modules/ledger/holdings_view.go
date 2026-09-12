package ledger

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

type HoldingItem struct {
	Instrument         instrumentJSON `json:"instrument"`
	Quantity           Quantity       `json:"quantity"`
	MarketValue        *Money         `json:"market_value"`
	AccountMarketValue *Money         `json:"account_market_value"`
	Price              *Price         `json:"price"`
	HoldingCost        *Price         `json:"holding_cost"`
	DilutedCost        *Price         `json:"diluted_cost"`
	Weight             *string        `json:"weight"`
	CostStatus         string         `json:"cost_status"`
	CycleID            string         `json:"cycle_id"`
	QuoteStatus        string         `json:"quote_status"`
	Quote              *Quote         `json:"quote"`
	FX                 *FXQuote       `json:"fx"`
}

type HoldingsView struct {
	AccountID      string        `json:"account_id"`
	Currency       Currency      `json:"currency"`
	Source         string        `json:"source"`
	AsOf           string        `json:"as_of"`
	LedgerAt       string        `json:"ledger_at"`
	Revision       string        `json:"revision"`
	ManualVersion  *string       `json:"manual_version"`
	TradeDateFloor *string       `json:"trade_date_floor"`
	Configured     bool          `json:"configured"`
	Cash           *Money        `json:"cash"`
	Complete       bool          `json:"complete"`
	TotalAssets    *Money        `json:"total_assets"`
	Items          []HoldingItem `json:"items"`
}

// Capture all financial inputs in one SQLite transaction. Quotes are fetched
// afterwards without any database lock or persistence of the observation.
func (s *Store) holdingsInputs(ctx context.Context, id string) (HoldingsView, []Instrument, error) {
	out := HoldingsView{AccountID: id, Source: "transaction_replay", Items: []HoldingItem{}}
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
		var costs map[string]HoldingCostBasis
		if info.AccountingMode == "reported" {
			out.Source = "manual_snapshot"
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
			manual := manualBasis(id, *current.Snapshot)
			costs = manual.Cycles
			out.TradeDateFloor = &manual.FloorDate
		} else {
			openings, is, records, err := loadLedger(ctx, tx)
			if err != nil {
				return err
			}
			book, err := replayRecords(openings, is, records, out.AsOf)
			if err != nil {
				return err
			}
			state := book.Accounts[id]
			if state == nil || state.Currency != info.Currency {
				return ErrCorrupt
			}
			out.Configured = true
			out.Cash = copyMoney(&state.Cash)
			basis, err := json.Marshal(struct {
				Openings    []Opening
				Instruments []Instrument
				Records     []Record
			}{openings, is, records})
			if err != nil {
				return err
			}
			out.Revision = receiptDigest(string(basis))
			costs, _, err = replayHoldingCosts(id, openings, records)
			if err != nil {
				return err
			}
			if len(costs) != len(state.Positions) {
				return ErrCorrupt
			}
			for instrument, c := range costs {
				original := state.Cycles[state.Positions[instrument]]
				if original == nil || original.ID != c.CycleID || original.Quantity != c.Quantity {
					return ErrCorrupt
				}
			}
		}
		ids := make([]string, 0, len(costs))
		for id := range costs {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, instrument := range ids {
			i, err := holdingInstrument(ctx, tx, instrument)
			if err != nil {
				return err
			}
			c := costs[instrument]
			item := HoldingItem{Instrument: instrumentJSON{ID: i.ID, Market: i.Market, Code: i.Code, Name: i.Name, Currency: i.Currency}, Quantity: c.Quantity, CycleID: c.CycleID, CostStatus: "unknown", QuoteStatus: "unavailable"}
			if c.Quantity == 0 {
				item.CostStatus = "closed"
			} else if c.Known {
				holding, err := UnitCost(c.Spent, c.Bought)
				if err != nil {
					return err
				}
				diluted, err := UnitCost(c.Basis, c.Quantity)
				if err != nil {
					return err
				}
				item.HoldingCost, item.DilutedCost, item.CostStatus = &holding, &diluted, "known"
			}
			out.Items = append(out.Items, item)
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
		if item.Quantity == 0 {
			zero := Money(0)
			item.MarketValue = &zero
		} else if q.Quote != nil {
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

// Opaque scoped cursors prevent accidental cross-account/instrument reuse.
// An anchor must still exist: replay edits/voids cannot silently change its order.
func tradeCursor(account, instrument string, r HoldingTransaction) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join([]string{account, instrument, r.Source, r.Date, strconv.FormatInt(r.sequence, 10), r.ID}, "|")))
}

func (s *Store) HoldingTransactions(ctx context.Context, account, instrument string, limit int, cursor string) (listJSON[HoldingTransaction], error) {
	out := listJSON[HoldingTransaction]{Items: []HoldingTransaction{}}
	if !validID(account) || !validID(instrument) || limit < 1 || limit > 100 || len(cursor) > 1024 {
		return out, ErrQuery
	}
	var anchor []string
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return out, ErrQuery
		}
		anchor = strings.Split(string(decoded), "|")
		if len(anchor) != 6 || anchor[0] != account || anchor[1] != instrument || !validDate(anchor[3]) || !validID(anchor[5]) {
			return out, ErrQuery
		}
		n, err := positiveInteger(anchor[4])
		if err != nil || strconv.FormatInt(n, 10) != anchor[4] {
			return out, ErrQuery
		}
	}
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		info, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, account))
		if err != nil {
			return err
		}
		if _, err := holdingInstrument(ctx, tx, instrument); err != nil {
			return err
		}
		source := "operation"
		if info.AccountingMode == "reported" {
			source = "manual"
		}
		if len(anchor) > 0 && anchor[2] != source {
			return ErrQuery
		}
		if source == "manual" {
			if _, err := readCurrentHoldings(ctx, tx, account); err != nil {
				return err
			}
			beforeDate, beforeVersion := "9999-12-31", int64(9223372036854775807)
			if len(anchor) > 0 {
				v, _ := positiveInteger(anchor[4])
				a, err := readManualTrade(ctx, tx, account, v)
				if err != nil || a.InstrumentID != instrument || a.Transaction.Date != anchor[3] || a.Transaction.ID != anchor[5] {
					return ErrQuery
				}
				beforeDate, beforeVersion = anchor[3], v
			}
			rows, err := tx.QueryContext(ctx, `SELECT version FROM manual_trades WHERE account_id=? AND instrument_id=? AND (business_date<? OR business_date=? AND version<?) ORDER BY business_date DESC,version DESC LIMIT ?`, account, instrument, beforeDate, beforeDate, beforeVersion, limit+1)
			if err != nil {
				return err
			}
			versions := []int64{}
			for rows.Next() {
				var v int64
				if err := rows.Scan(&v); err != nil {
					rows.Close()
					return err
				}
				versions = append(versions, v)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, v := range versions {
				r, err := readManualTrade(ctx, tx, account, v)
				if err != nil {
					return err
				}
				out.Items = append(out.Items, r.Transaction)
			}
		} else {
			date, _, err := s.cutoff()
			if err != nil {
				return err
			}
			openings, is, records, err := loadLedger(ctx, tx)
			if err != nil {
				return err
			}
			if _, err := replayRecords(openings, is, records, date); err != nil {
				return err
			}
			_, all, err := replayHoldingCosts(account, openings, records)
			if err != nil {
				return err
			}
			found := len(anchor) == 0
			for n := len(all) - 1; n >= 0; n-- {
				r := all[n]
				if r.instrumentID != instrument {
					continue
				}
				if !found {
					if tradeCursor(account, instrument, r) == cursor {
						found = true
					}
					continue
				}
				out.Items = append(out.Items, r)
				if len(out.Items) > limit {
					break
				}
			}
			if !found {
				return ErrQuery
			}
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			out.NextCursor = tradeCursor(account, instrument, out.Items[limit-1])
		}
		return nil
	})
	return out, err
}

func (h Handler) holdingTransactions(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD", "POST") {
		return
	}
	if r.Method == "POST" {
		key, ok := writeKey(w, r)
		if !ok {
			return
		}
		input, ok := body[ManualTradeInput](w, r)
		if !ok {
			return
		}
		out, err := h.Store.AddManualTrade(r.Context(), r.PathValue("id"), r.PathValue("instrumentID"), key, input)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		httpapi.Write(w, http.StatusCreated, out)
		return
	}
	values, limit, err := page(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if values.Get("limit") == "" {
		limit = 20
	}
	out, err := h.Store.HoldingTransactions(r.Context(), r.PathValue("id"), r.PathValue("instrumentID"), limit, values.Get("cursor"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, out)
}
