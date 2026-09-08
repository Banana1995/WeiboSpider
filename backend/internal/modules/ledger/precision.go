package ledger

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Money stores hundredths of a currency unit.
type Money int64

// Quantity stores millionths of a unit.
type Quantity int64

// Price stores millionths of a currency unit per unit.
type Price int64

// Rate stores hundred-millionths of an exchange rate.
type Rate int64

// ErrPrecision indicates invalid decimal syntax, precision, range, or operands.
var ErrPrecision = errors.New("invalid ledger precision or operands")

// Bound work even for inputs with arbitrarily many leading zeroes.
const maxDecimalLength = 32

func ParseMoney(s string) (Money, error) {
	v, err := parseFixed(s, 2)
	return Money(v), err
}

func ParseQuantity(s string) (Quantity, error) {
	v, err := parseFixed(s, 6)
	return Quantity(v), err
}

func ParsePrice(s string) (Price, error) {
	v, err := parseFixed(s, 6)
	return Price(v), err
}

func ParseRate(s string) (Rate, error) {
	v, err := parseFixed(s, 8)
	return Rate(v), err
}

func parseFixed(s string, digits int) (int64, error) {
	if len(s) == 0 || len(s) > maxDecimalLength {
		return 0, ErrPrecision
	}
	start := 0
	if s[0] == '-' {
		start = 1
	}
	point := len(s)
	for i := start; i < len(s); i++ {
		if s[i] == '.' && point == len(s) {
			point = i
		} else if s[i] < '0' || s[i] > '9' {
			return 0, ErrPrecision
		}
	}
	if point == start {
		return 0, ErrPrecision
	}
	fraction := ""
	if point < len(s) {
		fraction = s[point+1:]
		if len(fraction) == 0 || len(fraction) > digits {
			return 0, ErrPrecision
		}
	}
	v, err := strconv.ParseInt(s[:point]+fraction+strings.Repeat("0", digits-len(fraction)), 10, 64)
	if err != nil {
		return 0, ErrPrecision
	}
	return v, nil
}

func formatFixed(v int64, digits int) string {
	s := strconv.FormatInt(v, 10)
	sign := ""
	if s[0] == '-' {
		sign, s = "-", s[1:]
	}
	if len(s) <= digits {
		s = strings.Repeat("0", digits+1-len(s)) + s
	}
	point := len(s) - digits
	return sign + s[:point] + "." + s[point:]
}

func (v Money) String() string    { return formatFixed(int64(v), 2) }
func (v Quantity) String() string { return formatFixed(int64(v), 6) }
func (v Price) String() string    { return formatFixed(int64(v), 6) }
func (v Rate) String() string     { return formatFixed(int64(v), 8) }

func (v Money) MarshalJSON() ([]byte, error)    { return []byte(`"` + v.String() + `"`), nil }
func (v Quantity) MarshalJSON() ([]byte, error) { return []byte(`"` + v.String() + `"`), nil }
func (v Price) MarshalJSON() ([]byte, error)    { return []byte(`"` + v.String() + `"`), nil }
func (v Rate) MarshalJSON() ([]byte, error)     { return []byte(`"` + v.String() + `"`), nil }

func (v *Money) UnmarshalJSON(data []byte) error    { return unmarshalFixed(data, 2, v) }
func (v *Quantity) UnmarshalJSON(data []byte) error { return unmarshalFixed(data, 6, v) }
func (v *Price) UnmarshalJSON(data []byte) error    { return unmarshalFixed(data, 6, v) }
func (v *Rate) UnmarshalJSON(data []byte) error     { return unmarshalFixed(data, 8, v) }

func unmarshalFixed[T ~int64](data []byte, digits int, target *T) error {
	// A JSON character can occupy six bytes as a Unicode escape.
	if target == nil || len(data) < 2 || len(data) > 6*maxDecimalLength+2 || data[0] != '"' || data[len(data)-1] != '"' {
		return ErrPrecision
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return ErrPrecision
	}
	v, err := parseFixed(s, digits)
	if err != nil {
		return err
	}
	*target = T(v)
	return nil
}

func AddMoney(a, b Money) (Money, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, ErrPrecision
	}
	return a + b, nil
}

func SubMoney(a, b Money) (Money, error) {
	if (b > 0 && a < math.MinInt64+b) || (b < 0 && a > math.MaxInt64+b) {
		return 0, ErrPrecision
	}
	return a - b, nil
}

// TradeAmount rounds quantity times price to cents. Both operands must be nonnegative.
func TradeAmount(q Quantity, p Price) (Money, error) {
	if q < 0 || p < 0 {
		return 0, ErrPrecision
	}
	v, err := roundedProduct(int64(q), int64(p), 10_000_000_000)
	return Money(v), err
}

// ConvertMoney permits signed amounts but requires a nonnegative rate.
func ConvertMoney(amount Money, rate Rate) (Money, error) {
	if rate < 0 {
		return 0, ErrPrecision
	}
	v, err := roundedProduct(int64(amount), int64(rate), 100_000_000)
	return Money(v), err
}

// AllocateCost allocates remaining cost; a full liquidation consumes all cost.
func AllocateCost(cost Money, sold, total Quantity) (Money, error) {
	if cost < 0 || sold <= 0 || sold > total {
		return 0, ErrPrecision
	}
	if sold == total {
		return cost, nil
	}
	v, err := roundedProduct(int64(cost), int64(sold), int64(total))
	return Money(v), err
}

// UnitCost permits negative diluted cost, but quantity must be positive.
func UnitCost(cost Money, quantity Quantity) (Price, error) {
	if quantity <= 0 {
		return 0, ErrPrecision
	}
	v, err := roundedProduct(int64(cost), 10_000_000_000, int64(quantity))
	return Price(v), err
}

// All callers supply a positive divisor. QuoRem truncates toward zero, so a
// remainder of at least half the divisor moves the result away from zero.
func roundedProduct(a, b, divisor int64) (int64, error) {
	product := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	denominator := big.NewInt(divisor)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(product, denominator, remainder)
	remainder.Abs(remainder).Lsh(remainder, 1)
	if remainder.Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(int64(product.Sign())))
	}
	if !quotient.IsInt64() {
		return 0, ErrPrecision
	}
	return quotient.Int64(), nil
}
