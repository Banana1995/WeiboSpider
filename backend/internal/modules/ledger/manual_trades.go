package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"
)

var (
	ErrManualHoldingsRequired = errors.New("configured manual holdings required")
	ErrUnsafeTradeDate        = errors.New("trade predates manual baseline or last appended trade")
)

type ManualTradeInput struct {
	ExpectedVersion string    `json:"expected_version"`
	Kind            Kind      `json:"kind"`
	Date            string    `json:"date"`
	Quantity        *Quantity `json:"quantity,omitempty"`
	Price           *Price    `json:"price,omitempty"`
	Fee             *Money    `json:"fee,omitempty"`
	Amount          *Money    `json:"amount,omitempty"`
	FX              *fxJSON   `json:"fx,omitempty"`
	Note            string    `json:"note"`
	Reason          string    `json:"reason"`
}

type ManualTradeResult struct {
	AccountID    string             `json:"account_id"`
	InstrumentID string             `json:"instrument_id"`
	Version      string             `json:"version"`
	Transaction  HoldingTransaction `json:"transaction"`
}

func (input *ManualTradeInput) UnmarshalJSON(data []byte) error {
	type wire ManualTradeInput
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrOperation
		}
		if decoded.Kind == Dividend && (name == "quantity" || name == "price" || name == "fee") || decoded.Kind != Dividend && name == "amount" {
			return ErrOperation
		}
	}
	*input = ManualTradeInput(decoded)
	return nil
}

func (s *Store) AddManualTrade(ctx context.Context, accountID, instrumentID, key string, input ManualTradeInput) (ManualTradeResult, error) {
	var out ManualTradeResult
	expected, err := strconv.ParseInt(input.ExpectedVersion, 10, 64)
	if !validID(accountID) || !validID(instrumentID) {
		return out, ErrQuery
	}
	if !validID(key) || err != nil || expected < 0 || expected == math.MaxInt64 || strconv.FormatInt(expected, 10) != input.ExpectedVersion || !validDate(input.Date) || !validText(input.Reason) || !utf8.ValidString(input.Note) || len(input.Note) > 4096 {
		return out, ErrOperation
	}
	if input.Kind == Dividend {
		if input.Amount == nil || *input.Amount <= 0 || input.Quantity != nil || input.Price != nil || input.Fee != nil {
			return out, ErrOperation
		}
	} else if input.Kind == Buy || input.Kind == Sell {
		if input.Amount != nil || input.Quantity == nil || *input.Quantity <= 0 || input.Price == nil || *input.Price <= 0 || input.Fee != nil && *input.Fee < 0 {
			return out, ErrOperation
		}
	} else {
		return out, ErrOperation
	}
	raw, err := json.Marshal(struct {
		AccountID, InstrumentID string
		Input                   ManualTradeInput
	}{accountID, instrumentID, input})
	if err != nil {
		return out, err
	}
	// Freeze pointer-valued caller input as well as receipt identity.
	var frozen struct {
		AccountID, InstrumentID string
		Input                   ManualTradeInput
	}
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return out, err
	}
	input = frozen.Input
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		// Reuse the existing current_holdings receipt kind, with a distinct request
		// envelope. Keys remain global across PUT, journal writes and operations.
		receipt, found, err := loadReceipt(ctx, tx, key, "current_holdings", string(raw))
		if err != nil {
			return err
		}
		if found {
			if decodeReceipt(receipt.Response, &out) != nil || out.AccountID != accountID || out.InstrumentID != instrumentID || out.Version != strconv.FormatInt(expected+1, 10) {
				return ErrCorrupt
			}
			stored, err := readManualTrade(ctx, tx, accountID, expected+1)
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(stored)
			var request string
			if string(payload) != receipt.Response || tx.QueryRowContext(ctx, `SELECT json_extract(metadata_json,'$.request') FROM audit_log WHERE id=? AND correlation_id=? AND entity_type='manual_trade' AND after_json=?`, receipt.AuditID, key, receipt.Response).Scan(&request) != nil || request != string(raw) {
				return ErrCorrupt
			}
			out = stored
			_, err = readCurrentHoldings(ctx, tx, accountID)
			return err
		}
		info, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, accountID))
		if err != nil {
			return err
		}
		if info.AccountingMode != "reported" {
			return ErrManualHoldingsRequired
		}
		old, err := readCurrentHoldings(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if old.Snapshot == nil {
			return ErrManualHoldingsRequired
		}
		if old.Snapshot.Version != input.ExpectedVersion {
			return ErrVersion
		}
		i, err := holdingInstrument(ctx, tx, instrumentID)
		if err != nil {
			return err
		}
		date, stamp, err := s.cutoff()
		if err != nil {
			return err
		}
		if input.Date > date || input.Date < info.OpeningDate {
			return ErrOperation
		}
		basis := manualBasis(accountID, *old.Snapshot)
		if input.Date < basis.FloorDate {
			return ErrUnsafeTradeDate
		}
		o := Operation{ID: "mt-" + receiptDigest(key+string(raw)), AccountID: accountID, InstrumentID: instrumentID, Date: input.Date, Kind: input.Kind, Fee: input.Fee, Sequence: expected + 1}
		if input.Quantity != nil {
			o.Quantity = *input.Quantity
		}
		if input.Price != nil {
			o.Price = *input.Price
		}
		if input.Amount != nil {
			o.Amount = *input.Amount
		}
		if input.FX != nil {
			o.FX = &FXSnapshot{Rate: input.FX.Rate, Date: input.FX.Date, Source: input.FX.Source, FetchedAt: input.FX.FetchedAt}
			fetched, err := time.Parse(time.RFC3339Nano, o.FX.FetchedAt)
			now, _ := time.Parse(time.RFC3339Nano, stamp)
			if err != nil || fetched.After(now) {
				return ErrOperation
			}
		}
		r, err := holdingTransaction(o, input.Note, "manual")
		if err != nil {
			return err
		}
		if r.Amount < 0 {
			return ErrOperation
		}
		if input.Kind != Dividend {
			gross, err := TradeAmount(o.Quantity, o.Price)
			if err != nil {
				return err
			}
			if gross <= 0 {
				return ErrOperation
			}
		}
		delta, err := convert(r.Amount, i.Currency, info.Currency, o)
		if err != nil {
			return err
		}
		if input.Kind == Buy {
			if delta <= 0 {
				return ErrOperation
			}
			delta = -delta
		}
		cash, err := AddMoney(old.Snapshot.Cash, delta)
		if err != nil {
			return err
		}
		if cash < 0 {
			return ErrInsufficientCash
		}
		c, exists := basis.Cycles[instrumentID]
		if input.Kind == Buy && c.Quantity == 0 {
			c = HoldingCostBasis{CycleID: o.ID, Known: true}
		}
		// With no cycle selector, dividends belong to the latest cycle, including
		// a closed one. After reopening, old-cycle dividends require operations.
		if input.Kind == Dividend && !exists {
			return ErrOperation
		}
		r.CycleID = c.CycleID
		if err := c.apply(r); err != nil {
			return err
		}
		basis.Cycles[instrumentID] = c
		basis.FloorDate, basis.LastVersion = input.Date, strconv.FormatInt(expected+1, 10)
		next := HoldingsSnapshot{Version: basis.LastVersion, SavedAt: stamp, Cash: cash, Positions: []CurrentPosition{}, Trades: &basis}
		for id, c := range basis.Cycles {
			if c.Quantity > 0 {
				next.Positions = append(next.Positions, CurrentPosition{id, c.Quantity})
			}
		}
		slices.SortFunc(next.Positions, func(a, b CurrentPosition) int {
			if a.InstrumentID < b.InstrumentID {
				return -1
			}
			if a.InstrumentID > b.InstrumentID {
				return 1
			}
			return 0
		})
		if !next.valid() {
			return ErrOperation
		}
		holdingsAudit, err := appendAudit(ctx, tx, key, "trade", "current_holdings", accountID, accountID, expected+1, stamp, "human", old.Snapshot, next, map[string]string{"from_date": date, "reason": input.Reason})
		if err != nil {
			return err
		}
		payload, err := json.Marshal(next)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE current_holdings SET version=?,audit_id=?,payload=? WHERE account_id=? AND version=?`, expected+1, holdingsAudit, string(payload), accountID, expected)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrVersion
		}
		out = ManualTradeResult{AccountID: accountID, InstrumentID: instrumentID, Version: next.Version, Transaction: r}
		auditID, err := appendAudit(ctx, tx, key, "create", "manual_trade", r.ID, accountID, 1, stamp, "human", nil, out, map[string]string{"from_date": input.Date, "reason": input.Reason, "request": string(raw)})
		if err != nil {
			return err
		}
		response, err := json.Marshal(out)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO manual_trades(account_id,instrument_id,version,business_date,audit_id,holdings_audit_id,payload) VALUES(?,?,?,?,?,?,?)`, accountID, instrumentID, expected+1, input.Date, auditID, holdingsAudit, string(response))
		if err != nil {
			return err
		}
		return saveReceipt(ctx, tx, key, "current_holdings", string(raw), string(response), auditID)
	})
	if err != nil {
		return ManualTradeResult{}, err
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

func readManualTrade(ctx context.Context, tx *sql.Tx, accountID string, version int64) (ManualTradeResult, error) {
	var out ManualTradeResult
	var payload, audited, instrument, date, holdingsPayload string
	err := tx.QueryRowContext(ctx, `SELECT t.payload,coalesce(a.after_json,''),t.instrument_id,t.business_date,coalesce(h.after_json,'')
	FROM manual_trades t LEFT JOIN audit_log a ON a.id=t.audit_id AND a.entity_type='manual_trade' AND a.account_id=t.account_id AND a.version=1
	LEFT JOIN audit_log h ON h.id=t.holdings_audit_id AND h.entity_type='current_holdings' AND h.account_id=t.account_id AND h.entity_id=t.account_id AND h.version=t.version AND h.correlation_id=a.correlation_id
	WHERE t.account_id=? AND t.version=?`, accountID, version).Scan(&payload, &audited, &instrument, &date, &holdingsPayload)
	if err != nil {
		return out, ErrCorrupt
	}
	var snapshot HoldingsSnapshot
	if payload != audited || decodeReceipt(payload, &out) != nil || out.AccountID != accountID || out.InstrumentID != instrument || out.Version != strconv.FormatInt(version, 10) || out.Transaction.Date != date || out.Transaction.Source != "manual" || !validID(out.Transaction.ID) || out.Transaction.CycleID == "" || decodeReceipt(holdingsPayload, &snapshot) != nil || !snapshot.valid() || snapshot.Version != out.Version || snapshot.Trades == nil || snapshot.Trades.LastVersion != out.Version {
		return out, ErrCorrupt
	}
	c, ok := snapshot.Trades.Cycles[instrument]
	if !ok || c.CycleID != out.Transaction.CycleID {
		return out, ErrCorrupt
	}
	out.Transaction.sequence = version
	out.Transaction.instrumentID = instrument
	return out, nil
}
