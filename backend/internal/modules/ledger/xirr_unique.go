package ledger

import (
	"context"
	"math"
	"math/big"
)

// For nonzero C at increasing distinct days d (shift d[0] to zero), normalize
// C[0]<0. If every proper prefix S[i]<=0 and total S[n]>=0, Abel summation gives
// P(q)=S[n]*q^d[n]+sum S[i]*(q^d[i]-q^d[i+1]), q=(1+r)^(-1/365).
// On (0,1), P(q)/q^d[n] strictly increases from -infinity to S[n]: each
// q^(d[i]-d[n])-q^(d[i+1]-d[n]) is positive and strictly decreasing.
// For q>1, P(q)>0. Thus there is exactly one root, with r>=0; total=0
// gives only q=1. Reversing amounts and reflecting dates replaces P(q) by
// q^d[n]*P(1/q), proving the corresponding unique r<=0 case. Multiplying
// every amount by -1 does not change roots. Neither test assumes equal gaps.
func certifyXIRRUnique(ctx context.Context, dates []string, grouped map[string]*big.Int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(dates) < 2 {
		return false, nil
	}
	if len(dates) > 10002 {
		return false, ErrQuery
	}
	for _, reverse := range []bool{false, true} {
		first := 0
		if reverse {
			first = len(dates) - 1
		}
		firstSign := grouped[dates[first]].Sign()
		if firstSign == 0 {
			return false, nil // caller removes zero groups
		}
		sum := new(big.Int)
		for i := range dates {
			if i%256 == 0 && ctx.Err() != nil {
				return false, ctx.Err()
			}
			j := i
			if reverse {
				j = len(dates) - 1 - i
			}
			sum.Add(sum, grouped[dates[j]])
			if i == len(dates)-1 {
				if sum.Sign()*firstSign <= 0 {
					return true, nil
				}
			} else if sum.Sign()*firstSign < 0 {
				break
			}
		}
	}
	return false, nil
}

// Scale the daily polynomial by a positive discount q0 near the candidate:
// P(q0*u) has coefficients C[i]*q0^d[i]. The same Abel certificate above applies
// to their prefix sums, proving uniqueness over ALL u>0 (hence all r>-1).
// This is the discounted-investment-balance / Soper condition: withdrawals and
// later deposits do not prevent a unique IRR when capital remains invested.
//
// q0 is an exactly representable binary rational, not an assumed exact exp()
// result. Outward 192-bit bounds certify every prefix and the final sign. At most
// four candidate-near probes locate a certificate; neither floating
// signs, a root scan, nor proximity to a guessed rate can establish uniqueness.
func certifyXIRRDiscountedUnique(ctx context.Context, dates []string, grouped map[string]*big.Int, logRate float64) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(dates) < 2 {
		return false, nil
	}
	if len(dates) > 10002 {
		return false, ErrQuery
	}
	discount := math.Exp(-logRate / 365)
	if discount <= 0 || math.IsNaN(discount) || math.IsInf(discount, 0) {
		return false, nil
	}
	firstSign := grouped[dates[0]].Sign()
	if firstSign == 0 {
		return false, nil
	}
	origin := civilDay(dates[0])
	down, up := big.ToNegativeInf, big.ToPositiveInf
	for attempt := 0; attempt < 4; attempt++ {
		q := xirrFloat(big.ToNearestEven).SetFloat64(discount)
		sumLo, sumHi := xirrFloat(down), xirrFloat(up)
		for i, date := range dates {
			if i%32 == 0 && ctx.Err() != nil {
				return false, ctx.Err()
			}
			days := civilDay(date) - origin
			powerLo, powerHi := xirrPower(q, days, down), xirrPower(q, days, up)
			amount := grouped[date]
			if amount.Sign() < 0 {
				powerLo, powerHi = powerHi, powerLo
			}
			sumLo.Add(sumLo, xirrFloat(down).Mul(xirrFloat(down).SetInt(amount), powerLo))
			sumHi.Add(sumHi, xirrFloat(up).Mul(xirrFloat(up).SetInt(amount), powerHi))
			if i == len(dates)-1 {
				if firstSign < 0 && sumLo.Sign() >= 0 || firstSign > 0 && sumHi.Sign() <= 0 {
					return true, nil
				}
			} else if firstSign < 0 && sumHi.Sign() > 0 || firstSign > 0 && sumLo.Sign() < 0 {
				break
			}
		}
		// Increasing q moves just beyond the root, where the final sum must
		// oppose the first flow. Its sign is still independently certified.
		discount = math.Nextafter(discount, math.Inf(1))
	}
	return false, nil
}
