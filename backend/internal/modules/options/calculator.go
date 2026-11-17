package options

import (
	"fmt"
	"math/big"
	"regexp"
	"time"
	_ "time/tzdata"
)

var decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,14})(\.[0-9]{1,9})?$`)
var eastern, _ = time.LoadLocation("America/New_York")

func number(s string) (*big.Rat, error) {
	if !decimalPattern.MatchString(s) {
		return nil, ErrInput
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, ErrInput
	}
	return r, nil
}
func positive(s string, zero bool) (*big.Rat, error) {
	r, e := number(s)
	if e != nil || r.Sign() < 0 || (!zero && r.Sign() == 0) {
		return nil, ErrInput
	}
	return r, nil
}
func optional(s *string) *big.Rat {
	if s == nil {
		return nil
	}
	r, _ := number(*s)
	return r
}
func ptr(s string) *string       { return &s }
func integer(n int) *big.Rat     { return new(big.Rat).SetInt64(int64(n)) }
func add(a, b *big.Rat) *big.Rat { return new(big.Rat).Add(a, b) }
func sub(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }
func mul(a, b *big.Rat) *big.Rat { return new(big.Rat).Mul(a, b) }
func div(a, b *big.Rat) *big.Rat { return new(big.Rat).Quo(a, b) }
func formatted(r *big.Rat, digits int) *string {
	if r == nil {
		return nil
	}
	return ptr(r.FloatString(digits))
}
func dateET(at time.Time) string { return at.In(eastern).Format(time.DateOnly) }

// Parse date-only values in UTC solely to count civil days, never elapsed DST hours.
func Days(from, to string) (int, error) {
	a, e := time.Parse(time.DateOnly, from)
	if e != nil {
		return 0, ErrInput
	}
	b, e := time.Parse(time.DateOnly, to)
	if e != nil {
		return 0, ErrInput
	}
	return int(b.Sub(a) / (24 * time.Hour)), nil
}
func fee(rule FeeRule, n int) (*big.Rat, error) {
	values := []*big.Rat{}
	for _, s := range []string{rule.PerContract, rule.Minimum, rule.Fixed, rule.Regulatory} {
		r, e := positive(s, true)
		if e != nil {
			return nil, e
		}
		values = append(values, r)
	}
	commission := mul(values[0], integer(n))
	if commission.Cmp(values[1]) < 0 {
		commission = values[1]
	}
	return add(add(commission, values[2]), mul(values[3], integer(n))), nil
}
func validateInput(in Input) error {
	if in.Quantity < 1 || in.Quantity > 10000 || (in.Mode != "full" && in.Mode != "net") || (in.Basis != "bid" && in.Basis != "mid" && in.Basis != "custom") || in.FeeVersion < 0 {
		return ErrInput
	}
	if in.Basis == "custom" {
		if in.Price == nil {
			return ErrInput
		}
		if _, e := positive(*in.Price, false); e != nil {
			return e
		}
	} else if in.Price != nil {
		return fmt.Errorf("%w：Bid/Mid 使用快照价格", ErrInput)
	}
	if in.FeeOverride != nil {
		if _, e := positive(*in.FeeOverride, true); e != nil {
			return e
		}
		if in.FeeVersion != 0 {
			return ErrInput
		}
	}
	if v := in.Interest; v != nil {
		if _, e := positive(v.Principal, true); e != nil {
			return e
		}
		r, e := positive(v.AnnualRate, true)
		if e != nil || r.Cmp(integer(100)) > 0 {
			return ErrInput
		}
		if v.Days < 0 || v.Days > 3650 {
			return ErrInput
		}
	}
	if v := in.Scenario; v != nil {
		if v.Kind != "expiry" && v.Kind != "close" {
			return ErrInput
		}
		if _, e := positive(v.Price, true); e != nil {
			return e
		}
		for _, s := range []*string{v.Fee, v.Interest} {
			if s != nil {
				if _, e := positive(*s, true); e != nil {
					return e
				}
			}
		}
		if v.StockCost != nil {
			if _, e := positive(*v.StockCost, true); e != nil {
				return e
			}
		}
	}
	return nil
}

// exact contains unrounded metrics for filtering/sorting, never re-parsed display values.
func calculate(q Quote, valuation string, in Input, fees *FeeProfile, underlying *string) (Calculation, map[string]*big.Rat, error) {
	out := Calculation{Version: CalculationVersion, Status: "ok"}
	exact := map[string]*big.Rat{}
	if e := validateInput(in); e != nil {
		return out, exact, e
	}
	d, e := Days(valuation, q.Contract.Expiry)
	if e != nil {
		return out, exact, e
	}
	out.DTE = d
	k, e := positive(q.Contract.Strike, false)
	if e != nil {
		return out, exact, e
	}
	c := q.Contract
	if (c.Type != "PUT" && c.Type != "CALL") || c.Currency != "USD" || c.Standard != "STANDARD" || c.Settlement != "PHYSICAL" || c.Multiplier != 100 || q.Status == "metadata_invalid" {
		out.Status = "unsupported_contract"
		return out, exact, nil
	}
	isCall := c.Type == "CALL"
	if in.Scenario != nil && in.Scenario.StockCost != nil && (!isCall || in.Scenario.Kind != "expiry") {
		return out, exact, ErrInput
	}
	shares := integer(c.Multiplier * in.Quantity)
	stock := optional(underlying)
	if stock != nil && stock.Sign() <= 0 {
		stock = nil
	}
	capital := mul(k, shares)
	out.CapitalBasis = "strike_value"
	if isCall {
		out.CapitalBasis = "stock_value"
		out.CoveredShares = formatted(shares, 0)
		out.StrikeProceeds = formatted(capital, 2)
		capital = nil
		if stock != nil {
			capital = mul(stock, shares)
		}
	}
	out.Capital = formatted(capital, 2)
	exact["capital"] = capital
	exact["strike"] = k
	bid, ask := optional(q.Bid), optional(q.Ask)
	valid := bid != nil && bid.Sign() > 0 && ask != nil && ask.Cmp(bid) >= 0 && q.Status != "suspended"
	if valid {
		exact["spread"] = mul(div(sub(ask, bid), div(add(ask, bid), integer(2))), integer(100))
	}
	var p *big.Rat
	switch in.Basis {
	case "bid":
		if valid {
			p = bid
		}
	case "mid":
		if valid {
			p = div(add(bid, ask), integer(2))
		}
	case "custom":
		p, _ = number(*in.Price)
	}
	if p == nil {
		out.Status = "quote_unavailable"
		return out, exact, nil
	}
	out.Price = formatted(p, 6)
	gross := mul(p, shares)
	income := gross
	out.Gross = formatted(gross, 2)
	// Intrinsic is K-S for puts and S-K for calls, floored at zero.
	// The split uses the gross assumed sale premium; fees never alter option value.
	if stock != nil {
		intrinsicPerShare := optionIntrinsic(c.Type, k, stock)
		intrinsic := mul(intrinsicPerShare, shares)
		out.Intrinsic = formatted(intrinsic, 2)
		out.TimeValue = formatted(sub(gross, intrinsic), 2)
		exact["intrinsic"] = intrinsic
		exact["time_value"] = sub(gross, intrinsic)
	}
	var f *big.Rat
	if in.FeeOverride != nil {
		f, _ = positive(*in.FeeOverride, true)
	} else if in.FeeVersion > 0 {
		if fees == nil {
			return out, exact, ErrNotFound
		}
		f, e = fee(fees.Open, in.Quantity)
		if e != nil {
			return out, exact, e
		}
	}
	if f != nil {
		income = sub(gross, f)
		out.Fee = formatted(f, 2)
		out.Net = formatted(income, 2)
	}
	out.Income = formatted(income, 2)
	exact["premium"] = income
	if isCall {
		salePrice := add(k, div(income, shares))
		out.NetSalePrice = formatted(salePrice, 6)
		exact["sale_price"] = salePrice
	}
	base := capital
	if in.Mode == "net" && capital != nil {
		base = sub(capital, income)
	}
	out.Base = formatted(base, 6)
	reference := k
	if isCall {
		reference = stock
	}
	if reference != nil {
		cost := sub(reference, div(income, shares))
		out.Cost = formatted(cost, 6)
		exact["cost"] = cost
	}
	if base == nil {
		out.Status = "underlying_unavailable"
	} else if base.Sign() > 0 {
		rate := mul(div(income, base), integer(100))
		out.Rate = formatted(rate, 6)
		exact["rate"] = rate
		if d > 0 {
			apr := mul(rate, div(integer(365), integer(d)))
			out.APR = formatted(apr, 6)
			exact["yield"] = apr
		} else {
			out.Status = "expired"
		}
	} else {
		out.Status = "invalid_base"
	}
	if v := in.Interest; v != nil {
		a, _ := number(v.Principal)
		r, _ := number(v.AnnualRate)
		i := mul(mul(a, div(r, integer(100))), div(integer(v.Days), integer(365)))
		out.Interest = formatted(i, 2)
		if base != nil && base.Sign() > 0 && d > 0 && v.Days == d {
			out.CombinedAPR = formatted(mul(div(add(income, i), base), div(integer(36500), integer(d))), 6)
		}
	}
	if v := in.Scenario; v != nil {
		price, _ := number(v.Price)
		liability := mul(price, shares)
		if v.Kind == "expiry" {
			intrinsic := optionIntrinsic(c.Type, k, price)
			liability = mul(intrinsic, shares)
		}
		pnl := sub(income, liability)
		extra := optional(v.Fee)
		if extra == nil && fees != nil && in.FeeVersion > 0 {
			rule := fees.Close
			if v.Kind == "expiry" {
				rule = fees.Assignment
				if (!isCall && price.Cmp(k) > 0) || (isCall && price.Cmp(k) < 0) {
					extra = integer(0)
					rule = nil
				}
			}
			if rule != nil {
				extra, e = fee(*rule, in.Quantity)
				if e != nil {
					return out, exact, e
				}
			}
		}
		if extra != nil {
			pnl = sub(pnl, extra)
		}
		out.ScenarioFeesKnown = f != nil && extra != nil
		if i := optional(v.Interest); i != nil {
			pnl = add(pnl, i)
			out.ScenarioInterestKnown = true
		}
		out.ScenarioPnL = formatted(pnl, 2)
		if isCall && v.Kind == "expiry" && v.StockCost != nil {
			stockCost, _ := number(*v.StockCost)
			// Long-stock mark-to-market + short-call payoff equals min(ST,K)-B.
			out.CoveredPnL = formatted(add(pnl, mul(sub(price, stockCost), shares)), 2)
		}
	}
	return out, exact, nil
}

func optionIntrinsic(side string, strike, stock *big.Rat) *big.Rat {
	value := sub(strike, stock)
	if side == "CALL" {
		value = sub(stock, strike)
	}
	if value.Sign() < 0 {
		return integer(0)
	}
	return value
}
