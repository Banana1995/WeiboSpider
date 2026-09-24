package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
)

// StoredBenchmarks is the read-only provider used by HTTP and annual returns.
// It never calls a market source, even when a requested range is not ready.
type StoredBenchmarks struct{ db *database.DB }

func NewStoredBenchmarks(db *database.DB) *StoredBenchmarks { return &StoredBenchmarks{db: db} }

type BenchmarkSyncStatus struct {
	Code          string `json:"code"`
	LastAttemptAt string `json:"last_attempt_at"`
	LastSuccessAt string `json:"last_success_at"`
	LastCloseDate string `json:"last_close_date"`
	ErrorCode     string `json:"error_code"`
}

func (s *StoredBenchmarks) Status(ctx context.Context) ([]BenchmarkSyncStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.code, COALESCE(st.last_attempt_at,''), COALESCE(st.last_success_at,''),
		COALESCE((SELECT MAX(b.business_date) FROM benchmark_closes b WHERE b.code=c.code),''), COALESCE(st.error_code,'')
		FROM (SELECT 'H00300' code UNION ALL SELECT 'H00922' UNION ALL SELECT 'usINX') c
		LEFT JOIN benchmark_sync_status st ON st.code=c.code ORDER BY c.code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	statuses := make([]BenchmarkSyncStatus, 0, 3)
	for rows.Next() {
		var item BenchmarkSyncStatus
		if err := rows.Scan(&item.Code, &item.LastAttemptAt, &item.LastSuccessAt, &item.LastCloseDate, &item.ErrorCode); err != nil {
			return nil, err
		}
		statuses = append(statuses, item)
	}
	return statuses, errors.Join(rows.Err(), ctx.Err())
}

func (s *StoredBenchmarks) Fetch(ctx context.Context, code, from, to string) (Benchmark, error) {
	if err := validateBenchmarkRange(code, from, to); err != nil {
		return Benchmark{}, err
	}
	definition, _ := benchmarkDefinition(code)
	// The first requested day must be covered; otherwise a later first point
	// could masquerade as a valid baseline. Recent unsynced days remain empty.
	var covered int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM benchmark_coverage
		WHERE code=? AND from_date<=? AND to_date>=?)`, code, from, from).Scan(&covered); err != nil {
		return Benchmark{}, err
	}
	if covered == 0 {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	var latest sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(to_date) FROM benchmark_coverage WHERE code=? AND to_date>=?`, code, from).Scan(&latest); err != nil {
		return Benchmark{}, err
	}
	through := to
	if latest.String < through {
		through = latest.String // Recent unsynced closes may still be displayed as stale.
	}
	missing, err := s.Missing(ctx, code, from, through)
	if err != nil {
		return Benchmark{}, err
	}
	if len(missing) != 0 {
		return Benchmark{}, ErrBenchmarkUnavailable
	}
	result := Benchmark{Code: code, Name: definition.Name, Currency: definition.Currency,
		Source: definition.Source, From: from, To: to, Items: []BenchmarkItem{}}
	rows, err := s.db.QueryContext(ctx, `SELECT business_date,close FROM benchmark_closes
		WHERE code=? AND business_date>=? AND business_date<=? ORDER BY business_date LIMIT ?`, code, from, to, benchmarkMaxItems+1)
	if err != nil {
		return Benchmark{}, err
	}
	defer rows.Close()
	var base *big.Rat
	for rows.Next() {
		var date, text string
		if err := rows.Scan(&date, &text); err != nil {
			return Benchmark{}, err
		}
		close, ok := benchmarkClose(text)
		if !ok || !validDate(date) || len(result.Items) >= benchmarkMaxItems {
			return Benchmark{}, ErrCorrupt
		}
		if base == nil {
			base = close
		}
		rate, err := benchmarkReturn(close, base)
		if err != nil {
			return Benchmark{}, err
		}
		result.Items = append(result.Items, BenchmarkItem{Date: date, Close: text, Return: rate})
	}
	if err := errors.Join(rows.Err(), ctx.Err()); err != nil {
		return Benchmark{}, err
	}
	if len(result.Items) > 0 {
		result.From = result.Items[0].Date
		result.To = result.Items[len(result.Items)-1].Date
	}
	return result, nil
}

// SaveWindow commits only fully validated upstream windows. Earlier successful
// windows remain intact on a later failure or cancellation.
func (s *StoredBenchmarks) SaveWindow(ctx context.Context, value Benchmark, from, to string, now time.Time) error {
	if err := validateBenchmarkRange(value.Code, from, to); err != nil {
		return err
	}
	definition, _ := benchmarkDefinition(value.Code)
	if value.Name != definition.Name || value.Currency != definition.Currency || value.Source != definition.Source || len(value.Items) > benchmarkMaxItems {
		return ErrBenchmarkUnavailable
	}
	previous := ""
	for _, item := range value.Items {
		if !validDate(item.Date) || item.Date <= previous || item.Date < from || item.Date > to {
			return ErrBenchmarkUnavailable
		}
		if _, ok := benchmarkClose(item.Close); !ok {
			return ErrBenchmarkUnavailable
		}
		previous = item.Date
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		for _, item := range value.Items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO benchmark_closes(code,business_date,close,fetched_at) VALUES(?,?,?,?)
				ON CONFLICT(code,business_date) DO UPDATE SET close=excluded.close,fetched_at=excluded.fetched_at`,
				value.Code, item.Date, item.Close, stamp); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO benchmark_coverage(code,from_date,to_date,synced_at) VALUES(?,?,?,?)
			ON CONFLICT(code,from_date,to_date) DO UPDATE SET synced_at=excluded.synced_at`, value.Code, from, to, stamp); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO benchmark_sync_status(code,last_attempt_at,last_success_at,error_code) VALUES(?,?,?,'')
			ON CONFLICT(code) DO UPDATE SET last_attempt_at=excluded.last_attempt_at,last_success_at=excluded.last_success_at,error_code=''`,
			value.Code, stamp, stamp)
		return err
	})
}

func (s *StoredBenchmarks) RecordFailure(ctx context.Context, code string, at time.Time, failure string) error {
	if _, err := benchmarkDefinition(code); err != nil {
		return err
	}
	if failure != "source_unavailable" && failure != "timeout" && failure != "storage_error" {
		return ErrOperation
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO benchmark_sync_status(code,last_attempt_at,error_code) VALUES(?,?,?)
		ON CONFLICT(code) DO UPDATE SET last_attempt_at=excluded.last_attempt_at,error_code=excluded.error_code`,
		code, at.UTC().Format(time.RFC3339Nano), failure)
	return err
}

type benchmarkInterval struct{ from, to string }

func (s *StoredBenchmarks) Missing(ctx context.Context, code, from, to string) ([]benchmarkInterval, error) {
	if _, err := benchmarkDefinition(code); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT from_date,to_date FROM benchmark_coverage
		WHERE code=? AND to_date>=? AND from_date<=? ORDER BY from_date,to_date`, code, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cursor := from
	missing := []benchmarkInterval{}
	for rows.Next() {
		var first, last string
		if err := rows.Scan(&first, &last); err != nil {
			return nil, err
		}
		if first > cursor {
			missing = append(missing, benchmarkInterval{cursor, previousBenchmarkDay(first)})
		}
		if next := nextBenchmarkDay(last); next > cursor {
			cursor = next
		}
	}
	if err := errors.Join(rows.Err(), ctx.Err()); err != nil {
		return nil, err
	}
	if cursor <= to {
		missing = append(missing, benchmarkInterval{cursor, to})
	}
	return missing, nil
}

func nextBenchmarkDay(day string) string {
	date, _ := time.Parse(time.DateOnly, day)
	return date.AddDate(0, 0, 1).Format(time.DateOnly)
}

func previousBenchmarkDay(day string) string {
	date, _ := time.Parse(time.DateOnly, day)
	return date.AddDate(0, 0, -1).Format(time.DateOnly)
}

func (s *StoredBenchmarks) EarliestAsset(ctx context.Context) (string, error) {
	var day sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT MIN(business_date) FROM account_records
		WHERE voided=0 AND total_assets_minor IS NOT NULL`).Scan(&day)
	if err != nil {
		return "", fmt.Errorf("benchmark history start: %w", err)
	}
	return day.String, nil
}
