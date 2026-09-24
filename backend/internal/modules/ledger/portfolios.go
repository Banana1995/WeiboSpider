package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

const maxPortfolioMembers = 50

var (
	ErrPortfolioCurrency = errors.New("portfolio members must use one currency")
	ErrPortfolioMember   = errors.New("portfolio member no longer exists")
	ErrPortfolioBasis    = errors.New("portfolio member has no valid asset basis")
)

type Portfolio struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Currency   Currency `json:"currency"`
	AccountIDs []string `json:"account_ids"`
	Version    string   `json:"version"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

type PortfolioCommand struct {
	Action          string
	ID              string
	Name            string
	AccountIDs      []string
	ExpectedVersion string
}

type PortfolioDeletion struct {
	PortfolioID string `json:"portfolio_id"`
	Deleted     bool   `json:"deleted"`
}

func validPortfolioMembers(ids []string) bool {
	if len(ids) < 1 || len(ids) > maxPortfolioMembers {
		return false
	}
	for i, id := range ids {
		if !validID(id) || i > 0 && ids[i-1] >= id {
			return false
		}
	}
	return true
}

func (p Portfolio) valid() bool {
	_, versionErr := positiveInteger(p.Version)
	created, createdErr := time.Parse(time.RFC3339Nano, p.CreatedAt)
	updated, updatedErr := time.Parse(time.RFC3339Nano, p.UpdatedAt)
	return validID(p.ID) && validText(p.Name) && p.Name == strings.TrimSpace(p.Name) && p.Currency.valid() &&
		validPortfolioMembers(p.AccountIDs) && versionErr == nil && createdErr == nil && updatedErr == nil && !updated.Before(created)
}

const portfolioSelect = `SELECT id,version,payload,EXISTS(SELECT 1 FROM audit_log a WHERE a.id=portfolios.audit_id
 AND a.entity_type='portfolio' AND a.entity_id=portfolios.id AND a.account_id IS NULL
 AND a.version=portfolios.version AND a.after_json=portfolios.payload AND a.action IN ('create','replace')) FROM portfolios`

func scanPortfolio(row interface{ Scan(...any) error }) (Portfolio, error) {
	var p Portfolio
	var id, payload string
	var version int64
	var audited bool
	if err := row.Scan(&id, &version, &payload, &audited); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, ErrNotFound
		}
		return p, err
	}
	if decodeReceipt(payload, &p) != nil || !p.valid() || p.ID != id || p.Version != strconv.FormatInt(version, 10) || !audited {
		return Portfolio{}, ErrCorrupt
	}
	return p, nil
}

func (s *Store) Portfolio(ctx context.Context, id string) (Portfolio, error) {
	if !validID(id) {
		return Portfolio{}, ErrQuery
	}
	return scanPortfolio(s.db.QueryRowContext(ctx, portfolioSelect+` WHERE id=?`, id))
}

func (s *Store) ListPortfolios(ctx context.Context, q PageQuery) ([]Portfolio, error) {
	if q.Limit < 1 || q.Limit > 100 || q.After != "" && !validID(q.After) {
		return nil, ErrQuery
	}
	rows, err := s.db.QueryContext(ctx, portfolioSelect+` WHERE id>? ORDER BY id LIMIT ?`, q.After, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Portfolio{}
	for rows.Next() {
		p, err := scanPortfolio(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

// All mutations use the existing global receipt namespace. Membership is a set,
// canonicalized before intent hashing, and is never materialized as account rows.
func (s *Store) WritePortfolio(ctx context.Context, key string, c PortfolioCommand) (json.RawMessage, error) {
	c.AccountIDs = slices.Clone(c.AccountIDs)
	slices.Sort(c.AccountIDs)
	c.Name = strings.TrimSpace(c.Name)
	if !validID(key) || !validID(c.ID) || c.Action != "create" && c.Action != "replace" && c.Action != "delete" {
		return nil, ErrOperation
	}
	if c.Action != "delete" && (!validText(c.Name) || !validPortfolioMembers(c.AccountIDs)) {
		return nil, ErrOperation
	}
	if c.Action == "delete" && (c.Name != "" || len(c.AccountIDs) != 0) || c.Action == "create" && c.ExpectedVersion != "" {
		return nil, ErrOperation
	}
	var expected int64
	if c.Action != "create" {
		var err error
		expected, err = positiveInteger(c.ExpectedVersion)
		if err != nil || expected == math.MaxInt64 {
			return nil, ErrOperation
		}
	}
	request, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var response string
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		receipt, found, err := loadReceipt(ctx, tx, key, "portfolio", string(request))
		if err != nil {
			return err
		}
		if found {
			if err := validatePortfolioReceipt(ctx, tx, c, receipt, key, expected); err != nil {
				return err
			}
			response = receipt.Response
			return nil
		}
		_, stamp, err := s.cutoff()
		if err != nil {
			return err
		}
		var before any
		version := int64(1)
		p := Portfolio{ID: c.ID, Name: c.Name, AccountIDs: c.AccountIDs, CreatedAt: stamp, UpdatedAt: stamp}
		if c.Action == "create" {
			var used bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit_log WHERE entity_type='portfolio' AND entity_id=?)`, c.ID).Scan(&used); err != nil {
				return err
			}
			if used {
				return ErrConflict
			}
		} else {
			previous, err := scanPortfolio(tx.QueryRowContext(ctx, portfolioSelect+` WHERE id=?`, c.ID))
			if err != nil {
				return err
			}
			if previous.Version != c.ExpectedVersion {
				return ErrVersion
			}
			before, p.CreatedAt, version = previous, previous.CreatedAt, expected+1
		}
		p.Version = strconv.FormatInt(version, 10)
		var result any = p
		if c.Action == "delete" {
			result = PortfolioDeletion{PortfolioID: c.ID, Deleted: true}
		} else {
			for _, id := range c.AccountIDs {
				account, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, id))
				if errors.Is(err, ErrNotFound) {
					return ErrPortfolioMember
				}
				if err != nil {
					return err
				}
				if p.Currency != "" && p.Currency != account.Currency {
					return ErrPortfolioCurrency
				}
				p.Currency = account.Currency
			}
			if !p.valid() {
				return ErrOperation
			}
			result = p
		}
		auditID, err := appendAudit(ctx, tx, key, c.Action, "portfolio", c.ID, "", version, stamp, "human", before, result, nil)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		response = string(encoded)
		switch c.Action {
		case "create":
			_, err = tx.ExecContext(ctx, `INSERT INTO portfolios(id,version,audit_id,payload) VALUES(?,?,?,?)`, c.ID, version, auditID, response)
		case "replace":
			_, err = tx.ExecContext(ctx, `UPDATE portfolios SET version=?,audit_id=?,payload=? WHERE id=?`, version, auditID, response, c.ID)
		case "delete":
			_, err = tx.ExecContext(ctx, `DELETE FROM portfolios WHERE id=?`, c.ID)
		}
		if err != nil {
			return constraintError(err)
		}
		return saveReceipt(ctx, tx, key, "portfolio", string(request), response, auditID)
	})
	return json.RawMessage(response), err
}

func validatePortfolioReceipt(ctx context.Context, tx *sql.Tx, c PortfolioCommand, receipt storedReceipt, key string, expected int64) error {
	version := int64(1)
	if c.Action != "create" {
		version = expected + 1
	}
	var matches bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit_log WHERE id=? AND correlation_id=?
 AND entity_type='portfolio' AND entity_id=? AND account_id IS NULL AND action=? AND version=? AND after_json=?)`,
		receipt.AuditID, key, c.ID, c.Action, version, receipt.Response).Scan(&matches); err != nil {
		return err
	}
	if !matches {
		return ErrCorrupt
	}
	if c.Action == "delete" {
		var result PortfolioDeletion
		if decodeReceipt(receipt.Response, &result) != nil || result.PortfolioID != c.ID || !result.Deleted {
			return ErrCorrupt
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM portfolios WHERE id=?)`, c.ID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrCorrupt
		}
		return nil
	}
	var result Portfolio
	if decodeReceipt(receipt.Response, &result) != nil || !result.valid() || result.ID != c.ID || result.Name != c.Name ||
		result.Version != strconv.FormatInt(version, 10) || !slices.Equal(result.AccountIDs, c.AccountIDs) {
		return ErrCorrupt
	}
	current, err := scanPortfolio(tx.QueryRowContext(ctx, portfolioSelect+` WHERE id=?`, c.ID))
	if err != nil {
		return err
	}
	currentVersion, _ := positiveInteger(current.Version)
	if currentVersion < version {
		return ErrCorrupt
	}
	return nil
}
