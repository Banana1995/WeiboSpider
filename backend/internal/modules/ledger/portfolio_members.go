package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

// Account deletion and every affected portfolio projection share a transaction.
// Keep the original definition in the audit; an empty portfolio is retired.
func removeAccountFromPortfolios(ctx context.Context, tx *sql.Tx, account, correlation, stamp string) error {
	rows, err := tx.QueryContext(ctx, portfolioSelect+` WHERE EXISTS (
		SELECT 1 FROM json_each(portfolios.payload,'$.account_ids') member WHERE member.value=?
	) ORDER BY id`, account)
	if err != nil {
		return err
	}
	var affected []Portfolio
	for rows.Next() {
		p, scanErr := scanPortfolio(rows)
		if scanErr != nil {
			return errors.Join(scanErr, rows.Close())
		}
		affected = append(affected, p)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, previous := range affected {
		version, err := positiveInteger(previous.Version)
		if err != nil || version == math.MaxInt64 {
			return ErrCorrupt
		}
		next := previous
		next.AccountIDs = make([]string, 0, len(previous.AccountIDs)-1)
		for _, id := range previous.AccountIDs {
			if id != account {
				next.AccountIDs = append(next.AccountIDs, id)
			}
		}
		next.Version = strconv.FormatInt(version+1, 10)
		next.UpdatedAt = stamp
		action := "replace"
		var result any = next
		if len(next.AccountIDs) == 0 {
			action = "delete"
			result = PortfolioDeletion{PortfolioID: previous.ID, Deleted: true}
		} else if !next.valid() {
			return ErrCorrupt
		}
		// Triggers compare the exact frozen bytes, including JSON escaping.
		var before string
		if err := tx.QueryRowContext(ctx, `SELECT payload FROM portfolios WHERE id=? AND version=?`, previous.ID, version).Scan(&before); err != nil {
			return err
		}
		auditID, err := appendAudit(ctx, tx, correlation, action, "portfolio", previous.ID, "", version+1, stamp, "system",
			json.RawMessage(before), result, map[string]string{"reason": "account_deleted", "account_id": account})
		if err != nil {
			return err
		}
		var changed sql.Result
		if action == "delete" {
			changed, err = tx.ExecContext(ctx, `DELETE FROM portfolios WHERE id=? AND version=?`, previous.ID, version)
		} else {
			payload, marshalErr := json.Marshal(next)
			if marshalErr != nil {
				return marshalErr
			}
			changed, err = tx.ExecContext(ctx, `UPDATE portfolios SET version=?,audit_id=?,payload=? WHERE id=? AND version=?`,
				version+1, auditID, string(payload), previous.ID, version)
		}
		if err != nil {
			return err
		}
		count, err := changed.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrVersion
		}
	}
	return nil
}

// Repair the former retained-member behavior at startup, never during a GET.
// Only a verified account-deletion audit authorizes removal of a missing member.
// Reopening an already repaired database leaves projections and audits unchanged.
func (s *Store) reconcileDeletedPortfolioMembers(ctx context.Context) error {
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT deletion.id,deletion.account_id,deletion.correlation_id,deletion.after_json
			FROM audit_log deletion WHERE deletion.entity_type='account_delete'
			AND NOT EXISTS(SELECT 1 FROM accounts WHERE id=deletion.account_id)
			AND EXISTS(SELECT 1 FROM portfolios p, json_each(p.payload,'$.account_ids') member WHERE member.value=deletion.account_id)
			ORDER BY deletion.id`)
		if err != nil {
			return err
		}
		type deletion struct {
			auditID                int64
			account, key, response string
		}
		var deletions []deletion
		for rows.Next() {
			var d deletion
			if err := rows.Scan(&d.auditID, &d.account, &d.key, &d.response); err != nil {
				return errors.Join(err, rows.Close())
			}
			deletions = append(deletions, d)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(deletions) == 0 {
			return nil
		}
		_, stamp, err := s.cutoff()
		if err != nil {
			return err
		}
		for _, d := range deletions {
			if err := validateAccountReceipt(ctx, tx, accountDeleteIntent{d.account}, d.response, d.key, d.auditID); err != nil {
				return err
			}
			if err := removeAccountFromPortfolios(ctx, tx, d.account, d.key, stamp); err != nil {
				return err
			}
		}
		return nil
	})
}
