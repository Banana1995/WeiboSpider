package ledger

import (
	"encoding/json"
	"math/big"
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// Optional private reproduction: keep actual cash flows and independently
// calculated expectations outside the repository, never in committed fixtures.
func TestPrivateXIRRBaseline(t *testing.T) {
	path := os.Getenv("LEDGER_XIRR_BASELINE")
	if path == "" {
		t.Skip("LEDGER_XIRR_BASELINE is not set")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var baseline struct {
		Flows              []InvestorFlow `json:"flows"`
		ExpectedValue      string         `json:"expected_value"`
		ExpectedPercentage string         `json:"expected_percentage"`
	}
	require.NoError(t, json.Unmarshal(data, &baseline))
	require.NotEmpty(t, baseline.ExpectedValue)
	require.NotEmpty(t, baseline.ExpectedPercentage)
	grouped := map[string]*big.Int{}
	for _, flow := range baseline.Flows {
		require.True(t, validDate(flow.Date))
		amount, err := ParseMoney(flow.Amount)
		require.NoError(t, err)
		if grouped[flow.Date] == nil {
			grouped[flow.Date] = new(big.Int)
		}
		grouped[flow.Date].Add(grouped[flow.Date], big.NewInt(int64(amount)))
	}
	dates := make([]string, 0, len(grouped))
	for date := range grouped {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	result, err := solveXIRR(t.Context(), dates, grouped, "available")
	require.NoError(t, err)
	require.Equal(t, "available", result.Status, result.Reason)
	require.NotNil(t, result.Value)
	require.NotNil(t, result.Percentage)
	require.Equal(t, baseline.ExpectedValue, *result.Value)
	require.Equal(t, baseline.ExpectedPercentage, *result.Percentage)
}
