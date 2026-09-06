package liquor

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

var ErrStopped = errors.New("sync worker stopped")

var autoSyncHours = [...]int{9, 21}

var beijing = time.FixedZone("Beijing", 8*60*60)

type Fetcher interface {
	Fetch(context.Context) (Snapshot, error)
}

type WorkerConfig struct {
	Now      func() time.Time
	Logger   *slog.Logger
	Timeout  time.Duration
	AutoSync bool
}

type Worker struct {
	store    *Store
	source   Fetcher
	config   WorkerConfig
	requests chan string
	mu       sync.Mutex
	stopped  bool
}

func NewWorker(store *Store, source Fetcher, config WorkerConfig) *Worker {
	return &Worker{store: store, source: source, config: config, requests: make(chan string, 1)}
}

func (w *Worker) Trigger(ctx context.Context) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return "", ErrStopped
	}
	id, err := w.store.Begin(ctx, w.config.Now())
	if err != nil {
		return "", err
	}
	w.requests <- id
	return id, nil
}

func (w *Worker) Run(ctx context.Context) (err error) {
	defer func() {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			err = nil
		}
		w.mu.Lock()
		w.stopped = true
		w.mu.Unlock()
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 6*time.Second)
		defer cancel()
		err = errors.Join(err, w.store.Recover(cleanup, w.config.Now()))
	}()
	var autoTimer *time.Timer
	var autoTimerC <-chan time.Time
	if w.config.AutoSync {
		now := w.config.Now()
		autoTimer = time.NewTimer(nextAutoSync(now).Sub(now))
		autoTimerC = autoTimer.C
		defer autoTimer.Stop()
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case id := <-w.requests:
			if err := w.execute(ctx, id); err != nil {
				return err
			}
		case scheduledAt := <-autoTimerC:
			if err := w.requestScheduled(ctx, scheduledAt); err != nil {
				return err
			}
			now := w.config.Now()
			autoTimer.Reset(nextAutoSync(now).Sub(now))
		}
	}
}

func (w *Worker) execute(ctx context.Context, id string) error {
	job, cancel := context.WithTimeout(ctx, w.config.Timeout)
	defer cancel()
	w.config.Logger.InfoContext(ctx, "liquor.sync.started", "run_id", id)
	snapshot, err := w.source.Fetch(job)
	code := "source_unavailable"
	if err == nil {
		var count int
		count, err = w.store.Complete(job, Import{RunID: id, Snapshot: snapshot, At: w.config.Now()})
		if err == nil {
			w.config.Logger.InfoContext(ctx, "liquor.sync.completed", "run_id", id, "records", count, "price_date", snapshot.Date)
			return nil
		}
		code = "storage_error"
	}
	switch {
	// HTTP can return context.Cause (for example a process signal), not ctx.Err.
	case errors.Is(job.Err(), context.Canceled), errors.Is(err, context.Canceled):
		code = "cancelled"
	case errors.Is(job.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		code = "timeout"
	case errors.Is(err, ErrSourceData):
		code = "invalid_source_data"
	}
	if code == "cancelled" {
		w.config.Logger.InfoContext(ctx, "liquor.sync.cancelled", "run_id", id)
	} else {
		w.config.Logger.ErrorContext(ctx, "liquor.sync.failed", "run_id", id, "error", err)
	}
	cleanup, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 6*time.Second)
	defer cleanupCancel()
	return w.store.Fail(cleanup, Failure{RunID: id, At: w.config.Now(), Code: code})
}

func (w *Worker) requestScheduled(ctx context.Context, scheduledAt time.Time) error {
	status, err := w.store.Status(ctx)
	if err != nil {
		return err
	}
	last, err := time.Parse(time.RFC3339Nano, status.LastSuccessAt)
	if status.State == Running || err == nil && !last.Before(scheduledAt) {
		return nil
	}
	_, err = w.Trigger(ctx)
	if errors.Is(err, ErrRunning) {
		return nil
	}
	return err
}

func nextAutoSync(now time.Time) time.Time {
	local := now.In(beijing)
	for _, hour := range autoSyncHours {
		next := time.Date(local.Year(), local.Month(), local.Day(), hour, 0, 0, 0, beijing)
		if local.Before(next) {
			return next
		}
	}
	return time.Date(local.Year(), local.Month(), local.Day()+1, autoSyncHours[0], 0, 0, 0, beijing)
}
