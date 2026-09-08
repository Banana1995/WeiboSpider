package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

// Version 1 freezes request-time marks and identities, not closing prices.
// Never join these identities or recompute amounts against the current ledger.
type valuationSnapshot struct {
	SchemaVersion int              `json:"schema_version"`
	AccountName   string           `json:"account_name"`
	Valuation     Valuation        `json:"valuation"`
	Instruments   []instrumentJSON `json:"instruments"`
}

type ValuationHistory struct {
	ID      string `json:"id"`
	SavedAt string `json:"saved_at"`
	valuationSnapshot
}

type ValuationSummary struct {
	ID             string   `json:"id"`
	AccountID      string   `json:"account_id"`
	Currency       Currency `json:"currency"`
	AsOf           string   `json:"as_of"`
	LedgerAt       string   `json:"ledger_at"`
	CalculatedAt   string   `json:"calculated_at"`
	SavedAt        string   `json:"saved_at"`
	LedgerRevision string   `json:"ledger_revision"`
	Cash           Money    `json:"cash"`
	PositionsValue Money    `json:"positions_value"`
	TotalAssets    Money    `json:"total_assets"`
}

func (s valuationSnapshot) validate() error {
	v := s.Valuation
	if v.Source == "manual_snapshot" {
		c := v.CurrentHoldings
		if c == nil || c.AccountID != v.AccountID || c.Snapshot == nil || !c.Snapshot.valid() || c.Snapshot.Cash != v.Cash || len(c.Snapshot.Positions) != len(v.Items) {
			return ErrCorrupt
		}
		if _, err := positiveInteger(c.AuditID); err != nil {
			return ErrCorrupt
		}
		stamp, _ := time.Parse(time.RFC3339Nano, c.Snapshot.SavedAt)
		ledger, err := time.Parse(time.RFC3339Nano, v.LedgerAt)
		if err != nil || stamp.After(ledger) {
			return ErrCorrupt
		}
		basis, _ := json.Marshal(c)
		if receiptDigest(string(basis)) != v.LedgerRevision {
			return ErrCorrupt
		}
		for n, p := range c.Snapshot.Positions {
			if p.InstrumentID != v.Items[n].InstrumentID || p.Quantity != v.Items[n].Quantity {
				return ErrCorrupt
			}
		}
	} else if v.Source != "transaction_replay" || v.CurrentHoldings != nil {
		return ErrCorrupt
	}
	revision, err := hex.DecodeString(v.LedgerRevision)
	if err != nil || len(revision) != 32 || hex.EncodeToString(revision) != v.LedgerRevision || s.SchemaVersion != 1 || !validText(s.AccountName) || !validID(v.AccountID) || !v.Currency.valid() || !validDate(v.AsOf) || !v.Complete || v.HistoryID != "" || v.Cash < 0 || v.PositionsValue == nil || v.TotalAssets == nil || v.Items == nil || s.Instruments == nil || len(v.Items) != len(s.Instruments) {
		return ErrCorrupt
	}
	ledgerAt, e1 := time.Parse(time.RFC3339Nano, v.LedgerAt)
	calculatedAt, e2 := time.Parse(time.RFC3339Nano, v.CalculatedAt)
	if e1 != nil || e2 != nil || ledgerAt.In(fxBeijing).Format(time.DateOnly) != v.AsOf {
		return ErrCorrupt
	}
	var sum Money
	previous := ""
	for n, item := range v.Items {
		i := s.Instruments[n]
		if !validID(i.ID) || i.ID <= previous || i.ID != item.InstrumentID || !validText(i.Market) || !validText(i.Code) || !validText(i.Name) || !i.Currency.valid() || item.Quantity < 0 || item.MarketValue == nil || item.ErrorCode != "" {
			return ErrCorrupt
		}
		previous = i.ID
		if item.Quantity == 0 {
			if item.Status != "closed" || *item.MarketValue != 0 || item.Quote != nil || item.FX != nil {
				return ErrCorrupt
			}
			continue
		}
		q := item.Quote
		symbol, code := quoteSymbol(Instrument{ID: i.ID, Market: i.Market, Code: i.Code, Name: i.Name, Currency: i.Currency})
		if q == nil || code != "" || q.Symbol != symbol || q.Price <= 0 || q.Currency != i.Currency || q.Source != "Tencent" || !validDate(q.Date) || q.Date > v.AsOf {
			return ErrCorrupt
		}
		stamp, e1 := time.Parse(time.RFC3339Nano, q.QuotedAt)
		_, e2 := time.Parse(time.RFC3339Nano, q.FetchedAt)
		if e1 != nil || e2 != nil || stamp.After(calculatedAt) || stamp.In(fxBeijing).Format(time.DateOnly) != q.Date {
			return ErrCorrupt
		}
		amount, err := TradeAmount(item.Quantity, q.Price)
		if err != nil {
			return ErrCorrupt
		}
		if i.Currency == v.Currency {
			if item.FX != nil {
				return ErrCorrupt
			}
		} else {
			fx := item.FX
			if fx == nil || fx.Base != i.Currency || fx.Quote != v.Currency || fx.Mode != "latest" || fx.Rate <= 0 || !validText(fx.Source) || !validDate(fx.Date) || fx.Date > v.AsOf || (fx.RequestedDate != "" && !validDate(fx.RequestedDate)) {
				return ErrCorrupt
			}
			stamp, e1 := time.Parse(time.RFC3339Nano, fx.QuotedAt)
			_, e2 := time.Parse(time.RFC3339Nano, fx.FetchedAt)
			if e1 != nil || e2 != nil || stamp.After(calculatedAt) || fx.Date > stamp.In(fxBeijing).Format(time.DateOnly) {
				return ErrCorrupt
			}
			amount, err = ConvertMoney(amount, fx.Rate)
			if err != nil {
				return ErrCorrupt
			}
		}
		status := "current"
		if q.Date != v.AsOf || item.FX != nil && item.FX.Date != v.AsOf {
			status = "prior_date"
		}
		if item.Status != status || *item.MarketValue != amount {
			return ErrCorrupt
		}
		sum, err = AddMoney(sum, amount)
		if err != nil {
			return ErrCorrupt
		}
	}
	total, err := AddMoney(v.Cash, sum)
	if err != nil || sum != v.KnownPositionsValue || sum != *v.PositionsValue || total != *v.TotalAssets {
		return ErrCorrupt
	}
	return nil
}

// RecordValuation appends every observation, even identical ones. The caller must
// supply the basis/identities captured before network I/O, using its live context.
func (s *Store) RecordValuation(ctx context.Context, v Valuation, instruments []Instrument) (string, error) {
	var id int64
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		id, _, err = s.recordValuation(ctx, tx, v, instruments)
		return err
	})
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

// The caller owns the transaction so weekly completion can share this commit.
func (s *Store) recordValuation(ctx context.Context, tx *sql.Tx, v Valuation, instruments []Instrument) (int64, int64, error) {
	if v.Source != "manual_snapshot" {
		if err := requireHoldings(ctx, tx, v.AccountID); err != nil {
			return 0, 0, err
		}
	}
	snapshot := valuationSnapshot{SchemaVersion: 1, AccountName: v.accountName, Valuation: v, Instruments: make([]instrumentJSON, 0, len(instruments))}
	for _, i := range instruments {
		snapshot.Instruments = append(snapshot.Instruments, instrumentJSON{i.ID, i.Market, i.Code, i.Name, i.Currency})
	}
	if err := snapshot.validate(); err != nil {
		return 0, 0, err
	}
	if v.Source == "manual_snapshot" {
		current, err := readCurrentHoldings(ctx, tx, v.AccountID)
		if err != nil {
			return 0, 0, err
		}
		if !reflect.DeepEqual(current, *v.CurrentHoldings) {
			return 0, 0, errWeeklyBasis
		}
		revision, _ := positiveInteger(current.AuditID)
		v.changeRevision = &revision
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return 0, 0, err
	}
	_, savedAt, err := s.cutoff()
	if err != nil {
		return 0, 0, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(sequence),0)+1 FROM account_records`).Scan(&id); err != nil {
		return 0, 0, err
	}
	metadata := map[string]any{"currency": v.Currency, "as_of": v.AsOf, "ledger_at": v.LedgerAt, "calculated_at": v.CalculatedAt, "ledger_revision": v.LedgerRevision, "cash_minor": strconv.FormatInt(int64(v.Cash), 10), "positions_value_minor": strconv.FormatInt(int64(*v.PositionsValue), 10), "total_assets_minor": strconv.FormatInt(int64(*v.TotalAssets), 10), "schema_version": 1, "change_revision": v.changeRevision}
	auditID, err := appendAudit(ctx, tx, v.correlation, "observe", "valuation", strconv.FormatInt(id, 10), v.AccountID, 1, savedAt, "system", nil, json.RawMessage(payload), metadata)
	if err != nil {
		return 0, 0, err
	}
	origin := "currentrefresh"
	if v.weekly {
		origin = "weekly"
	}
	next := AccountRecord{ID: "valuation-" + strconv.FormatInt(id, 10), AccountID: v.AccountID, AccountEntry: AccountEntry{Kind: "asset", Date: v.AsOf, TotalAssets: v.TotalAssets}, Sequence: strconv.FormatInt(id, 10), Origin: origin, QuoteAuditID: strconv.FormatInt(auditID, 10), Version: "1", CreatedAt: savedAt, UpdatedAt: savedAt}
	if _, err := putAccountRecord(ctx, tx, &next, nil, v.correlation, "current valuation", "system"); err != nil {
		return 0, 0, err
	}
	return id, auditID, nil
}

const historySelect = `SELECT r.sequence,r.account_id,
	json_extract(a.metadata_json,'$.currency'),json_extract(a.metadata_json,'$.as_of'),
	json_extract(a.metadata_json,'$.ledger_at'),json_extract(a.metadata_json,'$.calculated_at'),a.recorded_at,
	json_extract(a.metadata_json,'$.ledger_revision'),CAST(json_extract(a.metadata_json,'$.cash_minor') AS INTEGER),
	CAST(json_extract(a.metadata_json,'$.positions_value_minor') AS INTEGER),
	CAST(json_extract(a.metadata_json,'$.total_assets_minor') AS INTEGER),
	json_extract(a.metadata_json,'$.schema_version'),a.after_json,
	r.business_date,r.origin,r.quote_audit_id,a.id,a.entity_id,r.voided,
	EXISTS(SELECT 1 FROM audit_log current WHERE current.entity_type='account_record'
	 AND current.account_id=r.account_id AND current.entity_id=r.id AND current.version=r.version
	 AND current.after_json=r.payload),
	r.id,r.payload,r.created_at,r.kind,r.flow_minor,r.total_assets_minor,r.note,r.version,
	r.updated_at,r.operation_id,r.manual_assertion,
	(json_extract(a.after_json,'$.valuation.source')='transaction_replay' OR EXISTS(
	 SELECT 1 FROM audit_log source WHERE source.entity_type='current_holdings'
	 AND source.account_id=r.account_id AND source.entity_id=r.account_id
	 AND CAST(source.id AS TEXT)=json_extract(a.after_json,'$.valuation.current_holdings.audit_id')
	 AND CAST(source.version AS TEXT)=json_extract(a.after_json,'$.valuation.current_holdings.snapshot.version')
	 AND source.recorded_at=json_extract(a.after_json,'$.valuation.current_holdings.snapshot.saved_at')
	 AND json(source.after_json)=json(json_extract(a.after_json,'$.valuation.current_holdings.snapshot'))))
	FROM account_records r
	LEFT JOIN audit_log a ON a.id=r.quote_audit_id AND a.entity_type='valuation'
	 AND a.account_id=r.account_id AND a.entity_id=CAST(r.sequence AS TEXT)`

func scanValuationHistory(row interface{ Scan(...any) error }) (ValuationHistory, ValuationSummary, error) {
	var h ValuationHistory
	var m ValuationSummary
	var id int64
	var version int
	var payload string
	var recordDate, origin, auditEntity, recordID, recordPayload, recordCreated, recordKind, recordNote, recordUpdated string
	var quoteAuditID, auditID int64
	var voided, currentAudited bool
	var recordFlow, recordAssets, recordVersion sql.NullInt64
	var operationID sql.NullString
	var manualAssertion bool
	var sourceAudited bool
	err := row.Scan(&id, &m.AccountID, &m.Currency, &m.AsOf, &m.LedgerAt, &m.CalculatedAt, &m.SavedAt, &m.LedgerRevision, &m.Cash, &m.PositionsValue, &m.TotalAssets, &version, &payload,
		&recordDate, &origin, &quoteAuditID, &auditID, &auditEntity, &voided, &currentAudited,
		&recordID, &recordPayload, &recordCreated, &recordKind, &recordFlow, &recordAssets, &recordNote, &recordVersion,
		&recordUpdated, &operationID, &manualAssertion, &sourceAudited)
	if errors.Is(err, sql.ErrNoRows) {
		return h, m, ErrNotFound
	}
	if err != nil {
		return h, m, ErrCorrupt
	}
	var record AccountRecord
	if decodeReceipt(recordPayload, &record) != nil || record.ID != recordID || record.AccountID != m.AccountID || record.Date != recordDate ||
		record.Sequence != strconv.FormatInt(id, 10) || record.Kind != recordKind || record.Note != recordNote || record.Origin != origin ||
		record.Voided != voided || record.Version != strconv.FormatInt(recordVersion.Int64, 10) || record.CreatedAt != recordCreated ||
		record.UpdatedAt != recordUpdated || record.OperationID != operationID.String || record.QuoteAuditID != strconv.FormatInt(quoteAuditID, 10) ||
		record.ManualAssertion != manualAssertion || (record.Flow != nil) != recordFlow.Valid || (record.TotalAssets != nil) != recordAssets.Valid ||
		record.Flow != nil && int64(*record.Flow) != recordFlow.Int64 || record.TotalAssets != nil && int64(*record.TotalAssets) != recordAssets.Int64 {
		return h, m, ErrCorrupt
	}
	if json.Unmarshal([]byte(payload), &h.valuationSnapshot) != nil {
		return h, m, ErrCorrupt
	}
	encoded, err := json.Marshal(h.valuationSnapshot)
	var compact bytes.Buffer
	if err != nil || json.Compact(&compact, []byte(payload)) != nil || !bytes.Equal(encoded, compact.Bytes()) || h.validate() != nil {
		return h, m, ErrCorrupt
	}
	v := h.Valuation
	_, err = time.Parse(time.RFC3339Nano, m.SavedAt)
	if err != nil || id <= 0 || version != h.SchemaVersion || m.AccountID != v.AccountID || m.Currency != v.Currency || m.AsOf != v.AsOf || m.LedgerAt != v.LedgerAt || m.CalculatedAt != v.CalculatedAt || m.LedgerRevision != v.LedgerRevision || m.Cash != v.Cash || m.PositionsValue != *v.PositionsValue || m.TotalAssets != *v.TotalAssets ||
		origin != "currentrefresh" && origin != "weekly" || quoteAuditID != auditID || auditEntity != strconv.FormatInt(id, 10) || !currentAudited || !sourceAudited {
		return h, m, ErrCorrupt
	}
	h.ID, h.SavedAt = strconv.FormatInt(id, 10), m.SavedAt
	m.ID = h.ID
	return h, m, nil
}

func (s *Store) ValuationHistory(ctx context.Context, accountID string, id int64) (ValuationHistory, error) {
	if !validID(accountID) || id <= 0 {
		return ValuationHistory{}, ErrQuery
	}
	h, _, err := scanValuationHistory(s.db.QueryRowContext(ctx, historySelect+` WHERE r.account_id=? AND r.sequence=? AND r.origin IN ('currentrefresh','weekly')`, accountID, id))
	if err := errors.Join(err, ctx.Err()); err != nil {
		return ValuationHistory{}, err
	}
	return h, nil
}

func (s *Store) ListValuations(ctx context.Context, accountID, from, to string, cursor int64, limit int) (listJSON[ValuationSummary], error) {
	result := listJSON[ValuationSummary]{Items: make([]ValuationSummary, 0)}
	if !validID(accountID) || from != "" && !validDate(from) || to != "" && !validDate(to) || from != "" && to != "" && from > to || cursor < 0 || limit < 1 || limit > 100 {
		return result, ErrQuery
	}
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM accounts WHERE id=?`, accountID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		statement, args := historySelect+` WHERE r.account_id=? AND r.origin IN ('currentrefresh','weekly')`, []any{accountID}
		if from != "" {
			statement += ` AND r.business_date>=?`
			args = append(args, from)
		}
		if to != "" {
			statement += ` AND r.business_date<=?`
			args = append(args, to)
		}
		if cursor != 0 {
			statement += ` AND r.sequence<?`
			args = append(args, cursor)
		}
		statement += ` ORDER BY r.sequence DESC LIMIT ?`
		args = append(args, limit+1)
		rows, err := tx.QueryContext(ctx, statement, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			_, summary, err := scanValuationHistory(rows)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, summary)
		}
		return errors.Join(rows.Err(), rows.Close(), ctx.Err())
	})
	if err != nil {
		return listJSON[ValuationSummary]{}, err
	}
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		result.NextCursor = result.Items[limit-1].ID
	}
	return result, nil
}

func (h Handler) valuations(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	values, limit, err := page(r, "from", "to")
	var cursor int64
	if err == nil && values.Get("cursor") != "" {
		cursor, err = positiveInteger(values.Get("cursor"))
	}
	if err != nil {
		h.fail(w, r, ErrQuery)
		return
	}
	result, err := h.Store.ListValuations(r.Context(), r.PathValue("id"), values.Get("from"), values.Get("to"), cursor, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, result)
}

func (h Handler) valuationHistory(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET", "HEAD") {
		return
	}
	_, err := query(r)
	id, idErr := positiveInteger(r.PathValue("historyID"))
	if err != nil || idErr != nil {
		h.fail(w, r, ErrQuery)
		return
	}
	result, err := h.Store.ValuationHistory(r.Context(), r.PathValue("id"), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, 200, result)
}
