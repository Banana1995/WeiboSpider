package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
	"github.com/stretchr/testify/require"
)

func TestWeeklyConfigDefaultsStrictAndModuleDependency(t *testing.T) {
	base := DefaultConfig()
	require.False(t, base.LedgerWeeklyEnabled)
	require.Equal(t, "08:00", base.LedgerWeeklyTime)
	for _, stamp := range []string{"", "8:00", "24:00", "08:60", "08:00:00", " 08:00", "08:00Z"} {
		c := base
		c.LedgerWeeklyTime = stamp
		require.ErrorContains(t, c.Validate(), "LEDGER_WEEKLY_TIME")
	}
	for _, stamp := range []string{"00:00", "08:00", "23:59"} {
		c := base
		c.LedgerWeeklyTime = stamp
		require.NoError(t, c.Validate())
	}
	base.LedgerWeeklyEnabled = true
	require.ErrorContains(t, base.Validate(), "requires LEDGER_ENABLED")
	base.DataDir = filepath.Join(t.TempDir(), "must-not-create")
	a, err := New(t.Context(), base, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.Error(t, err)
	require.Nil(t, a)
	_, err = os.Stat(base.DataDir)
	require.ErrorIs(t, err, os.ErrNotExist)
	for _, value := range []string{"", "1", "TRUE", "yes", " false "} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LEDGER_WEEKLY_ENABLED", value)
			_, err := LoadConfig()
			require.ErrorContains(t, err, "LEDGER_WEEKLY_ENABLED")
		})
	}
	t.Setenv("LEDGER_ENABLED", "true")
	t.Setenv("LEDGER_WEEKLY_ENABLED", "true")
	t.Setenv("LEDGER_WEEKLY_TIME", "08:00")
	t.Setenv("BACKEND_ADDR", "127.0.0.1:0")
	c, err := LoadConfig()
	require.NoError(t, err)
	require.True(t, c.LedgerWeeklyEnabled)
}

type cancelWeeklyQuotes struct {
	entered chan struct{}
	exited  chan struct{}
}

func (q cancelWeeklyQuotes) Fetch(ctx context.Context, _ []ledger.Instrument) map[string]ledger.QuoteResult {
	close(q.entered)
	<-ctx.Done()
	close(q.exited)
	return nil
}

func TestServeWaitsForLedgerCancellationBeforeStorageClose(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.LedgerEnabled = true
	cfg.LedgerWeeklyEnabled = true
	cfg.SourceURL = "http://127.0.0.1:1"
	a, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, a.Close()) })
	now, _ := time.Parse(time.RFC3339, "2026-09-12T08:00:00+08:00")
	s := ledger.NewStore(a.ledgerDB, func() time.Time { return now })
	require.NoError(t, s.AddInstrument(t.Context(), ledger.Instrument{ID: "i", Market: "SH", Code: "600000", Name: "Synthetic", Currency: ledger.CNY}))
	require.NoError(t, s.InitializeAccount(t.Context(), "Synthetic", ledger.Opening{AccountID: "a", Currency: ledger.CNY, Date: "2026-01-01", Positions: []ledger.OpeningPosition{{InstrumentID: "i", Quantity: 1_000_000}}}))
	q := cancelWeeklyQuotes{make(chan struct{}), make(chan struct{})}
	a.ledgerWorker, err = ledger.NewWeeklyWorker(s, q, nil, ledger.WeeklyConfig{Enabled: true, Time: "08:00"}, nil)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, listener) }()
	select {
	case <-q.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start with Serve")
	}
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not wait and stop")
	}
	select {
	case <-q.exited:
	default:
		t.Fatal("provider still running after Serve")
	}
	jobs, err := s.ListWeeklyJobs(t.Context(), "a", "", 0, 30)
	require.NoError(t, err)
	require.Len(t, jobs.Items, 1)
	require.Equal(t, "canceled", jobs.Items[0].ErrorCode)
	require.NoError(t, a.Close())
	reopened, err := ledger.Open(t.Context(), cfg.DataDir)
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
}
