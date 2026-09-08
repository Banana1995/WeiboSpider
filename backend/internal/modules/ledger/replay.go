package ledger

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// Replay rebuilds derived state from openings and the supplied operation history.
// It never mutates its inputs or returns partial state on failure. asOf is an
// explicit validation cutoff, not a request to silently omit future operations.
func Replay(openings []Opening, instruments []Instrument, operations []Operation, asOf string) (*Book, error) {
	if !validDate(asOf) {
		return nil, fmt.Errorf("%w: cutoff date", ErrOperation)
	}
	securities := make(map[string]Instrument, len(instruments))
	identities := make(map[[2]string]bool)
	for _, i := range instruments {
		key := [2]string{i.Market, i.Code}
		if !validID(i.ID) || !validText(i.Market) || !validText(i.Code) || !validText(i.Name) || !i.Currency.valid() || identities[key] {
			return nil, fmt.Errorf("%w: instrument", ErrOperation)
		}
		if _, exists := securities[i.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate instrument", ErrOperation)
		}
		securities[i.ID], identities[key] = i, true
	}
	book := &Book{Accounts: make(map[string]*AccountState), Movements: make([]Movement, 0)}
	startDates := make(map[string]string)
	for _, o := range openings {
		if !validID(o.AccountID) || !o.Currency.valid() || !validDate(o.Date) || o.Date > asOf || o.Cash < 0 || book.Accounts[o.AccountID] != nil {
			return nil, fmt.Errorf("%w: opening account", ErrOperation)
		}
		a := &AccountState{Currency: o.Currency, Cash: o.Cash, Positions: make(map[string]string), Cycles: make(map[string]*Cycle)}
		for _, p := range o.Positions {
			if _, ok := securities[p.InstrumentID]; !ok || p.Quantity <= 0 || p.Cost != nil && *p.Cost < 0 || a.Positions[p.InstrumentID] != "" {
				return nil, fmt.Errorf("%w: opening position", ErrOperation)
			}
			id := OpeningCycleID(o.AccountID, p.InstrumentID)
			cycle := &Cycle{ID: id, InstrumentID: p.InstrumentID, Quantity: p.Quantity, RemainingCost: copyMoney(p.Cost), DilutedBasis: copyMoney(p.DilutedBasis)}
			if p.Cost != nil {
				cycle.RealizedProfit = new(Money)
			}
			a.Positions[p.InstrumentID], a.Cycles[id] = id, cycle
		}
		book.Accounts[o.AccountID], startDates[o.AccountID] = a, o.Date
	}
	ordered := slices.Clone(operations)
	slices.SortFunc(ordered, func(a, b Operation) int {
		if a.Date < b.Date {
			return -1
		}
		if a.Date > b.Date {
			return 1
		}
		if a.Sequence < b.Sequence {
			return -1
		}
		if a.Sequence > b.Sequence {
			return 1
		}
		return 0
	})
	seen := make(map[string]bool)
	for n, o := range ordered {
		fail := func(err error) (*Book, error) { return nil, &OperationError{ID: o.ID, Date: o.Date, Err: err} }
		if !validID(o.ID) || seen[o.ID] || !validDate(o.Date) || o.Date > asOf || o.Sequence <= 0 {
			return fail(fmt.Errorf("%w: identity, date or sequence", ErrOperation))
		}
		seen[o.ID] = true
		if n > 0 && ordered[n-1].Date == o.Date && ordered[n-1].Sequence == o.Sequence {
			return fail(fmt.Errorf("%w: duplicate daily sequence", ErrOperation))
		}
		if book.Accounts[o.AccountID] == nil || o.Date < startDates[o.AccountID] {
			return fail(fmt.Errorf("%w: account or opening date", ErrOperation))
		}
		// Voided facts reserve their ID/order but have no financial effect. Their
		// former cycle may no longer exist after a correction, so do not apply it.
		if o.Voided {
			continue
		}
		if err := book.apply(o, securities, startDates); err != nil {
			return fail(err)
		}
	}
	return book, nil
}

func (b *Book) apply(o Operation, securities map[string]Instrument, startDates map[string]string) error {
	if o.Amount < 0 || o.Quantity < 0 || o.Price < 0 || o.Fee != nil && *o.Fee < 0 {
		return ErrOperation
	}
	a := b.Accounts[o.AccountID]
	trade := o.Kind == Buy || o.Kind == Sell || o.Kind == DepositBuy || o.Kind == SellWithdraw
	if !trade && (o.Quantity != 0 || o.Price != 0 || o.Fee != nil) {
		return ErrOperation
	}
	if o.Kind != Transfer && o.ToAccountID != "" || o.Kind != Dividend && o.CycleID != "" {
		return ErrOperation
	}
	if !trade && o.Kind != Dividend && (o.InstrumentID != "" || o.FX != nil) {
		return ErrOperation
	}
	switch o.Kind {
	case Deposit, Withdrawal:
		if o.Amount <= 0 {
			return ErrOperation
		}
		delta := o.Amount
		if o.Kind == Withdrawal {
			delta = -delta
		}
		return b.move(o, o.AccountID, o.Kind, delta, delta, "")
	case Transfer:
		target := b.Accounts[o.ToAccountID]
		if o.Amount <= 0 || target == nil || o.ToAccountID == o.AccountID || o.Date < startDates[o.ToAccountID] {
			return ErrOperation
		}
		if a.Currency != target.Currency {
			return ErrUnsupported
		}
		if err := b.move(o, o.AccountID, Transfer, -o.Amount, -o.Amount, o.ToAccountID); err != nil {
			return err
		}
		return b.move(o, o.ToAccountID, Transfer, o.Amount, o.Amount, o.AccountID)
	case Dividend:
		i, exists := securities[o.InstrumentID]
		cycle := a.Cycles[o.CycleID]
		if !exists || o.Amount <= 0 || cycle == nil || cycle.InstrumentID != o.InstrumentID {
			return ErrOperation
		}
		amount, err := convert(o.Amount, i.Currency, a.Currency, o)
		if err != nil {
			return err
		}
		cycle.Dividends, err = AddMoney(cycle.Dividends, amount)
		if err != nil {
			return err
		}
		if cycle.DilutedBasis != nil {
			n, err := SubMoney(*cycle.DilutedBasis, amount)
			if err != nil {
				return err
			}
			cycle.DilutedBasis = &n
		}
		return b.move(o, o.AccountID, Dividend, amount, 0, "")
	case Buy, Sell, DepositBuy, SellWithdraw:
		i, exists := securities[o.InstrumentID]
		if !exists || o.Quantity <= 0 || o.Price <= 0 || ((o.Kind == Buy || o.Kind == Sell) && o.Amount != 0) || ((o.Kind == DepositBuy || o.Kind == SellWithdraw) && o.Amount <= 0) {
			return ErrOperation
		}
		gross, err := TradeAmount(o.Quantity, o.Price)
		if err != nil {
			return err
		}
		fee := Money(0)
		if o.Fee != nil {
			fee = *o.Fee
		}
		buy := o.Kind == Buy || o.Kind == DepositBuy
		var net Money
		if buy {
			net, err = AddMoney(gross, fee)
		} else {
			net, err = SubMoney(gross, fee)
		}
		if err != nil {
			return err
		}
		if gross <= 0 {
			return fmt.Errorf("%w: rounded notional is zero", ErrOperation)
		}
		net, err = convert(net, i.Currency, a.Currency, o)
		if err != nil {
			return err
		}
		if buy && net <= 0 {
			return fmt.Errorf("%w: rounded purchase is zero", ErrOperation)
		}
		if o.Kind == DepositBuy {
			if err := b.move(o, o.AccountID, Deposit, o.Amount, o.Amount, ""); err != nil {
				return err
			}
		}
		cycle := a.Cycles[a.Positions[o.InstrumentID]]
		if buy {
			if cycle == nil || cycle.Quantity == 0 {
				cycle = &Cycle{ID: o.ID, InstrumentID: i.ID, RemainingCost: new(Money), DilutedBasis: new(Money), RealizedProfit: new(Money)}
				a.Cycles[o.ID], a.Positions[i.ID] = cycle, o.ID
			}
			if cycle.Quantity > Quantity(math.MaxInt64)-o.Quantity {
				return ErrPrecision
			}
			cycle.Quantity += o.Quantity
			for _, basis := range []**Money{&cycle.RemainingCost, &cycle.DilutedBasis} {
				if *basis != nil {
					n, err := AddMoney(**basis, net)
					if err != nil {
						return err
					}
					*basis = &n
				}
			}
			return b.move(o, o.AccountID, Buy, -net, 0, "")
		}
		if cycle == nil || cycle.Quantity < o.Quantity {
			return ErrInsufficientStock
		}
		if cycle.RemainingCost != nil {
			allocated, err := AllocateCost(*cycle.RemainingCost, o.Quantity, cycle.Quantity)
			if err != nil {
				return err
			}
			remaining := *cycle.RemainingCost - allocated
			profit, err := SubMoney(net, allocated)
			if err != nil {
				return err
			}
			realized, err := AddMoney(*cycle.RealizedProfit, profit)
			if err != nil {
				return err
			}
			cycle.RemainingCost, cycle.RealizedProfit = &remaining, &realized
		}
		if cycle.DilutedBasis != nil {
			n, err := SubMoney(*cycle.DilutedBasis, net)
			if err != nil {
				return err
			}
			cycle.DilutedBasis = &n
		}
		cycle.Quantity -= o.Quantity
		if cycle.Quantity == 0 {
			cycle.RemainingCost = new(Money)
		}
		if err := b.move(o, o.AccountID, Sell, net, 0, ""); err != nil {
			return err
		}
		if o.Kind == SellWithdraw {
			return b.move(o, o.AccountID, Withdrawal, -o.Amount, -o.Amount, "")
		}
		return nil
	default:
		return ErrOperation
	}
}

func (b *Book) move(o Operation, accountID string, kind Kind, delta, flow Money, counterparty string) error {
	a := b.Accounts[accountID]
	value, err := AddMoney(a.Cash, delta)
	if err != nil {
		return err
	}
	if value < 0 {
		return ErrInsufficientCash
	}
	a.Cash = value
	b.Movements = append(b.Movements, Movement{OperationID: o.ID, Date: o.Date, AccountID: accountID,
		Kind: kind, CashDelta: delta, CapitalFlow: flow, Counterparty: counterparty,
		FeeProvided: (kind == Buy || kind == Sell) && o.Fee != nil})
	return nil
}

func convert(amount Money, from, to Currency, o Operation) (Money, error) {
	if from == to {
		if o.FX != nil {
			return 0, fmt.Errorf("%w: unexpected same-currency FX", ErrOperation)
		}
		return amount, nil
	}
	if o.FX == nil || o.FX.Rate <= 0 || !validDate(o.FX.Date) || o.FX.Date > o.Date || !validText(o.FX.Source) {
		return 0, fmt.Errorf("%w: FX snapshot", ErrOperation)
	}
	if _, err := time.Parse(time.RFC3339Nano, o.FX.FetchedAt); err != nil {
		return 0, fmt.Errorf("%w: FX fetched time", ErrOperation)
	}
	return ConvertMoney(amount, o.FX.Rate)
}
