package ledger

import "math/big"

const xirrPrecision = 192

func xirrFloat(mode big.RoundingMode) *big.Float {
	return new(big.Float).SetPrec(xirrPrecision).SetMode(mode)
}

// For positive x, rounding every multiplication in the same direction encloses
// the exact integer power. The sparse daily polynomial never needs expansion.
func xirrPower(x *big.Float, n int, mode big.RoundingMode) *big.Float {
	result, base := xirrFloat(mode).SetInt64(1), xirrFloat(mode).Set(x)
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
