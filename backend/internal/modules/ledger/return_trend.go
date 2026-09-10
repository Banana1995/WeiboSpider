package ledger

import (
	"context"
	"math/big"
)

type ReturnPoint struct {
	Date     string       `json:"date"`
	RecordID string       `json:"record_id"`
	Baseline bool         `json:"baseline"`
	Profit   ReturnMetric `json:"profit"`
	Dietz    ReturnMetric `json:"modified_dietz"`
	TWR      ReturnMetric `json:"twr"`
}

// Bound exact product growth after Rat's cross-cancellation. No rounded product
// is ever fed back into the chain. The JSON display also has a 100-digit budget.
const maxTWRBits = 32768

func boundedGrowth(g *big.Rat) bool {
	return g.Num().BitLen() <= maxTWRBits && g.Denom().BitLen() <= maxTWRBits &&
		new(big.Int).Quo(g.Num(), g.Denom()).BitLen() <= 320
}

func returnEndpoint(p *BasisPoint) (string, string) {
	if p == nil || p.Assets == nil {
		return "", "missing_flow_boundary"
	}
	if p.Status == "stale" || p.Status == "untracked" {
		return "", p.Status + "_endpoint"
	}
	if p.Status == "carried" {
		return "reference", ""
	}
	return "available", ""
}

func calculateReturns(ctx context.Context, b AnalysisBasis, from, to string) (Returns, error) {
	r, err := calculateMoneyReturns(ctx, b, from, to)
	if err != nil {
		return r, err
	}
	r.Curve = []ReturnPoint{}
	r.TWR, r.TWRAnnualized = returnUnavailable(r.Profit.Reason), returnUnavailable(r.Profit.Reason)
	if r.Opening == nil || r.Opening.Assets == nil || r.EffectiveFrom == "" {
		return r, nil
	}
	openingStatus, openingReason := returnEndpoint(r.Opening)
	if r.Opening.SourceDate < r.EffectiveFrom && openingReason == "" {
		openingStatus = "reference"
	}
	anchor := ReturnPoint{Date: r.EffectiveFrom, RecordID: r.Opening.RecordID, Baseline: true,
		Profit: returnValue(new(big.Rat), 2, openingStatus), Dietz: returnValue(new(big.Rat), 12, openingStatus), TWR: returnValue(new(big.Rat), 12, openingStatus)}
	if openingReason != "" {
		anchor.Profit, anchor.Dietz, anchor.TWR = returnUnavailable(openingReason), returnUnavailable(openingReason), returnUnavailable(openingReason)
	}
	r.Curve = append(r.Curve, anchor)
	type day struct {
		point    *BasisPoint
		flow     *big.Int
		boundary bool
	}
	days := map[string]*day{}
	dates := make([]string, 0, len(b.Points))
	for i := range b.Points {
		p := &b.Points[i]
		if p.Date <= r.EffectiveFrom {
			continue
		}
		d := days[p.Date]
		if d == nil {
			d = &day{flow: new(big.Int)}
			days[p.Date] = d
			dates = append(dates, p.Date) // basis is already in stable date order
		}
		if p.Selected {
			d.point = p
		}
		if p.Flow != nil {
			d.flow.Add(d.flow, big.NewInt(int64(*p.Flow)))
			// Two nonzero opposing events still require this day's boundary.
			d.boundary = d.boundary || *p.Flow != 0
		}
	}
	net, moment := new(big.Int), new(big.Int)
	opening := big.NewInt(int64(*r.Opening.Assets))
	boundaryAssets := new(big.Int).Set(opening)
	chain := big.NewRat(1, 1)
	chainStatus, chainReason := openingStatus, openingReason
	for _, date := range dates {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		d := days[date]
		n := int64(civilDay(date) - civilDay(r.EffectiveFrom))
		net.Add(net, d.flow)
		moment.Add(moment, new(big.Int).Mul(d.flow, big.NewInt(n)))
		status, reason := returnEndpoint(d.point)
		if status == "reference" || openingStatus == "reference" {
			status = "reference"
		}
		point := ReturnPoint{Date: date, Profit: returnUnavailable("missing_closing"), Dietz: returnUnavailable("missing_closing")}
		if d.point != nil {
			point.RecordID = d.point.RecordID
			moneyReason := reason
			if moneyReason == "missing_flow_boundary" {
				moneyReason = "missing_closing"
			}
			if openingReason != "" {
				moneyReason = openingReason
			}
			if moneyReason != "" {
				point.Profit, point.Dietz = returnUnavailable(moneyReason), returnUnavailable(moneyReason)
			} else {
				profit := new(big.Int).Sub(big.NewInt(int64(*d.point.Assets)), opening)
				profit.Sub(profit, net)
				point.Profit = returnValue(new(big.Rat).SetFrac(profit, big.NewInt(100)), 2, status)
				// sum(flow*(n-flowDay)) = n*sum(flow)-sum(flow*flowDay).
				// Only opening uses the inclusive period; flows gain no extra day.
				den := new(big.Int).Mul(opening, big.NewInt(n+1))
				den.Add(den, new(big.Int).Mul(net, big.NewInt(n)))
				den.Sub(den, moment)
				point.Dietz = returnUnavailable("nonpositive_denominator")
				if den.Sign() > 0 {
					point.Dietz = returnValue(new(big.Rat).SetFrac(new(big.Int).Mul(profit, big.NewInt(n+1)), den), 12, status)
				}
			}
		}
		twrReason := chainReason
		if twrReason == "" {
			twrReason = reason
		}
		if twrReason == "" && boundaryAssets.Sign() == 0 {
			twrReason = "zero_twr_base"
		}
		growth := new(big.Rat)
		if twrReason == "" {
			preFlow := new(big.Int).Sub(big.NewInt(int64(*d.point.Assets)), d.flow)
			if preFlow.Sign() < 0 {
				twrReason = "negative_twr_factor"
			} else {
				growth.Mul(chain, new(big.Rat).SetFrac(preFlow, boundaryAssets))
				if !boundedGrowth(growth) {
					twrReason = "twr_product_limit"
				}
			}
		}
		twrStatus := chainStatus
		if status == "reference" {
			twrStatus = "reference"
		}
		point.TWR = returnUnavailable(twrReason)
		if twrReason == "" {
			point.TWR = returnValue(new(big.Rat).Sub(growth, big.NewRat(1, 1)), 12, twrStatus)
		}
		if d.boundary {
			if d.point != nil && d.point.Status == "observed" {
				found := false
				for _, warning := range r.Warnings {
					found = found || warning == "sampled_valuation_not_daily_close"
				}
				if !found {
					r.Warnings = append(r.Warnings, "sampled_valuation_not_daily_close")
				}
			}
			chainReason = twrReason
			if twrReason == "" {
				chain.Set(growth)
				boundaryAssets.SetInt64(int64(*d.point.Assets))
				chainStatus = twrStatus
			}
		}
		if d.point != nil {
			// A known later flow invalidates the endpoint, not earlier prefixes.
			// Propagate this gate explicitly, never overwrite computed end values.
			if date == r.EffectiveTo && r.Profit.Reason == "closing_before_flow" {
				twrReason = "closing_before_flow"
				point.Profit, point.Dietz, point.TWR = returnUnavailable(twrReason), returnUnavailable(twrReason), returnUnavailable(twrReason)
			}
			r.Curve = append(r.Curve, point)
			if date == r.EffectiveTo {
				r.TWR = point.TWR
				r.TWRAnnualized = returnUnavailable(point.TWR.Reason)
				if twrReason == "" {
					r.TWRAnnualized, err = annualizeTWR(ctx, growth, r.EffectiveFrom, date, twrStatus)
					if err != nil {
						return r, err
					}
				}
			}
		}
		if date > r.EffectiveTo && d.boundary {
			r.TWR, r.TWRAnnualized = returnUnavailable("closing_before_flow"), returnUnavailable("closing_before_flow")
		}
	}
	if r.Days <= 0 {
		r.TWR, r.TWRAnnualized = returnUnavailable("no_interval"), returnUnavailable("no_interval")
	}
	if r.TWR.Status == "reference" {
		found := false
		for _, w := range r.Warnings {
			found = found || w == "carried_assets_unchanged"
		}
		if !found {
			r.Warnings = append(r.Warnings, "carried_assets_unchanged")
		}
	}
	return r, nil
}

// With growth=N/D, the synthetic cash flows -D, +N have exactly the
// annualized TWR root. No Money/int64 conversion or rounding is involved.
// The existing bounded candidate search must certify both rounding cells.
func annualizeTWR(ctx context.Context, growth *big.Rat, from, to, status string) (ReturnMetric, error) {
	if err := ctx.Err(); err != nil {
		return ReturnMetric{}, err
	}
	n := civilDay(to) - civilDay(from)
	if n <= 0 {
		return returnUnavailable("no_interval"), nil
	}
	if growth.Sign() == 0 {
		return returnValue(big.NewRat(-1, 1), 12, status), nil
	}
	if 365%n == 0 {
		// Reduced numerator/denominator stay coprime when raised to a power.
		// Refuse guaranteed budget overruns before allocating their large powers.
		if (growth.Num().BitLen()-1)*(365/n)+1 > maxTWRBits || (growth.Denom().BitLen()-1)*(365/n)+1 > maxTWRBits {
			return returnUnavailable("twr_product_limit"), nil
		}
		power := big.NewInt(int64(365 / n))
		g := new(big.Rat).SetFrac(new(big.Int).Exp(growth.Num(), power, nil), new(big.Int).Exp(growth.Denom(), power, nil))
		if !boundedGrowth(g) {
			return returnUnavailable("twr_product_limit"), nil
		}
		return returnValue(new(big.Rat).Sub(g, big.NewRat(1, 1)), 12, status), nil
	}
	return solveXIRR(ctx, []string{from, to}, map[string]*big.Int{from: new(big.Int).Neg(growth.Denom()), to: new(big.Int).Set(growth.Num())}, status)
}
