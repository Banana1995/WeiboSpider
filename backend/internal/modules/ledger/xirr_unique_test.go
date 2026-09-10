package ledger

import (
	"context"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestXIRRUniqueMultipleCashFlowSigns(t *testing.T) {
	dates := []string{"2021-01-01", "2022-01-01", "2023-01-01", "2024-01-01"}
	for _, tt := range []struct {
		name    string
		amounts []int64
		value   string
	}{
		{"withdraw_redeposit", []int64{-100, 30, -20, 120}, ""},
		{"positive_ten", []int64{-1000, 300, -200, 1188}, "0.100000000000"},
		{"negative_ten", []int64{-1000, 300, -200, 666}, "-0.100000000000"},
		{"reversed_positive", []int64{1188, -200, 300, -1000}, "-0.090909090909"},
		{"zero", []int64{-100, 30, -20, 90}, "0.000000000000"},
		{"zero_proper_prefix", []int64{-100, 100, -20, 30}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var original ReturnMetric
			for _, scale := range []int64{1, -1, 1000000000000000} {
				flows := map[string]*big.Int{}
				for i, amount := range tt.amounts {
					flows[dates[i]] = new(big.Int).Mul(big.NewInt(amount), big.NewInt(scale))
				}
				unique, err := certifyXIRRUnique(t.Context(), dates, flows)
				require.NoError(t, err)
				require.True(t, unique)
				r, err := solveXIRR(t.Context(), dates, flows, "reference")
				require.NoError(t, err)
				require.Equal(t, "reference", r.Status)
				require.Empty(t, r.Reason)
				require.NotNil(t, r.Value)
				if tt.value != "" {
					require.Equal(t, tt.value, *r.Value)
				}
				if scale == 1 {
					original = r
				} else {
					require.Equal(t, original, r)
				}
				// Independently verify both rounding cells with an EXACT rational
				// cubic in annual discount q, not the solver's daily root enclosure.
				for _, cell := range []struct {
					text          string
					divisor, half int64
				}{{*r.Value, 1, 2000000000000}, {*r.Percentage, 100, 20000}} {
					center, ok := new(big.Rat).SetString(cell.text)
					require.True(t, ok)
					center.Quo(center, big.NewRat(cell.divisor, 1))
					for _, side := range []int64{-1, 1} {
						rate := new(big.Rat).Add(center, big.NewRat(side, cell.half))
						q := new(big.Rat).Inv(new(big.Rat).Add(big.NewRat(1, 1), rate))
						npv := new(big.Rat)
						for i := len(dates) - 1; i >= 0; i-- {
							npv.Mul(npv, q)
							npv.Add(npv, new(big.Rat).SetInt(flows[dates[i]]))
						}
						require.Equal(t, -int(side)*flows[dates[3]].Sign(), npv.Sign())
					}
				}
			}
		})
	}
}

func TestXIRRMultipleSignsUnprovenAndRoundingTies(t *testing.T) {
	dates := []string{"2021-01-01", "2022-01-01", "2023-01-01", "2024-01-01"}
	for _, tt := range []struct {
		name    string
		amounts []int64
		unique  bool
	}{
		{"two_roots", []int64{-100, 230, -132}, false},
		{"tangent_zero", []int64{-100, 200, -100}, false},
		{"three_roots", []int64{-1000, 3600, -4310, 1716}, false},
		{"zero_and_other_roots", []int64{-100, 230, -132, 2}, false},
		{"tangent_and_other_root", []int64{-100, 400, -500, 200}, false},
		// P(q)=(a*q-b)*(2*q*q+q+2), whose latter factor is positive.
		// The cumulative certificate holds, but the exact root is a display tie.
		{"positive_percent_tie", []int64{-40000, 20002, -19999, 40002}, true},
		{"negative_percent_tie", []int64{-40000, 19998, -20001, 39998}, true},
		{"positive_rate_tie", []int64{-4000000000000, 2000000000002, -1999999999999, 4000000000002}, true},
		{"negative_rate_tie", []int64{-4000000000000, 1999999999998, -2000000000001, 3999999999998}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, scale := range []int64{1, 10000000000} {
				flows := map[string]*big.Int{}
				for i, amount := range tt.amounts {
					flows[dates[i]] = new(big.Int).Mul(big.NewInt(amount), big.NewInt(scale))
				}
				d := dates[:len(tt.amounts)]
				unique, err := certifyXIRRUnique(t.Context(), d, flows)
				require.NoError(t, err)
				require.Equal(t, tt.unique, unique)
				r, err := solveXIRR(t.Context(), d, flows, "available")
				require.NoError(t, err)
				reason := "possible_multiple_roots"
				if tt.unique {
					reason = "precision_unresolved"
				}
				require.Equal(t, reason, r.Reason)
				require.Nil(t, r.Value)
				require.Nil(t, r.Percentage)
			}
		})
	}
}

func TestXIRRUniqueUnequalDatesAndBounds(t *testing.T) {
	for _, dates := range [][]string{
		{"2020-02-28", "2020-02-29", "2021-01-01", "2023-01-01"},
		{"0001-01-01", "0001-01-02", "2024-02-29", "9999-12-31"},
	} {
		for _, amounts := range [][]int64{{-100, 30, -20, 120}, {-120, 20, -30, 100}} {
			flows := map[string]*big.Int{}
			for i, amount := range amounts {
				flows[dates[i]] = big.NewInt(amount)
			}
			r, err := solveXIRR(t.Context(), dates, flows, "available")
			require.NoError(t, err)
			require.NotNil(t, r.Value, r.Reason)
			certain, err := certifyXIRRRounding(t.Context(), dates, flows, r)
			require.NoError(t, err)
			require.True(t, certain)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := certifyXIRRUnique(ctx, nil, nil)
	require.ErrorIs(t, err, context.Canceled)
	_, err = certifyXIRRUnique(t.Context(), make([]string, 10003), nil)
	require.ErrorIs(t, err, ErrQuery)
}
