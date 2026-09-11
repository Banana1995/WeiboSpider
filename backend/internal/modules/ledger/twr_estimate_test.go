package ledger

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func moneyPtr(value Money) *Money { return &value }

func requireTWRMoneyUnchanged(t *testing.T, b AnalysisBasis, from, to string, r Returns) {
	t.Helper()
	before, err := json.Marshal(b)
	require.NoError(t, err)
	money, err := calculateMoneyReturns(t.Context(), b, from, to)
	require.NoError(t, err)
	for _, warning := range money.Warnings {
		require.Contains(t, r.Warnings, warning)
	}
	r.TWR, r.TWRAnnualized, r.Curve, r.Warnings = ReturnMetric{}, ReturnMetric{}, nil, money.Warnings
	require.Equal(t, money, r)
	after, err := json.Marshal(b)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestTWREstimateConfirmedExamples(t *testing.T) {
	for _, tt := range []struct {
		name, extra, assets, net, twr, annual string
	}{
		{"one_deposit", "", "12000.00", "2000.00", "10.00", "7.39"},
		{"another_deposit", "1000", "13000.00", "3000.00", "1.54", "1.15"},
		{"withdrawal", "-1000", "11000.00", "1000.00", "20.00", "14.61"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := reportedFixture(t)
			basisEntry(t, f, "manual-base", "2024-01-01", "asset", "null", `"10000"`)
			basisEntry(t, f, "manual-flow", "2024-09-01", "cash_flow", `"2000"`, "null")
			if tt.extra != "" {
				basisEntry(t, f, "manual-extra", "2024-12-01", "cash_flow", fmt.Sprintf("%q", tt.extra), "null")
			}
			basisEntry(t, f, "manual-close", "2025-05-03", "asset", "null", `"13200"`)
			before := f.snapshot(t)
			b := basisRead(t, f, "a", "", "")
			f.request(t, "HEAD", "/accounts/a/analysis-basis", "", "", 200)
			require.Equal(t, before, f.snapshot(t))
			r := b.Returns
			require.Equal(t, tt.twr, *r.TWR.Percentage)
			require.Equal(t, tt.annual, *r.TWRAnnualized.Percentage)
			require.Equal(t, "reference", r.TWR.Status)
			require.Equal(t, "reference", r.TWRAnnualized.Status)
			require.Equal(t, r.TWR, r.Curve[len(r.Curve)-1].TWR)
			require.Contains(t, r.Warnings, "twr_estimated_assets")
			require.NotContains(t, r.Warnings, "carried_assets_unchanged")
			require.Equal(t, &TWREstimate{tt.assets, "manual-base", "2024-01-01", tt.net}, r.Curve[len(r.Curve)-2].TWREstimate)
			for i := 1; i < len(b.Points)-1; i++ {
				require.Equal(t, Money(1000000), *b.Points[i].Assets)
				require.Nil(t, b.Points[i].Record.TotalAssets)
				require.Equal(t, "0.000000000000", *r.Curve[i].TWR.Value)
			}
			require.Nil(t, r.Curve[0].TWREstimate)
			require.Nil(t, r.Curve[len(r.Curve)-1].TWREstimate)
			requireTWRMoneyUnchanged(t, b, "", "", r)
			for i := 1; i < len(b.Points); i++ {
				prefix, err := calculateMoneyReturns(t.Context(), AnalysisBasis{Points: b.Points[:i+1], Closing: &b.Points[i]}, "", "")
				require.NoError(t, err)
				require.Equal(t, prefix.Profit, r.Curve[i].Profit)
				require.Equal(t, prefix.Dietz, r.Curve[i].Dietz)
			}
		})
	}
}

func TestTWREstimateDailyPostFlowAndReanchor(t *testing.T) {
	for _, mode := range []string{"combined", "flow_first", "asset_first", "distinct_dates", "no_flow_reanchor"} {
		t.Run(mode, func(t *testing.T) {
			f := reportedFixture(t)
			basisEntry(t, f, "manual-base", "2024-01-01", "asset", "null", `"10000"`)
			switch mode {
			case "combined":
				basisEntry(t, f, "manual-observed", "2024-09-01", "cash_flow", `"2000"`, `"13200"`)
			case "asset_first", "flow_first":
				if mode == "asset_first" {
					basisEntry(t, f, "manual-observed", "2024-09-01", "asset", "null", `"13200"`)
				}
				// Aggregate ALL flows, not just the selected record's flow.
				basisEntry(t, f, "manual-flow", "2024-09-01", "cash_flow", `"2500"`, "null")
				basisEntry(t, f, "manual-out", "2024-09-01", "cash_flow", `"-500"`, "null")
				if mode == "flow_first" {
					basisEntry(t, f, "manual-observed", "2024-09-01", "asset", "null", `"13200"`)
				}
			default:
				basisEntry(t, f, "manual-flow", "2024-09-01", "cash_flow", `"2000"`, "null")
				basisEntry(t, f, "manual-observed", "2024-10-01", "asset", "null", `"13200"`)
			}
			closing := `"14520"`
			if mode == "no_flow_reanchor" {
				basisEntry(t, f, "manual-next", "2024-12-01", "cash_flow", `"1000"`, "null")
				closing = `"15620"` // 14200 * 1.1
			}
			basisEntry(t, f, "manual-close", "2025-05-03", "asset", "null", closing)
			b := basisRead(t, f, "a", "", "")
			r := b.Returns
			want, annual, status := "23.20", "16.89", "available"
			if mode == "distinct_dates" || mode == "no_flow_reanchor" {
				want, annual, status = "21.00", "15.32", "reference"
			}
			require.Equal(t, want, *r.TWR.Percentage)
			require.Equal(t, annual, *r.TWRAnnualized.Percentage)
			require.Equal(t, status, r.TWR.Status)
			if mode == "no_flow_reanchor" {
				require.Equal(t, &TWREstimate{"14200.00", "manual-observed", "2024-10-01", "1000.00"}, r.Curve[3].TWREstimate)
			} else if status == "available" {
				require.Nil(t, r.Curve[1].TWREstimate)
				require.NotContains(t, r.Warnings, "twr_estimated_assets")
			}
			requireTWRMoneyUnchanged(t, b, "", "", r)
		})
	}
}

func TestTWREstimateCustomOpeningWeeklyHistory(t *testing.T) {
	w, clock := weeklyFixture(t, false)
	_, err := w.store.CreateReportedAccount(t.Context(), "reported", ReportedAccountInput{ID: "reported", Name: "Synthetic", Currency: CNY, OpeningDate: "2020-01-01"})
	require.NoError(t, err)
	for _, e := range []struct {
		id, date     string
		assets, flow *Money
	}{
		{"manual-base", "2024-01-01", moneyPtr(1000000), nil},
		{"manual-flow", "2024-09-01", nil, moneyPtr(200000)},
		{"manual-extra", "2024-12-01", nil, moneyPtr(100000)},
		{"manual-in-range", "2025-02-01", nil, moneyPtr(50000)},
		{"manual-close", "2025-05-03", moneyPtr(1485000), nil},
	} {
		kind := "asset"
		if e.flow != nil {
			kind = "cash_flow"
		}
		_, err = w.store.WriteAccountRecord(t.Context(), e.id, AccountRecordCommand{Action: CreateOperation, AccountID: "reported", ID: e.id,
			Entry: &AccountEntry{Kind: kind, Date: e.date, TotalAssets: e.assets, Flow: e.flow}})
		require.NoError(t, err)
	}
	for _, date := range []string{"2024-10-05", "2024-12-07", "2024-12-14", "2025-01-04", "2025-02-08"} {
		clock.set(date + "T08:00:00+08:00")
		require.NoError(t, w.Tick(t.Context()))
	}
	clock.set("2025-05-03T08:00:00+08:00")
	f := &httpFixture{store: w.store}
	before := f.snapshot(t)
	b, err := w.store.AnalysisBasis(t.Context(), "reported", "2025-01-01", "2025-05-03", 0)
	require.NoError(t, err)
	require.Equal(t, before, f.snapshot(t))
	r := b.Returns
	require.Equal(t, "2024-12-31", r.Curve[0].Date)
	require.Equal(t, "2024-12-14", b.Opening.Date)
	require.Equal(t, Money(1000000), *b.Opening.Assets)
	require.Equal(t, &TWREstimate{"13000.00", "manual-base", "2024-01-01", "3000.00"}, r.Curve[0].TWREstimate)
	require.Equal(t, r.Curve[0].TWREstimate, r.Curve[1].TWREstimate)
	require.Equal(t, &TWREstimate{"13500.00", "manual-base", "2024-01-01", "3500.00"}, r.Curve[2].TWREstimate)
	require.Equal(t, r.Curve[2].TWREstimate, r.Curve[3].TWREstimate)
	require.Equal(t, "0.000000000000", *r.Curve[2].TWR.Value)
	require.Equal(t, "10.00", *r.TWR.Percentage)
	require.Equal(t, "reference", r.TWR.Status)
	requireTWRMoneyUnchanged(t, b, "2025-01-01", "2025-05-03", r)
	input, err := json.Marshal(b)
	require.NoError(t, err)
	_, err = calculateReturns(t.Context(), b, "2025-01-01", "2025-05-03")
	require.NoError(t, err)
	after, err := json.Marshal(b)
	require.NoError(t, err)
	require.Equal(t, input, after)
	// A pre-range flow edit changes TWR's dependency fingerprint even though the
	// public opening and all in-range facts (including frozen weekly carries) do not.
	_, err = w.store.WriteAccountRecord(t.Context(), "edit-pre-range", AccountRecordCommand{Action: ReplaceOperation, AccountID: "reported", ID: "manual-flow", ExpectedVersion: "1", Reason: "Synthetic correction",
		Entry: &AccountEntry{Kind: "cash_flow", Date: "2024-09-01", Flow: moneyPtr(250000)}})
	require.NoError(t, err)
	changed, err := w.store.AnalysisBasis(t.Context(), "reported", "2025-01-01", "2025-05-03", 0)
	require.NoError(t, err)
	require.Equal(t, b.Opening, changed.Opening)
	require.Equal(t, b.Points, changed.Points)
	require.NotEqual(t, b.Revision, changed.Revision)
	require.Equal(t, "13500.00", changed.Returns.Curve[0].TWREstimate.Assets)
	require.Equal(t, "6.07", *changed.Returns.TWR.Percentage)
}

func TestTWREstimateSameDayMissingFlows(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		f := reportedFixture(t)
		basisEntry(t, f, "manual-base", "2024-01-01", "asset", "null", `"10000"`)
		flows := []string{`"2500"`, `"-500"`}
		if reverse {
			flows[0], flows[1] = flows[1], flows[0]
		}
		for i, flow := range flows {
			basisEntry(t, f, fmt.Sprintf("manual-flow-%d", i), "2024-09-01", "cash_flow", flow, "null")
		}
		basisEntry(t, f, "manual-close", "2025-05-03", "asset", "null", `"13200"`)
		b := basisRead(t, f, "a", "", "")
		require.Len(t, b.Points, 4)
		require.Len(t, b.Returns.Curve, 3)
		require.Equal(t, &TWREstimate{"12000.00", "manual-base", "2024-01-01", "2000.00"}, b.Returns.Curve[1].TWREstimate)
		require.Equal(t, "10.00", *b.Returns.TWR.Percentage)
		requireTWRMoneyUnchanged(t, b, "", "", b.Returns)
	}
}

func TestTWREstimateTrustAndNonMandatoryDependency(t *testing.T) {
	for _, status := range []string{"carried", "stale", "untracked"} {
		for _, mandatory := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", status, mandatory), func(t *testing.T) {
				o, m, c := returnPoint("2024-01-01", 1000000), returnPoint("2024-09-01", 1000000), returnPoint("2025-05-03", 1100000)
				o.RecordID = "source"
				m.Status = status
				if mandatory {
					m.Flow = moneyPtr(200000)
				}
				b := AnalysisBasis{Points: []BasisPoint{o, m, c}, Closing: &c}
				before, err := json.Marshal(b)
				require.NoError(t, err)
				r, err := calculateReturns(t.Context(), b, "", "")
				require.NoError(t, err)
				after, err := json.Marshal(b)
				require.NoError(t, err)
				require.Equal(t, before, after)
				if mandatory && status != "carried" {
					require.Equal(t, status+"_endpoint", r.TWR.Reason)
					require.Nil(t, r.Curve[1].TWREstimate)
				} else if mandatory {
					require.Equal(t, "reference", r.TWR.Status)
				} else {
					require.Equal(t, "available", r.TWR.Status)
					require.Equal(t, "10.00", *r.TWR.Percentage)
					require.Equal(t, "available", r.Curve[0].TWR.Status)
				}
			})
		}
	}
	o := returnPoint("2024-01-01", 1000000)
	o.Status = "carried" // A carried amount alone is not a trustworthy source.
	c := returnPoint("2025-05-03", 1100000)
	r, err := calculateReturns(t.Context(), AnalysisBasis{Opening: &o, Points: []BasisPoint{c}, Closing: &c}, "2024-02-01", "")
	require.NoError(t, err)
	require.Equal(t, "missing_flow_boundary", r.TWR.Reason)
	require.Nil(t, r.Curve[0].TWREstimate)
	for _, status := range []string{"stale", "untracked"} {
		o.Status = "reported"
		bad, carry := returnPoint("2024-06-01", 1000000), returnPoint("2024-09-01", 1000000)
		bad.Status, carry.Status, carry.Flow = status, "carried", moneyPtr(200000)
		r, err = calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, bad, carry, c}, Closing: &c}, "", "")
		require.NoError(t, err)
		require.Equal(t, "missing_flow_boundary", r.TWR.Reason)
		require.Nil(t, r.Curve[2].TWREstimate)
	}
}

func TestTWREstimateZeroNegativeAndLargeAssets(t *testing.T) {
	for _, tt := range []struct {
		base, flow Money
		reason     string
	}{
		{10000, -10001, "negative_twr_factor"},
		{10000, -10000, "zero_twr_base"},
		{0, 10000, "zero_twr_base"},
		{math.MaxInt64, math.MaxInt64, ""},
	} {
		o, m, c := returnPoint("2024-01-01", tt.base), returnPoint("2024-09-01", tt.base), returnPoint("2025-05-03", 11000)
		m.Status, m.Flow = "carried", &tt.flow
		r, err := calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, m, c}, Closing: &c}, "", "")
		require.NoError(t, err)
		require.Equal(t, tt.reason, r.TWR.Reason)
		if tt.base == math.MaxInt64 {
			require.Equal(t, "184467440737095516.14", r.Curve[1].TWREstimate.Assets)
			require.Equal(t, "0.000000000000", *r.Curve[1].TWR.Value)
		}
	}
}
