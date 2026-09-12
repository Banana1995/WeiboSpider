package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

type CurrentPosition struct {
	InstrumentID string   `json:"instrument_id"`
	Quantity     Quantity `json:"quantity"`
}

type HoldingsSnapshot struct {
	Version   string            `json:"version"`
	SavedAt   string            `json:"saved_at"`
	Cash      Money             `json:"cash"`
	Positions []CurrentPosition `json:"positions"`
}

type CurrentHoldings struct {
	AccountID string            `json:"account_id"`
	AuditID   string            `json:"audit_id"`
	Snapshot  *HoldingsSnapshot `json:"snapshot"`
}

type CurrentHoldingsInput struct {
	Securities      []instrumentJSON  `json:"securities,omitempty"`
	ExpectedVersion string            `json:"expected_version"`
	Cash            *Money            `json:"cash"`
	Positions       []CurrentPosition `json:"positions"`
}

func (p HoldingsSnapshot) valid() bool {
	v, err := positiveInteger(p.Version)
	_, stampErr := time.Parse(time.RFC3339Nano, p.SavedAt)
	if err != nil || strconv.FormatInt(v, 10) != p.Version || stampErr != nil || p.Cash < 0 || p.Positions == nil || len(p.Positions) > 200 {
		return false
	}
	prior := ""
	for _, row := range p.Positions {
		if !validID(row.InstrumentID) || row.InstrumentID <= prior || row.Quantity <= 0 {
			return false
		}
		prior = row.InstrumentID
	}
	return true
}

func readCurrentHoldings(ctx context.Context, tx *sql.Tx, id string) (CurrentHoldings, error) {
	out := CurrentHoldings{AccountID: id}
	var version int64
	var payload, audited string
	err := tx.QueryRowContext(ctx, `SELECT c.version,CAST(c.audit_id AS TEXT),c.payload,coalesce(a.after_json,'')
        FROM current_holdings c LEFT JOIN audit_log a ON a.id=c.audit_id AND a.entity_type='current_holdings'
        AND a.account_id=c.account_id AND a.entity_id=c.account_id AND a.version=c.version
        WHERE c.account_id=?`, id).Scan(&version, &out.AuditID, &payload, &audited)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Snapshot = &HoldingsSnapshot{}
	if payload != audited || decodeReceipt(payload, out.Snapshot) != nil || !out.Snapshot.valid() || out.Snapshot.Version != strconv.FormatInt(version, 10) {
		return out, ErrCorrupt
	}
	return out, nil
}

func (s *Store) CurrentHoldings(ctx context.Context, id string) (CurrentHoldings, error) {
	var out CurrentHoldings
	if !validID(id) {
		return out, ErrQuery
	}
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		out, err = readCurrentHoldings(ctx, tx, id)
		return err
	})
	return out, err
}

func (s *Store) PutCurrentHoldings(ctx context.Context, id, key string, input CurrentHoldingsInput) (CurrentHoldings, error) {
	var out CurrentHoldings
	expected, err := strconv.ParseInt(input.ExpectedVersion, 10, 64)
	if !validID(id) || !validID(key) || err != nil || expected < 0 || expected == math.MaxInt64 || strconv.FormatInt(expected, 10) != input.ExpectedVersion || input.Cash == nil || *input.Cash < 0 || input.Positions == nil || len(input.Positions) > 200 {
		return out, ErrOperation
	}
	input.Securities = append([]instrumentJSON(nil), input.Securities...)
	// Freeze caller-owned input before any database work. Receipt identity includes row order.
	raw, err := json.Marshal(struct {
		AccountID string
		Input     CurrentHoldingsInput
	}{id, input})
	if err != nil {
		return out, err
	}
	next := HoldingsSnapshot{Version: strconv.FormatInt(expected+1, 10), Cash: *input.Cash, Positions: append([]CurrentPosition{}, input.Positions...)}
	slices.SortFunc(next.Positions, func(a, b CurrentPosition) int {
		if a.InstrumentID < b.InstrumentID {
			return -1
		}
		if a.InstrumentID > b.InstrumentID {
			return 1
		}
		return 0
	})
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		receipt, found, err := loadReceipt(ctx, tx, key, "current_holdings", string(raw))
		if err != nil {
			return err
		}
		if found {
			if decodeReceipt(receipt.Response, &out) != nil || out.AccountID != id || out.Snapshot == nil || !out.Snapshot.valid() || out.AuditID != strconv.FormatInt(receipt.AuditID, 10) {
				return ErrCorrupt
			}
			var payload string
			if err := tx.QueryRowContext(ctx, `SELECT after_json FROM audit_log WHERE id=? AND account_id=? AND entity_id=? AND entity_type='current_holdings' AND version=? AND correlation_id=?`, receipt.AuditID, id, id, expected+1, key).Scan(&payload); err != nil {
				return ErrCorrupt
			}
			var saved HoldingsSnapshot
			if decodeReceipt(payload, &saved) != nil || !reflect.DeepEqual(saved, *out.Snapshot) || saved.Cash != next.Cash || !reflect.DeepEqual(saved.Positions, next.Positions) {
				return ErrCorrupt
			}
			return nil
		}
		_, err = scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		old, err := readCurrentHoldings(ctx, tx, id)
		if err != nil {
			return err
		}
		version := "0"
		if old.Snapshot != nil {
			version = old.Snapshot.Version
		}
		if version != input.ExpectedVersion {
			return ErrVersion
		}
		date, stamp, err := s.cutoff()
		if err != nil {
			return err
		}
		next.SavedAt = stamp
		if !next.valid() {
			return ErrOperation
		}
		identities := map[string]bool{}
		if len(input.Securities) > len(next.Positions) {
			return ErrOperation
		}
		for _, i := range input.Securities {
			if !validID(i.ID) || !slices.ContainsFunc(next.Positions, func(p CurrentPosition) bool { return p.InstrumentID == i.ID }) || !validText(i.Name) || strings.TrimSpace(i.Name) == "" || !validText(i.Market) || !validText(i.Code) || !i.Currency.valid() || i.Market != strings.ToUpper(strings.TrimSpace(i.Market)) || i.Code != strings.ToUpper(strings.TrimSpace(i.Code)) {
				return ErrOperation
			}
			switch i.Market {
			case "SH", "SZ":
				if len(i.Code) != 6 || strings.Trim(i.Code, "0123456789") != "" {
					return ErrOperation
				}
			case "HK":
				if len(i.Code) != 5 || strings.Trim(i.Code, "0123456789") != "" {
					return ErrOperation
				}
			case "US":
			default:
				return ErrOperation
			}
			result, err := tx.ExecContext(ctx, `INSERT INTO instruments(id,market,code,name,currency) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, i.ID, i.Market, i.Code, i.Name, i.Currency)
			if err != nil {
				return constraintError(err)
			}
			if n, err := result.RowsAffected(); err != nil {
				return err
			} else if n == 1 {
				if _, err := appendAudit(ctx, tx, key, "create", "instrument", i.ID, id, 1, stamp, "human", nil, i, nil); err != nil {
					return err
				}
			}
			stored, err := holdingInstrument(ctx, tx, i.ID)
			if err != nil || i != (instrumentJSON{stored.ID, stored.Market, stored.Code, stored.Name, stored.Currency}) {
				return ErrConflict
			}
		}
		for _, p := range next.Positions {
			i, err := holdingInstrument(ctx, tx, p.InstrumentID)
			if err != nil {
				return ErrOperation
			}
			identity := i.Market + "/" + i.Code
			if identities[identity] {
				return ErrConflict
			}
			identities[identity] = true
		}
		action := "create"
		var before any
		if old.Snapshot != nil {
			before = old.Snapshot
			action = "replace"
		}
		auditID, err := appendAudit(ctx, tx, key, action, "current_holdings", id, id, expected+1, stamp, "human", before, next, map[string]string{"from_date": date, "reason": "current holdings replaced"})
		if err != nil {
			return err
		}
		payload, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if old.Snapshot == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO current_holdings(account_id,version,audit_id,payload) VALUES(?,?,?,?)`, id, expected+1, auditID, string(payload))
		} else {
			var result sql.Result
			result, err = tx.ExecContext(ctx, `UPDATE current_holdings SET version=?,audit_id=?,payload=? WHERE account_id=? AND version=?`, expected+1, auditID, string(payload), id, expected)
			if err == nil {
				n, e := result.RowsAffected()
				if e != nil {
					return e
				}
				if n != 1 {
					return ErrVersion
				}
			}
		}
		if err != nil {
			return err
		}
		out = CurrentHoldings{AccountID: id, AuditID: strconv.FormatInt(auditID, 10), Snapshot: &next}
		response, err := json.Marshal(out)
		if err != nil {
			return err
		}
		return saveReceipt(ctx, tx, key, "current_holdings", string(raw), string(response), auditID)
	})
	if err != nil {
		return CurrentHoldings{}, err
	}
	return out, nil
}

func holdingInstrument(ctx context.Context, tx *sql.Tx, id string) (Instrument, error) {
	var i Instrument
	err := tx.QueryRowContext(ctx, `SELECT id,market,code,name,currency FROM instruments WHERE id=?`, id).Scan(&i.ID, &i.Market, &i.Code, &i.Name, &i.Currency)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err == nil && (!i.Currency.valid() || !validID(i.ID)) {
		err = ErrCorrupt
	}
	return i, err
}

func (h Handler) currentHoldings(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD", "PUT") {
		return
	}
	var out CurrentHoldings
	var err error
	if r.Method == http.MethodPut {
		key, ok := writeKey(w, r)
		if !ok {
			return
		}
		input, ok := body[CurrentHoldingsInput](w, r)
		if !ok {
			return
		}
		out, err = h.Store.PutCurrentHoldings(r.Context(), r.PathValue("id"), key, input)
	} else {
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			h.fail(w, r, ErrQuery)
			return
		}
		out, err = h.Store.CurrentHoldings(r.Context(), r.PathValue("id"))
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, out)
}
