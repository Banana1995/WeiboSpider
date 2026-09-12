package ledger

import (
	"context"
	"math"
	"math/big"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func returnPoint(date string, assets Money) BasisPoint {
	return BasisPoint{Date: date, Assets: &assets, Selected: true, Status: "reported", SourceDate: date, SourceID: "synthetic", SourceVersion: "1"}
}

func TestXIRRExactAnnualHalfAwayRegression(t *testing.T) {
	for _, tt := range []struct {
		name              string
		close             int64
		value, percentage string
	}{
		{"positive_half", 20001, "0.000050000000", "0.01"},
		{"negative_half", 19999, "-0.000050000000", "-0.01"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dates := []string{"2021-01-01", "2022-01-01"}
			r, err := solveXIRR(t.Context(), dates, map[string]*big.Int{dates[0]: big.NewInt(-20000), dates[1]: big.NewInt(tt.close)}, "available")
			require.NoError(t, err)
			require.NotNil(t, r.Value, r.Reason)
			t.Logf("cashflows=[-20000,%d] days=365 value=%s percentage=%s", tt.close, *r.Value, *r.Percentage)
			require.Equal(t, tt.value, *r.Value)
			require.Equal(t, tt.percentage, *r.Percentage)
		})
	}
}

func TestReturnsFixedExamples(t *testing.T) {
	for _, tt := range []struct {
		name, end, profit, dietz, xirr string
		close                          Money
	}{
		{"annual_ten_percent", "2021-01-01", "10.00", "0.100000000000", "0.100000000000", 11000},
		{"annual_loss", "2021-01-01", "-10.00", "-0.100000000000", "-0.100000000000", 9000},
		{"zero", "2021-01-01", "0.00", "0.000000000000", "0.000000000000", 10000},
		{"leap_actual_365", "2022-01-01", "21.00", "0.210000000000", "0.100000000000", 12100},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o, c := returnPoint("2020-01-02", 10000), returnPoint(tt.end, tt.close)
			r, err := calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, c}, Closing: &c}, "", "")
			require.NoError(t, err)
			require.Equal(t, tt.profit, *r.Profit.Value)
			require.Equal(t, tt.dietz, *r.Dietz.Value)
			require.NotNil(t, r.XIRR.Value, r.XIRR.Reason)
			require.Equal(t, tt.xirr, *r.XIRR.Value)
		})
	}
}

func TestReturnsBaselineAndCustomBoundariesHTTP(t *testing.T) {
	f := reportedFixture(t)
	basisEntry(t, f, "manual-initial-flow", "2019-12-31", "cash_flow", `"50"`, "null")
	basisEntry(t, f, "manual-base", "2020-01-01", "cash_flow", `"100"`, `"100"`)
	basisEntry(t, f, "manual-base-last", "2020-01-01", "cash_flow", `"10"`, `"110"`)
	basisEntry(t, f, "manual-flow", "2020-01-02", "cash_flow", `"20"`, `"130"`)
	basisEntry(t, f, "manual-end", "2020-01-03", "cash_flow", `"-30"`, `"105"`)
	basisEntry(t, f, "manual-later-log", "2020-02-01", "log", "null", "null")
	b := basisRead(t, f, "a", "reported", "")
	r := b.Returns
	require.Nil(t, b.Opening) // T02's original from=0001 semantics are unchanged.
	require.Equal(t, "2020-01-01", r.EffectiveFrom)
	require.Equal(t, "2020-01-03", r.EffectiveTo)
	require.Equal(t, b.Revision, r.Revision)
	require.Equal(t, "-10.00", r.NetFlow)
	require.Equal(t, "5.00", *r.Profit.Value)
	require.Equal(t, 2, r.Days)
	require.Equal(t, 3, r.PeriodDays)
	require.Equal(t, "350/3", *r.Denominator)
	require.Equal(t, "0.042857142857", *r.Dietz.Value)
	require.Len(t, r.Flows, 2)
	require.Equal(t, []int{1, 0}, []int{r.Flows[0].WeightDays, r.Flows[1].WeightDays})
	require.Equal(t, []int{3, 3}, []int{r.Flows[0].PeriodDays, r.Flows[1].PeriodDays})
	require.Equal(t, []InvestorFlow{{"2020-01-01", "-110.00"}, {"2020-01-02", "-20.00"}, {"2020-01-03", "135.00"}}, r.InvestorFlows)
	custom := basisRead(t, f, "a", "reported", "&from=2020-01-02&to=2020-01-03").Returns
	require.Equal(t, r.Profit, custom.Profit)
	require.Equal(t, r.Dietz, custom.Dietz)
	require.Equal(t, r.PeriodDays, custom.PeriodDays)
	require.Equal(t, "custom", custom.StartMode)
	one := basisRead(t, f, "a", "reported", "&from=2020-01-03&to=2020-01-03").Returns
	require.Equal(t, 1, one.Days)
	require.Equal(t, 2, one.PeriodDays)
	require.Equal(t, 0, one.Flows[0].WeightDays)
	require.Equal(t, 2, one.Flows[0].PeriodDays)
	require.Equal(t, "130", *one.Denominator)
	require.Equal(t, "5.00", *one.Profit.Value)
	missing := basisRead(t, f, "a", "reported", "&from=2020-01-01").Returns
	require.Equal(t, "missing_opening", missing.Profit.Reason)
	missing = basisRead(t, f, "a", "reported", "&from=0001-01-01").Returns
	require.Equal(t, "missing_opening", missing.XIRR.Reason)
	require.NotEqual(t, b.Revision, missing.Revision)
	empty := basisRead(t, f, "a", "reported", "&from=2020-03-01").Returns
	require.Equal(t, "no_interval", empty.Profit.Reason)
	require.Equal(t, 0, empty.PeriodDays)
}

func TestReturnsMissingCarryAndTrust(t *testing.T) {
	f := reportedFixture(t)
	require.Equal(t, "missing_opening", basisRead(t, f, "a", "reported", "").Returns.Profit.Reason)
	basisEntry(t, f, "manual-first", "2020-01-01", "cash_flow", `"20"`, "null")
	require.Nil(t, basisRead(t, f, "a", "reported", "").Returns.Profit.Value)
	basisEntry(t, f, "manual-base", "2020-01-02", "asset", "null", `"100"`)
	require.Equal(t, "no_interval", basisRead(t, f, "a", "reported", "").Returns.Profit.Reason)
	basisEntry(t, f, "manual-carry", "2020-01-03", "cash_flow", `"20"`, "null")
	r := basisRead(t, f, "a", "reported", "").Returns
	require.Equal(t, "0.00", *r.Profit.Value)
	require.Equal(t, "reference", r.Profit.Status)
	require.Contains(t, r.Warnings, "carried_assets_unchanged")
	require.Equal(t, "reference", r.TWR.Status)
	require.Equal(t, "0.000000000000", *r.TWR.Value)
	require.Contains(t, r.Warnings, "twr_estimated_assets")
	require.Equal(t, "2020-01-02", r.Closing.SourceDate)
	basisEntry(t, f, "manual-close", "2020-02-01", "asset", "null", `"120"`)
	r = basisRead(t, f, "a", "reported", "&from=2020-01-15").Returns
	require.Equal(t, "reference", r.Dietz.Status)
	require.Equal(t, "2020-01-14", r.EffectiveFrom)
	for _, status := range []string{"stale", "untracked", "observed"} {
		t.Run(status, func(t *testing.T) {
			o, c := returnPoint("2020-01-01", 10000), returnPoint("2021-01-01", 11000)
			c.Status = status
			middle := returnPoint("2020-06-01", 10500)
			middle.Status = "stale"
			r, err := calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, middle, c}, Closing: &c}, "", "")
			require.NoError(t, err)
			if status == "observed" {
				require.Equal(t, "available", r.Profit.Status) // unrelated intermediate stale does not block endpoints
				require.Contains(t, r.Warnings, "sampled_valuation_not_daily_close")
			} else {
				require.Equal(t, status+"_endpoint", r.Profit.Reason)
				require.Nil(t, r.XIRR.Value)
			}
		})
	}
}

func TestReturnsExactHugeMoneyRoundingAndZeroDenominator(t *testing.T) {
	o, c := returnPoint("2020-01-01", 0), returnPoint("2020-01-03", 0)
	r, err := calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, c}, Closing: &c}, "", "")
	require.NoError(t, err)
	require.Equal(t, "0.00", *r.Profit.Value)
	require.Equal(t, "nonpositive_denominator", r.Dietz.Reason)
	require.Equal(t, "indeterminate_all_zero", r.XIRR.Reason)
	o, c = returnPoint("2020-01-01", math.MaxInt64), returnPoint("2021-01-01", math.MaxInt64)
	points := []BasisPoint{o}
	for i := 0; i < 3; i++ {
		amount := Money(math.MaxInt64)
		points = append(points, BasisPoint{Date: "2020-06-01", Flow: &amount})
	}
	points = append(points, c)
	r, err = calculateReturns(t.Context(), AnalysisBasis{Points: points, Closing: &c}, "", "")
	require.NoError(t, err)
	require.Equal(t, "276701161105643274.21", r.NetFlow)
	require.Equal(t, "-276701161105643274.21", *r.Profit.Value)
	require.NotNil(t, r.XIRR.Value, r.XIRR.Reason)
	for _, amount := range []int64{5, -5} {
		m := returnValue(big.NewRat(amount, 1000), 2, "available")
		want := "0.01"
		if amount < 0 {
			want = "-0.01"
		}
		require.Equal(t, want, *m.Value)
	}
	require.Equal(t, "0.00", *returnValue(big.NewRat(-1, 1000), 2, "available").Value)
	// Do not double round a rate just below the percentage display tie.
	m := returnValue(big.NewRat(499999996, 10000000000000), 12, "available")
	require.Equal(t, "0.000050000000", *m.Value)
	require.Equal(t, "0.00", *m.Percentage)
	withdraw := Money(-math.MaxInt64)
	r, err = calculateReturns(t.Context(), AnalysisBasis{Opening: &o, Closing: &c, Points: []BasisPoint{{Date: "2020-01-02", Flow: &withdraw}, {Date: "2020-01-02", Flow: &withdraw}, c}}, "2020-01-02", "")
	require.NoError(t, err)
	require.Equal(t, "nonpositive_denominator", r.Dietz.Reason)
}

func TestXIRRBoundedSolver(t *testing.T) {
	for _, tt := range []struct {
		name           string
		dates, amounts []string
		value, reason  string
	}{
		{"ten", []string{"2021-01-01", "2022-01-01"}, []string{"-100", "110"}, "0.100000000000", ""},
		{"negative", []string{"2021-01-01", "2022-01-01"}, []string{"-100", "90"}, "-0.100000000000", ""},
		{"zero", []string{"2021-01-01", "2022-01-01"}, []string{"-100", "100"}, "0.000000000000", ""},
		{"known_two_roots", []string{"2021-01-01", "2022-01-01", "2023-01-01"}, []string{"-100", "230", "-132"}, "", "possible_multiple_roots"},
		{"tangent_root", []string{"2021-01-01", "2022-01-01", "2023-01-01"}, []string{"-100", "200", "-100"}, "", "possible_multiple_roots"},
		{"nonconventional_no_root_not_proven", []string{"2021-01-01", "2022-01-01", "2023-01-01"}, []string{"-100", "20", "-100"}, "", "possible_multiple_roots"},
		{"no_sign_change", []string{"2021-01-01", "2022-01-01"}, []string{"100", "110"}, "", "no_solution"},
		{"minus_100_percent_is_not_finite", []string{"2021-01-01", "2022-01-01"}, []string{"-100", "0"}, "", "no_solution"},
		{"allzero", []string{"2021-01-01", "2022-01-01"}, []string{"0", "0"}, "", "indeterminate_all_zero"},
		{"range", []string{"2021-01-01", "2021-01-02"}, []string{"-1", "100000000"}, "", "out_of_solver_range"},
		{"large_exact_annual", []string{"2021-01-01", "2022-01-01"}, []string{"-1", "100000000"}, "99999999.000000000000", ""},
		{"precision_guard", []string{"2021-01-01", "2022-01-02"}, []string{"-1", "100000000"}, "", "not_converged"},
		{"huge_ten", []string{"2021-01-01", "2022-01-01"}, []string{"-8000000000000000000", "8800000000000000000"}, "0.100000000000", ""},
		{"huge_cent", []string{"2021-01-01", "2022-01-01"}, []string{"-9223372036854775806", "9223372036854775807"}, "0.000000000000", ""},
		{"leading_zero_long_calendar", []string{"0001-01-01", "9998-01-01", "9999-01-01"}, []string{"0", "-100", "110"}, "0.100000000000", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			flows := map[string]*big.Int{}
			for i, date := range tt.dates {
				flows[date], _ = new(big.Int).SetString(tt.amounts[i], 10)
			}
			r, err := solveXIRR(t.Context(), tt.dates, flows, "available")
			require.NoError(t, err)
			require.Equal(t, tt.reason, r.Reason)
			if tt.reason == "" {
				require.NotNil(t, r.Value)
				require.Equal(t, tt.value, *r.Value)
			} else {
				require.Nil(t, r.Value)
			}
		})
	}
	// Actual leap-year day count, not a nominal year or overflowing duration.
	require.Equal(t, 366, civilDay("2021-01-01")-civilDay("2020-01-01"))
	require.Equal(t, 3652058, civilDay("9999-12-31")-civilDay("0001-01-01"))
	flows := map[string]*big.Int{"2020-01-01": big.NewInt(-100), "2021-01-01": big.NewInt(110)}
	r, err := solveXIRR(t.Context(), []string{"2020-01-01", "2021-01-01"}, flows, "available")
	require.NoError(t, err)
	n, err := strconv.ParseFloat(*r.Value, 64)
	require.NoError(t, err)
	require.InDelta(t, math.Pow(1.1, 365.0/366)-1, n, 1e-12)
}

func TestReturnsCancellationAndCapacity(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := calculateReturns(ctx, AnalysisBasis{}, "", "")
	require.ErrorIs(t, err, context.Canceled)
	_, err = solveXIRR(ctx, nil, nil, "available")
	require.ErrorIs(t, err, context.Canceled)
	_, err = calculateReturns(t.Context(), AnalysisBasis{Points: make([]BasisPoint, 10001)}, "", "")
	require.ErrorIs(t, err, ErrQuery)
	dates, flows := []string{}, map[string]*big.Int{}
	start, _ := time.Parse(time.DateOnly, "2000-01-01")
	for i := 0; i < 10002; i++ {
		date := start.AddDate(0, 0, i).Format(time.DateOnly)
		dates = append(dates, date)
		flows[date] = big.NewInt(-1)
	}
	flows[dates[len(dates)-1]] = big.NewInt(10002)
	r, err := solveXIRR(t.Context(), dates, flows, "available")
	require.NoError(t, err)
	require.NotNil(t, r.Value, r.Reason)
	// The full supported capacity also accepts repeated withdrawals/redeposits
	// with an exact prefix certificate; it is not limited to one sign change.
	flows[dates[0]] = big.NewInt(-10002)
	for i := 1; i < len(dates)-1; i++ {
		flows[dates[i]] = big.NewInt(int64(2*(i%2) - 1))
	}
	flows[dates[len(dates)-1]] = big.NewInt(10003)
	r, err = solveXIRR(t.Context(), dates, flows, "available")
	require.NoError(t, err)
	require.NotNil(t, r.Value, r.Reason)
	_, err = solveXIRR(t.Context(), append(dates, "9999-01-01"), flows, "available")
	require.ErrorIs(t, err, ErrQuery)
}

func TestXIRRRoundingCertification(t *testing.T) {
	for _, tt := range []struct {
		name              string
		days              []int
		amounts           []int64
		value, percentage string
	}{
		{"annual_positive_below", []int{0, 365}, []int64{-2000000, 2000099}, "0.000049500000", "0.00"},
		{"annual_positive_above", []int{0, 365}, []int64{-2000000, 2000101}, "0.000050500000", "0.01"},
		{"annual_negative_inside", []int{0, 365}, []int64{-2000000, 1999901}, "-0.000049500000", "0.00"},
		{"annual_negative_outside", []int{0, 365}, []int64{-2000000, 1999899}, "-0.000050500000", "-0.01"},
		{"730_positive_half", []int{0, 730}, []int64{-400000000, 400040001}, "", ""},
		{"730_negative_half", []int{0, 730}, []int64{-400000000, 399960001}, "", ""},
		{"730_positive_below", []int{0, 730}, []int64{-400000000, 400040000}, "0.000049998750", "0.00"},
		{"730_positive_above", []int{0, 730}, []int64{-400000000, 400040002}, "0.000050001250", "0.01"},
		{"730_negative_inside", []int{0, 730}, []int64{-400000000, 399960002}, "-0.000049998750", "0.00"},
		{"730_negative_outside", []int{0, 730}, []int64{-400000000, 399960000}, "-0.000050001250", "-0.01"},
		// NPV=(A*q-B)*(q+1), where q=(1+r)^-1, hence r=A/B-1.
		// These are true ties with three nonzero cashflows, not the annual fast path.
		{"three_positive_percent_half", []int{0, 365, 730}, []int64{-20000, 1, 20001}, "", ""},
		{"three_negative_percent_half", []int{0, 365, 730}, []int64{-20000, -1, 19999}, "", ""},
		{"three_positive_rate_half", []int{0, 365, 730}, []int64{-2000000000000, 1, 2000000000001}, "", ""},
		{"three_negative_rate_half", []int{0, 365, 730}, []int64{-2000000000000, -1, 1999999999999}, "", ""},
		{"annual_near_minus_one", []int{0, 365}, []int64{-math.MaxInt64, 1}, "-1.000000000000", "-100.00"},
		{"730_near_minus_one", []int{0, 730}, []int64{-math.MaxInt64, 1}, "-0.999999999671", "-100.00"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			origin, _ := time.Parse(time.DateOnly, "2021-01-01")
			dates, flows := []string{}, map[string]*big.Int{}
			for i, day := range tt.days {
				date := origin.AddDate(0, 0, day).Format(time.DateOnly)
				dates = append(dates, date)
				flows[date] = big.NewInt(tt.amounts[i])
			}
			r, err := solveXIRR(t.Context(), dates, flows, "available")
			require.NoError(t, err)
			if tt.value == "" {
				require.Equal(t, "precision_unresolved", r.Reason)
				require.Nil(t, r.Value)
				require.Nil(t, r.Percentage)
				t.Logf("%s: %s (both output values null)", tt.name, r.Reason)
			} else {
				require.NotNil(t, r.Value, r.Reason)
				require.Equal(t, tt.value, *r.Value)
				require.Equal(t, tt.percentage, *r.Percentage)
			}
		})
	}
}

func TestXIRRConventionalFlowsScalingAndIndependentCertificate(t *testing.T) {
	dates := []string{"2021-01-01", "2021-06-30", "2022-01-01"} // day 0, 180, 365
	var first ReturnMetric
	for _, scale := range []int64{1, 10000000000000} {
		flows := map[string]*big.Int{}
		for i, amount := range []int64{-100000, 50000, 60000} {
			flows[dates[i]] = new(big.Int).Mul(big.NewInt(amount), big.NewInt(scale))
		}
		r, err := solveXIRR(t.Context(), dates, flows, "available")
		require.NoError(t, err)
		require.NotNil(t, r.Value, r.Reason)
		t.Logf("day0=-1000 day180=500 day365=600 scale=%d value=%s percentage=%s", scale, *r.Value, *r.Percentage)
		n, err := strconv.ParseFloat(*r.Value, 64)
		require.NoError(t, err)
		require.InDelta(t, 0.13256378707, n, 1e-11)
		require.Equal(t, "13.26", *r.Percentage)
		if scale == 1 {
			first = r
		} else {
			require.Equal(t, first, r)
		}
	}
	// A false floating bracket below the tie must not certify the wrong side.
	dates = []string{"2021-01-01", "2023-01-01"}
	for _, scale := range []int64{1, 10000000000} {
		flows := map[string]*big.Int{
			dates[0]: new(big.Int).Mul(big.NewInt(-400000000), big.NewInt(scale)),
			dates[1]: new(big.Int).Mul(big.NewInt(400040001), big.NewInt(scale)),
		}
		for _, guess := range []float64{0.00004999999999453, 0.000050000000005} {
			candidate := returnValue(new(big.Rat).SetFloat64(guess), 12, "available")
			certain, err := certifyXIRRRounding(t.Context(), dates, flows, candidate)
			require.NoError(t, err)
			require.False(t, certain)
		}
		r, err := solveXIRR(t.Context(), dates, flows, "available")
		require.NoError(t, err)
		require.Equal(t, "precision_unresolved", r.Reason)
		for _, sign := range []int64{-1, 1} {
			boundary := new(big.Rat).Add(big.NewRat(1, 20000), big.NewRat(sign, 1000000000000000000))
			got, err := xirrSignAtRate(t.Context(), dates, flows, boundary)
			require.NoError(t, err)
			require.Equal(t, -int(sign), got) // exact signs only 1e-18 away from the tie
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := xirrSignAtRate(ctx, dates, map[string]*big.Int{}, big.NewRat(1, 10))
	require.ErrorIs(t, err, context.Canceled)
}

func TestReturnsPureCapitalAndExactSameDayNet(t *testing.T) {
	f := reportedFixture(t)
	basisEntry(t, f, "manual-base", "2021-01-01", "asset", "null", `"100"`)
	basisEntry(t, f, "manual-deposit", "2021-06-01", "cash_flow", `"50"`, `"150"`)
	basisEntry(t, f, "manual-withdraw", "2021-09-01", "cash_flow", `"-20"`, `"130"`)
	basisEntry(t, f, "manual-big-in", "2022-01-01", "cash_flow", `"92233720368547758.07"`, "null")
	basisEntry(t, f, "manual-big-out", "2022-01-01", "cash_flow", `"-92233720368547758.07"`, `"130"`)
	r := basisRead(t, f, "a", "reported", "").Returns
	require.Equal(t, "0.00", *r.Profit.Value)
	require.Equal(t, "0.000000000000", *r.Dietz.Value)
	require.Equal(t, "0.000000000000", *r.XIRR.Value)
	require.Equal(t, "30.00", r.NetFlow)
	require.Equal(t, InvestorFlow{"2022-01-01", "130.00"}, r.InvestorFlows[len(r.InvestorFlows)-1])
	require.Len(t, r.Flows, 4)
	// A later same-day flow does not replace the explicit daily asset baseline.
	basisEntry(t, f, "manual-baseline-carry", "2021-01-01", "cash_flow", `"1"`, "null")
	r = basisRead(t, f, "a", "reported", "").Returns
	require.Equal(t, "available", r.Profit.Status)
	require.Equal(t, "reported", r.Opening.Status)
	require.Equal(t, "30.00", r.NetFlow)
}

func TestReturnsReadOnlyCorrectionAndVoid(t *testing.T) {
	f := reportedFixture(t)
	basisEntry(t, f, "manual-open", "2020-01-02", "asset", "null", `"100"`)
	basisEntry(t, f, "manual-close", "2021-01-01", "asset", "null", `"110"`)
	before := f.snapshot(t)
	b := basisRead(t, f, "a", "reported", "")
	require.Equal(t, "0.100000000000", *b.Returns.XIRR.Value)
	f.request(t, "HEAD", "/accounts/a/analysis-basis", "", "", 200)
	require.Equal(t, before, f.snapshot(t))
	for _, q := range []string{"track=reported&from=", "track=reported&to=2099-01-01", "track=reported&from=2020-02-30", "track=reported&from=2021-01-01&to=2020-01-01", "track=reported&track=holdings", "track=reported&assets=100", "track=reported&from=2020-01-01&from=2020-01-02"} {
		f.request(t, "GET", "/accounts/a/analysis-basis?"+q, "", "", 400)
	}
	f.request(t, "POST", "/accounts/a/analysis-basis?track=reported", "", "{}", 405)
	f.request(t, "PUT", "/accounts/a/records/manual-close", "edit-return", `{"expected_version":"1","reason":"Synthetic","entry":{"kind":"asset","date":"2021-01-01","total_assets":"90"}}`, 200)
	changed := basisRead(t, f, "a", "reported", "&since_revision="+b.ChangeRevision)
	require.NotEqual(t, b.Returns.Revision, changed.Returns.Revision)
	require.Equal(t, "-10.00", *changed.Returns.Profit.Value)
	require.Equal(t, "10.00", *b.Returns.Profit.Value)
	f.request(t, "DELETE", "/accounts/a/records/manual-open", "void-return", `{"expected_version":"1","reason":"Synthetic"}`, 200)
	require.Equal(t, "no_interval", basisRead(t, f, "a", "reported", "").Returns.Profit.Reason)
	historyCount(t, f.store, 0)
}

func TestReturnsFixedAssetsAndFlowsRemainIndependentOfCurrentCash(t *testing.T) {
	f := newHTTPFixture(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	f.store.now = func() time.Time { return now }
	f.account(t, "a", "CNY", "100.00", nil)
	mux := historyMux(f.store)
	sampleValuation(t, f.store, "a", Handler{})
	now = now.AddDate(0, 0, 1)
	basisEntry(t, f, "manual-in", "2026-01-02", "cash_flow", `"20"`, "null")
	basisEntry(t, f, "manual-out", "2026-01-02", "cash_flow", `"-10"`, "null")
	basisEntry(t, f, "manual-note", "2026-01-02", "log", "null", "null")
	putSource(t, f.store, "a", "new-cash", "1", 11500)
	closing := sampleValuation(t, f.store, "a", Handler{})
	b := basisRead(t, f, "a", "holdings", "")
	require.Equal(t, "5.00", *b.Returns.Profit.Value)
	require.Equal(t, "10.00", b.Returns.NetFlow)
	require.Len(t, b.Returns.Flows, 2)
	require.Equal(t, "100", *b.Returns.Denominator)
	require.Equal(t, "0.050000000000", *b.Returns.Dietz.Value)
	old := historyRequest(t, mux, "GET", "a/valuations/"+closing.HistoryID, 200)
	now = now.AddDate(0, 0, 1)
	basisEntry(t, f, "manual-later", "2026-01-03", "cash_flow", `"1"`, "null")
	require.Equal(t, "reference", basisRead(t, f, "a", "holdings", "").Returns.Profit.Status)
	f.request(t, "PUT", "/accounts/a/records/manual-out", "correct", `{"expected_version":"1","reason":"correct flow","entry":{"kind":"cash_flow","date":"2026-01-02","flow":"-9"}}`, 200)
	require.Equal(t, "4.00", *basisRead(t, f, "a", "", "").Returns.Profit.Value)
	require.Equal(t, "115.00", f.get(t, "/accounts/a")["cash"])
	require.Equal(t, old, historyRequest(t, mux, "GET", "a/valuations/"+closing.HistoryID, 200))
	putSource(t, f.store, "a", "observed-cash", "2", 11600)
	sampleValuation(t, f.store, "a", Handler{})
	b = basisRead(t, f, "a", "holdings", "")
	require.Equal(t, "available", b.Returns.Profit.Status)
	require.Equal(t, "4.00", *b.Returns.Profit.Value)
	require.Equal(t, b.Revision, b.Returns.Revision)
}
