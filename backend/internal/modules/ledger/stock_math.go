package ledger

import (
	"errors"
	"math/big"
	"slices"
	"strconv"
)

var (
	ErrStockQuantity = errors.New("sell exceeds historical holdings")
	ErrStockCash     = errors.New("insufficient stock cash")
	ErrStockManaged  = errors.New("positions are managed by the stock journal")
)

type StockEntry struct {
	ID           string         `json:"id"`
	Version      string         `json:"version"`
	Sequence     int64          `json:"sequence,string"`
	InstrumentID string         `json:"instrument_id"`
	Kind         string         `json:"kind"`
	Date         string         `json:"date"`
	Quantity     Quantity       `json:"quantity"`
	Price        Price          `json:"price"`
	Fee          Money          `json:"fee"`
	Amount       Money          `json:"amount"`
	FX           Rate           `json:"fx"`
	Note         string         `json:"note"`
	Voided       bool           `json:"voided"`
	Event        *DividendEvent `json:"event,omitempty"`
	Override     bool           `json:"override,omitempty"`
	Opening      bool           `json:"opening,omitempty"`
	SettlementFX *FXQuote       `json:"settlement_fx,omitempty"`
	Cycle        string         `json:"cycle,omitempty"`
	CreatedAt    string         `json:"created_at"`
	UpdatedAt    string         `json:"updated_at"`
}

type CashAnchor struct {
	Amount   Money  `json:"amount"`
	Date     string `json:"date"`
	Sequence int64  `json:"sequence,string"`
}
type StockJournal struct {
	Version  string            `json:"version"`
	Openings []CurrentPosition `json:"openings"`
	Cash     *CashAnchor       `json:"cash,omitempty"`
}
type StockMetrics struct {
	Cost            *Price  `json:"cost"`
	DilutedCost     *Price  `json:"diluted_cost"`
	Profit          *Money  `json:"profit"`
	ProfitRate      *string `json:"profit_rate"`
	TotalProfit     *Money  `json:"total_profit"`
	TotalRate       *string `json:"total_rate"`
	Dividends       Money   `json:"dividends"`
	PendingDividend Money   `json:"pending_dividend"`
}
type stockPosition struct {
	id                                    string
	quantity                              Quantity
	cycle                                 string
	known, allKnown                       bool
	buys, sells, dividends                Money
	cycleBuys, cycleSells, cycleDividends Money
	bought                                Quantity
	pending                               Money
}

func addQuantity(a, b Quantity) (Quantity, error) {
	v, err := AddMoney(Money(a), Money(b))
	return Quantity(v), err
}
func entryLess(a, b StockEntry) int {
	if a.Date != b.Date {
		if a.Date < b.Date {
			return -1
		}
		return 1
	}
	// Entitlements are fixed before trading on the ex-dividend date.
	if (a.Event != nil) != (b.Event != nil) {
		if a.Event != nil {
			return -1
		}
		return 1
	}
	if a.Sequence < b.Sequence {
		return -1
	}
	if a.Sequence > b.Sequence {
		return 1
	}
	return 0
}
func replayStocks(j StockJournal, entries []StockEntry, today string) (map[string]*stockPosition, error) {
	positions := map[string]*stockPosition{}
	for _, p := range j.Openings {
		positions[p.InstrumentID] = &stockPosition{id: p.InstrumentID, quantity: p.Quantity, cycle: "opening-" + p.InstrumentID}
	}
	ordered := slices.Clone(entries)
	slices.SortFunc(ordered, entryLess)
	for _, e := range ordered {
		p := positions[e.InstrumentID]
		if p == nil {
			p = &stockPosition{id: e.InstrumentID, known: true, allKnown: true}
			positions[e.InstrumentID] = p
		}
		if e.Voided || e.Date > today {
			continue
		}
		var err error
		switch e.Kind {
		case "buy":
			if p.quantity == 0 {
				p.cycle = e.ID
				p.known = true
				p.cycleBuys = 0
				p.cycleSells = 0
				p.cycleDividends = 0
				p.bought = 0
			}
			p.quantity, err = addQuantity(p.quantity, e.Quantity)
			if err != nil {
				return nil, err
			}
			p.bought, err = addQuantity(p.bought, e.Quantity)
			if err != nil {
				return nil, err
			}
			p.buys, err = AddMoney(p.buys, e.Amount)
			if err != nil {
				return nil, err
			}
			p.cycleBuys, err = AddMoney(p.cycleBuys, e.Amount)
		case "sell":
			if e.Quantity > p.quantity {
				return nil, ErrStockQuantity
			}
			p.quantity -= e.Quantity
			p.sells, err = AddMoney(p.sells, e.Amount)
			if err != nil {
				return nil, err
			}
			p.cycleSells, err = AddMoney(p.cycleSells, e.Amount)
		case "dividend":
			p.dividends, err = AddMoney(p.dividends, e.Amount)
			if err != nil {
				return nil, err
			}
			if e.Event == nil || e.Cycle == p.cycle {
				p.cycleDividends, err = AddMoney(p.cycleDividends, e.Amount)
				if err != nil {
					return nil, err
				}
			}
			if e.Event != nil && (e.Event.PayDate > today || e.FX == 0 && j.Cash != nil && e.Event.PayDate > j.Cash.Date) {
				p.pending, err = AddMoney(p.pending, e.Amount)
			}
		default:
			return nil, ErrCorrupt
		}
		if err != nil {
			return nil, err
		}
	}
	return positions, nil
}

// The user's last explicit cash amount is the anchor, never inferred from a zero.
// Trades already covered by that dated balance do not get charged a second time.
func stockCash(j StockJournal, entries []StockEntry, today string) (Money, error) {
	if j.Cash == nil {
		return 0, nil
	}
	type flow struct {
		date     string
		sequence int64
		amount   Money
	}
	flows := []flow{}
	for _, e := range entries {
		if e.Voided || e.Opening {
			continue
		}
		date := e.Date
		if e.Event != nil {
			date = e.Event.PayDate
		}
		if date > today || date < j.Cash.Date || date == j.Cash.Date && (e.Event != nil || e.Sequence <= j.Cash.Sequence) {
			continue
		}
		if e.FX == 0 {
			if e.Event != nil {
				continue
			}
			return 0, ErrFXUnavailable
		}
		amount, err := ConvertMoney(e.Amount, e.FX)
		if err != nil {
			return 0, err
		}
		if e.Kind == "buy" {
			amount = -amount
		}
		flows = append(flows, flow{date, e.Sequence, amount})
	}
	slices.SortFunc(flows, func(a, b flow) int {
		if a.date < b.date {
			return -1
		}
		if a.date > b.date {
			return 1
		}
		if a.sequence < b.sequence {
			return -1
		}
		if a.sequence > b.sequence {
			return 1
		}
		return 0
	})
	cash := j.Cash.Amount
	for _, f := range flows {
		var err error
		cash, err = AddMoney(cash, f.amount)
		if err != nil {
			return 0, err
		}
		if cash < 0 {
			return 0, ErrStockCash
		}
	}
	return cash, nil
}

func stockPercent(n, d Money) *string {
	if d <= 0 {
		return nil
	}
	s := new(big.Rat).Mul(new(big.Rat).SetFrac(big.NewInt(int64(n)), big.NewInt(int64(d))), big.NewRat(100, 1)).FloatString(2)
	if s == "-0.00" {
		s = "0.00"
	}
	return &s
}
func metricsFor(p *stockPosition, market *Money) (StockMetrics, error) {
	m := StockMetrics{Dividends: p.dividends, PendingDividend: p.pending}
	if p.known && p.quantity > 0 && p.bought > 0 {
		cost, err := UnitCost(p.cycleBuys, p.bought)
		if err != nil {
			return m, err
		}
		m.Cost = &cost
		net, err := SubMoney(p.cycleBuys, p.cycleSells)
		if err != nil {
			return m, err
		}
		net, err = SubMoney(net, p.cycleDividends)
		if err != nil {
			return m, err
		}
		diluted, err := UnitCost(net, p.quantity)
		if err != nil {
			return m, err
		}
		m.DilutedCost = &diluted
		if market != nil {
			// Round the amount once, not the displayed unit cost times the quantity.
			basis, err := roundedProduct(int64(p.cycleBuys), int64(p.quantity), int64(p.bought))
			if err != nil {
				return m, err
			}
			profit, err := SubMoney(*market, Money(basis))
			if err != nil {
				return m, err
			}
			m.Profit = &profit
			m.ProfitRate = stockPercent(profit, Money(basis))
		}
	}
	if p.quantity == 0 {
		zero := Money(0)
		market = &zero
	}
	if p.allKnown && market != nil {
		profit, err := SubMoney(*market, p.buys)
		if err != nil {
			return m, err
		}
		profit, err = AddMoney(profit, p.sells)
		if err != nil {
			return m, err
		}
		profit, err = AddMoney(profit, p.dividends)
		if err != nil {
			return m, err
		}
		m.TotalProfit = &profit
		m.TotalRate = stockPercent(profit, p.buys)
	}
	return m, nil
}
func stockVersion(v string) (int64, error) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 || n == 9223372036854775807 || strconv.FormatInt(n, 10) != v {
		return 0, ErrOperation
	}
	return n, nil
}
