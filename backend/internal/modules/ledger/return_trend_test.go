package ledger

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReturnTrendImportDailySelectionPreservesOriginal(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			rows := [][7]string{
				{"记总资产", "2021-01-01", "", "730", "Synthetic baseline", "", ""},
				{"转入转出", "2021-01-01", "19", "", "Synthetic baseline flow", "", ""},
				{"记总资产", "2021-07-01", "", "810", "Synthetic first", "", ""},
				{"记总资产", "2021-07-01", "", "880", "Synthetic last", "", ""},
				{"转入转出", "2021-07-01", "77", "", "Synthetic flow", "", ""},
				{"记总资产", "2022-01-01", "", "968", "Synthetic close", "", ""},
			}
			if reverse {
				rows[2], rows[3], rows[4] = rows[4], rows[2], rows[3]
			}
			f := newHTTPFixture(t)
			data := syntheticImportZip(t, syntheticImportParts(t, rows))
			p := parseSynthetic(t, data)
			require.Nil(t, p.Rows[0].Flow)
			_, err := f.store.ConfirmAccountImport(t.Context(), "a", "import", p.Digest, true, data)
			require.NoError(t, err)
			before := f.snapshot(t)
			b := basisRead(t, f, "a", "", "")
			require.Equal(t, before, f.snapshot(t))
			require.Len(t, b.Points, len(rows))
			for i, point := range b.Points {
				require.Equal(t, p.Rows[i], *point.Record.Original)
			}
			r := b.Returns
			require.Equal(t, Money(73000), *r.Opening.Assets)
			require.Nil(t, r.Opening.Flow)
			require.Equal(t, "available", r.Profit.Status)
			require.Equal(t, "77.00", r.NetFlow)
			require.Equal(t, "161.00", *r.Profit.Value)
			require.Len(t, r.Curve, 3)
			require.Equal(t, "73.00", *r.Curve[1].Profit.Value)
			require.Equal(t, "0.210000000000", *r.TWR.Value)
			require.Equal(t, r.TWR, r.TWRAnnualized)
			require.Equal(t, r.TWR, r.Curve[2].TWR)
			require.Equal(t, r.Dietz, r.Curve[2].Dietz)
			id := r.Curve[1].RecordID
			f.request(t, "PUT", "/accounts/a/records/"+id, "correct", `{"expected_version":"1","reason":"Synthetic correction","entry":{"kind":"asset","date":"2021-07-01","total_assets":"957"}}`, 200)
			changed := basisRead(t, f, "a", "", "")
			require.NotEqual(t, r.Revision, changed.Revision)
			require.NotEqual(t, r.TWR, changed.Returns.TWR)
			for i, point := range changed.Points {
				require.Equal(t, p.Rows[i], *point.Record.Original)
			}
			f.request(t, "DELETE", "/accounts/a/records/"+id, "void", `{"expected_version":"2","reason":"Synthetic void"}`, 200)
			voided := basisRead(t, f, "a", "", "")
			require.Equal(t, "3.00", *voided.Returns.Curve[1].Profit.Value)
			require.Equal(t, "161.00", *voided.Returns.Profit.Value)
		})
	}
}

func TestReturnTrendMandatoryBoundariesAndIrrelevantObservations(t *testing.T) {
	for _, status := range []string{"reported", "carried", "stale", "untracked", "missing"} {
		for _, flow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", status, flow), func(t *testing.T) {
				o, m, c := returnPoint("2021-01-01", 42000), returnPoint("2021-07-01", 46200), returnPoint("2022-01-01", 50820)
				m.Status = status
				if status == "missing" {
					m.Assets = nil
				}
				points := []BasisPoint{o, m}
				if flow {
					in, out := Money(3100), Money(-3100)
					points = append(points, BasisPoint{Date: m.Date, Flow: &in}, BasisPoint{Date: m.Date, Flow: &out})
				}
				points = append(points, c)
				r, err := calculateReturns(t.Context(), AnalysisBasis{Points: points, Closing: &c}, "", "")
				require.NoError(t, err)
				if flow && (status == "stale" || status == "untracked" || status == "missing") {
					require.Equal(t, "unavailable", r.TWR.Status)
				} else {
					require.Equal(t, "0.210000000000", *r.TWR.Value)
					want := "available"
					if flow && status == "carried" {
						want = "reference"
					}
					require.Equal(t, want, r.TWR.Status)
				}
				if status == "stale" || status == "untracked" || status == "missing" {
					require.Equal(t, "unavailable", r.Curve[1].TWR.Status)
				}
				require.Equal(t, "available", r.Profit.Status)
			})
		}
	}
	// There is no selected asset at a mandatory boundary at all.
	o, c := returnPoint("2021-01-01", 42000), returnPoint("2022-01-01", 50820)
	flow := Money(2000)
	r, err := calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, {Date: "2021-06-01", Flow: &flow}, c}, Closing: &c}, "", "")
	require.NoError(t, err)
	require.Equal(t, "missing_flow_boundary", r.TWR.Reason)
	require.Len(t, r.Curve, 2)
	// A nonzero flow beyond the last closing must not disappear, even if net zero.
	opposite := Money(-2000)
	r, err = calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, c, {Date: "2022-02-01", Flow: &flow}, {Date: "2022-02-01", Flow: &opposite}}, Closing: &c}, "", "")
	require.NoError(t, err)
	require.Equal(t, "closing_before_flow", r.TWR.Reason)
	require.Equal(t, r.TWR, r.Curve[1].TWR)
}

func TestReturnTrendZeroNegativeCustomAndPrefix(t *testing.T) {
	o, m, c := returnPoint("2021-01-01", 67000), returnPoint("2021-03-01", 0), returnPoint("2022-01-01", 73700)
	r, err := calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, m, c}, Closing: &c}, "", "")
	require.NoError(t, err)
	require.Equal(t, "-1.000000000000", *r.Curve[1].TWR.Value)
	require.Equal(t, "0.100000000000", *r.TWR.Value) // ordinary zero observation is not chained
	f := Money(-67000)
	m.Flow = &f
	r, err = calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, m, c}, Closing: &c}, "", "")
	require.NoError(t, err)
	require.Equal(t, "zero_twr_base", r.TWR.Reason)
	f = Money(1)
	r, err = calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, m, c}, Closing: &c}, "", "")
	require.NoError(t, err)
	require.Equal(t, "negative_twr_factor", r.TWR.Reason)
	m = returnPoint("2021-03-01", 0)
	r, err = calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o, m}, Closing: &m}, "", "")
	require.NoError(t, err)
	require.Equal(t, "-1.000000000000", *r.TWRAnnualized.Value)
	// Every prefix matches the original money calculation, without using it in production.
	f = Money(4700)
	m = returnPoint("2021-03-01", 75000)
	m.Flow = &f
	points := []BasisPoint{m, returnPoint("2021-07-01", 82000), c}
	r, err = calculateReturns(t.Context(), AnalysisBasis{Opening: &o, Points: points, Closing: &c}, "2021-02-01", "")
	require.NoError(t, err)
	require.Equal(t, "2021-01-31", r.Curve[0].Date)
	require.Equal(t, "reference", r.TWR.Status)
	for i := range points {
		prefix, err := calculateMoneyReturns(t.Context(), AnalysisBasis{Opening: &o, Points: points[:i+1], Closing: &points[i]}, "2021-02-01", "")
		require.NoError(t, err)
		require.Equal(t, prefix.Profit, r.Curve[i+1].Profit)
		require.Equal(t, prefix.Dietz, r.Curve[i+1].Dietz)
	}
	r, err = calculateReturns(t.Context(), AnalysisBasis{Points: []BasisPoint{o}, Closing: &o}, "", "")
	require.NoError(t, err)
	require.Equal(t, "no_interval", r.TWR.Reason)
	require.Equal(t, "0.000000000000", *r.Curve[0].TWR.Value)
}

func TestReturnTrendAnnualizationAndResourceLimits(t *testing.T) {
	for _, tt := range []struct {
		from, to string
		growth   *big.Rat
		want     string
	}{
		{"2021-01-01", "2022-01-01", big.NewRat(22001, 20000), "0.100050000000"},
		{"2021-01-01", "2022-01-01", big.NewRat(19999, 20000), "-0.000050000000"},
		{"2020-01-02", "2022-01-01", big.NewRat(121, 100), "0.100000000000"},
		{"2021-01-01", "2021-01-06", big.NewRat(1, 1), "0.000000000000"},
	} {
		r, err := annualizeTWR(t.Context(), tt.growth, tt.from, tt.to, "available")
		require.NoError(t, err)
		require.NotNil(t, r.Value, r.Reason)
		require.Equal(t, tt.want, *r.Value)
	}
	huge := new(big.Int).Lsh(big.NewInt(1), 200)
	g := new(big.Rat).SetFrac(new(big.Int).Add(huge, big.NewInt(1)), huge)
	r, err := annualizeTWR(t.Context(), g, "2021-01-01", "2022-01-01", "available")
	require.NoError(t, err)
	require.Equal(t, "0.000000000000", *r.Value)
	require.False(t, boundedGrowth(new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), 321))))
	r, err = annualizeTWR(t.Context(), big.NewRat(400040001, 400000000), "2020-01-02", "2022-01-01", "available")
	require.NoError(t, err)
	require.Equal(t, "precision_unresolved", r.Reason) // exact half tie outside rational fast paths
	require.Nil(t, r.Value)
	points := make([]BasisPoint, 10000)
	start := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range points {
		points[i] = returnPoint(start.AddDate(0, 0, i).Format(time.DateOnly), 37000)
		flow := Money(0)
		points[i].Flow = &flow
	}
	result, err := calculateReturns(t.Context(), AnalysisBasis{Points: points, Closing: &points[len(points)-1]}, "", "")
	require.NoError(t, err)
	require.Len(t, result.Curve, 10000)
	require.Equal(t, "0.000000000000", *result.TWR.Value)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = calculateReturns(ctx, AnalysisBasis{}, "", "")
	require.ErrorIs(t, err, context.Canceled)
}

func TestReturnTrendWeeklyCarryAndManualAssertionSelection(t *testing.T) {
	w, _ := weeklyFixture(t, false)
	_, err := w.store.CreateReportedAccount(t.Context(), "reported", ReportedAccountInput{ID: "reported", Name: "Synthetic", Currency: CNY, OpeningDate: "2020-01-01"})
	require.NoError(t, err)
	for i, date := range []string{"2026-09-01", "2026-09-12"} {
		amount := Money(53000 + i*5300)
		_, err = w.store.WriteAccountRecord(t.Context(), fmt.Sprintf("asset-%d", i), AccountRecordCommand{Action: CreateOperation, AccountID: "reported", ID: fmt.Sprintf("manual-%d", i), Entry: &AccountEntry{Kind: "asset", Date: date, TotalAssets: &amount}})
		require.NoError(t, err)
	}
	require.NoError(t, w.Tick(t.Context()))
	b, err := w.store.AnalysisBasis(t.Context(), "reported", "", "", 0)
	require.NoError(t, err)
	require.Len(t, b.Points, 3)
	require.Equal(t, "carried", b.Points[2].Status)
	require.False(t, b.Points[2].Selected)
	require.Equal(t, "manual-1", b.Closing.RecordID)
	require.Equal(t, "available", b.Returns.TWR.Status)
	amount := Money(63600)
	_, err = w.store.WriteAccountRecord(t.Context(), "assert", AccountRecordCommand{Action: ReplaceOperation, AccountID: "reported", ID: b.Points[2].RecordID, ExpectedVersion: "1", Reason: "Synthetic assertion", Entry: &AccountEntry{Kind: "asset", Date: "2026-09-12", TotalAssets: &amount}})
	require.NoError(t, err)
	asserted, err := w.store.AnalysisBasis(t.Context(), "reported", "", "", 0)
	require.NoError(t, err)
	require.Equal(t, b.Points[2].RecordID, asserted.Closing.RecordID)
	require.True(t, asserted.Closing.Record.ManualAssertion)
	require.Equal(t, "0.200000000000", *asserted.Returns.TWR.Value)
	require.Equal(t, *b.Points[2].Record.CarriedFrom, *asserted.Closing.Record.CarriedFrom)
}

func TestReturnTrendProductCancellationAndLimit(t *testing.T) {
	for _, cancelProducts := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelProducts), func(t *testing.T) {
			points := make([]BasisPoint, 2001)
			start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			for i := range points {
				assets, flow := Money(1000003), Money(1)
				if cancelProducts {
					assets, flow = 3, -3
					if i%2 == 1 {
						assets, flow = 4, 2
					}
				}
				points[i] = returnPoint(start.AddDate(0, 0, i).Format(time.DateOnly), assets)
				if i > 0 {
					points[i].Flow = &flow
				}
			}
			r, err := calculateReturns(t.Context(), AnalysisBasis{Points: points, Closing: &points[len(points)-1]}, "", "")
			require.NoError(t, err)
			if cancelProducts {
				require.Equal(t, "0.000000000000", *r.TWR.Value)
			} else {
				require.Equal(t, "twr_product_limit", r.TWR.Reason)
			}
		})
	}
}
