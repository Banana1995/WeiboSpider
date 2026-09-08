package ledger

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func checkPrecisionType[T ~int64](t *testing.T, parse func(string) (T, error), digits int, maximum, minimum string) {
	t.Helper()
	scale := int64(1)
	for range digits {
		scale *= 10
	}
	unit := "0." + strings.Repeat("0", digits-1) + "1"
	for _, tc := range []struct {
		input string
		want  int64
	}{
		{"0", 0}, {"-0", 0}, {"000.0", 0}, {"1", scale},
		{"-1", -scale}, {unit, 1}, {"-" + unit, -1},
		{"0.1", scale / 10}, {"0001.2", 12 * (scale / 10)},
		{maximum, math.MaxInt64}, {minimum, math.MinInt64},
		{strings.Repeat("0", maxDecimalLength), 0},
	} {
		t.Run("parse/"+tc.input, func(t *testing.T) {
			v, err := parse(tc.input)
			if err != nil || int64(v) != tc.want {
				t.Fatalf("parse(%q) = %d, %v; want %d", tc.input, v, err, tc.want)
			}
			var decoded T
			encoded, _ := json.Marshal(tc.input)
			if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != v {
				t.Fatalf("JSON parse(%q) = %d, %v; want %d", tc.input, decoded, err, v)
			}
		})
	}
	bad := []string{
		"", "-", "+", "+1", "--1", "-+1", ".1", "-.1", "1.", "1..0", "1.2.3",
		" 1", "1 ", "\t1", "1\n", "1\r", "1 2", "1e0", "1E2", "1e-2",
		"NaN", "Infinity", "0x10", "1_000", "1,000", "1/2", "\x001", "\u00a01", "\uff11",
		"0." + strings.Repeat("0", digits+1), "0." + strings.Repeat("0", digits) + "1",
		maximum[:len(maximum)-1] + "8", minimum[:len(minimum)-1] + "9",
		strings.Repeat("9", 25), strings.Repeat("0", maxDecimalLength+1), strings.Repeat("0", 10000),
	}
	for _, input := range bad {
		v, err := parse(input)
		if !errors.Is(err, ErrPrecision) || v != 0 {
			t.Errorf("parse(%q) = %d, %v; want zero and ErrPrecision", input, v, err)
		}
		encoded, _ := json.Marshal(input)
		old := T(12345)
		if err := json.Unmarshal(encoded, &old); !errors.Is(err, ErrPrecision) || old != T(12345) {
			t.Errorf("JSON parse(%q) = %d, %v; want unchanged and ErrPrecision", input, old, err)
		}
	}
	for _, input := range []string{"0", "1.23", "-1", "1e2", "null", "true", "false", "[]", "{}", `["1"]`} {
		old := T(12345)
		if err := json.Unmarshal([]byte(input), &old); !errors.Is(err, ErrPrecision) || old != T(12345) {
			t.Errorf("JSON parse(%s) = %d, %v; want unchanged and ErrPrecision", input, old, err)
		}
	}
	for _, input := range []string{"", `"1`, `"1" "2"`, `"\x31"`, `"1" `, ` "1"`, `"1` + "\n" + `"`} {
		old := T(12345)
		err := any(&old).(json.Unmarshaler).UnmarshalJSON([]byte(input))
		if !errors.Is(err, ErrPrecision) || old != T(12345) {
			t.Errorf("direct JSON parse(%q) = %d, %v; want unchanged and ErrPrecision", input, old, err)
		}
	}
	var escaped T
	if err := json.Unmarshal([]byte(`"\u0031"`), &escaped); err != nil || int64(escaped) != scale {
		t.Errorf("escaped JSON decimal = %d, %v", escaped, err)
	}
	var nilTarget *T
	if err := any(nilTarget).(json.Unmarshaler).UnmarshalJSON([]byte(`"0"`)); !errors.Is(err, ErrPrecision) {
		t.Errorf("nil receiver error = %v", err)
	}
}

func TestPrecisionParsing(t *testing.T) {
	t.Run("Money", func(t *testing.T) {
		checkPrecisionType(t, ParseMoney, 2, "92233720368547758.07", "-92233720368547758.08")
	})
	t.Run("Quantity", func(t *testing.T) {
		checkPrecisionType(t, ParseQuantity, 6, "9223372036854.775807", "-9223372036854.775808")
	})
	t.Run("Price", func(t *testing.T) {
		checkPrecisionType(t, ParsePrice, 6, "9223372036854.775807", "-9223372036854.775808")
	})
	t.Run("Rate", func(t *testing.T) {
		checkPrecisionType(t, ParseRate, 8, "92233720368.54775807", "-92233720368.54775808")
	})
}

func checkPrecisionRoundTrip(t *testing.T, raw int64) {
	t.Helper()
	for _, tc := range []struct {
		value interface {
			String() string
			json.Marshaler
		}
		parse func(string) (int64, error)
		dest  json.Unmarshaler
	}{
		{Money(raw), func(s string) (int64, error) { v, e := ParseMoney(s); return int64(v), e }, new(Money)},
		{Quantity(raw), func(s string) (int64, error) { v, e := ParseQuantity(s); return int64(v), e }, new(Quantity)},
		{Price(raw), func(s string) (int64, error) { v, e := ParsePrice(s); return int64(v), e }, new(Price)},
		{Rate(raw), func(s string) (int64, error) { v, e := ParseRate(s); return int64(v), e }, new(Rate)},
	} {
		text := tc.value.String()
		if got, err := tc.parse(text); err != nil || got != raw {
			t.Fatalf("%T roundtrip %d via %q = %d, %v", tc.value, raw, text, got, err)
		}
		encoded, err := json.Marshal(tc.value)
		if err != nil || string(encoded) != `"`+text+`"` {
			t.Fatalf("%T MarshalJSON = %s, %v", tc.value, encoded, err)
		}
		if err := json.Unmarshal(encoded, tc.dest); err != nil {
			t.Fatal(err)
		}
		reencoded, err := json.Marshal(tc.dest)
		if err != nil || string(reencoded) != string(encoded) {
			t.Fatalf("%T JSON roundtrip = %s, %v; want %s", tc.value, reencoded, err, encoded)
		}
	}
}

func TestPrecisionFormatting(t *testing.T) {
	for _, tc := range []struct {
		value interface{ String() string }
		want  string
	}{
		{Money(0), "0.00"}, {Money(1), "0.01"}, {Money(-1), "-0.01"}, {Money(123), "1.23"},
		{Quantity(0), "0.000000"}, {Quantity(1), "0.000001"}, {Quantity(-1234567), "-1.234567"},
		{Price(0), "0.000000"}, {Price(1000000), "1.000000"}, {Price(-1), "-0.000001"},
		{Rate(0), "0.00000000"}, {Rate(1), "0.00000001"}, {Rate(-100000000), "-1.00000000"},
		{Money(math.MaxInt64), "92233720368547758.07"}, {Money(math.MinInt64), "-92233720368547758.08"},
		{Quantity(math.MaxInt64), "9223372036854.775807"}, {Quantity(math.MinInt64), "-9223372036854.775808"},
		{Price(math.MaxInt64), "9223372036854.775807"}, {Price(math.MinInt64), "-9223372036854.775808"},
		{Rate(math.MaxInt64), "92233720368.54775807"}, {Rate(math.MinInt64), "-92233720368.54775808"},
	} {
		if got := tc.value.String(); got != tc.want {
			t.Errorf("%T String() = %q; want %q", tc.value, got, tc.want)
		}
	}
	for _, raw := range []int64{0, 1, -1, 99, -99, 100, -100, 1000000, -1000000, 9007199254740993, -9007199254740993, math.MaxInt64, math.MinInt64} {
		checkPrecisionRoundTrip(t, raw)
	}
}

func TestPrecisionAddSubtract(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(Money, Money) (Money, error)
		a, b Money
		want Money
		bad  bool
	}{
		{"add", AddMoney, 10, 20, 30, false}, {"negative add", AddMoney, -10, -20, -30, false},
		{"cancel", AddMoney, math.MinInt64, math.MaxInt64, -1, false},
		{"add max", AddMoney, math.MaxInt64 - 1, 1, math.MaxInt64, false},
		{"add min", AddMoney, math.MinInt64 + 1, -1, math.MinInt64, false},
		{"add overflow", AddMoney, math.MaxInt64, 1, 0, true},
		{"add underflow", AddMoney, math.MinInt64, -1, 0, true},
		{"add two min", AddMoney, math.MinInt64, math.MinInt64, 0, true},
		{"add min zero", AddMoney, 0, math.MinInt64, math.MinInt64, false},
		{"subtract", SubMoney, 10, 20, -10, false}, {"subtract negative", SubMoney, -10, -20, 10, false},
		{"subtract same min", SubMoney, math.MinInt64, math.MinInt64, 0, false},
		{"subtract same max", SubMoney, math.MaxInt64, math.MaxInt64, 0, false},
		{"subtract max", SubMoney, math.MaxInt64 - 1, -1, math.MaxInt64, false},
		{"subtract min", SubMoney, math.MinInt64 + 1, 1, math.MinInt64, false},
		{"subtract overflow", SubMoney, math.MaxInt64, -1, 0, true},
		{"subtract underflow", SubMoney, math.MinInt64, 1, 0, true},
		{"subtract min from zero", SubMoney, 0, math.MinInt64, 0, true},
		{"subtract min from negative one", SubMoney, -1, math.MinInt64, math.MaxInt64, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn(tc.a, tc.b)
			if got != tc.want || (err != nil) != tc.bad || (tc.bad && !errors.Is(err, ErrPrecision)) {
				t.Fatalf("got %d, %v; want %d, bad=%v", got, err, tc.want, tc.bad)
			}
		})
	}
}

func TestPrecisionProducts(t *testing.T) {
	type productCase struct {
		name       string
		a, b, want int64
		bad        bool
	}
	for _, group := range []struct {
		name  string
		fn    func(int64, int64) (int64, error)
		cases []productCase
	}{
		{"trade", func(a, b int64) (int64, error) { v, e := TradeAmount(Quantity(a), Price(b)); return int64(v), e }, []productCase{
			{"float trap 0.1 times 0.2", 100000, 200000, 2, false},
			{"float trap 1.005", 1000000, 1005000, 101, false},
			{"fractional shares", 125000, 123450000, 1543, false},
			{"micro share", 1, 10000000000, 1, false},
			{"below half cent", 1, 4999999999, 0, false},
			{"half cent", 1, 5000000000, 1, false},
			{"above half cent", 1, 5000000001, 1, false},
			{"zero quantity", 0, math.MaxInt64, 0, false},
			{"zero price", math.MaxInt64, 0, 0, false},
			{"negative quantity", -1, 0, 0, true}, {"negative price", 0, -1, 0, true},
			{"both negative", -1, -1, 0, true},
			{"large intermediate", math.MaxInt64, 10000000000, math.MaxInt64, false},
			{"overflow", math.MaxInt64, 10000000001, 0, true},
			{"maximum product", math.MaxInt64, math.MaxInt64, 0, true},
		}},
		{"convert", func(a, b int64) (int64, error) { v, e := ConvertMoney(Money(a), Rate(b)); return int64(v), e }, []productCase{
			{"exchange rate", 12345, 712345678, 87939, false},
			{"negative amount", -12345, 712345678, -87939, false},
			{"below half", 1, 49999999, 0, false}, {"half", 1, 50000000, 1, false},
			{"above half", 1, 50000001, 1, false},
			{"negative below half", -1, 49999999, 0, false},
			{"negative half", -1, 50000000, -1, false},
			{"negative above half", -1, 50000001, -1, false},
			{"positive one and half", 3, 50000000, 2, false},
			{"negative one and half", -3, 50000000, -2, false},
			{"zero rate", math.MinInt64, 0, 0, false}, {"zero amount", 0, math.MaxInt64, 0, false},
			{"negative rate", 0, -1, 0, true},
			{"max identity", math.MaxInt64, 100000000, math.MaxInt64, false},
			{"min identity", math.MinInt64, 100000000, math.MinInt64, false},
			{"overflow", math.MaxInt64, 100000001, 0, true},
			{"underflow", math.MinInt64, 100000001, 0, true},
		}},
		{"unit cost", func(a, b int64) (int64, error) { v, e := UnitCost(Money(a), Quantity(b)); return int64(v), e }, []productCase{
			{"fractional shares", 1543, 125000, 123440000, false},
			{"negative diluted", -100, 3000000, -333333, false},
			{"negative rounded", -200, 3000000, -666667, false},
			{"below half", 1, 20000000001, 0, false}, {"half", 1, 20000000000, 1, false},
			{"above half", 1, 19999999999, 1, false},
			{"negative below half", -1, 20000000001, 0, false},
			{"negative half", -1, 20000000000, -1, false},
			{"negative above half", -1, 19999999999, -1, false},
			{"zero cost", 0, 1, 0, false}, {"zero quantity", 0, 0, 0, true},
			{"negative quantity", 1, -1, 0, true},
			{"max identity", math.MaxInt64, 10000000000, math.MaxInt64, false},
			{"min identity", math.MinInt64, 10000000000, math.MinInt64, false},
			{"overflow", math.MaxInt64, 9999999999, 0, true},
			{"underflow", math.MinInt64, 9999999999, 0, true},
			{"tiny quantity overflow", 1000000000, 1, 0, true},
		}},
	} {
		t.Run(group.name, func(t *testing.T) {
			for _, tc := range group.cases {
				t.Run(tc.name, func(t *testing.T) {
					got, err := group.fn(tc.a, tc.b)
					if got != tc.want || (err != nil) != tc.bad || (tc.bad && !errors.Is(err, ErrPrecision)) {
						t.Fatalf("got %d, %v; want %d, bad=%v", got, err, tc.want, tc.bad)
					}
				})
			}
		})
	}
}

func TestPrecisionAllocateCost(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cost        Money
		sold, total Quantity
		want        Money
		bad         bool
	}{
		{"third", 100, 1000000, 3000000, 33, false},
		{"fractional shares", 100, 125000, 500000, 25, false},
		{"half cent", 1, 1, 2, 1, false}, {"below half", 1, 1, 3, 0, false},
		{"above half", 1, 2, 3, 1, false},
		{"zero cost", 0, 1, 2, 0, false},
		{"negative cost", -1, 1, 1, 0, true},
		{"zero sold", 100, 0, 10, 0, true}, {"negative sold", 100, -1, 10, 0, true},
		{"oversold", 100, 11, 10, 0, true}, {"zero total", 100, 1, 0, 0, true},
		{"negative total", 100, 1, -1, 0, true}, {"both zero", 0, 0, 0, 0, true},
		{"liquidation", 101, 123456, 123456, 101, false},
		{"max liquidation", math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64, false},
		{"large intermediate", math.MaxInt64, math.MaxInt64 - 1, math.MaxInt64, math.MaxInt64 - 1, false},
		{"max half rounds up", math.MaxInt64, 1, 2, 4611686018427387904, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AllocateCost(tc.cost, tc.sold, tc.total)
			if got != tc.want || (err != nil) != tc.bad || (tc.bad && !errors.Is(err, ErrPrecision)) {
				t.Fatalf("got %d, %v; want %d, bad=%v", got, err, tc.want, tc.bad)
			}
		})
	}
	remaining := Money(100)
	for i, want := range []Money{33, 34, 33} {
		allocated, err := AllocateCost(remaining, 1000000, Quantity(3-i)*1000000)
		if err != nil || allocated != want {
			t.Fatalf("sale %d: got %d, %v; want %d", i, allocated, err, want)
		}
		remaining -= allocated
	}
	if remaining != 0 {
		t.Fatalf("liquidation left cost %d", remaining)
	}
}

func TestPrecisionRoundingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                string
		a, b, divisor, want int64
		bad                 bool
	}{
		{"exact below max", 6148914691236517204, 3, 2, math.MaxInt64 - 1, false},
		{"round beyond max", 6148914691236517205, 3, 2, 0, true},
		{"round to min", -6148914691236517205, 3, 2, math.MinInt64, false},
		{"round beyond min", -274177, 67280421310721, 2, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := roundedProduct(tc.a, tc.b, tc.divisor)
			if got != tc.want || (err != nil) != tc.bad || (tc.bad && !errors.Is(err, ErrPrecision)) {
				t.Fatalf("got %d, %v; want %d, bad=%v", got, err, tc.want, tc.bad)
			}
		})
	}
}

func FuzzPrecisionRoundTrip(f *testing.F) {
	for _, v := range []int64{0, 1, -1, math.MinInt64, math.MaxInt64, 9007199254740993} {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, v int64) { checkPrecisionRoundTrip(t, v) })
}
