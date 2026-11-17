package options

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct {
	db     *database.DB
	now    func() time.Time
	limits Limits
}

func Open(ctx context.Context, directory string, limits Limits) (*Store, error) {
	db, e := database.Open(ctx, directory, "options")
	if e != nil {
		return nil, e
	}
	f, _ := fs.Sub(migrationFiles, "migrations")
	if e = db.Migrate(ctx, f); e != nil {
		return nil, errors.Join(e, db.Close())
	}
	s := &Store{db: db, now: time.Now, limits: limits}
	e = db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO settings(version,data) VALUES(1,?)", encode(DefaultSettings())); e != nil {
			return e
		}
		for _, v := range seedInstruments {
			if _, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO instruments(code,data) VALUES(?,?)", v.Code, encode(v)); e != nil {
				return e
			}
		}
		return nil
	})
	if e == nil {
		e = s.recover(ctx)
	}
	if e != nil {
		return nil, errors.Join(e, db.Close())
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func encode(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return string(b)
}
func uid() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func fingerprint(v any) string {
	sum := sha256.Sum256([]byte(encode(v)))
	return hex.EncodeToString(sum[:])
}
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

type reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readJSON[T any](ctx context.Context, db reader, query string, args ...any) (T, error) {
	var out T
	var raw string
	e := db.QueryRowContext(ctx, query, args...).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if e != nil {
		return out, e
	}
	e = json.Unmarshal([]byte(raw), &out)
	return out, e
}
func listJSON[T any](ctx context.Context, db reader, query string, args ...any) ([]T, error) {
	out := []T{}
	rows, e := db.QueryContext(ctx, query, args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var v T
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Settings(ctx context.Context, version int64) (Settings, error) {
	if version == 0 {
		return readJSON[Settings](ctx, s.db, "SELECT data FROM settings ORDER BY version DESC LIMIT 1")
	}
	return readJSON[Settings](ctx, s.db, "SELECT data FROM settings WHERE version=?", version)
}
func (s *Store) SaveSettings(ctx context.Context, v Settings) (Settings, error) {
	if (v.Mode != "full" && v.Mode != "net") || v.HorizonDays < 1 || v.HorizonDays > 1095 || validateFilter(v.Filter) != nil || validateFilter(v.CallFilter) != nil {
		return v, ErrInput
	}
	if v.Fees != nil {
		if len(v.Fees.Name) > 120 || len(v.Fees.Source) > 1000 {
			return v, ErrInput
		}
		for _, r := range []*FeeRule{&v.Fees.Open, v.Fees.Close, v.Fees.Assignment} {
			if r != nil {
				if _, e := fee(*r, 1); e != nil {
					return v, e
				}
			}
		}
	}
	e := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var version int64
		if e := tx.QueryRowContext(ctx, "SELECT MAX(version) FROM settings").Scan(&version); e != nil {
			return e
		}
		if v.Version != version {
			return ErrVersion
		}
		v.Version++
		_, e := tx.ExecContext(ctx, "INSERT INTO settings(version,data) VALUES(?,?)", v.Version, encode(v))
		return e
	})
	return v, e
}
func (s *Store) Instruments(ctx context.Context) ([]Instrument, error) {
	return listJSON[Instrument](ctx, s.db, "SELECT data FROM instruments ORDER BY code")
}
func (s *Store) PutInstruments(ctx context.Context, values []Instrument) error {
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		for _, v := range values {
			if !symbolPattern.MatchString(v.Code) || v.Currency != "USD" || (v.Kind != "STOCK" && v.Kind != "ETF") || len(v.Name) > 200 || len(v.EnglishName) > 200 {
				continue
			}
			old, err := readJSON[Instrument](ctx, tx, "SELECT data FROM instruments WHERE code=?", v.Code)
			if err == nil {
				if v.Aliases == "" {
					v.Aliases = old.Aliases
				}
				if v.EnglishName == "" {
					v.EnglishName = old.EnglishName
				}
			} else if !errors.Is(err, ErrNotFound) {
				return err
			}
			if _, e := tx.ExecContext(ctx, "INSERT INTO instruments(code,data) VALUES(?,?) ON CONFLICT(code) DO UPDATE SET data=excluded.data", v.Code, encode(v)); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Store) Watchlist(ctx context.Context) ([]Watch, error) {
	return listJSON[Watch](ctx, s.db, "SELECT data FROM watchlist ORDER BY code")
}
func (s *Store) AddWatch(ctx context.Context, code string) (Watch, error) {
	var out Watch
	e := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		old, e := readJSON[Watch](ctx, tx, "SELECT data FROM watchlist WHERE code=?", code)
		if e == nil {
			out = old
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		v, e := readJSON[Instrument](ctx, tx, "SELECT data FROM instruments WHERE code=?", code)
		if e != nil {
			return e
		}
		var n int
		if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM watchlist").Scan(&n); e != nil {
			return e
		}
		if n >= s.limits.MaxWatch {
			return ErrLimit
		}
		out = Watch{Instrument: v, Enabled: true, Version: 1}
		_, e = tx.ExecContext(ctx, "INSERT INTO watchlist(code,version,data) VALUES(?,1,?)", code, encode(out))
		return e
	})
	return out, e
}
func (s *Store) SaveWatch(ctx context.Context, code string, v Watch) (Watch, error) {
	if validateFilter(v.Filter) != nil || validateFilter(v.CallFilter) != nil {
		return v, ErrInput
	}
	e := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		old, e := readJSON[Watch](ctx, tx, "SELECT data FROM watchlist WHERE code=?", code)
		if e != nil {
			return e
		}
		if old.Version != v.Version {
			return ErrVersion
		}
		v.Instrument = old.Instrument
		v.Version++
		_, e = tx.ExecContext(ctx, "UPDATE watchlist SET version=?,data=? WHERE code=?", v.Version, encode(v), code)
		return e
	})
	return v, e
}
func (s *Store) RemoveWatch(ctx context.Context, code string) error {
	_, e := s.db.ExecContext(ctx, "DELETE FROM watchlist WHERE code=?", code)
	return e
}
func (s *Store) Snapshot(ctx context.Context, id string) (Snapshot, error) {
	return readJSON[Snapshot](ctx, s.db, "SELECT data FROM snapshots WHERE id=?", id)
}
func (s *Store) Latest(ctx context.Context) ([]Snapshot, error) {
	return listJSON[Snapshot](ctx, s.db, "SELECT s.data FROM snapshots s JOIN latest l ON s.id=l.snapshot_id ORDER BY s.symbol,l.option_type")
}
func (s *Store) latestForSide(ctx context.Context, side string) ([]Snapshot, error) {
	return listJSON[Snapshot](ctx, s.db, "SELECT s.data FROM snapshots s JOIN latest l ON s.id=l.snapshot_id WHERE l.option_type=? ORDER BY s.symbol", side)
}
func (s *Store) Publish(ctx context.Context, snapshot Snapshot) error {
	if !validSide(snapshot.OptionType) {
		return ErrInput
	}
	side := optionSide(snapshot.OptionType)
	for _, q := range snapshot.Quotes {
		if q.Contract.Type != side || q.Contract.Symbol != snapshot.Symbol {
			return ErrInput
		}
	}
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "INSERT INTO snapshots(id,symbol,option_type,captured_at,data) VALUES(?,?,?,?,?)", snapshot.ID, snapshot.Symbol, side, snapshot.CapturedAt, encode(snapshot)); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO latest(symbol,option_type,snapshot_id) VALUES(?,?,?) ON CONFLICT(symbol,option_type) DO UPDATE SET snapshot_id=excluded.snapshot_id", snapshot.Symbol, side, snapshot.ID); e != nil {
			return e
		}
		return nil
	})
}
func (s *Store) fees(ctx context.Context, in Input) (*FeeProfile, error) {
	if in.FeeVersion == 0 {
		return nil, nil
	}
	v, e := s.Settings(ctx, in.FeeVersion)
	if e == nil && v.Fees == nil {
		e = ErrNotFound
	}
	return v.Fees, e
}
func (s *Store) Calculate(ctx context.Context, snapshotID, code string, in Input) (Candidate, *FeeProfile, error) {
	var out Candidate
	snap, e := s.Snapshot(ctx, snapshotID)
	if e != nil {
		return out, nil, e
	}
	var q *Quote
	for i := range snap.Quotes {
		if snap.Quotes[i].Contract.Code == code {
			q = &snap.Quotes[i]
			break
		}
	}
	if q == nil {
		return out, nil, ErrNotFound
	}
	fees, e := s.fees(ctx, in)
	if e != nil {
		return out, nil, e
	}
	result, _, e := calculate(*q, snap.ValuationDate, in, fees, snap.Underlying)
	if e != nil {
		return out, nil, e
	}
	snap.Quotes = nil
	out = Candidate{Snapshot: snap, Quote: *q, Input: in, Result: result, Stale: s.isStale(snap)}
	return out, fees, nil
}
func (s *Store) isStale(v Snapshot) bool {
	t, e := time.Parse(time.RFC3339Nano, v.CapturedAt)
	return e != nil || s.now().Sub(t) > 15*time.Minute
}
func (s *Store) Favorite(ctx context.Context, id string) (Favorite, error) {
	return readJSON[Favorite](ctx, s.db, "SELECT data FROM favorites WHERE id=?", id)
}
func (s *Store) SaveFavorite(ctx context.Context, requestID, snapshotID, code string, in Input) (Favorite, error) {
	var out Favorite
	if !idPattern.MatchString(requestID) || validateInput(in) != nil {
		return out, ErrInput
	}
	// Own all pointer fields before normalizing; callers may reuse inputs concurrently.
	var owned Input
	if e := json.Unmarshal([]byte(encode(in)), &owned); e != nil {
		return out, e
	}
	in = owned
	// Normalize decimal spelling so 2 and 2.00 describe the same assumption.
	normalize := func(p *string) {
		if p != nil {
			if r, e := number(*p); e == nil {
				*p = strings.TrimRight(strings.TrimRight(r.FloatString(9), "0"), ".")
			}
		}
	}
	normalize(in.Price)
	normalize(in.FeeOverride)
	if in.Interest != nil {
		normalize(&in.Interest.Principal)
		normalize(&in.Interest.AnnualRate)
	}
	if in.Scenario != nil {
		normalize(&in.Scenario.Price)
		normalize(in.Scenario.Fee)
		normalize(in.Scenario.Interest)
		normalize(in.Scenario.StockCost)
	}
	hash := fingerprint([]any{snapshotID, code, in})
	var previousHash, id string
	e := s.db.QueryRowContext(ctx, "SELECT fingerprint,favorite_id FROM receipts WHERE request_id=?", requestID).Scan(&previousHash, &id)
	if e == nil {
		if previousHash != hash {
			return out, ErrReplay
		}
		return s.Favorite(ctx, id)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	c, fees, e := s.Calculate(ctx, snapshotID, code, in)
	if e != nil {
		return out, e
	}
	out = Favorite{ID: uid(), CreatedAt: stamp(s.now()), FormatVersion: 1, Candidate: c, FeeProfile: fees}
	e = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var old string
		e := tx.QueryRowContext(ctx, "SELECT data FROM favorites WHERE fingerprint=?", hash).Scan(&old)
		if e == nil {
			if e = json.Unmarshal([]byte(old), &out); e != nil {
				return e
			}
		} else if errors.Is(e, sql.ErrNoRows) {
			if _, e = tx.ExecContext(ctx, "INSERT INTO favorites(id,fingerprint,snapshot_id,contract_code,created_at,data) VALUES(?,?,?,?,?,?)", out.ID, hash, snapshotID, code, out.CreatedAt, encode(out)); e != nil {
				return e
			}
		} else {
			return e
		}
		var storedHash string
		e = tx.QueryRowContext(ctx, "SELECT fingerprint FROM receipts WHERE request_id=?", requestID).Scan(&storedHash)
		if e == nil {
			if storedHash != hash {
				return ErrReplay
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO receipts(request_id,fingerprint,favorite_id) VALUES(?,?,?)", requestID, hash, out.ID)
		return e
	})
	return out, e
}

type FavoritePage struct {
	Items      []Favorite `json:"items"`
	Total      int        `json:"total"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

func (s *Store) Favorites(ctx context.Context, cursor string, limit int) (FavoritePage, error) {
	out := FavoritePage{Items: []Favorite{}}
	if limit < 1 || limit > 100 || (cursor != "" && !idPattern.MatchString(cursor)) {
		return out, ErrInput
	}
	e := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		if e := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM favorites").Scan(&out.Total); e != nil {
			return e
		}
		query := "SELECT data FROM favorites"
		args := []any{}
		if cursor != "" {
			v, e := readJSON[Favorite](ctx, tx, "SELECT data FROM favorites WHERE id=?", cursor)
			if e != nil {
				return e
			}
			query += " WHERE (created_at,id)<(?,?)"
			args = append(args, v.CreatedAt, v.ID)
		}
		args = append(args, limit+1)
		v, e := listJSON[Favorite](ctx, tx, query+" ORDER BY created_at DESC,id DESC LIMIT ?", args...)
		if e != nil {
			return e
		}
		if len(v) > limit {
			v = v[:limit]
			out.NextCursor = v[len(v)-1].ID
		}
		out.Items = v
		return nil
	})
	return out, e
}
func (s *Store) DeleteFavorite(ctx context.Context, id string) error {
	_, e := s.db.ExecContext(ctx, "DELETE FROM favorites WHERE id=?", id)
	return e
}

var seedInstruments = []Instrument{
	{"US.AAPL", "苹果", "Apple Inc.", "蘋果 苹果公司", "STOCK", "USD"},
	{"US.MSFT", "微软", "Microsoft Corporation", "微軟", "STOCK", "USD"},
	{"US.NVDA", "英伟达", "NVIDIA Corporation", "英偉達 辉达 輝達", "STOCK", "USD"},
	{"US.TSLA", "特斯拉", "Tesla Inc.", "特斯拉汽车", "STOCK", "USD"},
	{"US.AMZN", "亚马逊", "Amazon.com Inc.", "亞馬遜", "STOCK", "USD"},
	{"US.GOOGL", "谷歌 A", "Alphabet Inc. Class A", "谷歌 Google", "STOCK", "USD"},
	{"US.META", "Meta Platforms", "Meta Platforms Inc.", "脸书 Facebook", "STOCK", "USD"},
	{"US.BABA", "阿里巴巴", "Alibaba Group Holding Ltd.", "阿里", "STOCK", "USD"},
	{"US.PDD", "拼多多", "PDD Holdings Inc.", "拼多多", "STOCK", "USD"},
	{"US.AMD", "超威半导体", "Advanced Micro Devices Inc.", "超微半導體", "STOCK", "USD"},
	{"US.SPY", "标普500 ETF", "SPDR S&P 500 ETF Trust", "標普500", "ETF", "USD"},
	{"US.QQQ", "纳斯达克100 ETF", "Invesco QQQ Trust", "納斯達克100", "ETF", "USD"},
}
