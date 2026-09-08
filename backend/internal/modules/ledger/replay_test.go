package ledger

import (
	"encoding/json"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const replayShare Quantity = 1_000_000
const replayPriceUnit Price = 1_000_000
const replayCutoff = "2026-09-06"

func replayMoney(v Money) *Money { return &v }

func replayFixture(cash Money) ([]Opening, []Instrument) {
	return []Opening{{AccountID: "a", Currency: CNY, Date: "2026-09-01", Cash: cash}},
		[]Instrument{{ID: "stock", Market: "TEST", Code: "001", Name: "Synthetic stock", Currency: CNY}}
}

func replayTrade(id string, sequence int64, kind Kind, quantity Quantity, price Price) Operation {
	return Operation{ID: id, Date: "2026-09-02", Sequence: sequence, Kind: kind,
		AccountID: "a", InstrumentID: "stock", Quantity: quantity, Price: price}
}

func replayMust(t *testing.T, openings []Opening, instruments []Instrument, ops []Operation) *Book {
	t.Helper()
	book, err := Replay(openings, instruments, ops, replayCutoff)
	require.NoError(t, err)
	require.NotNil(t, book)
	return book
}

func replayReject(t *testing.T, openings []Opening, instruments []Instrument, ops []Operation, id string, cause error) {
	t.Helper()
	book, err := Replay(openings, instruments, ops, replayCutoff)
	require.Error(t, err)
	assert.Nil(t, book, "failed replay must not expose partial financial state")
	assert.ErrorIs(t, err, cause)
	var operationError *OperationError
	require.ErrorAs(t, err, &operationError)
	assert.Equal(t, id, operationError.ID)
	for _, op := range ops {
		if op.ID == id {
			assert.Equal(t, op.Date, operationError.Date)
			break
		}
	}
	assert.Contains(t, err.Error(), id)
}

func TestReplayCashDepositsAndWithdrawals(t *testing.T) {
	openings, instruments := replayFixture(1_000)
	ops := []Operation{
		{ID: "deposit", Date: "2026-09-02", Sequence: 1, AccountID: "a", Kind: Deposit, Amount: 10_000},
		{ID: "withdraw", Date: "2026-09-03", Sequence: 1, AccountID: "a", Kind: Withdrawal, Amount: 2_500},
	}
	book := replayMust(t, openings, instruments, ops)
	assert.Equal(t, Money(8_500), book.Accounts["a"].Cash)
	assert.Empty(t, book.Accounts["a"].Cycles)
	assert.Empty(t, book.Accounts["a"].Positions)
	assert.Equal(t, []Movement{
		{OperationID: "deposit", Date: "2026-09-02", AccountID: "a", Kind: Deposit, CashDelta: 10_000, CapitalFlow: 10_000},
		{OperationID: "withdraw", Date: "2026-09-03", AccountID: "a", Kind: Withdrawal, CashDelta: -2_500, CapitalFlow: -2_500},
	}, book.Movements)
}

func TestReplayDepositBuySeparatesCapitalAndFeePresence(t *testing.T) {
	for _, tc := range []struct {
		name string
		fee  *Money
		cost Money
	}{
		{"omitted", nil, 10_000},
		{"explicit_zero", replayMoney(0), 10_000},
		{"explicit_fee", replayMoney(125), 10_125},
	} {
		t.Run(tc.name, func(t *testing.T) {
			openings, instruments := replayFixture(0)
			op := replayTrade("fund_buy", 1, DepositBuy, 10*replayShare, 10*replayPriceUnit)
			op.Amount, op.Fee = 15_000, tc.fee
			book := replayMust(t, openings, instruments, []Operation{op})
			assert.Equal(t, Money(15_000)-tc.cost, book.Accounts["a"].Cash)
			assert.Equal(t, &Cycle{ID: "fund_buy", InstrumentID: "stock", Quantity: 10 * replayShare,
				RemainingCost: replayMoney(tc.cost), DilutedBasis: replayMoney(tc.cost), RealizedProfit: replayMoney(0)},
				book.Accounts["a"].Cycles["fund_buy"])
			assert.Equal(t, []Movement{
				{OperationID: op.ID, Date: op.Date, AccountID: "a", Kind: Deposit, CashDelta: 15_000, CapitalFlow: 15_000},
				{OperationID: op.ID, Date: op.Date, AccountID: "a", Kind: Buy, CashDelta: -tc.cost, FeeProvided: tc.fee != nil},
			}, book.Movements)
		})
	}
	t.Run("deposit_can_be_smaller_than_trade_when_cash_exists", func(t *testing.T) {
		openings, instruments := replayFixture(8_000)
		op := replayTrade("fund_buy", 1, DepositBuy, 10*replayShare, 10*replayPriceUnit)
		op.Amount = 3_000
		book := replayMust(t, openings, instruments, []Operation{op})
		assert.Equal(t, Money(1_000), book.Accounts["a"].Cash)
		require.Len(t, book.Movements, 2)
		assert.Equal(t, Money(3_000), book.Movements[0].CapitalFlow)
		assert.Zero(t, book.Movements[1].CapitalFlow)
	})
}

func TestReplayInternalTradesHaveNoCapitalFlows(t *testing.T) {
	openings, instruments := replayFixture(20_000)
	buy := replayTrade("buy", 1, Buy, 10*replayShare, 10*replayPriceUnit)
	buy.Fee = replayMoney(100)
	sell := replayTrade("sell", 2, Sell, 4*replayShare, 15*replayPriceUnit)
	sell.Fee = replayMoney(50)
	book := replayMust(t, openings, instruments, []Operation{buy, sell})
	assert.Equal(t, Money(15_850), book.Accounts["a"].Cash)
	assert.Equal(t, &Cycle{ID: "buy", InstrumentID: "stock", Quantity: 6 * replayShare,
		RemainingCost: replayMoney(6_060), DilutedBasis: replayMoney(4_150), RealizedProfit: replayMoney(1_910)},
		book.Accounts["a"].Cycles["buy"])
	assert.Equal(t, []Movement{
		{OperationID: buy.ID, Date: buy.Date, AccountID: "a", Kind: Buy, CashDelta: -10_100, FeeProvided: true},
		{OperationID: sell.ID, Date: sell.Date, AccountID: "a", Kind: Sell, CashDelta: 5_950, FeeProvided: true},
	}, book.Movements)
}

func TestReplaySellWithdrawUsesIndependentAmount(t *testing.T) {
	for _, amount := range []Money{5_000, 12_000} {
		t.Run(amount.String(), func(t *testing.T) {
			openings, instruments := replayFixture(20_000)
			buy := replayTrade("buy", 1, Buy, 10*replayShare, 10*replayPriceUnit)
			sell := replayTrade("sell_out", 2, SellWithdraw, 5*replayShare, 20*replayPriceUnit)
			sell.Amount, sell.Fee = amount, replayMoney(100)
			book := replayMust(t, openings, instruments, []Operation{buy, sell})
			assert.Equal(t, Money(19_900)-amount, book.Accounts["a"].Cash)
			require.Len(t, book.Movements, 3)
			assert.Equal(t, []Movement{
				{OperationID: sell.ID, Date: sell.Date, AccountID: "a", Kind: Sell, CashDelta: 9_900, FeeProvided: true},
				{OperationID: sell.ID, Date: sell.Date, AccountID: "a", Kind: Withdrawal, CashDelta: -amount, CapitalFlow: -amount},
			}, book.Movements[1:])
			assert.Equal(t, &Cycle{ID: "buy", InstrumentID: "stock", Quantity: 5 * replayShare,
				RemainingCost: replayMoney(5_000), DilutedBasis: replayMoney(100), RealizedProfit: replayMoney(4_900)},
				book.Accounts["a"].Cycles["buy"])
		})
	}
}

func TestReplayMovingAverageUsesAccumulatedCost(t *testing.T) {
	openings, instruments := replayFixture(50_000)
	first := replayTrade("first", 1, Buy, 10*replayShare, 10*replayPriceUnit)
	first.Fee = replayMoney(100)
	second := replayTrade("second", 2, Buy, 10*replayShare, 20*replayPriceUnit)
	second.Fee = replayMoney(200)
	sell := replayTrade("sell", 3, Sell, 5*replayShare, 30*replayPriceUnit)
	sell.Fee = replayMoney(50)
	book := replayMust(t, openings, instruments, []Operation{first, second, sell})
	account := book.Accounts["a"]
	assert.Equal(t, Money(34_650), account.Cash)
	assert.Equal(t, "first", account.Positions["stock"])
	require.Len(t, account.Cycles, 1)
	cycle := account.Cycles["first"]
	require.Equal(t, &Cycle{ID: "first", InstrumentID: "stock", Quantity: 15 * replayShare,
		RemainingCost: replayMoney(22_725), DilutedBasis: replayMoney(15_350), RealizedProfit: replayMoney(7_375)}, cycle)
	average, err := cycle.MovingAverage()
	require.NoError(t, err)
	require.NotNil(t, average)
	assert.Equal(t, Price(15_150_000), *average)
}

func TestReplayAllocationRemainderAndNegativeDilution(t *testing.T) {
	openings, instruments := replayFixture(1_000)
	buy := replayTrade("buy", 1, Buy, 3*replayShare, replayPriceUnit)
	buy.Fee = replayMoney(1)
	ops := []Operation{buy,
		replayTrade("sell1", 2, Sell, replayShare, 2*replayPriceUnit),
		replayTrade("sell2", 3, Sell, replayShare, 2*replayPriceUnit),
		replayTrade("sell3", 4, Sell, replayShare, 2*replayPriceUnit),
	}
	for _, tc := range []struct {
		n                     int
		quantity              Quantity
		cost, diluted, profit Money
	}{
		{1, 3 * replayShare, 301, 301, 0},
		{2, 2 * replayShare, 201, 101, 100},
		{3, replayShare, 100, -99, 199},
		{4, 0, 0, -299, 299},
	} {
		t.Run(ops[tc.n-1].ID, func(t *testing.T) {
			book := replayMust(t, openings, instruments, ops[:tc.n])
			cycle := book.Accounts["a"].Cycles["buy"]
			require.Equal(t, &Cycle{ID: "buy", InstrumentID: "stock", Quantity: tc.quantity,
				RemainingCost: replayMoney(tc.cost), DilutedBasis: replayMoney(tc.diluted), RealizedProfit: replayMoney(tc.profit)}, cycle)
			assert.Equal(t, Money(699+200*(tc.n-1)), book.Accounts["a"].Cash)
			if tc.n == 3 {
				price, err := cycle.DilutedCost()
				require.NoError(t, err)
				require.NotNil(t, price)
				assert.Equal(t, Price(-990_000), *price)
			}
			if tc.quantity == 0 {
				average, err := cycle.MovingAverage()
				require.NoError(t, err)
				assert.Nil(t, average)
				diluted, err := cycle.DilutedCost()
				require.NoError(t, err)
				assert.Nil(t, diluted)
			}
		})
	}
}

func TestReplayLateDividendBelongsToClosedCycle(t *testing.T) {
	openings, instruments := replayFixture(5_000)
	ops := []Operation{
		replayTrade("old", 1, Buy, 2*replayShare, 10*replayPriceUnit),
		replayTrade("close", 2, Sell, 2*replayShare, 15*replayPriceUnit),
		replayTrade("new", 3, Buy, replayShare, 8*replayPriceUnit),
		{ID: "late_dividend", Date: "2026-09-03", Sequence: 1, Kind: Dividend,
			AccountID: "a", InstrumentID: "stock", CycleID: "old", Amount: 500},
	}
	before := replayMust(t, openings, instruments, ops[:3])
	book := replayMust(t, openings, instruments, ops)
	account := book.Accounts["a"]
	assert.Equal(t, Money(5_700), account.Cash)
	assert.Equal(t, "new", account.Positions["stock"])
	require.Len(t, account.Cycles, 2)
	assert.Equal(t, before.Accounts["a"].Cycles["new"], account.Cycles["new"])
	assert.Equal(t, &Cycle{ID: "new", InstrumentID: "stock", Quantity: replayShare,
		RemainingCost: replayMoney(800), DilutedBasis: replayMoney(800), RealizedProfit: replayMoney(0)}, account.Cycles["new"])
	assert.Equal(t, &Cycle{ID: "old", InstrumentID: "stock", RemainingCost: replayMoney(0),
		DilutedBasis: replayMoney(-1_500), RealizedProfit: replayMoney(1_000), Dividends: 500}, account.Cycles["old"])
	require.Len(t, book.Movements, 4)
	assert.Equal(t, Movement{OperationID: "late_dividend", Date: "2026-09-03", AccountID: "a", Kind: Dividend, CashDelta: 500}, book.Movements[3])
}

func TestReplayOpeningBasesAreIndependentlyKnown(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		cost, diluted                     *Money
		wantCost, wantDiluted, wantProfit *Money
	}{
		{"both_unknown", nil, nil, nil, nil, nil},
		{"cost_only", replayMoney(2_000), nil, replayMoney(3_000), nil, replayMoney(500)},
		{"diluted_only", nil, replayMoney(5_000), nil, replayMoney(5_300), nil},
		{"both_known", replayMoney(2_000), replayMoney(5_000), replayMoney(3_000), replayMoney(5_300), replayMoney(500)},
		{"known_zero", replayMoney(0), replayMoney(0), replayMoney(2_000), replayMoney(300), replayMoney(1_500)},
		{"negative_diluted", replayMoney(2_000), replayMoney(-1_000), replayMoney(3_000), replayMoney(-700), replayMoney(500)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			openings, instruments := replayFixture(10_000)
			openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: 2 * replayShare, Cost: tc.cost, DilutedBasis: tc.diluted}}
			cycleID := OpeningCycleID("a", "stock")
			initial := replayMust(t, openings, instruments, nil)
			assert.Empty(t, initial.Movements, "opening assets are not new contributions")
			assert.Equal(t, Money(10_000), initial.Accounts["a"].Cash)
			initialCycle := initial.Accounts["a"].Cycles[cycleID]
			require.NotNil(t, initialCycle)
			assert.Equal(t, tc.cost, initialCycle.RemainingCost)
			assert.Equal(t, tc.diluted, initialCycle.DilutedBasis)
			average, err := initialCycle.MovingAverage()
			require.NoError(t, err)
			assert.Equal(t, tc.cost == nil, average == nil)
			diluted, err := initialCycle.DilutedCost()
			require.NoError(t, err)
			assert.Equal(t, tc.diluted == nil, diluted == nil)
			ops := []Operation{
				replayTrade("sell", 1, Sell, replayShare, 15*replayPriceUnit),
				replayTrade("buy", 2, Buy, replayShare, 20*replayPriceUnit),
				{ID: "dividend", Date: "2026-09-02", Sequence: 3, AccountID: "a", Kind: Dividend,
					InstrumentID: "stock", CycleID: cycleID, Amount: 200},
			}
			book := replayMust(t, openings, instruments, ops)
			assert.Equal(t, Money(9_700), book.Accounts["a"].Cash)
			assert.Equal(t, cycleID, book.Accounts["a"].Positions["stock"])
			assert.Equal(t, &Cycle{ID: cycleID, InstrumentID: "stock", Quantity: 2 * replayShare,
				RemainingCost: tc.wantCost, DilutedBasis: tc.wantDiluted, RealizedProfit: tc.wantProfit, Dividends: 200},
				book.Accounts["a"].Cycles[cycleID])
			require.Len(t, book.Movements, 3)
			for _, movement := range book.Movements {
				assert.Zero(t, movement.CapitalFlow)
			}
		})
	}
}

func TestReplayUnknownOpeningClosesAndRestartsKnownCycle(t *testing.T) {
	openings, instruments := replayFixture(0)
	openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare}}
	ops := []Operation{
		replayTrade("close", 1, Sell, replayShare, 10*replayPriceUnit),
		replayTrade("restart", 2, Buy, replayShare, 5*replayPriceUnit),
	}
	book := replayMust(t, openings, instruments, ops)
	account := book.Accounts["a"]
	assert.Equal(t, Money(500), account.Cash)
	assert.Equal(t, "restart", account.Positions["stock"])
	assert.Equal(t, &Cycle{ID: OpeningCycleID("a", "stock"), InstrumentID: "stock", RemainingCost: replayMoney(0)},
		account.Cycles[OpeningCycleID("a", "stock")])
	assert.Equal(t, &Cycle{ID: "restart", InstrumentID: "stock", Quantity: replayShare,
		RemainingCost: replayMoney(500), DilutedBasis: replayMoney(500), RealizedProfit: replayMoney(0)}, account.Cycles["restart"])
	for _, movement := range book.Movements {
		assert.Zero(t, movement.CapitalFlow)
	}
}

func TestReplaySortsWithoutMutatingOrAliasingInputs(t *testing.T) {
	openings, instruments := replayFixture(5_000)
	openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: 2 * replayShare,
		Cost: replayMoney(1_000), DilutedBasis: replayMoney(-100)}}
	instruments[0].Currency = USD
	fx := &FXSnapshot{Rate: 100_000_000, Date: "2026-09-01", Source: "synthetic", FetchedAt: "2026-09-02T00:00:00Z"}
	buy := replayTrade("buy", 2, Buy, replayShare, 10*replayPriceUnit)
	buy.Fee, buy.FX = replayMoney(10), fx
	sell := replayTrade("sell", 1, Sell, replayShare, 15*replayPriceUnit)
	sell.Date, sell.FX = "2026-09-03", fx
	ops := []Operation{
		{ID: "deposit", Date: "2026-09-02", Sequence: 1, AccountID: "a", Kind: Deposit, Amount: 2_000},
		buy, sell,
		{ID: "withdraw", Date: "2026-09-03", Sequence: 2, AccountID: "a", Kind: Withdrawal, Amount: 1_000},
	}
	want := replayMust(t, openings, instruments, ops)
	rng := rand.New(rand.NewSource(42))
	for range 20 {
		shuffled := append([]Operation(nil), ops...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		before, err := json.Marshal([]any{openings, instruments, shuffled})
		require.NoError(t, err)
		book := replayMust(t, openings, instruments, shuffled)
		assert.Equal(t, want, book)
		after, err := json.Marshal([]any{openings, instruments, shuffled})
		require.NoError(t, err)
		assert.Equal(t, before, after, "Replay must not sort the caller's slice or mutate nested pointers")
		cycle := book.Accounts["a"].Cycles[OpeningCycleID("a", "stock")]
		*cycle.RemainingCost, *cycle.DilutedBasis = 123, 456
		afterMutation, err := json.Marshal([]any{openings, instruments, shuffled})
		require.NoError(t, err)
		assert.Equal(t, before, afterMutation, "output cost pointers must not alias opening inputs")
	}
}

func TestReplayVoidedSaleRecomputesLaterCost(t *testing.T) {
	openings, instruments := replayFixture(50_000)
	ops := []Operation{
		replayTrade("buy1", 1, Buy, 10*replayShare, 10*replayPriceUnit),
		replayTrade("void_sale", 2, Sell, 5*replayShare, 20*replayPriceUnit),
		replayTrade("buy2", 3, Buy, 10*replayShare, 20*replayPriceUnit),
		replayTrade("sell", 4, Sell, 5*replayShare, 30*replayPriceUnit),
	}
	before := replayMust(t, openings, instruments, ops)
	assert.Equal(t, replayMoney(16_667), before.Accounts["a"].Cycles["buy1"].RemainingCost)
	ops[1].Voided = true
	book := replayMust(t, openings, instruments, ops)
	assert.Equal(t, Money(35_000), book.Accounts["a"].Cash)
	assert.Equal(t, &Cycle{ID: "buy1", InstrumentID: "stock", Quantity: 15 * replayShare,
		RemainingCost: replayMoney(22_500), DilutedBasis: replayMoney(15_000), RealizedProfit: replayMoney(7_500)},
		book.Accounts["a"].Cycles["buy1"])
	require.Len(t, book.Movements, 3)
	for _, movement := range book.Movements {
		assert.NotEqual(t, "void_sale", movement.OperationID)
	}
	withoutVoided := []Operation{ops[0], ops[2], ops[3]}
	assert.Equal(t, replayMust(t, openings, instruments, withoutVoided), book)
}

func TestReplayVoidedEarlyPurchaseInvalidatesHistory(t *testing.T) {
	t.Run("later_oversell", func(t *testing.T) {
		openings, instruments := replayFixture(50_000)
		ops := []Operation{
			replayTrade("early", 1, Buy, 10*replayShare, 10*replayPriceUnit),
			replayTrade("later", 2, Buy, 2*replayShare, 10*replayPriceUnit),
			replayTrade("oversell", 3, Sell, 5*replayShare, 20*replayPriceUnit),
		}
		replayMust(t, openings, instruments, ops)
		ops[0].Voided = true
		replayReject(t, openings, instruments, ops, "oversell", ErrInsufficientStock)
	})
	t.Run("void_deposit_buy_removes_unspent_deposit", func(t *testing.T) {
		openings, instruments := replayFixture(0)
		funded := replayTrade("early", 1, DepositBuy, replayShare, 10*replayPriceUnit)
		funded.Amount = 5_000
		ops := []Operation{funded, replayTrade("unfunded", 2, Buy, replayShare, 20*replayPriceUnit)}
		replayMust(t, openings, instruments, ops)
		ops[0].Voided = true
		replayReject(t, openings, instruments, ops, "unfunded", ErrInsufficientCash)
	})
}

func TestReplayRejectsIdentityDatesAndSequence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Operation)
	}{
		{"empty_id", func(o *Operation) { o.ID = "" }},
		{"invalid_id", func(o *Operation) { o.ID = "bad:id" }},
		{"zero_sequence", func(o *Operation) { o.Sequence = 0 }},
		{"negative_sequence", func(o *Operation) { o.Sequence = -1 }},
		{"missing_date", func(o *Operation) { o.Date = "" }},
		{"non_padded_date", func(o *Operation) { o.Date = "2026-9-02" }},
		{"timestamp_not_date", func(o *Operation) { o.Date = "2026-09-02T00:00:00Z" }},
		{"invalid_calendar_day", func(o *Operation) { o.Date = "2026-09-31" }},
		{"invalid_leap_day", func(o *Operation) { o.Date = "2026-02-29" }},
		{"year_zero", func(o *Operation) { o.Date = "0000-09-02" }},
		{"future", func(o *Operation) { o.Date = "2026-09-07" }},
		{"before_opening", func(o *Operation) { o.Date = "2026-08-31" }},
		{"unknown_account", func(o *Operation) { o.AccountID = "missing" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			openings, instruments := replayFixture(0)
			op := Operation{ID: "invalid", Date: "2026-09-02", Sequence: 1, AccountID: "a", Kind: Deposit, Amount: 100}
			tc.mutate(&op)
			replayReject(t, openings, instruments, []Operation{op}, op.ID, ErrOperation)
		})
	}
	for _, voided := range []bool{false, true} {
		for _, collision := range []string{"duplicate_id", "daily_sequence"} {
			t.Run(collision+"/voided="+map[bool]string{false: "false", true: "true"}[voided], func(t *testing.T) {
				openings, instruments := replayFixture(0)
				first := Operation{ID: "first", Date: "2026-09-02", Sequence: 1, AccountID: "a", Kind: Deposit, Amount: 100, Voided: voided}
				second := Operation{ID: "second", Date: "2026-09-02", Sequence: 2, AccountID: "a", Kind: Deposit, Amount: 100}
				if collision == "duplicate_id" {
					second.ID = first.ID
				} else {
					second.Sequence = first.Sequence
				}
				replayReject(t, openings, instruments, []Operation{first, second}, second.ID, ErrOperation)
			})
		}
	}
	for _, asOf := range []string{"", "2026-9-06", "2026-02-29", "0000-01-01", "2026-09-06T00:00:00Z"} {
		t.Run("cutoff/"+asOf, func(t *testing.T) {
			openings, instruments := replayFixture(0)
			book, err := Replay(openings, instruments, nil, asOf)
			assert.Nil(t, book)
			assert.ErrorIs(t, err, ErrOperation)
		})
	}
	t.Run("valid_leap_day_and_cutoff_inclusive", func(t *testing.T) {
		openings, instruments := replayFixture(0)
		openings[0].Date = "2024-02-29"
		op := Operation{ID: "leap", Date: "2024-02-29", Sequence: 1, AccountID: "a", Kind: Deposit, Amount: 100}
		book, err := Replay(openings, instruments, []Operation{op}, "2024-02-29")
		require.NoError(t, err)
		require.NotNil(t, book)
		assert.Equal(t, Money(100), book.Accounts["a"].Cash)
	})
}

func TestReplayFXRoundsOriginalNetBeforeConversion(t *testing.T) {
	openings, instruments := replayFixture(10_000)
	instruments[0].Currency = USD
	fx := &FXSnapshot{Rate: 725_000_000, Date: "2026-09-01", Source: "synthetic", FetchedAt: "2026-09-02T00:00:00.123Z"}
	buy := replayTrade("buy", 1, Buy, replayShare, 1_005_000)
	buy.Fee, buy.FX = replayMoney(1), fx
	sell := replayTrade("sell", 2, Sell, replayShare/2, 2_010_000)
	sell.Fee, sell.FX = replayMoney(1), fx
	dividend := Operation{ID: "dividend", Date: "2026-09-02", Sequence: 3, AccountID: "a", Kind: Dividend,
		InstrumentID: "stock", CycleID: "buy", Amount: 2, FX: fx}
	book := replayMust(t, openings, instruments, []Operation{buy, sell, dividend})
	// Buy: round(1 * 1.005) = 1.01; (1.01 + .01) * 7.25 = 7.395 -> 7.40.
	// Sell: round(.5 * 2.01) = 1.01; (1.01 - .01) * 7.25 = 7.25.
	assert.Equal(t, Money(10_000), book.Accounts["a"].Cash)
	assert.Equal(t, &Cycle{ID: "buy", InstrumentID: "stock", Quantity: replayShare / 2,
		RemainingCost: replayMoney(370), DilutedBasis: replayMoney(0), RealizedProfit: replayMoney(355), Dividends: 15},
		book.Accounts["a"].Cycles["buy"])
	assert.Equal(t, []Movement{
		{OperationID: buy.ID, Date: buy.Date, AccountID: "a", Kind: Buy, CashDelta: -740, FeeProvided: true},
		{OperationID: sell.ID, Date: sell.Date, AccountID: "a", Kind: Sell, CashDelta: 725, FeeProvided: true},
		{OperationID: dividend.ID, Date: dividend.Date, AccountID: "a", Kind: Dividend, CashDelta: 15},
	}, book.Movements)
}

func TestReplayFXCombinationAmountsStayInAccountCurrency(t *testing.T) {
	openings, instruments := replayFixture(0)
	instruments[0].Currency = USD
	fx := &FXSnapshot{Rate: 700_000_000, Date: "2026-09-02", Source: "synthetic", FetchedAt: "2026-09-02T08:00:00+08:00"}
	buy := replayTrade("fund_buy", 1, DepositBuy, replayShare, replayPriceUnit)
	buy.Amount, buy.FX = 1_000, fx
	sell := replayTrade("sell_out", 2, SellWithdraw, replayShare, 2*replayPriceUnit)
	sell.Amount, sell.FX = 500, fx
	book := replayMust(t, openings, instruments, []Operation{buy, sell})
	assert.Equal(t, Money(1_200), book.Accounts["a"].Cash)
	assert.Equal(t, []Movement{
		{OperationID: buy.ID, Date: buy.Date, AccountID: "a", Kind: Deposit, CashDelta: 1_000, CapitalFlow: 1_000},
		{OperationID: buy.ID, Date: buy.Date, AccountID: "a", Kind: Buy, CashDelta: -700},
		{OperationID: sell.ID, Date: sell.Date, AccountID: "a", Kind: Sell, CashDelta: 1_400},
		{OperationID: sell.ID, Date: sell.Date, AccountID: "a", Kind: Withdrawal, CashDelta: -500, CapitalFlow: -500},
	}, book.Movements)
	assert.Equal(t, &Cycle{ID: buy.ID, InstrumentID: "stock", RemainingCost: replayMoney(0),
		DilutedBasis: replayMoney(-700), RealizedProfit: replayMoney(700)}, book.Accounts["a"].Cycles[buy.ID])
}

func TestReplayRejectsInvalidFXSnapshots(t *testing.T) {
	for _, kind := range []Kind{Buy, Sell, DepositBuy, SellWithdraw, Dividend} {
		for _, tc := range []struct {
			name   string
			mutate func(*Operation)
		}{
			{"missing", func(o *Operation) { o.FX = nil }},
			{"zero_rate", func(o *Operation) { o.FX.Rate = 0 }},
			{"negative_rate", func(o *Operation) { o.FX.Rate = -1 }},
			{"missing_date", func(o *Operation) { o.FX.Date = "" }},
			{"invalid_date", func(o *Operation) { o.FX.Date = "2026-02-29" }},
			{"future_to_operation", func(o *Operation) { o.FX.Date = "2026-09-03" }},
			{"missing_source", func(o *Operation) { o.FX.Source = "" }},
			{"blank_source", func(o *Operation) { o.FX.Source = " \t" }},
			{"long_source", func(o *Operation) { o.FX.Source = strings.Repeat("x", 513) }},
			{"missing_time", func(o *Operation) { o.FX.FetchedAt = "" }},
			{"date_only_time", func(o *Operation) { o.FX.FetchedAt = "2026-09-02" }},
			{"invalid_calendar_time", func(o *Operation) { o.FX.FetchedAt = "2026-02-29T00:00:00Z" }},
			{"missing_time_zone", func(o *Operation) { o.FX.FetchedAt = "2026-09-02T00:00:00" }},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				openings, instruments := replayFixture(10_000)
				instruments[0].Currency = USD
				openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare, Cost: replayMoney(100)}}
				op := replayTrade("invalid_fx", 1, kind, replayShare, replayPriceUnit)
				if kind == DepositBuy || kind == SellWithdraw {
					op.Amount = 100
				}
				if kind == Dividend {
					op.Quantity, op.Price, op.Amount, op.CycleID = 0, 0, 100, OpeningCycleID("a", "stock")
				}
				op.FX = &FXSnapshot{Rate: 700_000_000, Date: "2026-09-01", Source: "synthetic", FetchedAt: "2026-09-02T00:00:00Z"}
				tc.mutate(&op)
				replayReject(t, openings, instruments, []Operation{op}, op.ID, ErrOperation)
			})
		}
	}
	for _, kind := range []Kind{Buy, Sell, DepositBuy, SellWithdraw, Dividend} {
		t.Run(string(kind)+"/same_currency_even_identity_rate", func(t *testing.T) {
			openings, instruments := replayFixture(10_000)
			openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare}}
			op := replayTrade("same_currency", 1, kind, replayShare, replayPriceUnit)
			if kind == DepositBuy || kind == SellWithdraw {
				op.Amount = 100
			}
			if kind == Dividend {
				op.Quantity, op.Price, op.Amount, op.CycleID = 0, 0, 100, OpeningCycleID("a", "stock")
			}
			op.FX = &FXSnapshot{Rate: 100_000_000, Date: "2026-09-01", Source: "synthetic", FetchedAt: "2026-09-02T00:00:00Z"}
			replayReject(t, openings, instruments, []Operation{op}, op.ID, ErrOperation)
		})
	}
}

func TestReplayTransferLegsAndAtomicFailure(t *testing.T) {
	for _, tc := range []struct {
		name           string
		targetCash     Money
		targetCurrency Currency
		amount         Money
		wantError      error
	}{
		{"same_currency", 200, CNY, 300, nil},
		{"entire_balance", 200, CNY, 1_000, nil},
		{"target_overflow", math.MaxInt64, CNY, 300, ErrPrecision},
		{"source_overdraft", 200, CNY, 1_001, ErrInsufficientCash},
		{"cross_currency", 200, USD, 300, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			openings, instruments := replayFixture(1_000)
			openings = append(openings, Opening{AccountID: "b", Currency: tc.targetCurrency, Date: "2026-09-01", Cash: tc.targetCash})
			op := Operation{ID: "transfer", Date: "2026-09-02", Sequence: 1, Kind: Transfer, AccountID: "a", ToAccountID: "b", Amount: tc.amount}
			before := append([]Opening(nil), openings...)
			if tc.wantError != nil {
				replayReject(t, openings, instruments, []Operation{op}, op.ID, tc.wantError)
			} else {
				book := replayMust(t, openings, instruments, []Operation{op})
				assert.Equal(t, Money(1_000)-tc.amount, book.Accounts["a"].Cash)
				assert.Equal(t, tc.targetCash+tc.amount, book.Accounts["b"].Cash)
				assert.Equal(t, []Movement{
					{OperationID: op.ID, Date: op.Date, AccountID: "a", Kind: Transfer, CashDelta: -tc.amount, CapitalFlow: -tc.amount, Counterparty: "b"},
					{OperationID: op.ID, Date: op.Date, AccountID: "b", Kind: Transfer, CashDelta: tc.amount, CapitalFlow: tc.amount, Counterparty: "a"},
				}, book.Movements)
			}
			assert.Equal(t, before, openings)
			unaffected := replayMust(t, openings, instruments, nil)
			assert.Equal(t, Money(1_000), unaffected.Accounts["a"].Cash)
			assert.Equal(t, tc.targetCash, unaffected.Accounts["b"].Cash)
			assert.Empty(t, unaffected.Movements)
		})
	}
}

func TestReplayRejectsInvalidTransferTargets(t *testing.T) {
	for _, name := range []string{"missing", "unknown", "self", "before_target_opening"} {
		t.Run(name, func(t *testing.T) {
			openings, instruments := replayFixture(1_000)
			openings = append(openings, Opening{AccountID: "b", Currency: CNY, Date: "2026-09-03"})
			op := Operation{ID: "invalid_transfer", Date: "2026-09-02", Sequence: 1, Kind: Transfer,
				AccountID: "a", ToAccountID: "b", Amount: 100}
			switch name {
			case "missing":
				op.ToAccountID = ""
			case "unknown":
				op.ToAccountID = "unknown"
			case "self":
				op.ToAccountID = "a"
			}
			replayReject(t, openings, instruments, []Operation{op}, op.ID, ErrOperation)
		})
	}
}

func TestReplayRejectsOversellingAndOverdrafts(t *testing.T) {
	for _, kind := range []Kind{Buy, DepositBuy, Withdrawal, SellWithdraw, Sell} {
		t.Run(string(kind), func(t *testing.T) {
			openings, instruments := replayFixture(0)
			op := replayTrade("conflict", 1, kind, replayShare, replayPriceUnit)
			cause := ErrInsufficientCash
			switch kind {
			case DepositBuy:
				op.Amount = 99
			case Withdrawal:
				op.InstrumentID, op.Quantity, op.Price, op.Amount = "", 0, 0, 1
			case SellWithdraw:
				openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare, Cost: replayMoney(50)}}
				op.Amount = 101
			case Sell:
				cause = ErrInsufficientStock
			}
			replayReject(t, openings, instruments, []Operation{op}, op.ID, cause)
		})
	}
	t.Run("cannot_borrow_from_later_deposit", func(t *testing.T) {
		openings, instruments := replayFixture(0)
		ops := []Operation{
			{ID: "later_funding", Date: "2026-09-02", Sequence: 2, AccountID: "a", Kind: Deposit, Amount: 100},
			replayTrade("early_buy", 1, Buy, replayShare, replayPriceUnit),
		}
		replayReject(t, openings, instruments, ops, "early_buy", ErrInsufficientCash)
	})
}

func TestReplayRejectsPrecisionOverflow(t *testing.T) {
	for _, name := range []string{"cash", "trade_notional", "buy_fee", "quantity", "remaining_cost", "diluted_basis", "negative_diluted_basis", "fx"} {
		t.Run(name, func(t *testing.T) {
			openings, instruments := replayFixture(math.MaxInt64)
			op := replayTrade("overflow", 1, Buy, replayShare, replayPriceUnit)
			switch name {
			case "cash":
				op.Kind, op.InstrumentID, op.Quantity, op.Price, op.Amount = Deposit, "", 0, 0, 1
			case "trade_notional":
				op.Quantity, op.Price = math.MaxInt64, math.MaxInt64
			case "buy_fee":
				op.Fee = replayMoney(math.MaxInt64)
			case "quantity":
				openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: math.MaxInt64}}
			case "remaining_cost":
				openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare, Cost: replayMoney(math.MaxInt64)}}
			case "diluted_basis":
				openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare, DilutedBasis: replayMoney(math.MaxInt64)}}
			case "negative_diluted_basis":
				openings[0].Cash = 0
				openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare, DilutedBasis: replayMoney(math.MinInt64)}}
				op.Kind = Sell
			case "fx":
				instruments[0].Currency = USD
				op.Price = math.MaxInt64
				op.FX = &FXSnapshot{Rate: math.MaxInt64, Date: "2026-09-01", Source: "synthetic", FetchedAt: "2026-09-02T00:00:00Z"}
			}
			replayReject(t, openings, instruments, []Operation{op}, op.ID, ErrPrecision)
		})
	}
	t.Run("realized_profit", func(t *testing.T) {
		openings, instruments := replayFixture(0)
		const lot Quantity = 10_000_000_000
		openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: 3 * lot, Cost: replayMoney(0)}}
		ops := []Operation{
			replayTrade("sell1", 1, Sell, lot, math.MaxInt64/2),
			{ID: "withdraw1", Date: "2026-09-02", Sequence: 2, AccountID: "a", Kind: Withdrawal, Amount: math.MaxInt64 / 2},
			replayTrade("sell2", 3, Sell, lot, math.MaxInt64/2),
			{ID: "withdraw2", Date: "2026-09-02", Sequence: 4, AccountID: "a", Kind: Withdrawal, Amount: math.MaxInt64 / 2},
			replayTrade("profit_overflow", 5, Sell, lot, 100),
		}
		// Withdraw proceeds so only accumulated realized profit, not cash, overflows.
		before := replayMust(t, openings, instruments, ops[:4])
		assert.Zero(t, before.Accounts["a"].Cash)
		assert.Equal(t, replayMoney(math.MaxInt64-1), before.Accounts["a"].Cycles[OpeningCycleID("a", "stock")].RealizedProfit)
		replayReject(t, openings, instruments, ops, "profit_overflow", ErrPrecision)
	})
}

func TestReplaySaleFeesCanExceedProceedsWithoutOverdrawing(t *testing.T) {
	for _, cash := range []Money{0, 1, 10_000} {
		t.Run(cash.String(), func(t *testing.T) {
			openings, instruments := replayFixture(cash)
			openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare,
				Cost: replayMoney(200), DilutedBasis: replayMoney(200)}}
			op := replayTrade("small_sale", 1, Sell, replayShare, replayPriceUnit)
			op.Fee = replayMoney(101)
			if cash == 0 {
				replayReject(t, openings, instruments, []Operation{op}, op.ID, ErrInsufficientCash)
				return
			}
			book := replayMust(t, openings, instruments, []Operation{op})
			assert.Equal(t, cash-1, book.Accounts["a"].Cash)
			cycle := book.Accounts["a"].Cycles[OpeningCycleID("a", "stock")]
			assert.Zero(t, cycle.Quantity)
			assert.Equal(t, replayMoney(0), cycle.RemainingCost)
			assert.Equal(t, replayMoney(-201), cycle.RealizedProfit)
			assert.Equal(t, replayMoney(201), cycle.DilutedBasis)
			require.Len(t, book.Movements, 1)
			assert.Equal(t, Money(-1), book.Movements[0].CashDelta)
			assert.Zero(t, book.Movements[0].CapitalFlow)
		})
	}
}

func TestReplayRejectsUnexpectedOperationFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Operation)
	}{
		{"unknown_kind", func(o *Operation) { o.Kind = "mystery" }},
		{"negative_amount", func(o *Operation) { o.Amount = -1 }},
		{"negative_quantity", func(o *Operation) { o.Quantity = -1 }},
		{"zero_quantity", func(o *Operation) { o.Quantity = 0 }},
		{"negative_price", func(o *Operation) { o.Price = -1 }},
		{"zero_price", func(o *Operation) { o.Price = 0 }},
		{"negative_fee", func(o *Operation) { o.Fee = replayMoney(-1) }},
		{"unknown_instrument", func(o *Operation) { o.InstrumentID = "missing" }},
		{"buy_external_amount", func(o *Operation) { o.Amount = 1 }},
		{"sell_external_amount", func(o *Operation) { o.Kind, o.Amount = Sell, 1 }},
		{"missing_deposit_amount", func(o *Operation) { o.Kind = DepositBuy }},
		{"missing_withdraw_amount", func(o *Operation) { o.Kind = SellWithdraw }},
		{"trade_target", func(o *Operation) { o.ToAccountID = "b" }},
		{"trade_cycle", func(o *Operation) { o.CycleID = OpeningCycleID("a", "stock") }},
		{"rounded_zero_notional", func(o *Operation) { o.Quantity, o.Price = 1, 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			openings, instruments := replayFixture(10_000)
			openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare}}
			op := replayTrade("invalid", 1, Buy, replayShare, replayPriceUnit)
			tc.mutate(&op)
			replayReject(t, openings, instruments, []Operation{op}, op.ID, ErrOperation)
		})
	}
	for _, kind := range []Kind{Deposit, Withdrawal, Transfer, Dividend} {
		for _, field := range []string{"quantity", "price", "fee", "instrument", "fx", "cycle", "target", "zero_amount"} {
			if kind == Dividend && (field == "instrument" || field == "fx" || field == "cycle") || kind == Transfer && field == "target" {
				continue
			}
			t.Run(string(kind)+"/"+field, func(t *testing.T) {
				openings, instruments := replayFixture(10_000)
				openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare}}
				openings = append(openings, Opening{AccountID: "b", Currency: CNY, Date: "2026-09-01"})
				op := Operation{ID: "invalid", Date: "2026-09-02", Sequence: 1, Kind: kind, AccountID: "a", Amount: 100}
				if kind == Transfer {
					op.ToAccountID = "b"
				}
				if kind == Dividend {
					op.InstrumentID, op.CycleID = "stock", OpeningCycleID("a", "stock")
				}
				switch field {
				case "quantity":
					op.Quantity = 1
				case "price":
					op.Price = 1
				case "fee":
					op.Fee = replayMoney(0)
				case "instrument":
					op.InstrumentID = "stock"
				case "fx":
					op.FX = &FXSnapshot{Rate: 100_000_000, Date: "2026-09-01", Source: "synthetic", FetchedAt: "2026-09-02T00:00:00Z"}
				case "cycle":
					op.CycleID = OpeningCycleID("a", "stock")
				case "target":
					op.ToAccountID = "b"
				case "zero_amount":
					op.Amount = 0
				}
				replayReject(t, openings, instruments, []Operation{op}, op.ID, ErrOperation)
			})
		}
	}
}

func TestReplayRejectsInvalidCycleReferences(t *testing.T) {
	for _, name := range []string{"missing", "unknown", "other_instrument", "other_account", "future_cycle", "voided_cycle"} {
		t.Run(name, func(t *testing.T) {
			openings, instruments := replayFixture(10_000)
			openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare}}
			instruments = append(instruments, Instrument{ID: "other", Market: "TEST", Code: "002", Name: "Other synthetic stock", Currency: CNY})
			openings = append(openings, Opening{AccountID: "b", Currency: CNY, Date: "2026-09-01",
				Positions: []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare}}})
			op := Operation{ID: "bad_dividend", Date: "2026-09-02", Sequence: 2, Kind: Dividend,
				AccountID: "a", InstrumentID: "stock", CycleID: OpeningCycleID("a", "stock"), Amount: 100}
			var ops []Operation
			switch name {
			case "missing":
				op.CycleID = ""
			case "unknown":
				op.CycleID = "does_not_exist"
			case "other_instrument":
				op.InstrumentID = "other"
			case "other_account":
				op.CycleID = OpeningCycleID("b", "stock")
			case "future_cycle", "voided_cycle":
				openings[0].Positions = nil
				buy := replayTrade("referenced_buy", 3, Buy, replayShare, replayPriceUnit)
				if name == "voided_cycle" {
					buy.Sequence, buy.Voided = 1, true
				}
				op.CycleID = buy.ID
				ops = append(ops, buy)
			}
			ops = append(ops, op)
			replayReject(t, openings, instruments, ops, op.ID, ErrOperation)
		})
	}
}

func TestReplayFailureDoesNotMutateNestedInputs(t *testing.T) {
	openings, instruments := replayFixture(0)
	openings[0].Positions = []OpeningPosition{{InstrumentID: "stock", Quantity: replayShare,
		Cost: replayMoney(100), DilutedBasis: replayMoney(200)}}
	op := replayTrade("failed_combo", 1, SellWithdraw, replayShare, 2*replayPriceUnit)
	op.Amount, op.Fee = 201, replayMoney(0)
	ops := []Operation{op}
	before, err := json.Marshal([]any{openings, instruments, ops})
	require.NoError(t, err)
	replayReject(t, openings, instruments, ops, op.ID, ErrInsufficientCash)
	after, err := json.Marshal([]any{openings, instruments, ops})
	require.NoError(t, err)
	assert.Equal(t, before, after)
	// A corrected retry starts from the original cost and quantity, not a partial prior sale.
	ops[0].Amount = 200
	book := replayMust(t, openings, instruments, ops)
	assert.Zero(t, book.Accounts["a"].Cash)
	assert.Equal(t, replayMoney(100), book.Accounts["a"].Cycles[OpeningCycleID("a", "stock")].RealizedProfit)
}
