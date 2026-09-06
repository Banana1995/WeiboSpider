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

func TestWorker_AutoSyncWaitsForMorningAndEveningSchedule(t *testing.T) {
	// Given
	synctest.Test(t, func(t *testing.T) {
		store := testStore(t)
		release := make(chan struct{})
		close(release)
		now := time.Now
		date := now().In(beijing).Format(time.DateOnly)
		snapshot := Snapshot{Date: date, Series: []Series{{
			Product: Product{ID: 1, Name: "Scheduled fixture", Specifications: "500ml", Unit: Unit},
			Prices:  []Point{{Date: date, Price: 10000}},
		}}}
		worker := NewWorker(store, blockingSource{release: release, snapshot: snapshot}, WorkerConfig{
			Now: now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Timeout: time.Minute, AutoSync: true,
		})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()
		t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
		// When
		synctest.Wait()
		before, err := store.Status(t.Context())
		require.NoError(t, err)
		require.Equal(t, Idle, before.State)
		current := now()
		<-time.After(nextAutoSync(current).Sub(current) + time.Second)
		synctest.Wait()
		// Then
		after, err := store.Status(t.Context())
		require.NoError(t, err)
		require.Equal(t, Succeeded, after.State)
		require.NotEmpty(t, after.RunID)
		<-time.After(11 * time.Hour)
		synctest.Wait()
		beforeNextRun, err := store.Status(t.Context())
		require.NoError(t, err)
		require.Equal(t, after.RunID, beforeNextRun.RunID)
		<-time.After(2 * time.Hour)
		synctest.Wait()
		nextRun, err := store.Status(t.Context())
		require.NoError(t, err)
		require.Equal(t, Succeeded, nextRun.State)
		require.NotEqual(t, after.RunID, nextRun.RunID)
	})
}
