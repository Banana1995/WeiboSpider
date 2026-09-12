package ledger

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func replayMoney(v Money) *Money { return &v }

// Trade replay is retired. These replace its useful invariants at the actual
// current-input boundary: validation, input immutability and no partial state.
func TestCurrentInputValidationDoesNotPartiallyApply(t *testing.T) {
	s := importStoreFixture(t)
	manualSourceAccount(t, s, "a")
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{"i", "SH", "600000", "Synthetic", CNY}))
	old := putSource(t, s, "a", "initial", "0", 100, CurrentPosition{"i", 1_000_000})
	for _, positions := range [][]CurrentPosition{nil, {{"unknown", 1}}, {{"i", 0}}, {{"i", -1}}, {{"i", 1}, {"i", 2}}} {
		input := CurrentHoldingsInput{ExpectedVersion: "1", Cash: replayMoney(200), Positions: positions}
		before := httpPayload(t, input)
		_, err := s.PutCurrentHoldings(t.Context(), "a", "invalid", input)
		require.Error(t, err)
		require.Equal(t, before, httpPayload(t, input))
		current, err := s.CurrentHoldings(t.Context(), "a")
		require.NoError(t, err)
		require.Equal(t, old, current)
	}
	// Direct quantities are not capped by old trades or available cash.
	next := putSource(t, s, "a", "invalid", "1", 0, CurrentPosition{"i", 9_007_199_254_740_993})
	require.Equal(t, Quantity(9_007_199_254_740_993), next.Snapshot.Positions[0].Quantity)
	putSource(t, s, "a", "remove", "2", 0)
	var n int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records`).Scan(&n))
	require.Zero(t, n)
}
