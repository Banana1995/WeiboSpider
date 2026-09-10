package ledger

import (
	"context"
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
