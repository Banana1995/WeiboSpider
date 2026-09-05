package liquor

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type State string

const (
	Idle        State = "idle"
	Running     State = "running"
	Succeeded   State = "succeeded"
	Failed      State = "failed"
	Interrupted State = "interrupted"
)

var ErrRunChanged = errors.New("sync run no longer active")

type SyncStatus struct {
	Source        string `json:"source"`
	State         State  `json:"state"`
	RunID         string `json:"run_id"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at"`
	LastSuccessAt string `json:"last_success_at"`
	LastPriceDate string `json:"last_price_date"`
	Records       int    `json:"records"`
	ErrorCode     string `json:"error_code"`
}

func (s *Store) Begin(ctx context.Context, at time.Time) (string, error) {
	id := rand.Text()
	result, err := s.db.ExecContext(ctx, `UPDATE sync_status SET state='running',run_id=?,started_at=?,
		finished_at='',records=0,error_code='' WHERE source=? AND state<>'running'`, id, at.UTC().Format(time.RFC3339Nano), SourceID)
	err = changedRun(result, err)
	if errors.Is(err, ErrRunChanged) {
		return "", ErrRunning
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

type Failure struct {
	RunID string
	At    time.Time
	Code  string
}

func (s *Store) Fail(ctx context.Context, failure Failure) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sync_status SET state='failed',finished_at=?,error_code=?
		WHERE source=? AND run_id=? AND state='running'`, failure.At.UTC().Format(time.RFC3339Nano), failure.Code, SourceID, failure.RunID)
	return changedRun(result, err)
}

func (s *Store) Recover(ctx context.Context, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sync_status SET state='interrupted',finished_at=?,error_code='process_interrupted'
		WHERE source=? AND state='running'`, at.UTC().Format(time.RFC3339Nano), SourceID)
	if err != nil {
		return fmt.Errorf("recover sync: %w", err)
	}
	return nil
}

func (s *Store) Status(ctx context.Context) (status SyncStatus, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT source,state,run_id,started_at,finished_at,last_success_at,last_price_date,records,error_code
		FROM sync_status WHERE source=?`, SourceID).Scan(&status.Source, &status.State, &status.RunID, &status.StartedAt,
		&status.FinishedAt, &status.LastSuccessAt, &status.LastPriceDate, &status.Records, &status.ErrorCode)
	if err != nil {
		return status, fmt.Errorf("read sync status: %w", err)
	}
	return status, nil
}

func changedRun(result sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("update sync state: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sync rows affected: %w", err)
	}
	if n != 1 {
		return ErrRunChanged
	}
	return nil
}
