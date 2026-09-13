package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
)

type accountDeleteIntent struct {
	AccountID string `json:"account_id"`
}

type AccountDeletion struct {
	AccountID string `json:"account_id"`
	Deleted   bool   `json:"deleted"`
}

// DeleteAccount removes operational data, not immutable audit evidence or keys.
// The deletion audit permanently reserves the ID, including against import creation.
func (s *Store) DeleteAccount(ctx context.Context, id, key string) (json.RawMessage, error) {
	if !validID(id) {
		return nil, ErrQuery
	}
	return s.accountReceipt(ctx, key, "account_delete", accountDeleteIntent{id}, func(tx *sql.Tx) (any, int64, error) {
		info, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, id))
		if err != nil {
			return nil, 0, err
		}
		_, stamp, err := s.cutoff()
		if err != nil {
			return nil, 0, err
		}
		result := AccountDeletion{AccountID: id, Deleted: true}
		auditID, err := appendAudit(ctx, tx, key, "delete", "account_delete", id, id, 1, stamp, "human", publicAccount(info), result, nil)
		if err != nil {
			return nil, 0, err
		}
		// Delete jobs first (including terminal jobs referencing history). A worker
		// outside the transaction can no longer pass its claim/completion fence.
		for _, statement := range []string{
			`DELETE FROM weekly_jobs WHERE account_id=?`,
			`DELETE FROM current_holdings WHERE account_id=?`,
			`DELETE FROM account_records WHERE account_id=?`,
			`DELETE FROM accounts WHERE id=?`,
		} {
			if _, err := tx.ExecContext(ctx, statement, id); err != nil {
				return nil, 0, err
			}
		}
		return result, auditID, nil
	})
}
