package ledger

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open migrates the independent ledger database and reconciles deleted members.
// The caller owns Close.
func Open(ctx context.Context, directory string) (*database.DB, error) {
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("ledger migration files: %w", err)
	}
	db, err := database.Open(ctx, directory, "ledger")
	if err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	if err := db.Migrate(ctx, files); err != nil {
		return nil, errors.Join(fmt.Errorf("ledger migrations: %w", err), db.Close())
	}
	if err := NewStore(db, nil).reconcileDeletedPortfolioMembers(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("ledger portfolio members: %w", err), db.Close())
	}
	return db, nil
}
