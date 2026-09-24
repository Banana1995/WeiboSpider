package ledger

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// BenchmarkWindowSource is only used by the background worker, never by HTTP.
type BenchmarkWindowSource interface {
	FetchWindow(context.Context, string, string, string) (Benchmark, error)
}

const benchmarkPoll = time.Minute

type BenchmarkWorker struct {
	store    *StoredBenchmarks
	source   BenchmarkWindowSource
	now      func() time.Time
	logger   *slog.Logger
	retries  map[string]int
	retryAt  map[string]time.Time
	lastSlot time.Time
}

func NewBenchmarkWorker(store *StoredBenchmarks, source BenchmarkWindowSource, logger *slog.Logger) *BenchmarkWorker {
	return &BenchmarkWorker{store: store, source: source, now: time.Now, logger: logger,
		retries: make(map[string]int), retryAt: make(map[string]time.Time)}
}

func benchmarkSlot(now time.Time) time.Time {
	local := now.In(weeklyBeijing)
	for _, hour := range [...]int{20, 8} {
		candidate := time.Date(local.Year(), local.Month(), local.Day(), hour, 0, 0, 0, weeklyBeijing)
		if !candidate.After(now) {
			return candidate
		}
	}
	return time.Date(local.Year(), local.Month(), local.Day()-1, 20, 0, 0, 0, weeklyBeijing)
}

func (w *BenchmarkWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(benchmarkPoll)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil && w.logger != nil {
			w.logger.ErrorContext(ctx, "ledger.benchmark.sync.failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Tick checks for a new schedule window and for newly entered older assets.
// Network I/O is bounded per window and always precedes the short DB write.
func (w *BenchmarkWorker) Tick(ctx context.Context) error {
	now := w.now()
	slot := benchmarkSlot(now)
	if !slot.Equal(w.lastSlot) {
		w.lastSlot = slot
		clear(w.retries)
		clear(w.retryAt)
	}
	to := now.In(weeklyBeijing).Format(time.DateOnly)
	from, err := w.store.EarliestAsset(ctx)
	if err != nil {
		return err
	}
	if from == "" {
		return nil // No account curve needs history yet.
	}
	if from > to {
		from = to
	}
	from = previousDays(from, benchmarkLookbackDays)
	status, err := w.store.Status(ctx)
	if err != nil {
		return err
	}
	var failures error
	for _, item := range status {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if w.retries[item.Code] >= 3 || now.Before(w.retryAt[item.Code]) {
			continue
		}
		last, _ := time.Parse(time.RFC3339Nano, item.LastSuccessAt)
		refresh := last.Before(slot)
		if refresh {
			recent := previousDays(to, 45)
			if recent < from {
				recent = from
			}
			// Refresh first so slow initial backfills cannot postpone today's close.
			err = w.syncRange(ctx, item.Code, recent, to)
		}
		var windows []benchmarkInterval
		if err == nil {
			windows, err = w.store.Missing(ctx, item.Code, from, to)
		}
		if err == nil {
			for _, gap := range windows {
				if err = w.syncRange(ctx, item.Code, gap.from, gap.to); err != nil {
					break
				}
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			w.retries[item.Code]++
			delay := 5 * time.Minute
			if w.retries[item.Code] > 1 {
				delay = 30 * time.Minute
			}
			w.retryAt[item.Code] = now.Add(delay)
			code := "source_unavailable"
			if errors.Is(benchmarkError(err), ErrBenchmarkTimeout) {
				code = "timeout"
			}
			if !errors.Is(err, ErrBenchmarkUnavailable) && !errors.Is(err, ErrBenchmarkTimeout) && !errors.Is(err, context.DeadlineExceeded) {
				code = "storage_error"
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			failures = errors.Join(failures, err, w.store.RecordFailure(cleanup, item.Code, now, code))
			cancel()
		}
	}
	return failures
}

func previousDays(day string, count int) string {
	date, _ := time.Parse(time.DateOnly, day)
	if date.Year() == 1 && date.YearDay() <= count {
		return "0001-01-01"
	}
	return date.AddDate(0, 0, -count).Format(time.DateOnly)
}

func (w *BenchmarkWorker) syncRange(ctx context.Context, code, from, to string) error {
	start, _ := time.Parse(time.DateOnly, from)
	end, _ := time.Parse(time.DateOnly, to)
	years := 5
	if code == "usINX" {
		years = 2 // guaranteed daily bars below Tencent's 1000-row cap
	}
	for !start.After(end) {
		finish := start.AddDate(years, 0, 0).AddDate(0, 0, -1)
		if finish.After(end) {
			finish = end
		}
		first, last := start.Format(time.DateOnly), finish.Format(time.DateOnly)
		value, err := w.source.FetchWindow(ctx, code, first, last)
		if err != nil {
			return err
		}
		if err := w.store.SaveWindow(ctx, value, first, last, w.now()); err != nil {
			return err
		}
		start = finish.AddDate(0, 0, 1)
	}
	return nil
}
