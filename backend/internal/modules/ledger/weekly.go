package ledger

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"
	_ "time/tzdata"
)

const (
	DefaultWeeklyTime = "08:00"
	weeklyBatch       = 20
	weeklyAttempts    = 3
	weeklyPoll        = time.Minute
	weeklyStamp       = "2006-01-02T15:04:05.000000000Z"
)

var (
	weeklyBeijing, _  = time.LoadLocation("Asia/Shanghai") // Embedded tzdata.
	errWeeklyBasis    = errors.New("weekly basis changed")
	errWeeklyExpired  = errors.New("weekly slot expired")
	errWeeklyFence    = errors.New("weekly attempt no longer owns claim")
	errWeeklyNoSource = errors.New("weekly carry source unavailable")
)

type WeeklyConfig struct {
	Enabled bool
	Time    string
}

func (c WeeklyConfig) Validate() error {
	t, err := time.Parse("15:04", c.Time)
	if err != nil || t.Format("15:04") != c.Time {
		return errors.New("LEDGER_WEEKLY_TIME must be HH:MM in 00:00..23:59")
	}
	return nil
}

// WeeklyWorker owns no database. Run must return before its Store is closed.
// One worker per ledger DB owner; Tick is also usable with Store's injected clock.
type WeeklyWorker struct {
	store     *Store
	quotes    QuotesProvider
	fx        FXProvider
	cfg       WeeklyConfig
	logger    *slog.Logger
	mu        sync.Mutex
	recovered bool
}

func NewWeeklyWorker(s *Store, quotes QuotesProvider, fx FXProvider, cfg WeeklyConfig, logger *slog.Logger) (*WeeklyWorker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &WeeklyWorker{store: s, quotes: quotes, fx: fx, cfg: cfg, logger: logger}, nil
}

func (w *WeeklyWorker) window(now time.Time) (string, bool) {
	local := now.In(weeklyBeijing)
	return local.Format(time.DateOnly), local.Weekday() == time.Saturday && local.Format("15:04") >= w.cfg.Time
}

func (w *WeeklyWorker) next(now time.Time) time.Time {
	local := now.In(weeklyBeijing)
	t, _ := time.Parse("15:04", w.cfg.Time)
	next := time.Date(local.Year(), local.Month(), local.Day(), t.Hour(), t.Minute(), 0, 0, weeklyBeijing)
	next = next.AddDate(0, 0, (int(time.Saturday)-int(local.Weekday())+7)%7)
	if !next.After(now) {
		next = next.AddDate(0, 0, 7)
	}
	return next
}

// Fixed delay after each bounded tick prevents both empty-queue and failure spins.
func (w *WeeklyWorker) Run(ctx context.Context) error {
	if !w.cfg.Enabled {
		<-ctx.Done()
		return nil
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil && w.logger != nil {
			w.logger.ErrorContext(ctx, "ledger.weekly.failed", "code", "storage_error")
		}
		delay := weeklyPoll
		now := w.store.now()
		if until := w.next(now).Sub(now); until < delay {
			delay = until
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func weeklyRetry(now time.Time, attempts int) any {
	if attempts >= weeklyAttempts {
		return nil
	}
	delay := 5 * time.Minute
	if attempts >= 2 {
		delay = 30 * time.Minute
	}
	return now.Add(delay).UTC().Format(weeklyStamp)
}

// Tick never overlaps itself. The exclusive module lease fences other processes.
// Network providers must honor context; production adapters have bounded HTTP I/O.
func (w *WeeklyWorker) Tick(ctx context.Context) error {
	if !w.cfg.Enabled || !w.mu.TryLock() {
		return nil
	}
	defer w.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	now := w.store.now()
	date, eligible := w.window(now)
	stamp := now.UTC().Format(weeklyStamp)
	err := w.store.db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE weekly_jobs SET status='skipped',error_code='expired',finished_at=?,next_attempt_at=NULL,lease_until=NULL
			WHERE id IN (SELECT id FROM weekly_jobs WHERE scheduled_business_date<? AND (status IN ('pending','running') OR (status='failed' AND next_attempt_at IS NOT NULL)) ORDER BY id LIMIT ?)`, stamp, date, weeklyBatch)
		if err != nil {
			return err
		}
		// On restart the OS lease proves the previous owner is gone. In-process
		// completion failures are recovered only after their bounded claim expires.
		_, err = tx.ExecContext(ctx, `UPDATE weekly_jobs SET status='failed',error_code='interrupted',finished_at=?,lease_until=NULL,
			next_attempt_at=CASE WHEN attempts=1 THEN ? WHEN attempts=2 THEN ? ELSE NULL END
			WHERE id IN (SELECT id FROM weekly_jobs WHERE status='running' AND scheduled_business_date=? AND (? OR lease_until<=?) ORDER BY id LIMIT ?)`,
			stamp, weeklyRetry(now, 1), weeklyRetry(now, 2), date, !w.recovered, stamp, weeklyBatch)
		if err != nil || !eligible {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO weekly_jobs(account_id,scheduled_business_date,source,status,created_at)
			SELECT a.id,?,CASE WHEN a.accounting_mode='holdings' OR EXISTS(SELECT 1 FROM current_holdings c WHERE c.account_id=a.id) THEN 'holdings_current' ELSE 'account_record_carry' END,'pending',?
			FROM accounts a WHERE NOT EXISTS(SELECT 1 FROM weekly_jobs j WHERE j.account_id=a.id AND j.scheduled_business_date=?)
			ORDER BY a.id LIMIT ?`, date, stamp, date, weeklyBatch)
		return err
	})
	if err != nil {
		return err
	}
	w.recovered = true
	if !eligible {
		return nil
	}
	var failures error
	for range weeklyBatch {
		if ctx.Err() != nil {
			return errors.Join(failures, ctx.Err())
		}
		job, err := w.claim(ctx, date)
		if err != nil {
			return errors.Join(failures, err)
		}
		if job == nil {
			return failures
		}
		if err := w.execute(ctx, *job); err != nil {
			failures = errors.Join(failures, err)
		}
	}
	return failures
}

func (w *WeeklyWorker) claim(ctx context.Context, date string) (*WeeklyJob, error) {
	var job *WeeklyJob
	err := w.store.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := w.store.now()
		current, eligible := w.window(now)
		if current != date || !eligible {
			return nil
		}
		stamp := now.UTC().Format(weeklyStamp)
		j, err := scanWeeklyJob(tx.QueryRowContext(ctx, weeklySelect+` WHERE scheduled_business_date=? AND (status='pending' OR (status='failed' AND next_attempt_at<=?)) AND attempts<3 ORDER BY id LIMIT 1`, date, stamp))
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		// A not-yet-claimed carry may acquire a current source in this window.
		// Source assignment and claim share the transaction and trigger audit.
		var configured bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM current_holdings WHERE account_id=?)`, j.AccountID).Scan(&configured); err != nil {
			return err
		}
		if configured {
			j.Source = "holdings_current"
		}
		result, err := tx.ExecContext(ctx, `UPDATE weekly_jobs SET source=?,status='running',attempts=attempts+1,started_at=?,finished_at=NULL,next_attempt_at=NULL,error_code='',lease_until=? WHERE id=?`, j.Source,
			stamp, now.Add(valuationTimeout+time.Minute).UTC().Format(weeklyStamp), j.ID)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return errWeeklyFence
		}
		j.Attempts++
		job = &j
		return err
	})
	return job, err
}

func (w *WeeklyWorker) execute(ctx context.Context, job WeeklyJob) error {
	budget, cancel := context.WithTimeout(ctx, valuationTimeout)
	defer cancel()
	if job.Source == "account_record_carry" {
		err := w.completeCarry(budget, job)
		code := "storage_error"
		switch {
		case err == nil, errors.Is(err, errWeeklyFence):
			return nil
		case errors.Is(err, errWeeklyNoSource):
			code = "no_source"
		case errors.Is(err, errWeeklyBasis):
			code = "basis_changed"
		case errors.Is(err, errWeeklyExpired):
			code = "expired"
		case errors.Is(err, ErrCorrupt):
			code = "invalid_valuation"
		case errors.Is(err, context.Canceled):
			code = "canceled"
		case errors.Is(err, context.DeadlineExceeded):
			code = "timeout"
		}
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		return w.fail(cleanup, job, code)
	}
	if job.Source != "holdings_current" {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		return w.fail(cleanup, job, "invalid_valuation")
	}
	v, instruments, err := w.store.valuationInputs(budget, job.AccountID)
	if err == nil {
		err = valuePositions(budget, &v, instruments, w.quotes, w.fx, w.store.now)
	}
	code := ""
	if err != nil {
		code = "invalid_valuation"
	} else if !v.Complete {
		code = "incomplete_valuation"
	}
	if budget.Err() != nil {
		err = budget.Err()
	}
	if errors.Is(err, context.Canceled) {
		code = "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = "timeout"
	}
	if code == "" {
		err = w.complete(budget, job, v, instruments)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, errWeeklyFence):
			return nil
		case errors.Is(err, errWeeklyBasis):
			code = "basis_changed"
		case errors.Is(err, errWeeklyExpired):
			code = "expired"
		case errors.Is(err, ErrCorrupt):
			code = "invalid_valuation"
		case errors.Is(err, context.Canceled):
			code = "canceled"
		case errors.Is(err, context.DeadlineExceeded):
			code = "timeout"
		default:
			code = "storage_error"
		}
	}
	// Cancellation must not leave an indefinite running claim. If storage itself
	// fails, the persisted lease is recovered on a later tick or process restart.
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer stop()
	return w.fail(cleanup, job, code)
}

func (w *WeeklyWorker) completeCarry(ctx context.Context, job WeeklyJob) error {
	return w.store.db.WithTx(ctx, func(tx *sql.Tx) error {
		var owns int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM weekly_jobs WHERE id=? AND account_id=? AND source='account_record_carry' AND status='running' AND attempts=?`, job.ID, job.AccountID, job.Attempts).Scan(&owns); err != nil {
			return err
		}
		if owns != 1 {
			return errWeeklyFence
		}
		info, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, job.AccountID))
		if err != nil || info.AccountingMode != "reported" {
			return ErrCorrupt
		}
		var configured bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM current_holdings WHERE account_id=?)`, job.AccountID).Scan(&configured); err != nil {
			return err
		}
		if configured {
			return errWeeklyBasis
		}
		source, err := scanAccountRecord(tx.QueryRowContext(ctx, accountRecordSelect+` WHERE account_id=? AND kind='asset' AND voided=0 AND business_date<=?
			AND (origin!='weekly_carry' OR manual_assertion=1)
			ORDER BY business_date DESC,CAST(stable_sequence AS INTEGER) DESC LIMIT 1`, job.AccountID, job.ScheduledBusinessDate))
		if errors.Is(err, ErrNotFound) {
			return errWeeklyNoSource
		}
		if err != nil || source.TotalAssets == nil {
			return ErrCorrupt
		}
		now := w.store.now()
		date, eligible := w.window(now)
		if date != job.ScheduledBusinessDate || !eligible {
			return errWeeklyExpired
		}
		businessDate, savedAt, err := w.store.cutoff()
		if err != nil || businessDate != job.ScheduledBusinessDate {
			return errWeeklyExpired
		}
		var id int64
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(sequence),0)+1 FROM account_records`).Scan(&id); err != nil {
			return err
		}
		reference := AccountRecordSource{ID: source.ID, AccountID: source.AccountID, Sequence: source.Sequence, Version: source.Version, Date: source.Date, Origin: source.Origin, TotalAssets: *source.TotalAssets, ManualAssertion: source.ManualAssertion}
		recordID := "weekly-carry-" + job.ID
		snapshot := weeklyCarrySnapshot{SchemaVersion: 1, AccountName: info.Name, RecordID: recordID, AccountID: job.AccountID, Currency: info.Currency, AsOf: job.ScheduledBusinessDate, TotalAssets: *source.TotalAssets, SourceRecord: reference}
		if err := snapshot.validate(); err != nil {
			return err
		}
		auditID, err := appendAudit(ctx, tx, "weekly-"+job.ID, "carry_forward", "weekly_carry", strconv.FormatInt(id, 10), job.AccountID, 1, savedAt, "system", nil, snapshot,
			map[string]string{"source_record_id": source.ID, "source_record_version": source.Version, "source_record_date": source.Date})
		if err != nil {
			return err
		}
		next := AccountRecord{ID: recordID, AccountID: job.AccountID, AccountEntry: AccountEntry{Kind: "asset", Date: job.ScheduledBusinessDate, TotalAssets: copyMoney(source.TotalAssets), Note: "Carried forward from " + source.Date + " record " + source.ID + " v" + source.Version}, Sequence: strconv.FormatInt(id, 10), Origin: "weekly_carry", QuoteAuditID: strconv.FormatInt(auditID, 10), CarriedFrom: &reference, Version: "1", CreatedAt: savedAt, UpdatedAt: savedAt}
		if _, err := putAccountRecord(ctx, tx, &next, nil, "weekly-"+job.ID, "weekly carry-forward", "system"); err != nil {
			return err
		}
		now = w.store.now()
		date, eligible = w.window(now)
		if date != job.ScheduledBusinessDate || !eligible {
			return errWeeklyExpired
		}
		result, err := tx.ExecContext(ctx, `UPDATE weekly_jobs SET status='succeeded',history_id=?,finished_at=?,lease_until=NULL,error_code='' WHERE id=? AND status='running' AND attempts=?`, id, now.UTC().Format(weeklyStamp), job.ID, job.Attempts)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return errWeeklyFence
		}
		date, eligible = w.window(w.store.now())
		if date != job.ScheduledBusinessDate || !eligible {
			return errWeeklyExpired
		}
		return nil
	})
}

func (w *WeeklyWorker) complete(ctx context.Context, job WeeklyJob, v Valuation, instruments []Instrument) error {
	return w.store.db.WithTx(ctx, func(tx *sql.Tx) error {
		var owns int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM weekly_jobs WHERE id=? AND account_id=? AND status='running' AND attempts=?`, job.ID, job.AccountID, job.Attempts).Scan(&owns); err != nil {
			return err
		}
		if owns != 1 {
			return errWeeklyFence
		}
		if v.AccountID != job.AccountID || v.AsOf != job.ScheduledBusinessDate || v.changeRevision == nil {
			return ErrCorrupt
		}
		var changed int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM audit_log
			WHERE account_id=? AND entity_type='account_record' AND id>?
			AND json_extract(after_json,'$.origin')='operation'
			AND json_extract(metadata_json,'$.from_date')<=?`, v.AccountID, *v.changeRevision, v.AsOf).Scan(&changed); err != nil {
			return err
		}
		if changed != 0 {
			return errWeeklyBasis
		}
		if err := weeklyTimestamps(v, w.store.now()); err != nil {
			return err
		}
		v.correlation = "weekly-" + job.ID
		v.weekly = true
		id, _, err := w.store.recordValuation(ctx, tx, v, instruments)
		if err != nil {
			return err
		}
		// Check again after persistence preparation: never backdate a completion
		// whose network call (or transaction wait) crossed Beijing midnight.
		now := w.store.now()
		date, eligible := w.window(now)
		if date != job.ScheduledBusinessDate || !eligible {
			return errWeeklyExpired
		}
		result, err := tx.ExecContext(ctx, `UPDATE weekly_jobs SET status='succeeded',history_id=?,finished_at=?,lease_until=NULL,error_code='' WHERE id=? AND status='running' AND attempts=?`, id, now.UTC().Format(weeklyStamp), job.ID, job.Attempts)
		if err == nil {
			if n, err := result.RowsAffected(); err != nil {
				return err
			} else if n != 1 {
				return errWeeklyFence
			}
			date, eligible = w.window(w.store.now())
			if date != job.ScheduledBusinessDate || !eligible {
				return errWeeklyExpired
			}
		}
		return err
	})
}

// Additional current-sample checks do not reinterpret legacy schema-1 JSON.
func weeklyTimestamps(v Valuation, now time.Time) error {
	ledger, e1 := time.Parse(time.RFC3339Nano, v.LedgerAt)
	calculated, e2 := time.Parse(time.RFC3339Nano, v.CalculatedAt)
	if e1 != nil || e2 != nil || calculated.Before(ledger) || calculated.After(now) {
		return ErrCorrupt
	}
	for _, item := range v.Items {
		stamps := make([][2]string, 0, 2)
		if item.Quote != nil {
			stamps = append(stamps, [2]string{item.Quote.QuotedAt, item.Quote.FetchedAt})
		}
		if item.FX != nil {
			fx := item.FX
			pair := string(fx.Base) + string(fx.Quote)
			suffix := ""
			if pair != "USDCNY" && pair != "USDHKD" && pair != "HKDCNY" {
				pair = string(fx.Quote) + string(fx.Base)
				suffix = "/inverse"
			}
			if fx.Source != "Tencent/spot/"+pair+suffix || fx.RequestedDate != v.AsOf {
				return ErrCorrupt
			}
			stamps = append(stamps, [2]string{item.FX.QuotedAt, item.FX.FetchedAt})
		}
		for _, pair := range stamps {
			quoted, e1 := time.Parse(time.RFC3339Nano, pair[0])
			fetched, e2 := time.Parse(time.RFC3339Nano, pair[1])
			if e1 != nil || e2 != nil || fetched.After(calculated) || fetched.Before(quoted) || fetched.Before(ledger) {
				return ErrCorrupt
			}
		}
	}
	return nil
}

func (w *WeeklyWorker) fail(ctx context.Context, job WeeklyJob, code string) error {
	return w.store.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := w.store.now()
		date, eligible := w.window(now)
		status := "failed"
		next := weeklyRetry(now, job.Attempts)
		if code == "no_source" {
			status, next = "skipped", nil
		}
		if date != job.ScheduledBusinessDate || !eligible || code == "expired" {
			status, code, next = "skipped", "expired", nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE weekly_jobs SET status=?,error_code=?,finished_at=?,next_attempt_at=?,lease_until=NULL WHERE id=? AND status='running' AND attempts=?`, status, code, now.UTC().Format(weeklyStamp), next, job.ID, job.Attempts)
		return err
	})
}
