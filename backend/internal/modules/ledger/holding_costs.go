package ledger

import (
	"math"
	"slices"
	"strconv"
	"time"
)

// HoldingCostBasis is deliberately independent of replay's account-currency
// remaining cost. All money here is in the immutable instrument currency.
type HoldingCostBasis struct {
	CycleID  string   `json:"cycle_id"`
	Quantity Quantity `json:"quantity"`
	Known    bool     `json:"known"`
	Bought   Quantity `json:"bought"`
	Spent    Money    `json:"spent"`
	Basis    Money    `json:"basis"`
}

type ManualTradeBasis struct {
	FloorDate   string                      `json:"floor_date"`
	LastVersion string                      `json:"last_version"`
	Cycles      map[string]HoldingCostBasis `json:"cycles"`
}

func (m ManualTradeBasis) valid(p HoldingsSnapshot) bool {
	stamp, err := time.Parse(time.RFC3339Nano, p.SavedAt)
	if err != nil || !validDate(m.FloorDate) || m.FloorDate > stamp.In(fxBeijing).Format(time.DateOnly) || m.Cycles == nil {
		return false
	}
	if m.LastVersion != "" {
		v, err := positiveInteger(m.LastVersion)
		current, _ := positiveInteger(p.Version)
		if err != nil || v > current || strconv.FormatInt(v, 10) != m.LastVersion {
			return false
		}
	}
	positions := make(map[string]Quantity)
	for _, p := range p.Positions {
		positions[p.InstrumentID] = p.Quantity
	}
	for id, c := range m.Cycles {
		if !validID(id) || c.CycleID == "" || c.Quantity < 0 || c.Quantity != positions[id] || c.Bought < 0 || c.Spent < 0 || c.Known && (c.Bought <= 0 || c.Bought < c.Quantity) {
			return false
		}
		delete(positions, id)
	}
	return len(positions) == 0
}

func manualBasis(id string, p HoldingsSnapshot) ManualTradeBasis {
	if p.Trades != nil {
		out := *p.Trades
		out.Cycles = make(map[string]HoldingCostBasis, len(p.Trades.Cycles))
		for k, v := range p.Trades.Cycles {
			out.Cycles[k] = v
		}
		return out
	}
	stamp, _ := time.Parse(time.RFC3339Nano, p.SavedAt)
	out := ManualTradeBasis{FloorDate: stamp.In(fxBeijing).Format(time.DateOnly), Cycles: make(map[string]HoldingCostBasis)}
	for _, row := range p.Positions {
		out.Cycles[row.InstrumentID] = HoldingCostBasis{CycleID: "manual:" + id + ":" + row.InstrumentID + ":" + p.Version, Quantity: row.Quantity}
	}
	return out
}

func resetManualBasis(id string, old, next HoldingsSnapshot, date string) *ManualTradeBasis {
	m := manualBasis(id, old)
	m.FloorDate = date
	positions := make(map[string]Quantity)
	for _, p := range next.Positions {
		positions[p.InstrumentID] = p.Quantity
	}
	for instrument, c := range m.Cycles {
		q := positions[instrument]
		if q != c.Quantity {
			if q > 0 && c.Quantity > 0 {
				// A quantity correction invalidates cost evidence, not the ongoing
				// holding period. Only a zero boundary establishes a new cycle.
				m.Cycles[instrument] = HoldingCostBasis{CycleID: c.CycleID, Quantity: q}
			} else {
				delete(m.Cycles, instrument)
			}
		}
	}
	for instrument, q := range positions {
		if _, ok := m.Cycles[instrument]; !ok {
			m.Cycles[instrument] = HoldingCostBasis{CycleID: "manual:" + id + ":" + instrument + ":" + next.Version, Quantity: q}
		}
	}
	return &m
}

type HoldingTransaction struct {
	ID           string    `json:"id"`
	Kind         Kind      `json:"kind"`
	Date         string    `json:"date"`
	Quantity     *Quantity `json:"quantity"`
	Price        *Price    `json:"price"`
	Amount       Money     `json:"amount"`
	Fee          *Money    `json:"fee"`
	Note         string    `json:"note"`
	CycleID      string    `json:"cycle_id"`
	Source       string    `json:"source"`
	OperationID  string    `json:"operation_id,omitempty"`
	sequence     int64
	instrumentID string
}

func holdingTransaction(o Operation, note, source string) (HoldingTransaction, error) {
	r := HoldingTransaction{ID: o.ID, Kind: o.Kind, Date: o.Date, Fee: o.Fee, Note: note, CycleID: o.CycleID, Source: source, sequence: o.Sequence, instrumentID: o.InstrumentID}
	if source == "operation" {
		r.OperationID = o.ID
	}
	if r.Kind == DepositBuy {
		r.Kind = Buy
	}
	if r.Kind == SellWithdraw {
		r.Kind = Sell
	}
	if r.Kind == Dividend {
		r.Amount = o.Amount
		return r, nil
	}
	if r.Kind != Buy && r.Kind != Sell {
		return r, ErrOperation
	}
	r.Quantity, r.Price = &o.Quantity, &o.Price
	gross, err := TradeAmount(o.Quantity, o.Price)
	if err != nil {
		return r, err
	}
	fee := Money(0)
	if o.Fee != nil {
		fee = *o.Fee
	}
	if r.Kind == Buy {
		r.Amount, err = AddMoney(gross, fee)
	} else {
		r.Amount, err = SubMoney(gross, fee)
	}
	return r, err
}

func (c *HoldingCostBasis) apply(r HoldingTransaction) error {
	var err error
	switch r.Kind {
	case Buy:
		if r.Quantity == nil || *r.Quantity <= 0 || c.Quantity > Quantity(math.MaxInt64)-*r.Quantity || c.Bought > Quantity(math.MaxInt64)-*r.Quantity {
			return ErrPrecision
		}
		c.Quantity += *r.Quantity
		c.Bought += *r.Quantity
		c.Spent, err = AddMoney(c.Spent, r.Amount)
		if err == nil {
			c.Basis, err = AddMoney(c.Basis, r.Amount)
		}
	case Sell:
		if r.Quantity == nil || *r.Quantity <= 0 || c.Quantity < *r.Quantity {
			return ErrInsufficientStock
		}
		c.Quantity -= *r.Quantity
		c.Basis, err = SubMoney(c.Basis, r.Amount)
	case Dividend:
		c.Basis, err = SubMoney(c.Basis, r.Amount)
	default:
		return ErrOperation
	}
	return err
}

// The caller has already validated the complete ledger with replayRecords.
// Opening account-currency costs never establish an original-currency buy basis.
func replayHoldingCosts(id string, openings []Opening, records []Record) (map[string]HoldingCostBasis, []HoldingTransaction, error) {
	cycles := make(map[string]HoldingCostBasis)
	latest := make(map[string]string)
	for _, o := range openings {
		if o.AccountID != id {
			continue
		}
		for _, p := range o.Positions {
			cid := OpeningCycleID(id, p.InstrumentID)
			cycles[cid] = HoldingCostBasis{CycleID: cid, Quantity: p.Quantity}
			latest[p.InstrumentID] = cid
		}
	}
	ordered := slices.Clone(records)
	slices.SortFunc(ordered, func(a, b Record) int {
		if a.Operation.Date < b.Operation.Date {
			return -1
		}
		if a.Operation.Date > b.Operation.Date {
			return 1
		}
		if a.Operation.Sequence < b.Operation.Sequence {
			return -1
		}
		if a.Operation.Sequence > b.Operation.Sequence {
			return 1
		}
		return 0
	})
	rows := make([]HoldingTransaction, 0)
	for _, record := range ordered {
		o := record.Operation
		if o.Voided || o.AccountID != id || o.Kind != Buy && o.Kind != Sell && o.Kind != Dividend && o.Kind != DepositBuy && o.Kind != SellWithdraw {
			continue
		}
		cid := latest[o.InstrumentID]
		if o.Kind == Dividend {
			cid = o.CycleID
		}
		c := cycles[cid]
		if (o.Kind == Buy || o.Kind == DepositBuy) && c.Quantity == 0 {
			cid = o.ID
			c = HoldingCostBasis{CycleID: cid, Known: true}
			latest[o.InstrumentID] = cid
		}
		r, err := holdingTransaction(o, record.Note, "operation")
		if err != nil {
			return nil, nil, err
		}
		r.CycleID = cid
		if err := c.apply(r); err != nil {
			return nil, nil, err
		}
		cycles[cid] = c
		rows = append(rows, r)
	}
	out := make(map[string]HoldingCostBasis)
	for instrument, cid := range latest {
		out[instrument] = cycles[cid]
	}
	return out, rows, nil
}
