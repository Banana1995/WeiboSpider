package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

type WeeklyJob struct {
	ID                    string              `json:"id"`
	AccountID             string              `json:"account_id"`
	ScheduledBusinessDate string              `json:"scheduled_business_date"`
	Source                string              `json:"source"`
	Status                string              `json:"status"`
	Attempts              int                 `json:"attempts"`
	CreatedAt             string              `json:"created_at"`
	StartedAt             *string             `json:"started_at"`
	FinishedAt            *string             `json:"finished_at"`
	NextAttemptAt         *string             `json:"next_attempt_at"`
	ErrorCode             string              `json:"error_code"`
	HistoryID             *string             `json:"history_id"`
	Carry                 *WeeklyCarryHistory `json:"carry,omitempty"`
}

type weeklyCarrySnapshot struct {
	SchemaVersion int                 `json:"schema_version"`
	AccountName   string              `json:"account_name"`
	RecordID      string              `json:"record_id"`
	AccountID     string              `json:"account_id"`
	Currency      Currency            `json:"currency"`
	AsOf          string              `json:"as_of"`
	TotalAssets   Money               `json:"total_assets"`
	SourceRecord  AccountRecordSource `json:"source_record"`
}

type WeeklyCarryHistory struct {
	ID      string `json:"id"`
	SavedAt string `json:"saved_at"`
	weeklyCarrySnapshot
}

func (s weeklyCarrySnapshot) validate() error {
	if s.SchemaVersion != 1 || !validText(s.AccountName) || !validID(s.RecordID) || !validID(s.AccountID) || !s.Currency.valid() || !validDate(s.AsOf) || s.TotalAssets < 0 || !s.SourceRecord.valid(s.AccountID, s.AsOf) || s.SourceRecord.TotalAssets != s.TotalAssets {
		return ErrCorrupt
	}
	return nil
}

const weeklySelect = `SELECT id,account_id,scheduled_business_date,source,status,attempts,created_at,started_at,finished_at,next_attempt_at,error_code,history_id FROM weekly_jobs`

func scanWeeklyJob(row interface{ Scan(...any) error }) (WeeklyJob, error) {
	var j WeeklyJob
	err := row.Scan(&j.ID, &j.AccountID, &j.ScheduledBusinessDate, &j.Source, &j.Status, &j.Attempts, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &j.NextAttemptAt, &j.ErrorCode, &j.HistoryID)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

func weeklyCarryHistory(ctx context.Context, tx *sql.Tx, job WeeklyJob) (*WeeklyCarryHistory, error) {
	if job.Source != "account_record_carry" || job.Status != "succeeded" || job.HistoryID == nil {
		return nil, ErrCorrupt
	}
	id, err := positiveInteger(*job.HistoryID)
	if err != nil {
		return nil, ErrCorrupt
	}
	record, err := scanAccountRecord(tx.QueryRowContext(ctx, accountRecordSelect+` WHERE account_id=? AND sequence=?`, job.AccountID, id))
	if err != nil || record.Origin != "weekly_carry" || record.Sequence != *job.HistoryID || record.Date != job.ScheduledBusinessDate || record.CarriedFrom == nil {
		return nil, ErrCorrupt
	}
	auditID, err := positiveInteger(record.QuoteAuditID)
	if err != nil {
		return nil, ErrCorrupt
	}
	var savedAt, payload string
	if err := tx.QueryRowContext(ctx, `SELECT recorded_at,after_json FROM audit_log WHERE id=? AND entity_type='weekly_carry' AND entity_id=? AND account_id=?`, auditID, *job.HistoryID, job.AccountID).Scan(&savedAt, &payload); err != nil {
		return nil, ErrCorrupt
	}
	var snapshot weeklyCarrySnapshot
	var compact bytes.Buffer
	if json.Unmarshal([]byte(payload), &snapshot) != nil {
		return nil, ErrCorrupt
	}
	encoded, marshalErr := json.Marshal(snapshot)
	if marshalErr != nil || json.Compact(&compact, []byte(payload)) != nil || !bytes.Equal(encoded, compact.Bytes()) || snapshot.validate() != nil || snapshot.RecordID != record.ID || snapshot.AccountID != record.AccountID || snapshot.AsOf != record.Date || snapshot.SourceRecord != *record.CarriedFrom {
		return nil, ErrCorrupt
	}
	if _, err := time.Parse(time.RFC3339Nano, savedAt); err != nil {
		return nil, ErrCorrupt
	}
	return &WeeklyCarryHistory{ID: *job.HistoryID, SavedAt: savedAt, weeklyCarrySnapshot: snapshot}, nil
}

func (s *Store) GetWeeklyJob(ctx context.Context, accountID string, id int64) (WeeklyJob, error) {
	var job WeeklyJob
	if !validID(accountID) || id <= 0 {
		return job, ErrQuery
	}
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		job, err = scanWeeklyJob(tx.QueryRowContext(ctx, weeklySelect+` WHERE account_id=? AND id=?`, accountID, id))
		if err == nil && job.Source == "account_record_carry" && job.Status == "succeeded" {
			job.Carry, err = weeklyCarryHistory(ctx, tx, job)
		}
		return err
	})
	return job, err
}

func (h Handler) weeklyStatus(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	result := struct {
		Enabled         bool    `json:"enabled"`
		Timezone        string  `json:"timezone"`
		Weekday         string  `json:"weekday"`
		Time            string  `json:"time"`
		NextScheduledAt *string `json:"next_scheduled_at"`
		WindowOpen      bool    `json:"window_open"`
		MaxAttempts     int     `json:"max_attempts"`
	}{Timezone: "Asia/Shanghai", Weekday: "Saturday", Time: DefaultWeeklyTime, MaxAttempts: weeklyAttempts}
	if h.Weekly != nil {
		worker := h.Weekly
		result.Enabled, result.Time = worker.cfg.Enabled, worker.cfg.Time
		if result.Enabled {
			now := worker.store.now()
			next := worker.next(now).Format(time.RFC3339)
			result.NextScheduledAt = &next
			_, result.WindowOpen = worker.window(now)
		}
	}
	httpapi.Write(w, 200, result)
}

func (s *Store) ListWeeklyJobs(ctx context.Context, accountID, status string, cursor int64, limit int) (listJSON[WeeklyJob], error) {
	result := listJSON[WeeklyJob]{Items: make([]WeeklyJob, 0)}
	if !validID(accountID) || cursor < 0 || limit < 1 || limit > 100 || !slices.Contains([]string{"", "pending", "running", "succeeded", "failed", "skipped"}, status) {
		return result, ErrQuery
	}
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM accounts WHERE id=?`, accountID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		statement, args := weeklySelect+` WHERE account_id=?`, []any{accountID}
		if status != "" {
			statement += ` AND status=?`
			args = append(args, status)
		}
		if cursor != 0 {
			statement += ` AND id<?`
			args = append(args, cursor)
		}
		statement += ` ORDER BY id DESC LIMIT ?`
		args = append(args, limit+1)
		rows, err := tx.QueryContext(ctx, statement, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			j, err := scanWeeklyJob(rows)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, j)
		}
		return errors.Join(rows.Err(), ctx.Err())
	})
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		result.NextCursor = result.Items[limit-1].ID
	}
	return result, err
}

func (h Handler) weeklyJobs(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	values, limit, err := page(r, "status")
	var cursor int64
	if err == nil && values.Get("cursor") != "" {
		cursor, err = positiveInteger(values.Get("cursor"))
		if strconv.FormatInt(cursor, 10) != values.Get("cursor") {
			err = ErrQuery
		}
	}
	if err == nil && values.Get("limit") != "" && strconv.Itoa(limit) != values.Get("limit") {
		err = ErrQuery
	}
	if err != nil || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	result, err := h.Store.ListWeeklyJobs(r.Context(), r.PathValue("id"), values.Get("status"), cursor, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, result)
}

func (h Handler) weeklyJob(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	id, idErr := positiveInteger(r.PathValue("jobID"))
	if r.URL.RawQuery != "" || idErr != nil || strconv.FormatInt(id, 10) != r.PathValue("jobID") || !validID(r.PathValue("id")) || r.URL.ForceQuery {
		h.fail(w, r, ErrQuery)
		return
	}
	j, err := h.Store.GetWeeklyJob(r.Context(), r.PathValue("id"), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, j)
}
