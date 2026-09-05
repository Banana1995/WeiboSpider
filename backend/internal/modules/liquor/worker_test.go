package liquor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type blockingSource struct {
	release  <-chan struct{}
	snapshot Snapshot
	err      error
}

func (s blockingSource) Fetch(ctx context.Context) (Snapshot, error) {
	select {
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-s.release:
		return s.snapshot, s.err
	}
}

func TestWorker_AsyncSyncIsExclusiveAndPersistsResult(t *testing.T) {
	// Given
	snapshot, err := fixtureSource(t, listFixture, detailFixture).Fetch(t.Context())
	require.NoError(t, err)
	synctest.Test(t, func(t *testing.T) {
		store := testStore(t)
		release := make(chan struct{})
		worker := NewWorker(store, blockingSource{release: release, snapshot: snapshot}, WorkerConfig{
			Now: func() time.Time { return testTime }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Timeout: time.Minute,
		})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()
		t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
		// When
		id, err := worker.Trigger(t.Context())
		require.NoError(t, err)
		require.NotEmpty(t, id)
		synctest.Wait()
		_, err = worker.Trigger(t.Context())
		require.ErrorIs(t, err, ErrRunning)
		close(release)
		synctest.Wait()
		// Then
		status, err := store.Status(t.Context())
		require.NoError(t, err)
		require.Equal(t, Succeeded, status.State)
		latest, err := store.Latest(t.Context())
		require.NoError(t, err)
		require.Len(t, latest.Items, 1)
	})
}

func TestWorker_CancellationPersistsFailureAndStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Given
		store := testStore(t)
		worker := NewWorker(store, blockingSource{release: make(chan struct{})}, WorkerConfig{
			Now: time.Now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Timeout: time.Minute,
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()
		_, err := worker.Trigger(t.Context())
		require.NoError(t, err)
		synctest.Wait()
		// When
		cancel()
		require.NoError(t, <-done)
		// Then
		status, err := store.Status(t.Context())
		require.NoError(t, err)
		require.Equal(t, Failed, status.State)
		require.Equal(t, "cancelled", status.ErrorCode)
		_, err = worker.Trigger(t.Context())
		require.ErrorIs(t, err, ErrStopped)
	})
}

func TestWorker_SourceFailureDoesNotPublishPrices(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Given
		store := testStore(t)
		release := make(chan struct{})
		close(release)
		worker := NewWorker(store, blockingSource{release: release, err: errors.New("upstream down")}, WorkerConfig{
			Now: time.Now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Timeout: time.Minute,
		})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()
		t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
		// When
		_, err := worker.Trigger(t.Context())
		require.NoError(t, err)
		synctest.Wait()
		// Then
		status, err := store.Status(t.Context())
		require.NoError(t, err)
		require.Equal(t, Failed, status.State)
		require.Equal(t, "source_unavailable", status.ErrorCode)
	})
}

func TestSyncDue_UsesBeijingPublicationTimeAndRetryCooldown(t *testing.T) {
	tests := []struct {
		name   string
		now    time.Time
		status SyncStatus
		due    bool
	}{
		{"empty", testTime, SyncStatus{}, true},
		{"current", testTime, SyncStatus{LastSuccessAt: testTime.Format(time.RFC3339Nano), LastPriceDate: "2026-09-05"}, false},
		{"old quote", testTime, SyncStatus{LastSuccessAt: testTime.Format(time.RFC3339Nano), LastPriceDate: "2026-09-04"}, true},
		{"cooldown", testTime, SyncStatus{StartedAt: testTime.Add(-time.Minute).Format(time.RFC3339Nano)}, false},
		{"running", testTime, SyncStatus{State: Running}, false},
		{"before publication", time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), SyncStatus{LastSuccessAt: "2026-09-04T02:00:00Z", LastPriceDate: "2026-09-04"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given / When / Then
			require.Equal(t, test.due, SyncDue(test.now, test.status))
		})
	}
}
