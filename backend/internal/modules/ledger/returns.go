package ledger

import (
	"context"
	"math"
	"math/big"
	"sort"
	"strconv"
	"time"
)

// Values are decimal strings, never JSON floats. Rates are fractions (0.1 = 10%).
type ReturnMetric struct {
	Value      *string `json:"value"`
	Percentage *string `json:"percentage"` // rate display rounded directly from the calculation, not Value
	Status     string  `json:"status"`
	Reason     string  `json:"reason"`
}

type ReturnFlow struct {
	Date       string `json:"date"`
	RecordID   string `json:"record_id"`
	Version    string `json:"version"`
	Flow       Money  `json:"flow"`
	WeightDays int    `json:"weight_days"`
	PeriodDays int    `json:"period_days"`
}

type InvestorFlow struct {
	Date   string `json:"date"`
	Amount string `json:"amount"`
}

type Returns struct {
	Revision      string         `json:"revision"`
	RequestedFrom string         `json:"requested_from"`
	RequestedTo   string         `json:"requested_to"`
	StartMode     string         `json:"start_mode"`
	EffectiveFrom string         `json:"effective_from"` // opening day-end boundary
	EffectiveTo   string         `json:"effective_to"`
	Days          int            `json:"days"`
	Opening       *BasisPoint    `json:"opening"`
	Closing       *BasisPoint    `json:"closing"`
	NetFlow       string         `json:"net_flow"`
	Denominator   *string        `json:"denominator"` // exact rational, in currency units
	Profit        ReturnMetric   `json:"profit"`
	Dietz         ReturnMetric   `json:"modified_dietz"`
	XIRR          ReturnMetric   `json:"xirr"`
	TWR           ReturnMetric   `json:"twr"`
	TWRAnnualized ReturnMetric   `json:"twr_annualized"`
	Curve         []ReturnPoint  `json:"curve"`
	Warnings      []string       `json:"warnings"`
	Flows         []ReturnFlow   `json:"flows"`
	InvestorFlows []InvestorFlow `json:"investor_flows"` // exact net grouping by day
}

// Civil arithmetic, not time.Duration: the supported calendar spans years 1..9999.
func civilDay(date string) int {
	y, _ := strconv.Atoi(date[:4])
	m, _ := strconv.Atoi(date[5:7])
	d, _ := strconv.Atoi(date[8:])
	y--
	n := 365*y + y/4 - y/100 + y/400 + d
	monthDays := [...]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	for i := 1; i < m; i++ {
		n += monthDays[i-1]
	}
	y++
	if m > 2 && y%4 == 0 && (y%100 != 0 || y%400 == 0) {
		n++
	}
	return n
}

func returnUnavailable(reason string) ReturnMetric {
	return ReturnMetric{Status: "unavailable", Reason: reason}
}

// big.Rat.FloatString uses half-away-from-zero; only the returned display is rounded.
func returnValue(value *big.Rat, places int, status string) ReturnMetric {
	s := value.FloatString(places)
	if rounded, ok := new(big.Rat).SetString(s); ok && rounded.Sign() == 0 {
		s = new(big.Rat).FloatString(places)
	}
	r := ReturnMetric{Value: &s, Status: status}
	if places == 12 {
		percent := returnValue(new(big.Rat).Mul(value, big.NewRat(100, 1)), 2, status)
		r.Percentage = percent.Value
	}
	return r
}

func centsString(value *big.Int) string {
	return new(big.Rat).SetFrac(value, big.NewInt(100)).FloatString(2)
}

// Only consumes the completed immutable read projection, including its endpoints
// and flow versions. It never fetches current money or writes valuation history.
func calculateMoneyReturns(ctx context.Context, b AnalysisBasis, requestedFrom, requestedTo string) (Returns, error) {
	r := Returns{Revision: b.Revision, RequestedFrom: requestedFrom, RequestedTo: requestedTo,
		StartMode: "baseline", NetFlow: "0.00", Warnings: []string{}, Flows: []ReturnFlow{}, InvestorFlows: []InvestorFlow{}}
	block := func(reason string) (Returns, error) {
		r.Profit, r.Dietz, r.XIRR = returnUnavailable(reason), returnUnavailable(reason), returnUnavailable(reason)
		return r, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if len(b.Points) > 10000 {
		return r, ErrQuery
	}
	r.Closing = b.Closing
	if requestedFrom != "" {
		r.StartMode, r.Opening = "custom", b.Opening
		if requestedFrom == "0001-01-01" {
			return block("missing_opening")
		}
		d, _ := time.Parse(time.DateOnly, requestedFrom)
		r.EffectiveFrom = d.AddDate(0, 0, -1).Format(time.DateOnly)
	} else {
		// The first known daily closing value includes ALL flows on that baseline day.
		for i := range b.Points {
			p := &b.Points[i]
			if p.Selected && p.Assets != nil {
				r.Opening, r.EffectiveFrom = p, p.Date
				break
			}
		}
	}
	if r.Closing != nil {
		r.EffectiveTo = r.Closing.Date
	}
	if r.Opening == nil || r.Opening.Assets == nil {
		return block("missing_opening")
	}
	if r.Closing == nil || r.Closing.Assets == nil {
		return block("missing_closing")
	}
	r.Days = civilDay(r.EffectiveTo) - civilDay(r.EffectiveFrom)
	if r.Days <= 0 {
		return block("no_interval")
	}
	status := "available"
	for _, p := range []*BasisPoint{r.Opening, r.Closing} {
		if p.Status == "stale" {
			return block("stale_endpoint")
		}
		if p.Status == "untracked" {
			return block("untracked_endpoint")
		}
		if p.Status == "carried" || (p == r.Opening && p.SourceDate < r.EffectiveFrom) {
			status = "reference"
		}
	}
	if status == "reference" {
		r.Warnings = append(r.Warnings, "carried_assets_unchanged")
	}
	if r.Opening.Status == "observed" || r.Closing.Status == "observed" {
		r.Warnings = append(r.Warnings, "sampled_valuation_not_daily_close")
	}
	if r.Days < 365 {
		r.Warnings = append(r.Warnings, "short_period_extrapolation")
	}
	net, weighted := new(big.Int), new(big.Int)
	grouped := map[string]*big.Int{}
	add := func(date string, amount *big.Int) {
		if grouped[date] == nil {
			grouped[date] = new(big.Int)
		}
		grouped[date].Add(grouped[date], amount)
	}
	add(r.EffectiveFrom, new(big.Int).Neg(big.NewInt(int64(*r.Opening.Assets))))
	for _, p := range b.Points {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if p.Flow == nil || p.Date <= r.EffectiveFrom {
			continue
		}
		// Holdings do not invent an asset for a later flow-only day. Do not silently
		// omit known money beyond the last valuation and report an older return.
		if p.Date > r.EffectiveTo {
			if *p.Flow != 0 {
				return block("closing_before_flow")
			}
			continue
		}
		days := civilDay(r.EffectiveTo) - civilDay(p.Date)
		r.Flows = append(r.Flows, ReturnFlow{p.Date, p.RecordID, p.Version, *p.Flow, days, r.Days})
		amount := big.NewInt(int64(*p.Flow))
		net.Add(net, amount)
		weighted.Add(weighted, new(big.Int).Mul(amount, big.NewInt(int64(days))))
		add(p.Date, new(big.Int).Neg(amount))
	}
	add(r.EffectiveTo, big.NewInt(int64(*r.Closing.Assets)))
	r.NetFlow = centsString(net)
	profit := new(big.Int).Sub(big.NewInt(int64(*r.Closing.Assets)), big.NewInt(int64(*r.Opening.Assets)))
	profit.Sub(profit, net)
	r.Profit = returnValue(new(big.Rat).SetFrac(profit, big.NewInt(100)), 2, status)
	den := new(big.Int).Mul(big.NewInt(int64(*r.Opening.Assets)), big.NewInt(int64(r.Days)))
	den.Add(den, weighted)
	denText := new(big.Rat).SetFrac(den, big.NewInt(int64(r.Days)*100)).RatString()
	r.Denominator = &denText
	r.Dietz = returnUnavailable("nonpositive_denominator")
	if den.Sign() > 0 {
		r.Dietz = returnValue(new(big.Rat).SetFrac(new(big.Int).Mul(profit, big.NewInt(int64(r.Days))), den), 12, status)
	}
	dates := make([]string, 0, len(grouped))
	for date := range grouped {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	for _, date := range dates {
		r.InvestorFlows = append(r.InvestorFlows, InvestorFlow{date, centsString(grouped[date])})
	}
	var err error
	r.XIRR, err = solveXIRR(ctx, dates, grouped, status)
	return r, err
}

// One sign variation in an exponential polynomial guarantees exactly one real
// log-rate root. More variations are POSSIBLY multiple, not proof of multiplicity.
// This deliberately does not claim uniqueness from a finite sign-change scan.
func solveXIRR(ctx context.Context, dates []string, grouped map[string]*big.Int, status string) (ReturnMetric, error) {
	if err := ctx.Err(); err != nil {
		return ReturnMetric{}, err
	}
	if len(dates) > 10002 {
		return ReturnMetric{}, ErrQuery
	}
	changes, prior := 0, 0
	nonzero := make([]string, 0, len(dates))
	total, scale := new(big.Int), new(big.Int)
	for _, date := range dates {
		a := grouped[date]
		total.Add(total, a)
		scale.Add(scale, new(big.Int).Abs(a))
		if a.Sign() != 0 {
			nonzero = append(nonzero, date)
			if prior != 0 && prior != a.Sign() {
				changes++
			}
			prior = a.Sign()
		}
	}
	if scale.Sign() == 0 {
		return returnUnavailable("indeterminate_all_zero"), nil
	}
	if changes == 0 {
		return returnUnavailable("no_solution"), nil
	}
	if changes > 1 {
		return returnUnavailable("possible_multiple_roots"), nil
	}
	if total.Sign() == 0 {
		return returnValue(new(big.Rat), 12, status), nil
	}
	if len(nonzero) == 2 && civilDay(nonzero[1])-civilDay(nonzero[0]) == 365 {
		// NPV = first + last/(1+r). Preserve the exact rational at display ties;
		// even a correctly converged floating root cannot choose the tie's side.
		r := new(big.Rat).SetFrac(new(big.Int).Neg(total), grouped[nonzero[0]])
		return returnValue(r, 12, status), nil
	}
	type term struct{ amount, years float64 }
	terms := make([]term, 0, len(dates))
	origin := 0
	for _, date := range dates {
		if grouped[date].Sign() != 0 {
			if len(terms) == 0 {
				origin = civilDay(date)
			}
			a, _ := new(big.Rat).SetFrac(grouped[date], scale).Float64()
			terms = append(terms, term{a, float64(civilDay(date)-origin) / 365})
		}
	}
	base, _ := new(big.Rat).SetFrac(total, scale).Float64()
	// Scale exponents to avoid overflow, and retain the exact net residual near
	// zero via expm1 so cent-size profits on int64-scale balances are not erased.
	eval := func(y float64) (float64, error) {
		maxExp := math.Max(-terms[len(terms)-1].years*y, 0)
		sum, compensation := 0.0, 0.0
		near := math.Abs(terms[len(terms)-1].years*y) < 0.5
		if near {
			sum = base
		}
		for i, t := range terms {
			if i%256 == 0 && ctx.Err() != nil {
				return 0, ctx.Err()
			}
			v := t.amount * math.Exp(-t.years*y-maxExp)
			if near {
				v = t.amount * math.Expm1(-t.years*y)
			}
			v -= compensation
			next := sum + v
			compensation = (next - sum) - v
			sum = next
		}
		return sum, nil
	}
	lo, hi := -32.0, 32.0 // finite supported r = exp(y)-1, strictly greater than -1
	flo, err := eval(lo)
	if err != nil {
		return ReturnMetric{}, err
	}
	fhi, err := eval(hi)
	if err != nil {
		return ReturnMetric{}, err
	}
	if flo == 0 || fhi == 0 || math.Signbit(flo) == math.Signbit(fhi) {
		return returnUnavailable("out_of_solver_range"), nil
	}
	for i := 0; i < 256; i++ {
		mid := (lo + hi) / 2
		f, err := eval(mid)
		if err != nil {
			return ReturnMetric{}, err
		}
		// Both a normalized residual and rate-width guard are required. Returning
		// a rounded rate is refused if float precision cannot support 12 decimals.
		rate := math.Expm1(mid)
		width := math.Exp(hi) * (hi - lo)
		ulp := math.Nextafter(rate, math.Inf(1)) - rate
		if math.Abs(f) < 1e-12 && width < 2e-14 && ulp < 1e-14 {
			candidate := returnValue(new(big.Rat).SetFloat64(rate), 12, status)
			certain, err := certifyXIRRRounding(ctx, nonzero, grouped, candidate)
			if err != nil {
				return ReturnMetric{}, err
			}
			if !certain {
				return returnUnavailable("precision_unresolved"), nil
			}
			return candidate, nil
		}
		if mid == lo || mid == hi {
			break
		}
		if math.Signbit(f) == math.Signbit(flo) {
			lo, flo = mid, f
		} else {
			hi = mid
		}
	}
	return returnUnavailable("not_converged"), nil
}

// The floating search supplies only a candidate. Certify that the unique root
// lies STRICTLY inside both decimal rounding cells, independent of that search's
// floating signs/residual. An exact tie outside the rational fast paths remains
// unresolved: no epsilon is added and neither neighbouring display is selected.
func certifyXIRRRounding(ctx context.Context, dates []string, grouped map[string]*big.Int, candidate ReturnMetric) (bool, error) {
	value, _ := new(big.Rat).SetString(*candidate.Value)
	percent, _ := new(big.Rat).SetString(*candidate.Percentage)
	percent.Quo(percent, big.NewRat(100, 1))
	lo := new(big.Rat).Sub(value, big.NewRat(1, 2000000000000))
	hi := new(big.Rat).Add(value, big.NewRat(1, 2000000000000))
	plo := new(big.Rat).Sub(percent, big.NewRat(1, 20000))
	phi := new(big.Rat).Add(percent, big.NewRat(1, 20000))
	if plo.Cmp(lo) > 0 {
		lo = plo
	}
	if phi.Cmp(hi) < 0 {
		hi = phi
	}
	if lo.Cmp(hi) >= 0 || hi.Cmp(big.NewRat(-1, 1)) <= 0 {
		return false, nil
	}
	// One sign variation implies opposite first/last signs, with the last term
	// dominating as r approaches -1. That open domain boundary needs no NPV call.
	lastSign := grouped[dates[len(dates)-1]].Sign()
	for _, bound := range []struct {
		rate *big.Rat
		sign int
	}{{lo, lastSign}, {hi, -lastSign}} {
		if bound.rate.Cmp(big.NewRat(-1, 1)) <= 0 {
			continue
		}
		sign, err := xirrSignAtRate(ctx, dates, grouped, bound.rate)
		if err != nil || sign != bound.sign {
			return false, err
		}
	}
	return true, nil
}

// Let q=(1+r)^(-1/365). NPV becomes an integer-power polynomial in q, so its
// sign can be enclosed without floating exp/log accuracy assumptions. All basic
// operations below are correctly rounded OUTWARD at 192 bits. Resource budgets
// are fixed: <=192 root steps and O(points * log(calendar days)) multiplications.
func xirrSignAtRate(ctx context.Context, dates []string, grouped map[string]*big.Int, rate *big.Rat) (int, error) {
	const precision = 192
	makeFloat := func(mode big.RoundingMode) *big.Float {
		return new(big.Float).SetPrec(precision).SetMode(mode)
	}
	down, up := big.ToNegativeInf, big.ToPositiveInf
	pow := func(x *big.Float, n int, mode big.RoundingMode) *big.Float {
		result, base := makeFloat(mode).SetInt64(1), makeFloat(mode).Set(x)
		for n > 0 {
			if n&1 != 0 {
				result.Mul(result, base)
			}
			n >>= 1
			if n > 0 {
				base.Mul(base, base)
			}
		}
		return result
	}
	growth := new(big.Rat).Add(rate, big.NewRat(1, 1))
	if growth.Sign() <= 0 {
		return 0, nil
	}
	target := new(big.Rat).Inv(growth)
	targetLo, targetHi := makeFloat(down).SetRat(target), makeFloat(up).SetRat(target)
	lo, hi := makeFloat(down).SetFloat64(0.5), makeFloat(up).SetInt64(2)
	if pow(lo, 365, up).Cmp(targetLo) >= 0 || pow(hi, 365, down).Cmp(targetHi) <= 0 {
		return 0, nil
	}
	for i := 0; i < precision; i++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		mid := makeFloat(big.ToNearestEven).Add(lo, hi)
		mid.Quo(mid, big.NewFloat(2))
		if mid.Cmp(lo) == 0 || mid.Cmp(hi) == 0 {
			break
		}
		if pow(mid, 365, up).Cmp(targetLo) < 0 {
			lo = mid
		} else if pow(mid, 365, down).Cmp(targetHi) > 0 {
			hi = mid
		} else {
			break // retain the enclosing interval when the sign is uncertain
		}
	}
	sumLo, sumHi := makeFloat(down), makeFloat(up)
	origin := civilDay(dates[0])
	for i, date := range dates {
		if i%32 == 0 && ctx.Err() != nil {
			return 0, ctx.Err()
		}
		days := civilDay(date) - origin
		powerLo, powerHi := pow(lo, days, down), pow(hi, days, up)
		amount := grouped[date]
		if amount.Sign() < 0 {
			powerLo, powerHi = powerHi, powerLo
		}
		termLo := makeFloat(down).Mul(makeFloat(down).SetInt(amount), powerLo)
		termHi := makeFloat(up).Mul(makeFloat(up).SetInt(amount), powerHi)
		sumLo.Add(sumLo, termLo)
		sumHi.Add(sumHi, termHi)
	}
	if sumLo.Sign() > 0 {
		return 1, nil
	}
	if sumHi.Sign() < 0 {
		return -1, nil
	}
	return 0, nil
}
