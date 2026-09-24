package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
	"github.com/stretchr/testify/require"
)

type waitingBenchmark struct{ entered, exited chan struct{} }

func (b waitingBenchmark) FetchWindow(ctx context.Context, _, _, _ string) (ledger.Benchmark, error) {
	close(b.entered)
	<-ctx.Done()
	close(b.exited)
	return ledger.Benchmark{}, ctx.Err()
}

func TestServeStopsBenchmarkSyncBeforeClosingLedger(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.LedgerEnabled = true
	cfg.SourceURL = "http://127.0.0.1:1"
	a, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, a.Close()) })
	s := ledger.NewStore(a.ledgerDB, time.Now)
	_, err = s.CreateReportedAccount(t.Context(), "create-a", ledger.ReportedAccountInput{
		ID: "a", Name: "Synthetic", Currency: ledger.CNY, OpeningDate: "2020-01-01",
	})
	require.NoError(t, err)
	assets := ledger.Money(10000)
	_, err = s.WriteAccountRecord(t.Context(), "create-asset", ledger.AccountRecordCommand{
		Action: ledger.CreateOperation, AccountID: "a", ID: "manual-start",
		Entry: &ledger.AccountEntry{Kind: "asset", Date: "2020-01-02", TotalAssets: &assets},
	})
	require.NoError(t, err)
	source := waitingBenchmark{make(chan struct{}), make(chan struct{})}
	a.benchmarkWorker = ledger.NewBenchmarkWorker(ledger.NewStoredBenchmarks(a.ledgerDB), source, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, listener) }()
	select {
	case <-source.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("benchmark worker did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("benchmark worker did not stop")
	}
	select {
	case <-source.exited:
	default:
		t.Fatal("market source still running")
	}
	require.NoError(t, a.Close())
	reopened, err := ledger.Open(t.Context(), cfg.DataDir)
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
}
