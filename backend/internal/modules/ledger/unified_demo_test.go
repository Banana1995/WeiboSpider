package ledger

import (
	"context"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Explicitly opt-in, synthetic-only browser fixture. No production clock/provider
// override is added and ordinary test runs neither listen nor wait.
func TestUnifiedBrowserDemo(t *testing.T) {
	if os.Getenv("LEDGER_UNIFIED_DEMO") != "true" {
		t.Skip("synthetic browser fixture only")
	}
	clock := func() time.Time { return time.Date(2026, 9, 5, 0, 0, 3, 0, time.UTC) }
	db, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	s := NewStore(db, clock)
	i := Instrument{ID: "synthetic-stock", Market: "SZ", Code: "000001", Name: "Synthetic quote only", Currency: CNY}
	require.NoError(t, s.AddInstrument(t.Context(), i))
	_, err = s.CreateReportedAccount(t.Context(), "demo-account", ReportedAccountInput{ID: "demo", Name: "Synthetic unified account", Currency: CNY, OpeningDate: "2026-09-03"})
	require.NoError(t, err)
	putSource(t, s, "demo", "demo-current", "0", 4500, CurrentPosition{i.ID, 1000000})
	data := syntheticImportZip(t, syntheticImportParts(t, [][7]string{
		{"记总资产", "2026-09-01", "", "100.00", "Synthetic initialization", "", ""},
		{"记总资产", "2026-09-02", "", "105.00", "Synthetic historical amount", "", ""},
	}))
	p := parseSynthetic(t, data)
	_, err = s.ConfirmAccountImport(t.Context(), "demo", "demo-import", p.Digest, false, data)
	require.NoError(t, err)
	flow, total := Money(2000), Money(12500)
	c := AccountRecordCommand{Action: CreateOperation, AccountID: "demo", ID: "manual-postflow", Entry: &AccountEntry{Kind: "cash_flow", Date: "2026-09-03", Flow: &flow, TotalAssets: &total, Note: "Synthetic deposit; total already includes flow"}}
	_, err = s.WriteAccountRecord(t.Context(), "demo-manual", c)
	require.NoError(t, err)
	c.Action = ReplaceOperation
	c.ExpectedVersion = "1"
	c.Reason = "Synthetic note correction"
	c.Entry.Note = "Synthetic corrected note <b>literal</b>"
	_, err = s.WriteAccountRecord(t.Context(), "demo-correct", c)
	require.NoError(t, err)
	_, err = s.WriteAccountRecord(t.Context(), "demo-flow", AccountRecordCommand{Action: CreateOperation, AccountID: "demo", ID: "manual-deposit", Entry: &AccountEntry{Kind: "cash_flow", Date: "2026-09-04", Flow: replayMoney(500)}})
	require.NoError(t, err)
	quotes := valuationQuotes(func(ctx context.Context, items []Instrument) map[string]QuoteResult {
		out := map[string]QuoteResult{}
		for _, item := range items {
			symbol, _ := quoteSymbol(item)
			out[item.ID] = QuoteResult{Quote: &Quote{Symbol: symbol, Price: 100000000, Currency: CNY, Date: "2026-09-05", Source: "Tencent", QuotedAt: clock().Format(time.RFC3339Nano), FetchedAt: clock().Format(time.RFC3339Nano)}}
		}
		return out
	})
	_, err = s.CreateReportedAccount(t.Context(), "demo-reported", ReportedAccountInput{ID: "no-source", Name: "Synthetic no automatic holdings", Currency: CNY, OpeningDate: "2026-09-01"})
	require.NoError(t, err)
	worker, err := NewWeeklyWorker(s, quotes, nil, WeeklyConfig{Enabled: true, Time: DefaultWeeklyTime}, nil)
	require.NoError(t, err)
	require.NoError(t, worker.Tick(t.Context()))
	basis, err := s.AnalysisBasis(t.Context(), "demo", "", "", 0)
	require.NoError(t, err)
	require.Equal(t, "20.00", *basis.Returns.Profit.Value)
	require.Equal(t, "25.00", basis.Returns.NetFlow)
	manual := Money(20000)
	_, err = s.WriteAccountRecord(t.Context(), "demo-correct-auto", AccountRecordCommand{Action: ReplaceOperation, AccountID: "demo", ID: "valuation-1", ExpectedVersion: "1", Reason: "Synthetic manual correction: browsing must not replace this amount", Entry: &AccountEntry{Kind: "asset", Date: "2026-09-05", TotalAssets: &manual}})
	require.NoError(t, err)
	mux := http.NewServeMux()
	Handler{Store: s, Now: clock, Quotes: quotes, Weekly: worker}.Register(mux)
	listener, err := net.Listen("tcp", "127.0.0.1:18673")
	require.NoError(t, err)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Log("synthetic ledger API ready at http://127.0.0.1:18673; read-only preview and fixed weekly records; no real quote provider")
	select {
	case <-t.Context().Done():
	case err := <-done:
		require.ErrorIs(t, err, http.ErrServerClosed)
	}
}
