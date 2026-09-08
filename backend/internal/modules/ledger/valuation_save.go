package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

var errIncompleteValuation = errors.New("complete valuation required for save")

func validateValuationReceipt(ctx context.Context, tx *sql.Tx, intent valuationIntent, payload, key string, auditID int64) error {
	var v Valuation
	if decodeReceipt(payload, &v) != nil || v.AccountID != intent.AccountID {
		return ErrCorrupt
	}
	id, err := positiveInteger(v.HistoryID)
	if err != nil {
		return ErrCorrupt
	}
	h, _, err := scanValuationHistory(tx.QueryRowContext(ctx, historySelect+` WHERE r.account_id=? AND r.sequence=? AND r.origin='currentrefresh'`, intent.AccountID, id))
	if err != nil {
		return ErrCorrupt
	}
	want := h.Valuation
	want.HistoryID = v.HistoryID
	if !reflect.DeepEqual(want, v) {
		return ErrCorrupt
	}
	var recordID, origin string
	var quoteAuditID int64
	if err := tx.QueryRowContext(ctx, `SELECT id,origin,quote_audit_id FROM account_records
		WHERE sequence=? AND account_id=?`, id, v.AccountID).Scan(&recordID, &origin, &quoteAuditID); err != nil {
		return ErrCorrupt
	}
	if recordID != "valuation-"+v.HistoryID || origin != "currentrefresh" || quoteAuditID != auditID {
		return ErrCorrupt
	}
	var quoted, correlation, frozen string
	if err := tx.QueryRowContext(ctx, `SELECT entity_id,correlation_id,after_json FROM audit_log
		WHERE id=? AND account_id=? AND entity_type='valuation'`, auditID, v.AccountID).Scan(&quoted, &correlation, &frozen); err != nil || quoted != v.HistoryID || correlation != key || frozen == "" {
		return ErrCorrupt
	}
	return nil
}

func valuationReceipt(ctx context.Context, tx *sql.Tx, key string, intent valuationIntent) (json.RawMessage, bool, error) {
	expected, _ := json.Marshal(intent)
	receipt, found, err := loadReceipt(ctx, tx, key, "valuation", string(expected))
	if err != nil || !found {
		return nil, found, err
	}
	if err := validateValuationReceipt(ctx, tx, intent, receipt.Response, key, receipt.AuditID); err != nil {
		return nil, true, err
	}
	return json.RawMessage(receipt.Response), true, nil
}

func (h Handler) saveValuation(w http.ResponseWriter, r *http.Request) {
	key, ok := writeKey(w, r)
	if !ok {
		return
	}
	if _, ok := body[struct{}](w, r); !ok {
		return
	}
	id := r.PathValue("id")
	if !validID(id) {
		h.fail(w, r, ErrQuery)
		return
	}
	intent := valuationIntent{"save-valuation", id}
	ctx, cancel := context.WithTimeout(r.Context(), valuationTimeout)
	defer cancel()
	var response json.RawMessage
	var found bool
	err := h.Store.db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		response, found, err = valuationReceipt(ctx, tx, key, intent)
		return err
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if found {
		httpapi.Write(w, 200, response)
		return
	}
	// Never hold a database transaction during provider I/O. A committed retry
	// returns above without fetching; concurrent first requests recheck below.
	v, instruments, err := h.Store.valuationInputs(ctx, id)
	if err == nil {
		err = h.valuePositions(ctx, &v, instruments)
	}
	if err == nil && !v.Complete {
		err = errIncompleteValuation
	}
	if err == nil {
		err = h.Store.db.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			response, found, err = valuationReceipt(ctx, tx, key, intent)
			if err != nil || found {
				return err
			}
			v.correlation = key
			historyID, auditID, err := h.Store.recordValuation(ctx, tx, v, instruments)
			if err != nil {
				return err
			}
			v.HistoryID = strconv.FormatInt(historyID, 10)
			response, err = json.Marshal(v)
			if err != nil {
				return err
			}
			request, _ := json.Marshal(intent)
			return saveReceipt(ctx, tx, key, "valuation", string(request), string(response), auditID)
		})
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, response)
}
