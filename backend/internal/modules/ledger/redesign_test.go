package ledger

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRedesignHoldingIdentityAtomicAccountLocalAndCAS(t *testing.T) {
	s := importStoreFixture(t)
	manualSourceAccount(t, s, "a")
	manualSourceAccount(t, s, "b")
	zero := Money(0)
	security := instrumentJSON{"security-a", "SH", "600000", "Synthetic", CNY}
	input := CurrentHoldingsInput{ExpectedVersion: "0", Cash: &zero,
		Positions: []CurrentPosition{{security.ID, 1_000_000}}, Securities: []instrumentJSON{security}}
	first, err := s.PutCurrentHoldings(t.Context(), "a", "holding-a", input)
	require.NoError(t, err)
	retry, err := s.PutCurrentHoldings(t.Context(), "a", "holding-a", input)
	require.NoError(t, err)
	require.Equal(t, first, retry)

	// Same security is allowed in another account, including independently named identity.
	secondSecurity := security
	secondSecurity.ID, secondSecurity.Name = "security-b", "Another account name"
	other := input
	other.Positions = []CurrentPosition{{secondSecurity.ID, 2_000_000}}
	other.Securities = []instrumentJSON{secondSecurity}
	_, err = s.PutCurrentHoldings(t.Context(), "b", "holding-b", other)
	require.NoError(t, err)

	duplicate := other
	duplicate.ExpectedVersion = "1"
	duplicate.Positions = append([]CurrentPosition{{security.ID, 1_000_000}}, other.Positions...)
	_, err = s.PutCurrentHoldings(t.Context(), "a", "duplicate", duplicate)
	require.ErrorIs(t, err, ErrConflict)
	current, err := s.CurrentHoldings(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, first, current)

	changed := security
	changed.ID, changed.Code, changed.Name, changed.Currency, changed.Market = "edited", "00700", "Edited", HKD, "HK"
	edit := CurrentHoldingsInput{ExpectedVersion: "1", Cash: &zero, Positions: []CurrentPosition{{changed.ID, 3_000_000}}, Securities: []instrumentJSON{changed}}
	saved, err := s.PutCurrentHoldings(t.Context(), "a", "edit", edit)
	require.NoError(t, err)
	require.Equal(t, "2", saved.Snapshot.Version)
	_, err = s.PutCurrentHoldings(t.Context(), "a", "stale-edit", edit)
	require.ErrorIs(t, err, ErrVersion)
	// A committed retry still returns its original snapshot after subsequent edits.
	retry, err = s.PutCurrentHoldings(t.Context(), "a", "holding-a", input)
	require.NoError(t, err)
	require.Equal(t, first, retry)
	putSource(t, s, "a", "delete", "2", 0)
	otherCurrent, err := s.CurrentHoldings(t.Context(), "b")
	require.NoError(t, err)
	require.Equal(t, other.Positions, otherCurrent.Snapshot.Positions)
	var count int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&count))
	require.Zero(t, count)
}

func TestRedesignFixedHistoryAndInflightFence(t *testing.T) {
	s := importStoreFixture(t)
	manualSourceAccount(t, s, "a")
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Original", Currency: CNY}))
	original := putSource(t, s, "a", "source", "0", 100, CurrentPosition{"i", 1_000_000})
	v, instruments, err := s.valuationInputs(t.Context(), "a")
	require.NoError(t, err)
	require.NoError(t, valuePositions(t.Context(), &v, instruments, valuationQuotes(validValuationQuotes), nil, s.now))
	v.weekly = true
	id, err := s.RecordValuation(t.Context(), v, instruments)
	require.NoError(t, err)
	before, err := s.AnalysisBasis(t.Context(), "a", "", "", 0)
	require.NoError(t, err)
	history, err := s.ValuationHistory(t.Context(), "a", mustInt(t, id))
	require.NoError(t, err)
	putSource(t, s, "a", "delete-holding", "1", 900)
	_, err = s.RecordValuation(t.Context(), v, instruments)
	require.ErrorIs(t, err, errWeeklyBasis)
	after, err := s.AnalysisBasis(t.Context(), "a", "", "", mustInt(t, before.ChangeRevision))
	require.NoError(t, err)
	require.False(t, after.PreviousBasisAffected)
	require.Equal(t, before.Revision, after.Revision)
	require.Equal(t, before.Returns, after.Returns)
	require.Equal(t, "observed", after.Closing.Status)
	frozen, err := s.ValuationHistory(t.Context(), "a", mustInt(t, id))
	require.NoError(t, err)
	require.Equal(t, history, frozen)
	require.Equal(t, original, *frozen.Valuation.CurrentHoldings)
	require.Equal(t, "Original", frozen.Instruments[0].Name)
	// Manual correction changes the fixed financial row, not its original audit inputs.
	corrected := Money(5000)
	_, err = s.WriteAccountRecord(t.Context(), "correct-record", AccountRecordCommand{Action: ReplaceOperation, AccountID: "a", ID: "valuation-" + id, ExpectedVersion: "1", Reason: "Correct fixed assets", Entry: &AccountEntry{Kind: "asset", Date: v.AsOf, TotalAssets: &corrected}})
	require.NoError(t, err)
	fixed, err := s.AnalysisBasis(t.Context(), "a", "", "", 0)
	require.NoError(t, err)
	require.Equal(t, &corrected, fixed.Closing.Assets)
	frozen, err = s.ValuationHistory(t.Context(), "a", mustInt(t, id))
	require.NoError(t, err)
	require.Equal(t, history, frozen)
}

func TestRedesignConcurrentHoldingEdits(t *testing.T) {
	s := importStoreFixture(t)
	manualSourceAccount(t, s, "a")
	putSource(t, s, "a", "initial", "0", 0)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"one", "two"} {
		wg.Go(func() {
			cash := Money(100)
			_, err := s.PutCurrentHoldings(t.Context(), "a", key, CurrentHoldingsInput{ExpectedVersion: "1", Cash: &cash, Positions: []CurrentPosition{}})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrVersion)
			conflicts++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflicts)
}

func TestRedesignProjectionSameDayFlowsAndRawNull(t *testing.T) {
	for _, assetFirst := range []bool{true, false} {
		f := reportedFixture(t)
		basisEntry(t, f, "manual-base", "2024-01-01", "asset", "null", `"10000"`)
		if assetFirst {
			basisEntry(t, f, "manual-next", "2024-02-01", "asset", "null", `"12500"`)
		}
		basisEntry(t, f, "manual-in", "2024-02-01", "cash_flow", `"2000"`, "null")
		basisEntry(t, f, "manual-out", "2024-02-01", "cash_flow", `"-500"`, "null")
		if !assetFirst {
			basisEntry(t, f, "manual-next", "2024-02-01", "asset", "null", `"12500"`)
		}
		basisEntry(t, f, "manual-later", "2024-03-01", "cash_flow", `"1000"`, "null")
		basisEntry(t, f, "manual-withdraw", "2024-04-01", "cash_flow", `"-200"`, "null")
		b := basisRead(t, f, "a", "", "")
		require.Equal(t, Money(1_330_000), *b.Closing.Assets)
		require.Nil(t, b.Closing.Record.TotalAssets)
		require.Equal(t, "1000.00", *b.Returns.Profit.Value)
		require.Equal(t, b.Returns.Profit, b.Returns.Curve[len(b.Returns.Curve)-1].Profit)
		require.Equal(t, b.Returns.Dietz, b.Returns.Curve[len(b.Returns.Curve)-1].Dietz)
		summary, err := f.store.EffectiveSummary(t.Context(), "a")
		require.NoError(t, err)
		require.Equal(t, b.Closing.Assets, summary.LatestAssets)
		custom := basisRead(t, f, "a", "", "from=2024-03-15&to=2024-04-01")
		require.Equal(t, Money(1_350_000), *custom.Opening.Assets)
		require.Equal(t, "0.00", *custom.Returns.Profit.Value)
		require.Equal(t, "0.000000000000", *custom.Returns.TWR.Value)
	}
}

func TestRedesignHTTPCurrentInputsAndReadOnlyQuote(t *testing.T) {
	f := newHTTPFixture(t)
	payload := `{"id":"a","name":"Synthetic","currency":"CNY","opening_date":"2020-01-01"}`
	f.request(t, "POST", "/accounts", "create-a", payload, 201)
	first := f.request(t, "PUT", "/accounts/a/current-holdings", "put-a", `{"expected_version":"0","cash":"10.00","positions":[{"instrument_id":"stock-a","quantity":"2"}],"securities":[{"id":"stock-a","market":"SH","code":"600000","name":"Synthetic","currency":"CNY"}]}`, 200)
	var current CurrentHoldings
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &current))
	require.Equal(t, Quantity(2_000_000), current.Snapshot.Positions[0].Quantity)
	require.Equal(t, "10.00", f.get(t, "/accounts/a")["cash"])
	f.request(t, "POST", "/accounts/a/valuation", "no-save", "{}", 405)
	f.request(t, "POST", "/operations", "no-replay", "{}", 404)
	f.request(t, "POST", "/accounts/a/holdings/stock-a/transactions", "no-trade", "{}", 404)
	before := f.snapshot(t)
	f.request(t, "GET", "/accounts/a/holdings", "", "", 200)
	require.Equal(t, before, f.snapshot(t))
}

func TestRedesignWeeklyFixedRecordAndNoSourceCarry(t *testing.T) {
	f := reportedFixture(t)
	f.store.now = func() time.Time { return time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC) }
	basisEntry(t, f, "manual-base", "2024-01-01", "asset", "null", `"10000"`)
	basisEntry(t, f, "manual-flow", "2024-02-01", "cash_flow", `"2000"`, "null")
	manualSourceAccount(t, f.store, "cash")
	putSource(t, f.store, "cash", "cash-input", "0", 4321)
	worker, err := NewWeeklyWorker(f.store, nil, nil, WeeklyConfig{Enabled: true, Time: DefaultWeeklyTime}, nil)
	require.NoError(t, err)
	require.NoError(t, worker.Tick(t.Context()))
	carry := weeklyJobFor(t, worker, "a")
	require.Equal(t, "account_record_carry", carry.Source)
	require.Equal(t, "succeeded", carry.Status)
	cash := weeklyJobFor(t, worker, "cash")
	require.Equal(t, "holdings_current", cash.Source)
	require.Equal(t, "succeeded", cash.Status)
	b := basisRead(t, f, "a", "", "")
	require.Equal(t, Money(1_200_000), *b.Closing.Assets)
	require.Equal(t, Money(1_000_000), *b.Closing.Record.TotalAssets)
	require.Equal(t, "0.00", *b.Returns.Profit.Value)
	summary, err := f.store.EffectiveSummary(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, b.Closing.Assets, summary.LatestAssets)
	corrected := Money(1_100_000)
	_, err = f.store.WriteAccountRecord(t.Context(), "correct-carry-source", AccountRecordCommand{Action: ReplaceOperation, AccountID: "a", ID: "manual-base", ExpectedVersion: "1", Reason: "Correct source", Entry: &AccountEntry{Kind: "asset", Date: "2024-01-01", TotalAssets: &corrected}})
	require.NoError(t, err)
	revised := basisRead(t, f, "a", "", "")
	require.Equal(t, Money(1_300_000), *revised.Closing.Assets)
	require.Equal(t, "2", revised.Closing.SourceVersion)
	require.Equal(t, "1", revised.Closing.Record.CarriedFrom.Version)
	before, err := f.store.AnalysisBasis(t.Context(), "cash", "", "", 0)
	require.NoError(t, err)
	putSource(t, f.store, "cash", "cash-edit", "1", 9999)
	after, err := f.store.AnalysisBasis(t.Context(), "cash", "", "", 0)
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
	require.Equal(t, Money(4321), *after.Closing.Assets)
	require.NoError(t, worker.Tick(t.Context()))
	require.Equal(t, cash, weeklyJobFor(t, worker, "cash"))
}

func TestRedesignVoidingOnlyExplicitSourceDoesNotResurrectWeeklyCarry(t *testing.T) {
	f := reportedFixture(t)
	f.store.now = func() time.Time { return time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC) }
	basisEntry(t, f, "manual-base", "2024-01-01", "asset", "null", `"10000"`)
	basisEntry(t, f, "manual-in", "2024-02-01", "cash_flow", `"2000"`, "null")
	w, err := NewWeeklyWorker(f.store, nil, nil, WeeklyConfig{Enabled: true, Time: DefaultWeeklyTime}, nil)
	require.NoError(t, err)
	require.NoError(t, w.Tick(t.Context()))
	job := weeklyJobFor(t, w, "a")
	before, err := f.store.GetWeeklyJob(t.Context(), "a", mustInt(t, job.ID))
	require.NoError(t, err)
	f.request(t, "DELETE", "/accounts/a/records/manual-base", "void-source", `{"expected_version":"1","reason":"not an asset observation"}`, 200)
	b := basisRead(t, f, "a", "", "")
	summary, err := f.store.EffectiveSummary(t.Context(), "a")
	require.NoError(t, err)
	require.Nil(t, summary.LatestAssets)
	require.Nil(t, b.Closing.Assets)
	require.Nil(t, b.Returns.Profit.Value)
	require.Nil(t, b.Returns.TWR.Value)
	require.Equal(t, Money(1000000), *b.Closing.Record.TotalAssets)
	after, err := f.store.GetWeeklyJob(t.Context(), "a", mustInt(t, job.ID))
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestRedesignProjectedAmountsAreNotAddedAgainByTWR(t *testing.T) {
	f := reportedFixture(t)
	basisEntry(t, f, "manual-base", "2024-01-01", "asset", "null", `"10000"`)
	basisEntry(t, f, "manual-in", "2024-02-01", "cash_flow", `"2000"`, "null")
	basisEntry(t, f, "manual-in-again", "2024-03-01", "cash_flow", `"1000"`, "null")
	basisEntry(t, f, "manual-close", "2024-04-01", "asset", "null", `"14300"`)
	b := basisRead(t, f, "a", "", "")
	require.Equal(t, Money(1200000), *b.Points[1].Assets)
	require.Equal(t, Money(1300000), *b.Points[2].Assets)
	require.Equal(t, "1300.00", *b.Returns.Profit.Value)
	require.Equal(t, "10.00", *b.Returns.TWR.Percentage)
	require.Equal(t, "0.000000000000", *b.Returns.Curve[1].TWR.Value)
	require.Equal(t, "0.000000000000", *b.Returns.Curve[2].TWR.Value)
	raw := httpPayload(t, b)
	for range 3 {
		again, err := calculateReturns(t.Context(), b, "", "")
		require.NoError(t, err)
		require.Equal(t, b.Returns, again)
		require.Equal(t, raw, httpPayload(t, b))
	}
	custom := basisRead(t, f, "a", "", "from=2024-03-15&to=2024-04-01")
	require.Equal(t, Money(1300000), *custom.Opening.Assets)
	require.Equal(t, "1300.00", *custom.Returns.Profit.Value)
	require.Equal(t, "10.00", *custom.Returns.TWR.Percentage)
}

func TestRedesignDuplicateIdentityEditRollsBackNewCatalogEntry(t *testing.T) {
	f := reportedFixture(t)
	first := `{"expected_version":"0","cash":"12.34","positions":[{"instrument_id":"one","quantity":"1"},{"instrument_id":"two","quantity":"2"}],"securities":[{"id":"one","market":"SH","code":"600000","name":"One","currency":"CNY"},{"id":"two","market":"SH","code":"600001","name":"Two","currency":"CNY"}]}`
	f.request(t, "PUT", "/accounts/a/current-holdings", "initial", first, 200)
	before := f.snapshot(t)
	duplicate := `{"expected_version":"1","cash":"99.00","positions":[{"instrument_id":"one","quantity":"1"},{"instrument_id":"edited","quantity":"3"}],"securities":[{"id":"edited","market":"SH","code":"600000","name":"Different name, same identity","currency":"USD"}]}`
	f.request(t, "PUT", "/accounts/a/current-holdings", "edit", duplicate, 409)
	require.Equal(t, before, f.snapshot(t))
	valid := strings.Replace(duplicate, `"code":"600000"`, `"code":"600002"`, 1)
	f.request(t, "PUT", "/accounts/a/current-holdings", "edit", valid, 200)
	committed := f.snapshot(t)
	f.request(t, "PUT", "/accounts/a/current-holdings", "stale", valid, 409)
	require.Equal(t, committed, f.snapshot(t))
	f.request(t, "PUT", "/accounts/a/current-holdings", "edit", valid, 200)
	require.Equal(t, committed, f.snapshot(t))
}

func TestRedesignValuationIdentitySnapshotSurvivesSecurityReplacementAndDeletion(t *testing.T) {
	f := reportedFixture(t)
	input := CurrentHoldingsInput{ExpectedVersion: "0", Cash: replayMoney(123), Positions: []CurrentPosition{{"original", 1_000_000}}, Securities: []instrumentJSON{{"original", "SH", "600000", "Original identity", CNY}}}
	_, err := f.store.PutCurrentHoldings(t.Context(), "a", "initial", input)
	require.NoError(t, err)
	v := sampleValuation(t, f.store, "a", Handler{Quotes: valuationQuotes(validValuationQuotes)})
	frozen, err := f.store.ValuationHistory(t.Context(), "a", mustInt(t, v.HistoryID))
	require.NoError(t, err)
	basis := basisRead(t, f, "a", "", "")
	input = CurrentHoldingsInput{ExpectedVersion: "1", Cash: replayMoney(900), Positions: []CurrentPosition{{"replacement", 2_000_000}}, Securities: []instrumentJSON{{"replacement", "HK", "00700", "Replacement identity", HKD}}}
	_, err = f.store.PutCurrentHoldings(t.Context(), "a", "replace-security", input)
	require.NoError(t, err)
	for _, remove := range []bool{false, true} {
		if remove {
			putSource(t, f.store, "a", "delete-security", "2", 0)
		}
		got, err := f.store.ValuationHistory(t.Context(), "a", mustInt(t, v.HistoryID))
		require.NoError(t, err)
		require.Equal(t, frozen, got)
		current := basisRead(t, f, "a", "", "")
		require.Equal(t, basis.Revision, current.Revision)
		require.Equal(t, basis.Returns, current.Returns)
		require.Equal(t, "Original identity", got.Instruments[0].Name)
		require.Equal(t, "SH", got.Instruments[0].Market)
		require.Equal(t, "600000", got.Instruments[0].Code)
		require.Equal(t, CNY, got.Instruments[0].Currency)
		require.Equal(t, Quantity(1_000_000), got.Valuation.Items[0].Quantity)
		require.Equal(t, Money(123), got.Valuation.Cash)
	}
}
