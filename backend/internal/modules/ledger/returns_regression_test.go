package ledger

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReturnsInclusivePeriodSyntheticComparison(t *testing.T) {
	// Public synthetic comparison, not a transcription of a financial account.
	o, m, c := returnPoint("2024-01-01", 1000000), returnPoint("2025-05-02", 2000000), returnPoint("2025-09-01", 2300000)
	deposit := Money(1000000)
	m.Flow = &deposit
	points := []BasisPoint{o, m, c}
	r, err := calculateReturns(t.Context(), AnalysisBasis{Points: points, Closing: &c}, "", "")
	require.NoError(t, err)
	require.Equal(t, 609, r.Days)
	require.Equal(t, 610, r.PeriodDays)
	require.Equal(t, "3000.00", *r.Profit.Value)
	require.Equal(t, "12000", *r.Denominator)
	require.Equal(t, "0.250000000000", *r.Dietz.Value)
	require.Equal(t, "25.00", *r.Dietz.Percentage)
	require.Len(t, r.Flows, 1)
	require.Equal(t, 122, r.Flows[0].WeightDays)
	require.Equal(t, 610, r.Flows[0].PeriodDays)
	require.Equal(t, []InvestorFlow{{o.Date, "-10000.00"}, {m.Date, "-10000.00"}, {c.Date, "23000.00"}}, r.InvestorFlows)
	require.Equal(t, r.Dietz, r.Curve[2].Dietz)
	require.Equal(t, "0.150000000000", *r.TWR.Value)
	annual, err := strconv.ParseFloat(*r.TWRAnnualized.Value, 64)
	require.NoError(t, err)
	require.InDelta(t, math.Pow(1.15, 365.0/609)-1, annual, 1e-12)
	xirr, err := strconv.ParseFloat(*r.XIRR.Value, 64)
	require.NoError(t, err)
	require.InDelta(t, 0, -10000-10000/math.Pow(1+xirr, 487.0/365)+23000/math.Pow(1+xirr, 609.0/365), 1e-7)
	encoded, err := json.Marshal(r)
	require.NoError(t, err)
	var wire struct {
		Days       int `json:"days"`
		PeriodDays int `json:"period_days"`
	}
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Equal(t, 609, wire.Days)
	require.Equal(t, 610, wire.PeriodDays)

	// The comparison endpoint is also checked as an interior curve node. Every
	// node must agree with a fresh prefix, not an overwritten summary endpoint.
	later := returnPoint("2025-09-02", 2310000)
	points = append(points, later)
	for _, from := range []string{"", "2024-01-02"} {
		r, err = calculateReturns(t.Context(), AnalysisBasis{Opening: &o, Points: points, Closing: &later}, from, "")
		require.NoError(t, err)
		for i := 1; i < len(points); i++ {
			prefix, err := calculateMoneyReturns(t.Context(), AnalysisBasis{Opening: &o, Points: points[:i+1], Closing: &points[i]}, from, "")
			require.NoError(t, err)
			require.Equal(t, prefix.Profit, r.Curve[i].Profit)
			require.Equal(t, prefix.Dietz, r.Curve[i].Dietz)
		}
		require.Equal(t, "25.00", *r.Curve[2].Dietz.Percentage)
		require.Equal(t, r.Dietz, r.Curve[3].Dietz)
	}
}

func TestReturnsInclusivePeriodCalendarAndMissingEndpoints(t *testing.T) {
	for _, tt := range []struct {
		from, to string
		days     int
	}{
		{"2024-02-28", "2024-03-01", 2},
		{"2023-12-31", "2024-01-01", 1},
		{"1900-02-28", "1900-03-01", 1},
		{"2000-02-28", "2000-03-01", 2},
		{"2021-01-02", "2022-01-01", 364},
		{"2021-01-01", "2022-01-01", 365},
		{"0001-01-01", "9999-12-31", 3652058},
		{"2024-02-29", "2024-02-29", 0},
	} {
		t.Run(tt.from+"/"+tt.to, func(t *testing.T) {
			o, c := returnPoint(tt.from, 10000), returnPoint(tt.to, 11000)
			r, err := calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, c}, Closing: &c}, "", "")
			require.NoError(t, err)
			require.Equal(t, tt.days, r.Days)
			require.Equal(t, tt.days+1, r.PeriodDays)
			if tt.days == 0 {
				require.Equal(t, "no_interval", r.Dietz.Reason)
				require.Equal(t, "no_interval", r.XIRR.Reason)
				require.Equal(t, "no_interval", r.TWRAnnualized.Reason)
			} else {
				// Inclusive weighting does not alter a no-flow holding return.
				require.Empty(t, r.Flows)
				require.Equal(t, "100", *r.Denominator)
				require.Equal(t, "0.100000000000", *r.Dietz.Value)
				require.Equal(t, r.Dietz, r.Curve[1].Dietz)
				if tt.days < 365 {
					require.Contains(t, r.Warnings, "short_period_extrapolation")
				} else {
					require.NotContains(t, r.Warnings, "short_period_extrapolation")
				}
				if tt.days == 1 {
					// TWR's existing exact integer-power path is not range-limited
					// like the numerical XIRR search: this is (11/10)^365-1.
					require.Equal(t, "out_of_solver_range", r.XIRR.Reason)
					require.Equal(t, "1283305580313351.696899448008", *r.TWRAnnualized.Value)
				} else {
					require.Equal(t, r.XIRR, r.TWRAnnualized)
				}
			}
		})
	}
	o := returnPoint("2024-01-01", 10000)
	for _, b := range []AnalysisBasis{{}, {Points: []BasisPoint{o}}, {Closing: &o}} {
		r, err := calculateReturns(t.Context(), b, "", "")
		require.NoError(t, err)
		require.Zero(t, r.PeriodDays)
		require.Equal(t, "unavailable", r.Dietz.Status)
	}
	r, err := calculateReturns(t.Context(), AnalysisBasis{Opening: &o, Closing: &o}, "2024-02-01", "")
	require.NoError(t, err)
	require.Equal(t, -30, r.Days)
	require.Zero(t, r.PeriodDays)
	require.Equal(t, "no_interval", r.Dietz.Reason)
}

func TestReturnsWithdrawRedepositXIRR(t *testing.T) {
	o, w, d, c := returnPoint("2021-01-01", 100000), returnPoint("2022-01-01", 80000), returnPoint("2023-01-01", 100000), returnPoint("2024-01-01", 118800)
	withdrawal, deposit := Money(-30000), Money(20000)
	w.Flow, d.Flow = &withdrawal, &deposit
	r, err := calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, w, d, c}, Closing: &c}, "", "")
	require.NoError(t, err)
	require.Equal(t, []InvestorFlow{{o.Date, "-1000.00"}, {w.Date, "300.00"}, {d.Date, "-200.00"}, {c.Date, "1188.00"}}, r.InvestorFlows)
	require.Equal(t, "available", r.XIRR.Status)
	require.Equal(t, "0.100000000000", *r.XIRR.Value)
	require.Equal(t, "10.00", *r.XIRR.Percentage)
	require.Equal(t, "288.00", *r.Profit.Value)
	require.Equal(t, r.Dietz, r.Curve[3].Dietz)
}
