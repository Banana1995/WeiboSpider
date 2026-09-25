package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

const maxStockEntries = 10000

type StockInput struct {
	InstrumentID string   `json:"instrument_id"`
	Kind         string   `json:"kind"`
	Date         string   `json:"date"`
	Quantity     Quantity `json:"quantity"`
	Price        Price    `json:"price"`
	Fee          Money    `json:"fee"`
	Amount       Money    `json:"amount"`
	FX           Rate     `json:"fx"`
	Note         string   `json:"note"`
}
type StockCommand struct {
	Action          string          `json:"action"`
	ExpectedVersion string          `json:"expected_version"`
	ID              string          `json:"id,omitempty"`
	Entry           *StockInput     `json:"entry,omitempty"`
	Security        *instrumentJSON `json:"security,omitempty"`
	Cash            *Money          `json:"cash,omitempty"`
	ReplaceOpening  bool            `json:"replace_opening,omitempty"`
}
type StockWriteResult struct {
	AccountID string `json:"account_id"`
	Version   string `json:"version"`
	ID        string `json:"id"`
	Action    string `json:"action"`
}
type stockState struct {
	account     AccountInfo
	current     CurrentHoldings
	journal     StockJournal
	journalRaw  string
	entries     []StockEntry
	instruments map[string]Instrument
}

func (st stockState) version() string {
	if st.current.Snapshot == nil {
		return "0"
	}
	return st.current.Snapshot.Version
}

func readStockState(ctx context.Context, tx *sql.Tx, id string) (stockState, error) {
	st := stockState{entries: []StockEntry{}, instruments: map[string]Instrument{}}
	var err error
	st.account, err = scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, id))
	if err != nil {
		return st, err
	}
	st.current, err = readCurrentHoldings(ctx, tx, id)
	if err != nil {
		return st, err
	}
	var audited string
	var version int64
	err = tx.QueryRowContext(ctx, `SELECT j.version,j.payload,coalesce(a.after_json,'') FROM stock_journals j LEFT JOIN audit_log a ON a.id=j.audit_id AND a.entity_type='stock_journal' AND a.entity_id=j.account_id AND a.account_id=j.account_id AND a.version=j.version WHERE j.account_id=?`, id).Scan(&version, &st.journalRaw, &audited)
	if errors.Is(err, sql.ErrNoRows) {
		st.journal = StockJournal{Version: "0", Openings: []CurrentPosition{}}
		if s := st.current.Snapshot; s != nil {
			st.journal.Openings = slices.Clone(s.Positions)
			if s.Cash > 0 {
				saved, err := time.Parse(time.RFC3339Nano, s.SavedAt)
				if err != nil {
					return st, ErrCorrupt
				}
				st.journal.Cash = &CashAnchor{Amount: s.Cash, Date: saved.In(fxBeijing).Format(time.DateOnly)}
			}
		}
	} else if err != nil {
		return st, err
	} else if st.journalRaw != audited || decodeReceipt(st.journalRaw, &st.journal) != nil || st.journal.Version != strconv.FormatInt(version, 10) || st.journal.Openings == nil {
		return st, ErrCorrupt
	}
	if a := st.journal.Cash; a != nil && (a.Amount < 0 || !validDate(a.Date) || a.Sequence < 0) {
		return st, ErrCorrupt
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.id,e.sequence,e.version,e.payload,coalesce(a.after_json,'') FROM stock_entries e LEFT JOIN audit_log a ON a.id=e.audit_id AND a.entity_type='stock_entry' AND a.account_id=e.account_id AND a.entity_id=e.id AND a.version=e.version WHERE e.account_id=? ORDER BY e.sequence LIMIT ?`, id, maxStockEntries+1)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var e StockEntry
		var eid, payload, proof string
		var seq, v int64
		if err = rows.Scan(&eid, &seq, &v, &payload, &proof); err != nil {
			break
		}
		if payload != proof || decodeReceipt(payload, &e) != nil || e.ID != eid || e.Sequence != seq || e.Version != strconv.FormatInt(v, 10) || !validStockEntry(e) {
			err = ErrCorrupt
			break
		}
		st.entries = append(st.entries, e)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return st, err
	}
	if rowErr != nil {
		return st, rowErr
	}
	if len(st.entries) > maxStockEntries {
		return st, ErrOperation
	}
	ids := map[string]bool{}
	for _, p := range st.journal.Openings {
		if p.Quantity <= 0 || !validID(p.InstrumentID) || ids[p.InstrumentID] {
			return st, ErrCorrupt
		}
		ids[p.InstrumentID] = true
	}
	for _, e := range st.entries {
		ids[e.InstrumentID] = true
	}
	for id := range ids {
		i, err := holdingInstrument(ctx, tx, id)
		if err != nil {
			return st, err
		}
		st.instruments[id] = i
	}
	return st, nil
}

func validStockEntry(e StockEntry) bool {
	if !validID(e.ID) || !validID(e.InstrumentID) || !validDate(e.Date) || e.Sequence <= 0 || e.Fee < 0 || e.Amount < 0 || e.FX < 0 || len(e.Note) > 2000 {
		return false
	}
	if _, err := positiveInteger(e.Version); err != nil {
		return false
	}
	if e.Event != nil && (e.Kind != "dividend" || !e.Event.valid() || e.Date != e.Event.ExDate || !validID(e.Cycle)) {
		return false
	}
	if e.Kind == "dividend" {
		return e.Quantity >= 0 && e.Price >= 0 && e.Fee == 0
	}
	if e.Kind != "buy" && e.Kind != "sell" || e.Quantity <= 0 || e.Price <= 0 {
		return false
	}
	amount, err := TradeAmount(e.Quantity, e.Price)
	if err != nil {
		return false
	}
	if e.Kind == "buy" {
		amount, err = AddMoney(amount, e.Fee)
	} else {
		amount, err = SubMoney(amount, e.Fee)
	}
	return err == nil && amount >= 0 && amount == e.Amount
}

func putStockEntry(ctx context.Context, tx *sql.Tx, account, key, source, stamp string, next *StockEntry, previous *StockEntry) error {
	var before any
	version := int64(1)
	if previous != nil {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT payload FROM stock_entries WHERE account_id=? AND id=?`, account, next.ID).Scan(&raw); err != nil {
			return err
		}
		before = json.RawMessage(raw)
		v, err := positiveInteger(previous.Version)
		if err != nil || v == 9223372036854775807 {
			return ErrPrecision
		}
		version = v + 1
		next.Sequence = previous.Sequence
		next.CreatedAt = previous.CreatedAt
	} else {
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(sequence),0)+1 FROM stock_entries`).Scan(&next.Sequence); err != nil {
			return err
		}
		next.CreatedAt = stamp
	}
	next.Version = strconv.FormatInt(version, 10)
	next.UpdatedAt = stamp
	if !validStockEntry(*next) {
		return ErrOperation
	}
	action := "create"
	if previous != nil {
		action = "replace"
	}
	if next.Voided {
		action = "void"
	}
	audit, err := appendAudit(ctx, tx, key, action, "stock_entry", next.ID, account, version, stamp, source, before, next, nil)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if previous == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO stock_entries(sequence,account_id,id,version,audit_id,payload) VALUES(?,?,?,?,?,?)`, next.Sequence, account, next.ID, version, audit, string(raw))
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE stock_entries SET version=?,audit_id=?,payload=? WHERE account_id=? AND id=?`, version, audit, string(raw), account, next.ID)
	}
	return constraintError(err)
}

func saveStockProjection(ctx context.Context, tx *sql.Tx, st *stockState, key, source, today, stamp string) (int64, error) {
	positions, err := replayStocks(st.journal, st.entries, today)
	if err != nil {
		return 0, err
	}
	if len(positions) > 200 {
		return 0, ErrOperation
	}
	cash, err := stockCash(st.journal, st.entries, today)
	if err != nil {
		return 0, err
	}
	v, err := stockVersion(st.version())
	if err != nil {
		return 0, err
	}
	next := HoldingsSnapshot{Version: strconv.FormatInt(v+1, 10), SavedAt: stamp, Cash: cash, Positions: []CurrentPosition{}}
	for id, p := range positions {
		if p.quantity > 0 {
			next.Positions = append(next.Positions, CurrentPosition{id, p.quantity})
		}
	}
	slices.SortFunc(next.Positions, func(a, b CurrentPosition) int { return strings.Compare(a.InstrumentID, b.InstrumentID) })
	if !next.valid() {
		return 0, ErrCorrupt
	}
	var before any
	action := "create"
	if st.current.Snapshot != nil {
		before = st.current.Snapshot
		action = "replace"
	}
	audit, err := appendAudit(ctx, tx, key, action, "current_holdings", st.account.ID, st.account.ID, v+1, stamp, source, before, next, nil)
	if err != nil {
		return 0, err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return 0, err
	}
	if st.current.Snapshot == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO current_holdings(account_id,version,audit_id,payload) VALUES(?,?,?,?)`, st.account.ID, v+1, audit, string(raw))
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE current_holdings SET version=?,audit_id=?,payload=? WHERE account_id=?`, v+1, audit, string(raw), st.account.ID)
	}
	if err != nil {
		return 0, err
	}
	st.current.Snapshot = &next
	jv, err := stockVersion(st.journal.Version)
	if err != nil {
		return 0, err
	}
	st.journal.Version = strconv.FormatInt(jv+1, 10)
	before = nil
	action = "create"
	if st.journalRaw != "" {
		before = json.RawMessage(st.journalRaw)
		action = "replace"
	}
	ja, err := appendAudit(ctx, tx, key, action, "stock_journal", st.account.ID, st.account.ID, jv+1, stamp, source, before, st.journal, nil)
	if err != nil {
		return 0, err
	}
	jraw, err := json.Marshal(st.journal)
	if err != nil {
		return 0, err
	}
	if st.journalRaw == "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO stock_journals(account_id,version,audit_id,payload) VALUES(?,?,?,?)`, st.account.ID, jv+1, ja, string(jraw))
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE stock_journals SET version=?,audit_id=?,payload=? WHERE account_id=?`, jv+1, ja, string(jraw), st.account.ID)
	}
	return audit, err
}

func (s *Store) WriteStock(ctx context.Context, account, key string, c StockCommand) (json.RawMessage, error) {
	if !validID(account) || !validID(key) {
		return nil, ErrOperation
	}
	if _, err := stockVersion(c.ExpectedVersion); err != nil {
		return nil, err
	}
	intent, err := json.Marshal(struct {
		Account string
		Command StockCommand
	}{account, c})
	if err != nil {
		return nil, err
	}
	// Freeze the caller's draft before acquiring the transaction.
	var frozen struct {
		Account string
		Command StockCommand
	}
	if json.Unmarshal(intent, &frozen) != nil {
		return nil, ErrOperation
	}
	c = frozen.Command
	var result json.RawMessage
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		receipt, found, err := loadReceipt(ctx, tx, key, "stock_command", string(intent))
		if err != nil {
			return err
		}
		if found {
			var saved StockWriteResult
			var proof string
			if decodeReceipt(receipt.Response, &saved) != nil || saved.AccountID != account || saved.ID != c.ID || saved.Action != c.Action {
				return ErrCorrupt
			}
			if err := tx.QueryRowContext(ctx, `SELECT after_json FROM audit_log WHERE id=? AND entity_type='stock_command' AND account_id=? AND correlation_id=?`, receipt.AuditID, account, key).Scan(&proof); err != nil || proof != receipt.Response {
				return ErrCorrupt
			}
			result = json.RawMessage(receipt.Response)
			return nil
		}
		st, err := readStockState(ctx, tx, account)
		if err != nil {
			return err
		}
		if st.version() != c.ExpectedVersion {
			return ErrVersion
		}
		today, stamp, err := s.cutoff()
		if err != nil {
			return err
		}
		if c.Security != nil {
			if c.Action != "create" || c.Entry == nil || c.Entry.InstrumentID != c.Security.ID {
				return ErrOperation
			}
			i := Instrument{c.Security.ID, c.Security.Market, c.Security.Code, c.Security.Name, c.Security.Currency}
			if _, code := quoteSymbol(i); code != "" || !validID(i.ID) || !validText(i.Name) {
				return ErrOperation
			}
			for _, existing := range st.instruments {
				if existing.Market == i.Market && existing.Code == i.Code && existing.ID != i.ID {
					return ErrConflict
				}
			}
			stored, e := holdingInstrument(ctx, tx, i.ID)
			if errors.Is(e, ErrNotFound) {
				if _, err := tx.ExecContext(ctx, `INSERT INTO instruments(id,market,code,name,currency) VALUES(?,?,?,?,?)`, i.ID, i.Market, i.Code, i.Name, i.Currency); err != nil {
					return err
				}
				if _, err := appendAudit(ctx, tx, key, "create", "instrument", i.ID, account, 1, stamp, "human", nil, c.Security, nil); err != nil {
					return err
				}
			} else if e != nil {
				return e
			} else if stored != i {
				return ErrConflict
			}
			st.instruments[i.ID] = i
		}
		switch c.Action {
		case "cash":
			if c.Cash == nil || *c.Cash < 0 || c.Entry != nil || c.ID != "" || c.ReplaceOpening {
				return ErrOperation
			}
			var sequence int64
			for _, e := range st.entries {
				sequence = max(sequence, e.Sequence)
			}
			st.journal.Cash = &CashAnchor{Amount: *c.Cash, Date: today, Sequence: sequence}
		case "clear_opening":
			if !validID(c.ID) || c.Entry != nil || c.Cash != nil {
				return ErrOperation
			}
			oldLen := len(st.journal.Openings)
			st.journal.Openings = slices.DeleteFunc(st.journal.Openings, func(p CurrentPosition) bool { return p.InstrumentID == c.ID })
			if len(st.journal.Openings) == oldLen {
				return ErrNotFound
			}
		case "create", "replace", "void":
			if !validID(c.ID) || c.Cash != nil {
				return ErrOperation
			}
			index := slices.IndexFunc(st.entries, func(e StockEntry) bool { return e.ID == c.ID })
			var previous *StockEntry
			next := StockEntry{ID: c.ID}
			if c.Action == "create" {
				if index >= 0 {
					return ErrConflict
				}
				if len(st.entries) >= maxStockEntries {
					return ErrOperation
				}
			} else {
				if index < 0 {
					return ErrNotFound
				}
				old := st.entries[index]
				previous = &old
				next = old
				if old.Voided {
					return ErrVoided
				}
				if c.ReplaceOpening {
					return ErrOperation
				}
			}
			if c.Action == "void" {
				if c.Entry != nil {
					return ErrOperation
				}
				next.Voided = true
				next.Override = true
			} else {
				if c.Entry == nil {
					return ErrOperation
				}
				in := *c.Entry
				i, ok := st.instruments[in.InstrumentID]
				if !ok {
					return ErrOperation
				}
				if !validDate(in.Date) || in.Date > today || in.Fee < 0 || len(in.Note) > 2000 || in.FX < 0 || i.Currency == st.account.Currency && in.FX != 100000000 {
					return ErrOperation
				}
				covered := st.journal.Cash == nil || c.ReplaceOpening || next.Opening
				if a := st.journal.Cash; a != nil {
					covered = covered || in.Date < a.Date || in.Date == a.Date && previous != nil && previous.Sequence <= a.Sequence
				}
				if !covered && in.FX == 0 {
					return ErrOperation
				}
				if previous != nil && in.InstrumentID != previous.InstrumentID {
					return ErrOperation
				}
				if next.Event != nil && (in.Kind != "dividend" || in.Date != next.Event.ExDate) {
					return ErrOperation
				}
				if next.Event != nil && in.FX == 0 {
					return ErrOperation
				}
				next.InstrumentID = in.InstrumentID
				next.Kind = in.Kind
				next.Date = in.Date
				next.Quantity = in.Quantity
				next.Price = in.Price
				next.Fee = in.Fee
				next.FX = in.FX
				next.Note = strings.TrimSpace(in.Note)
				if in.Kind == "dividend" {
					if in.Amount <= 0 || in.Fee != 0 || in.Quantity != 0 || in.Price != 0 {
						return ErrOperation
					}
					next.Amount = in.Amount
					if next.Event == nil {
						for _, e := range st.entries {
							if !e.Voided && e.ID != next.ID && e.InstrumentID == in.InstrumentID && e.Event != nil && (e.Date == in.Date || e.Event.PayDate == in.Date) {
								return ErrConflict
							}
						}
					}
				} else {
					if in.Kind != "buy" && in.Kind != "sell" || in.Quantity <= 0 || in.Price <= 0 || in.Amount != 0 {
						return ErrOperation
					}
					next.Amount, err = TradeAmount(in.Quantity, in.Price)
					if err != nil {
						return err
					}
					if in.Kind == "buy" {
						next.Amount, err = AddMoney(next.Amount, in.Fee)
					} else {
						next.Amount, err = SubMoney(next.Amount, in.Fee)
					}
					if err != nil {
						return err
					}
					if next.Amount < 0 {
						return ErrOperation
					}
				}
				if next.Event != nil {
					next.Override = true
					next.SettlementFX = nil
					next.Quantity = previous.Quantity
					next.Price = previous.Price
				}
				if c.ReplaceOpening {
					if in.Kind != "buy" {
						return ErrOperation
					}
					n := slices.IndexFunc(st.journal.Openings, func(p CurrentPosition) bool { return p.InstrumentID == in.InstrumentID && p.Quantity == in.Quantity })
					if n < 0 {
						return ErrOperation
					}
					st.journal.Openings = slices.Delete(st.journal.Openings, n, n+1)
					// Existing quantities were already included in the user's cash snapshot.
					next.Opening = true
				}
			}
			if err := putStockEntry(ctx, tx, account, key, "human", stamp, &next, previous); err != nil {
				return err
			}
			if index < 0 {
				st.entries = append(st.entries, next)
			} else {
				st.entries[index] = next
			}
		default:
			return ErrOperation
		}
		if err := reconcileStockDividends(ctx, tx, &st, today, stamp, key); err != nil {
			return err
		}
		if _, err := saveStockProjection(ctx, tx, &st, key, "human", today, stamp); err != nil {
			return err
		}
		out := StockWriteResult{account, st.version(), c.ID, c.Action}
		audit, err := appendAudit(ctx, tx, key, c.Action, "stock_command", key, account, 1, stamp, "human", nil, out, nil)
		if err != nil {
			return err
		}
		result, err = json.Marshal(out)
		if err != nil {
			return err
		}
		return saveReceipt(ctx, tx, key, "stock_command", string(intent), string(result), audit)
	})
	return result, err
}

// Providers never run inside a transaction. Readers and the settlement worker
// share this exact projection, including archived securities and unknown openings.
type StockItem struct {
	HoldingItem
	Metrics StockMetrics `json:"metrics"`
	Opening bool         `json:"opening"`
}
type StockBook struct {
	AccountID     string       `json:"account_id"`
	Currency      Currency     `json:"currency"`
	Version       string       `json:"version"`
	Cash          Money        `json:"cash"`
	CashDate      string       `json:"cash_date"`
	AsOf          string       `json:"as_of"`
	Items         []StockItem  `json:"items"`
	Entries       []StockEntry `json:"entries"`
	SyncCheckedAt string       `json:"sync_checked_at"`
	SyncMessage   string       `json:"sync_message"`
}

func (h Handler) stockBookView(ctx context.Context, id string) (StockBook, error) {
	out := StockBook{AccountID: id, Items: []StockItem{}, Entries: []StockEntry{}}
	var st stockState
	var err error
	err = h.Store.db.WithTx(ctx, func(tx *sql.Tx) error {
		st, err = readStockState(ctx, tx, id)
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `SELECT checked_at,message FROM stock_dividend_status WHERE account_id=?`, id).Scan(&out.SyncCheckedAt, &out.SyncMessage)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return out, err
	}
	today, _, err := h.Store.cutoff()
	if err != nil {
		return out, err
	}
	positions, err := replayStocks(st.journal, st.entries, today)
	if err != nil {
		return out, err
	}
	out.Currency = st.account.Currency
	out.Version = st.version()
	out.AsOf = today
	out.Entries = st.entries
	if st.current.Snapshot != nil {
		out.Cash = st.current.Snapshot.Cash
	}
	if st.journal.Cash != nil {
		out.CashDate = st.journal.Cash.Date
	}
	ids := []string{}
	for id := range positions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	view := HoldingsView{Configured: true, Currency: out.Currency, AsOf: today, Cash: &out.Cash, Items: []HoldingItem{}}
	instruments := []Instrument{}
	for _, id := range ids {
		i := st.instruments[id]
		view.Items = append(view.Items, HoldingItem{Instrument: instrumentJSON{i.ID, i.Market, i.Code, i.Name, i.Currency}, Quantity: positions[id].quantity})
		instruments = append(instruments, i)
	}
	if err = h.valueHoldings(ctx, &view, instruments); err != nil {
		return out, err
	}
	for n, item := range view.Items {
		if item.Quantity == 0 {
			z := Money(0)
			item.MarketValue = &z
		}
		m, err := metricsFor(positions[ids[n]], item.MarketValue)
		if err != nil {
			return out, err
		}
		opening := slices.ContainsFunc(st.journal.Openings, func(p CurrentPosition) bool { return p.InstrumentID == ids[n] })
		out.Items = append(out.Items, StockItem{item, m, opening})
	}
	return out, nil
}

func stockStateChanged(st stockState, original []StockEntry, today string) bool {
	if !reflect.DeepEqual(st.entries, original) {
		return true
	}
	cash, err := stockCash(st.journal, st.entries, today)
	return err == nil && st.current.Snapshot != nil && cash != st.current.Snapshot.Cash
}
