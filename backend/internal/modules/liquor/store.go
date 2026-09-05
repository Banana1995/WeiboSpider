package liquor

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ db *database.DB }

func NewStore(ctx context.Context, db *database.DB) (*Store, error) {
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("migration files: %w", err)
	}
	if err := db.Migrate(ctx, files); err != nil {
		return nil, fmt.Errorf("liquor migrations: %w", err)
	}
	return &Store{db: db}, nil
}

type Import struct {
	RunID    string
	Snapshot Snapshot
	At       time.Time
}

func (s *Store) Complete(ctx context.Context, imported Import) (int, error) {
	count := 0
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		today := imported.At.In(time.FixedZone("Beijing", 8*60*60)).Format(time.DateOnly)
		if !validDate(imported.Snapshot.Date) || imported.Snapshot.Date > today {
			return fmt.Errorf("%w: invalid or future snapshot date", ErrSourceData)
		}
		var lastDate string
		if err := tx.QueryRowContext(ctx, `SELECT last_price_date FROM sync_status WHERE source=?`, SourceID).Scan(&lastDate); err != nil {
			return fmt.Errorf("read last price date: %w", err)
		}
		if imported.Snapshot.Date < lastDate {
			return fmt.Errorf("%w: snapshot date precedes last committed date", ErrSourceData)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE products SET active=0 WHERE source=?`, SourceID); err != nil {
			return fmt.Errorf("reset current products: %w", err)
		}
		for _, series := range imported.Snapshot.Series {
			p := series.Product
			if _, err := tx.ExecContext(ctx, `INSERT INTO products(source,id,name,specifications,unit,sort_order,active)
				VALUES(?,?,?,?,?,?,1) ON CONFLICT(source,id) DO UPDATE SET
				name=excluded.name,specifications=excluded.specifications,unit=excluded.unit,sort_order=excluded.sort_order,active=1`,
				SourceID, p.ID, p.Name, p.Specifications, p.Unit, p.Sort); err != nil {
				return fmt.Errorf("save product: %w", err)
			}
			for _, point := range series.Prices {
				if _, err := tx.ExecContext(ctx, `INSERT INTO prices(source,product_id,price_date,price_cents,change_cents,fetched_at)
					VALUES(?,?,?,?,?,?) ON CONFLICT(source,product_id,price_date) DO UPDATE SET
					price_cents=excluded.price_cents,change_cents=excluded.change_cents,fetched_at=excluded.fetched_at`,
					SourceID, p.ID, point.Date, point.Price, point.Change, imported.At.UTC().Format(time.RFC3339Nano)); err != nil {
					return fmt.Errorf("save price: %w", err)
				}
				count++
			}
		}
		at := imported.At.UTC().Format(time.RFC3339Nano)
		result, err := tx.ExecContext(ctx, `UPDATE sync_status SET state='succeeded',finished_at=?,last_success_at=?,
			last_price_date=?,records=?,error_code='' WHERE source=? AND run_id=? AND state='running'`,
			at, at, imported.Snapshot.Date, count, SourceID, imported.RunID)
		return changedRun(result, err)
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) Latest(ctx context.Context) (result Latest, err error) {
	result = Latest{Source: SourceID, Basis: PriceBasis, Items: make([]Quote, 0)}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.name,p.specifications,p.unit,p.sort_order,
		q.price_date,q.price_cents,q.change_cents,q.fetched_at FROM products p
		JOIN prices q ON p.source=q.source AND p.id=q.product_id
		WHERE p.active=1 AND q.source=? AND q.price_date=(SELECT MAX(price_date) FROM prices WHERE source=?)
		ORDER BY p.sort_order,p.id`, SourceID, SourceID)
	if err != nil {
		return result, fmt.Errorf("latest prices: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var q Quote
		if err := rows.Scan(&q.ID, &q.Name, &q.Specifications, &q.Unit, &q.Sort, &q.Date, &q.Price, &q.Change, &q.FetchedAt); err != nil {
			return result, fmt.Errorf("scan latest price: %w", err)
		}
		result.Date = q.Date
		result.Items = append(result.Items, q)
	}
	return result, rows.Err()
}

func (s *Store) History(ctx context.Context, q HistoryQuery) (result History, err error) {
	result = History{Source: SourceID, Basis: PriceBasis, Items: make([]Point, 0)}
	p := &result.Product
	err = s.db.QueryRowContext(ctx, `SELECT id,name,specifications,unit,sort_order FROM products WHERE source=? AND id=?`, SourceID, q.ID).
		Scan(&p.ID, &p.Name, &p.Specifications, &p.Unit, &p.Sort)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, fmt.Errorf("find product: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT price_date,price_cents,change_cents,fetched_at FROM prices
		WHERE source=? AND product_id=? AND price_date>=? AND price_date<=? ORDER BY price_date DESC LIMIT ?`,
		SourceID, q.ID, q.From, q.To, q.Limit)
	if err != nil {
		return result, fmt.Errorf("price history: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var point Point
		if err := rows.Scan(&point.Date, &point.Price, &point.Change, &point.FetchedAt); err != nil {
			return result, fmt.Errorf("scan history: %w", err)
		}
		result.Items = append(result.Items, point)
	}
	return result, rows.Err()
}
