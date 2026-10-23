package tax

import (
	"errors"
	"math/big"
	"regexp"
)

var decimalPattern = regexp.MustCompile(`^-?[0-9]{1,18}(\.[0-9]{1,12})?$`)

func decimal(s string) (*big.Rat, error) {
	if !decimalPattern.MatchString(s) {
		return nil, errors.New("请输入最多十二位小数的十进制数值")
	}
	x, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, errors.New("无效数值")
	}
	return x, nil
}

func number(s string) *big.Rat   { x, _ := decimal(s); return x }
func zero() *big.Rat             { return new(big.Rat) }
func add(a, b *big.Rat) *big.Rat { return new(big.Rat).Add(a, b) }
func sub(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }
func mul(a, b *big.Rat) *big.Rat { return new(big.Rat).Mul(a, b) }
func abs(a *big.Rat) *big.Rat    { return new(big.Rat).Abs(a) }
func portion(amount, part, whole *big.Rat) *big.Rat {
	return mul(amount, new(big.Rat).Quo(part, whole))
}
func positive(a *big.Rat) *big.Rat {
	if a.Sign() < 0 {
		return zero()
	}
	return a
}
func fixed(a *big.Rat, digits int) string { return a.FloatString(digits) }
