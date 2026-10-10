package ledger

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTWRFullWithdrawalAndReentryPrefixes(t *testing.T) {
	// 100,000 -> withdraw 110,000; later deposit 80,000 -> 84,000.
	// The idle period and fresh capital do not change the first leg's +10%.
	// Linked TWR is 1.10 * 1.05 - 1 = 15.5%, not 5% or 24%.
	o := returnPoint("2021-01-01", 10000000)
	w := returnPoint("2021-04-01", 0)
	w.Flow = moneyPtr(-11000000)
	empty := returnPoint("2021-04-04", 0)
	d := returnPoint("2021-04-08", 8000000)
	d.Flow = moneyPtr(8000000)
	c := returnPoint("2022-01-01", 8400000)
	points := []BasisPoint{o, w, empty, d, c}
	b := AnalysisBasis{Points: points, Closing: &c}
	r, err := calculateReturns(t.Context(), b, "", "")
	require.NoError(t, err)
	require.Equal(t, "available", r.TWR.Status)
	require.Equal(t, "0.155000000000", *r.TWR.Value)
	require.Equal(t, r.TWR, r.TWRAnnualized) // 365 calendar days, including idle days
	require.Equal(t, "14000.00", *r.Profit.Value)
	require.Len(t, r.Curve, len(points))
	for i, want := range []string{"0.00", "10.00", "10.00", "10.00", "15.50"} {
		require.Equal(t, "available", r.Curve[i].TWR.Status, points[i].Date)
		require.Equal(t, want, *r.Curve[i].TWR.Percentage, points[i].Date)
		if i == 0 {
			continue
		}
		prefix := AnalysisBasis{Points: points[:i+1], Closing: &points[i]}
		pr, err := calculateReturns(t.Context(), prefix, "", "")
		require.NoError(t, err)
		require.Equal(t, r.Curve[i].TWR, pr.TWR)
		requireTWRMoneyUnchanged(t, prefix, "", "", pr)
	}
	requireTWRMoneyUnchanged(t, b, "", "", r)

	// A separate zero-balance observation during the idle period is optional.
	b.Points = []BasisPoint{o, w, d, c}
	withoutIdleObservation, err := calculateReturns(t.Context(), b, "", "")
	require.NoError(t, err)
	require.Equal(t, r.TWR, withoutIdleObservation.TWR)
}

func TestTWRReentryMultipleCyclesAndTotalLoss(t *testing.T) {
	o := returnPoint("2021-01-01", 1000000)
	w := returnPoint("2021-03-01", 0)
	w.Flow = moneyPtr(-1100000)
	d := returnPoint("2021-04-01", 800000)
	d.Flow = moneyPtr(800000)
	w2 := returnPoint("2021-06-01", 0)
	w2.Flow = moneyPtr(-840000)
	d2 := returnPoint("2021-07-01", 600000)
	d2.Flow = moneyPtr(600000)
	c := returnPoint("2022-01-01", 630000)
	r, err := calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, w, d, w2, d2, c}, Closing: &c}, "", "")
	require.NoError(t, err)
	require.Equal(t, "available", r.TWR.Status)
	require.Equal(t, "0.212750000000", *r.TWR.Value) // 1.10 * 1.05 * 1.05 - 1

	// Losing the original investment is not withdrawing it. A fresh deposit
	// must not reset a -100% cumulative result to 0% or to the new leg's +5%.
	for _, boundary := range []bool{false, true} {
		t.Run(fmt.Sprintf("total_loss/boundary=%t", boundary), func(t *testing.T) {
			loss := returnPoint("2021-03-01", 0)
			points := []BasisPoint{o, loss}
			if boundary {
				// Opposing nonzero events still create a mandatory boundary.
				points = append(points, BasisPoint{Date: loss.Date, Flow: moneyPtr(100)}, BasisPoint{Date: loss.Date, Flow: moneyPtr(-100)})
			}
			close := returnPoint("2022-01-01", 840000)
			points = append(points, d, close)
			r, err := calculateReturns(t.Context(), AnalysisBasis{Points: points, Closing: &close}, "", "")
			require.NoError(t, err)
			require.Equal(t, "available", r.TWR.Status)
			require.Equal(t, "-1.000000000000", *r.TWR.Value)
			require.Equal(t, r.TWR, r.TWRAnnualized)
		})
	}
}

func TestTWRReentryRequiresConsistentBoundary(t *testing.T) {
	for _, tt := range []struct {
		name   string
		assets Money
		flow   *Money
		status string
		reason string
	}{
		{"empty", 0, nil, "reported", ""},
		{"deposit", 800000, moneyPtr(800000), "reported", ""},
		{"unexplained_assets", 800000, nil, "reported", "zero_twr_base"},
		{"deposit_day_gain", 800001, moneyPtr(800000), "reported", "zero_twr_base"},
		{"deposit_day_loss_or_fee", 799999, moneyPtr(800000), "reported", "zero_twr_base"},
		{"withdraw_from_empty", 0, moneyPtr(-1), "reported", "zero_twr_base"},
		{"negative_balance", -1, moneyPtr(-1), "reported", "negative_twr_factor"},
		{"missing", 800000, moneyPtr(800000), "missing", "missing_flow_boundary"},
		{"stale", 800000, moneyPtr(800000), "stale", "stale_endpoint"},
		{"untracked", 800000, moneyPtr(800000), "untracked", "untracked_endpoint"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := returnPoint("2021-01-01", 1000000)
			w := returnPoint("2021-04-01", 0)
			w.Flow = moneyPtr(-1100000)
			d := returnPoint("2021-05-01", tt.assets)
			d.Flow, d.Status = tt.flow, tt.status
			if tt.status == "missing" {
				d.Assets = nil
			}
			c := returnPoint("2022-01-01", tt.assets)
			r, err := calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, w, d, c}, Closing: &c}, "", "")
			require.NoError(t, err)
			require.Equal(t, tt.reason, r.TWR.Reason)
			require.Equal(t, tt.reason, r.Curve[2].TWR.Reason)
			if tt.reason == "" {
				require.Equal(t, "available", r.TWR.Status)
				require.Equal(t, "10.00", *r.TWR.Percentage)
			} else {
				require.Equal(t, "unavailable", r.TWR.Status)
				require.Nil(t, r.TWR.Value)
			}
		})
	}
}

func TestTWRReentryHTTPProjectionAndAnnualReturns(t *testing.T) {
	for _, mode := range []string{"combined", "asset_first", "flow_first", "estimated"} {
		t.Run(mode, func(t *testing.T) {
			f := reportedFixture(t)
			basisEntry(t, f, "manual-base", "2024-01-01", "asset", "null", `"100000"`)
			basisEntry(t, f, "manual-before-exit", "2024-12-01", "asset", "null", `"110000"`)
			for _, e := range []struct{ id, date, flow, assets string }{
				{"manual-exit", "2024-12-02", `"-110000"`, `"0"`},
				{"manual-reenter", "2025-02-01", `"80000"`, `"80000"`},
			} {
				if mode == "combined" {
					basisEntry(t, f, e.id, e.date, "cash_flow", e.flow, e.assets)
					continue
				}
				if mode == "asset_first" {
					basisEntry(t, f, e.id+"-assets", e.date, "asset", "null", e.assets)
				}
				basisEntry(t, f, e.id, e.date, "cash_flow", e.flow, "null")
				if mode == "flow_first" {
					basisEntry(t, f, e.id+"-assets", e.date, "asset", "null", e.assets)
				}
			}
			basisEntry(t, f, "manual-empty", "2024-12-31", "asset", "null", `"0"`)
			basisEntry(t, f, "manual-close", "2025-12-31", "asset", "null", `"84000"`)
			before := f.snapshot(t)
			b := basisRead(t, f, "a", "", "")
			wantStatus := "available"
			if mode == "estimated" {
				wantStatus = "reference"
				require.Contains(t, b.Returns.Warnings, "twr_estimated_assets")
				for _, p := range b.Points {
					if p.Flow != nil {
						require.Nil(t, p.Record.TotalAssets)
					}
				}
			}
			require.Equal(t, wantStatus, b.Returns.TWR.Status)
			require.Equal(t, "15.50", *b.Returns.TWR.Percentage)
			requireTWRMoneyUnchanged(t, b, "", "", b.Returns)
			annual := annualRead(t, f, "/accounts/a/annual-returns")
			require.Equal(t, b.Returns.TWR, annual.Since.TimeWeighted)
			require.Equal(t, b.Returns.TWRAnnualized, annual.Annualized.TimeWeighted)
			require.Len(t, annual.Years, 2)
			require.Equal(t, "10.00", *annual.Years[0].TimeWeighted.Percentage)
			require.Equal(t, "5.00", *annual.Years[1].TimeWeighted.Percentage)
			year := basisRead(t, f, "a", "", "from=2025-01-01&to=2025-12-31")
			require.Equal(t, annual.Years[1].TimeWeighted, year.Returns.TWR)
			require.Equal(t, wantStatus, year.Returns.TWR.Status)
			require.Equal(t, b, basisRead(t, f, "a", "", ""))
			require.Equal(t, before, f.snapshot(t))

			if mode == "combined" {
				// Editing a reentry into an unexplained loss must invalidate the
				// chain; correcting it back recomputes from the preserved history.
				f.request(t, "PUT", "/accounts/a/records/manual-reenter", "edit-reentry", `{"expected_version":"1","reason":"Synthetic correction","entry":{"kind":"cash_flow","date":"2025-02-01","flow":"81000","total_assets":"80000"}}`, 200)
				changed := basisRead(t, f, "a", "", "")
				require.NotEqual(t, b.Revision, changed.Revision)
				require.Equal(t, "zero_twr_base", changed.Returns.TWR.Reason)
				f.request(t, "PUT", "/accounts/a/records/manual-reenter", "restore-reentry", `{"expected_version":"2","reason":"Synthetic correction","entry":{"kind":"cash_flow","date":"2025-02-01","flow":"80000","total_assets":"80000"}}`, 200)
				require.Equal(t, b.Returns.TWR, basisRead(t, f, "a", "", "").Returns.TWR)
			}
		})
	}
}
