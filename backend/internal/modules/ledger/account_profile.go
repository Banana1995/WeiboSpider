package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

type accountProfileIntent struct {
	AccountID       string `json:"account_id"`
	Name            string `json:"name"`
	ExpectedVersion string `json:"expected_version"`
}

// UpdateAccountName is a versioned profile write. It never touches the
// account's currency, dates, records or holdings.
func (s *Store) UpdateAccountName(ctx context.Context, key, id, name, expected string) (json.RawMessage, error) {
	name = strings.TrimSpace(name)
	if !validID(id) || !validText(name) {
		return nil, ErrOperation
	}
	version, err := positiveInteger(expected)
	if err != nil {
		return nil, ErrOperation
	}
	return s.accountReceipt(ctx, key, "account_profile", accountProfileIntent{id, name, expected}, func(tx *sql.Tx) (any, int64, error) {
		info, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, id))
		if err != nil {
			return nil, 0, err
		}
		if info.Version != version {
			return nil, 0, ErrVersion
		}
		_, stamp, err := s.cutoff()
		if err != nil {
			return nil, 0, err
		}
		next := publicAccount(info)
		next.Name = name
		next.Version = strconv.FormatInt(version+1, 10)
		result, err := tx.ExecContext(ctx, `UPDATE accounts SET name=?,version=? WHERE id=? AND version=?`, name, version+1, id, version)
		if err != nil {
			return nil, 0, constraintError(err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return nil, 0, err
		}
		if changed != 1 {
			return nil, 0, ErrVersion
		}
		auditID, err := appendAudit(ctx, tx, key, "replace", "account", id, id, version+1, stamp, "human", publicAccount(info), next, nil)
		if err != nil {
			return nil, 0, err
		}
		return next, auditID, nil
	})
}

func (h Handler) accountName(w http.ResponseWriter, r *http.Request, key string) {
	input, ok := body[struct {
		Name            string `json:"name"`
		ExpectedVersion string `json:"expected_version"`
	}](w, r)
	if !ok {
		return
	}
	result, err := h.Store.UpdateAccountName(r.Context(), key, r.PathValue("id"), input.Name, input.ExpectedVersion)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, result)
}
