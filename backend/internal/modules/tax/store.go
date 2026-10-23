package tax

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
)

var ErrLimit = errors.New("税务空间最多保存 40 个批次、10,000 条原始记录")

type Store struct{ db *database.DB }
type Batch struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Digest  string `json:"digest"`
	Count   int    `json:"count"`
	Created string `json:"created"`
}
type State struct {
	Imports []Batch  `json:"imports"`
	Records []Record `json:"records"`
	Rates   []Rate   `json:"rates"`
}
type ImportResult struct {
	ID        int64 `json:"id"`
	Added     int   `json:"added"`
	Duplicate bool  `json:"duplicate"`
}
type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func Open(ctx context.Context, directory string) (*Store, error) {
	db, err := database.Open(ctx, directory, "tax")
	if err != nil {
		return nil, err
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tax_imports (
		id INTEGER PRIMARY KEY, workspace TEXT NOT NULL, digest TEXT NOT NULL,
		name TEXT NOT NULL, count INTEGER NOT NULL, created TEXT NOT NULL, records TEXT NOT NULL,
		UNIQUE(workspace,digest));
		CREATE TABLE IF NOT EXISTS tax_rates (
		workspace TEXT NOT NULL, year INTEGER NOT NULL, currency TEXT NOT NULL, value TEXT NOT NULL,
		date TEXT NOT NULL, source TEXT NOT NULL, PRIMARY KEY(workspace,year,currency));`)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.db.Close() }

// Union is a multiset union: identical fills within a file remain distinct;
// overlapping files each retain their own provenance when another is deleted.
func mergeRecords(groups ...[]Record) []Record {
	seen := map[string]int{}
	out := []Record{}
	for _, group := range groups {
		counts := map[string]int{}
		for _, r := range group {
			key := r.identity()
			counts[key]++
			if counts[key] > seen[key] {
				out = append(out, r)
			}
		}
		for key, n := range counts {
			if n > seen[key] {
				seen[key] = n
			}
		}
	}
	return out
}
func load(ctx context.Context, db queryer, workspace string) (State, error) {
	state := State{Imports: []Batch{}, Records: []Record{}, Rates: DefaultRates()}
	rows, err := db.QueryContext(ctx, `SELECT id,name,digest,count,created,records FROM tax_imports WHERE workspace=? ORDER BY id`, workspace)
	if err != nil {
		return state, err
	}
	groups := [][]Record{}
	for rows.Next() {
		var b Batch
		var raw string
		var records []Record
		if err = rows.Scan(&b.ID, &b.Name, &b.Digest, &b.Count, &b.Created, &raw); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &records); err != nil {
			break
		}
		state.Imports = append(state.Imports, b)
		groups = append(groups, records)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return state, err
	}
	state.Records = mergeRecords(groups...)
	rows, err = db.QueryContext(ctx, `SELECT year,currency,value,date,source FROM tax_rates WHERE workspace=?`, workspace)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var rate Rate
		if err = rows.Scan(&rate.Year, &rate.Currency, &rate.Value, &rate.Date, &rate.Source); err != nil {
			break
		}
		found := false
		for i, v := range state.Rates {
			if v.Year == rate.Year && v.Currency == rate.Currency {
				state.Rates[i] = rate
				found = true
				break
			}
		}
		if !found {
			state.Rates = append(state.Rates, rate)
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	sort.Slice(state.Rates, func(i, j int) bool {
		a, b := state.Rates[i], state.Rates[j]
		if a.Year != b.Year {
			return a.Year > b.Year
		}
		return a.Currency < b.Currency
	})
	return state, err
}
func (s *Store) State(ctx context.Context, workspace string) (State, error) {
	var state State
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error { var err error; state, err = load(ctx, tx, workspace); return err })
	return state, err
}
func (s *Store) Import(ctx context.Context, workspace, name, hash string, records []Record) (ImportResult, error) {
	var result ImportResult
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		state, err := load(ctx, tx, workspace)
		if err != nil {
			return err
		}
		total := len(records)
		for _, b := range state.Imports {
			if b.Digest == hash {
				result = ImportResult{ID: b.ID, Duplicate: true}
				return nil
			}
			total += b.Count
		}
		if len(state.Imports) >= 40 || total > MaxRecords {
			return ErrLimit
		}
		combined := mergeRecords(state.Records, records)
		raw, err := json.Marshal(records)
		if err != nil {
			return err
		}
		row, err := tx.ExecContext(ctx, `INSERT INTO tax_imports(workspace,digest,name,count,created,records) VALUES(?,?,?,?,?,?)`, workspace, hash, name, len(records), time.Now().UTC().Format(time.RFC3339), string(raw))
		if err != nil {
			return err
		}
		id, err := row.LastInsertId()
		result = ImportResult{id, len(combined) - len(state.Records), false}
		return err
	})
	return result, err
}
func (s *Store) Delete(ctx context.Context, workspace string, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tax_imports WHERE workspace=? AND id=?`, workspace, id)
	return err
}
func (s *Store) SaveRate(ctx context.Context, workspace string, rate Rate) error {
	x, err := decimal(rate.Value)
	if err != nil || x.Sign() <= 0 || x.Cmp(number("1000")) > 0 || rate.Year < 1900 || rate.Year > 2100 || (rate.Currency != "HKD" && rate.Currency != "USD") {
		return fmt.Errorf("汇率须大于 0 且不超过 1000，币种为 HKD／USD，年份为 1900—2100")
	}
	d, err := time.Parse(time.DateOnly, rate.Date)
	if err != nil || d.Year() != rate.Year {
		return fmt.Errorf("汇率日期必须属于所选年度")
	}
	if len(rate.Source) > 512 {
		return fmt.Errorf("来源说明过长")
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO tax_rates(workspace,year,currency,value,date,source) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace,year,currency) DO UPDATE SET value=excluded.value,date=excluded.date,source=excluded.source`, workspace, rate.Year, rate.Currency, rate.Value, rate.Date, rate.Source)
	return err
}
