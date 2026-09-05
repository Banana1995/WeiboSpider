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
)

type causeSource struct{}

func (causeSource) Fetch(ctx context.Context) (Snapshot, error) {
	<-ctx.Done()
	return Snapshot{}, context.Cause(ctx)
}

func TestWorker_CustomCancellationCauseIsNotAnUpstreamFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := testStore(t)
		worker := NewWorker(store, causeSource{}, WorkerConfig{
			Now: time.Now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Timeout: time.Minute,
		})
		ctx, cancel := context.WithCancelCause(t.Context())
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()
		t.Cleanup(func() { cancel(nil); require.NoError(t, <-done) })
		_, err := worker.Trigger(t.Context())
		require.NoError(t, err)
		synctest.Wait()
		cancel(errors.New("terminated signal received"))
		synctest.Wait()
		status, err := store.Status(t.Context())
		require.NoError(t, err)
		require.Equal(t, "cancelled", status.ErrorCode)
	})
}

func TestWorker_CancelledAutoSyncStartupIsCleanShutdown(t *testing.T) {
	// Given
	store := testStore(t)
	worker := NewWorker(store, blockingSource{release: make(chan struct{})}, WorkerConfig{
		Now: time.Now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Timeout: time.Minute, AutoSync: true,
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// When
	err := worker.Run(ctx)
	// Then
	require.NoError(t, err)
}

func TestWorker_TimeoutPersistsFailureAndCanRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Given
		store := testStore(t)
		worker := NewWorker(store, blockingSource{release: make(chan struct{})}, WorkerConfig{
			Now: time.Now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Timeout: time.Second,
		})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()
		t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
		_, err := worker.Trigger(t.Context())
		require.NoError(t, err)
		// When: synctest advances this timer without waiting in real time.
		<-time.After(2 * time.Second)
		synctest.Wait()
		// Then
		status, err := store.Status(t.Context())
		require.NoError(t, err)
		require.Equal(t, Failed, status.State)
		require.Equal(t, "timeout", status.ErrorCode)
		_, err = worker.Trigger(t.Context())
		require.NoError(t, err)
	})
}

func TestWorker_FutureSourceDateIsValidationFailure(t *testing.T) {
	// Given
	snapshot, err := fixtureSource(t, listFixture, detailFixture).Fetch(t.Context())
	require.NoError(t, err)
	synctest.Test(t, func(t *testing.T) {
		store := testStore(t)
		release := make(chan struct{})
		close(release)
		worker := NewWorker(store, blockingSource{release: release, snapshot: snapshot}, WorkerConfig{
			Now:    func() time.Time { return testTime.AddDate(0, 0, -1) },
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Timeout: time.Minute,
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
		require.Equal(t, "invalid_source_data", status.ErrorCode)
	})
}

func TestWorker_AutoSyncFetchesOnStartupAndSkipsCurrentDate(t *testing.T) {
	// Given
	snapshot, err := fixtureSource(t, listFixture, detailFixture).Fetch(t.Context())
	require.NoError(t, err)
	synctest.Test(t, func(t *testing.T) {
		store := testStore(t)
		release := make(chan struct{})
		close(release)
		worker := NewWorker(store, blockingSource{release: release, snapshot: snapshot}, WorkerConfig{
			Now: func() time.Time { return testTime }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Timeout: time.Minute, AutoSync: true,
		})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()
		t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
		// When
		synctest.Wait()
		first, err := store.Status(t.Context())
		require.NoError(t, err)
		require.Equal(t, Succeeded, first.State)
		<-time.After(16 * time.Minute)
		synctest.Wait()
		// Then
		last, err := store.Status(t.Context())
		require.NoError(t, err)
		require.Equal(t, first.RunID, last.RunID)
	})
}
