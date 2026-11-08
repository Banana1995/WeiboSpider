package ledger

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
)

type MCPAccountQuery struct {
	Query  string `json:"query,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type MCPAccount struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Currency       Currency `json:"currency"`
	OpeningDate    string   `json:"opening_date"`
	ProfileVersion string   `json:"profile_version"`
}

func mcpAccount(a AccountInfo) MCPAccount {
	return MCPAccount{a.ID, a.Name, a.Currency, a.OpeningDate, strconv.FormatInt(a.Version, 10)}
}

type MCPAccounts struct {
	BusinessDate string       `json:"business_date"`
	Timezone     string       `json:"timezone"`
	Items        []MCPAccount `json:"items"`
	NextCursor   *string      `json:"next_cursor"`
}

func (s MCPService) Accounts(ctx context.Context, q MCPAccountQuery) (MCPAccounts, error) {
	out := MCPAccounts{Timezone: "Asia/Shanghai", Items: []MCPAccount{}}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > 100 || !mcpText(q.Query, 512, false) || q.Cursor != "" && !validID(q.Cursor) {
		return out, mcpInvalid("query", "limit", "cursor")
	}
	var err error
	out.BusinessDate, _, err = s.Store.cutoff()
	if err != nil {
		return out, err
	}
	err = s.Store.db.WithTx(ctx, func(tx *sql.Tx) error {
		// instr treats percent and underscore literally and filters before LIMIT.
		rows, err := tx.QueryContext(ctx, accountInfoSelect+` WHERE id>? AND instr(lower(name),lower(?))>0 ORDER BY id LIMIT ?`, q.Cursor, q.Query, q.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if len(out.Items) == q.Limit {
				cursor := out.Items[len(out.Items)-1].ID
				out.NextCursor = &cursor
				break
			}
			a, err := scanAccountInfo(ctx, rows)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, mcpAccount(a))
		}
		return rows.Err()
	})
	return out, err
}

type MCPBookQuery struct {
	AccountID      string `json:"account_id"`
	IncludeClosed  bool   `json:"include_closed,omitempty"`
	IncludeEntries bool   `json:"include_entries,omitempty"`
	EntryID        string `json:"entry_id,omitempty"`
	InstrumentID   string `json:"instrument_id,omitempty"`
	From           string `json:"from,omitempty"`
	To             string `json:"to,omitempty"`
	IncludeVoided  bool   `json:"include_voided,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	Cursor         string `json:"cursor,omitempty"`
}

type MCPPosition struct {
	Instrument instrumentJSON    `json:"instrument"`
	Quantity   Quantity          `json:"quantity"`
	Opening    bool              `json:"opening"`
	Metrics    StockMetrics      `json:"metrics"`
	Channels   []ChannelQuantity `json:"channels"`
}

type MCPBook struct {
	Account           MCPAccount    `json:"account"`
	Version           string        `json:"version"`
	BusinessDate      string        `json:"business_date"`
	Timezone          string        `json:"timezone"`
	LedgerSavedAt     string        `json:"ledger_saved_at"`
	Cash              Money         `json:"cash"`
	CashAnchor        *CashAnchor   `json:"cash_anchor"`
	Positions         []MCPPosition `json:"positions"`
	Entries           []StockEntry  `json:"entries"`
	EntriesNextCursor *string       `json:"entries_next_cursor"`
	SyncCheckedAt     string        `json:"sync_checked_at"`
	SyncMessage       string        `json:"sync_message"`
}

type mcpEntryCursor struct {
	Account  string `json:"account"`
	Version  string `json:"version"`
	Day      string `json:"day"`
	Filter   string `json:"filter"`
	Date     string `json:"date"`
	Sequence int64  `json:"sequence,string"`
}

func mcpBookFilter(q MCPBookQuery) string {
	q.Cursor, q.Limit = "", 0
	data, _ := json.Marshal(q)
	return receiptDigest(string(data))
}

func (s MCPService) StockBook(ctx context.Context, q MCPBookQuery) (MCPBook, error) {
	out := MCPBook{Timezone: "Asia/Shanghai", Positions: []MCPPosition{}, Entries: []StockEntry{}}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.EntryID != "" {
		q.IncludeEntries = true
	}
	if !validID(q.AccountID) || q.Limit < 1 || q.Limit > 100 ||
		q.EntryID != "" && !validID(q.EntryID) || q.InstrumentID != "" && !validID(q.InstrumentID) ||
		q.From != "" && !validDate(q.From) || q.To != "" && !validDate(q.To) || q.From != "" && q.To != "" && q.From > q.To ||
		q.Cursor != "" && (!q.IncludeEntries || q.EntryID != "") {
		return out, mcpInvalid("account_id", "entry_id", "instrument_id", "from", "to", "limit", "cursor")
	}
	var cursor *mcpEntryCursor
	if q.Cursor != "" {
		if len(q.Cursor) > 1024 {
			return out, mcpInvalid("cursor")
		}
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		c := &mcpEntryCursor{}
		if err != nil || decodeReceipt(string(raw), c) != nil || !validDate(c.Date) || c.Sequence <= 0 {
			return out, mcpInvalid("cursor")
		}
		cursor = c
	}
	err := s.Store.db.WithTx(ctx, func(tx *sql.Tx) error {
		st, err := readStockState(ctx, tx, q.AccountID)
		if err != nil {
			return err
		}
		out.BusinessDate, _, err = s.Store.cutoff()
		if err != nil {
			return err
		}
		out.Account, out.Version, out.CashAnchor = mcpAccount(st.account), st.version(), st.journal.Cash
		if cursor != nil && (cursor.Account != q.AccountID || cursor.Version != out.Version || cursor.Day != out.BusinessDate || cursor.Filter != mcpBookFilter(q)) {
			return mcpIssue("stale_cursor", "持仓版本、业务日期或筛选条件已改变，请重新分页", "restart_pagination", "cursor")
		}
		if st.current.Snapshot != nil {
			out.Cash, out.LedgerSavedAt = st.current.Snapshot.Cash, st.current.Snapshot.SavedAt
		}
		err = tx.QueryRowContext(ctx, `SELECT checked_at,message FROM stock_dividend_status WHERE account_id=?`, q.AccountID).Scan(&out.SyncCheckedAt, &out.SyncMessage)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		positions, err := replayStocks(st.journal, st.entries, out.BusinessDate)
		if err != nil {
			return err
		}
		ids := []string{}
		for id := range positions {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			p := positions[id]
			if q.InstrumentID != "" && q.InstrumentID != id || q.InstrumentID == "" && !q.IncludeClosed && p.quantity == 0 {
				continue
			}
			i := st.instruments[id]
			m, err := metricsFor(p, nil)
			if err != nil {
				return err
			}
			opening := slices.ContainsFunc(st.journal.Openings, func(p OpeningPosition) bool { return p.InstrumentID == id })
			out.Positions = append(out.Positions, MCPPosition{instrumentJSON{i.ID, i.Market, i.Code, i.Name, i.Currency}, p.quantity, opening, m, channelBreakdown(p)})
		}
		if !q.IncludeEntries {
			return nil
		}
		entries := slices.Clone(st.entries)
		slices.SortFunc(entries, func(a, b StockEntry) int {
			if a.Date != b.Date {
				return strings.Compare(b.Date, a.Date)
			}
			if a.Sequence > b.Sequence {
				return -1
			}
			if a.Sequence < b.Sequence {
				return 1
			}
			return 0
		})
		for _, e := range entries {
			if q.EntryID != "" && e.ID != q.EntryID || q.InstrumentID != "" && e.InstrumentID != q.InstrumentID ||
				!q.IncludeVoided && e.Voided || q.From != "" && e.Date < q.From || q.To != "" && e.Date > q.To {
				continue
			}
			if cursor != nil && (e.Date > cursor.Date || e.Date == cursor.Date && e.Sequence >= cursor.Sequence) {
				continue
			}
			if len(out.Entries) == q.Limit {
				last := out.Entries[len(out.Entries)-1]
				data, err := json.Marshal(mcpEntryCursor{q.AccountID, out.Version, out.BusinessDate, mcpBookFilter(q), last.Date, last.Sequence})
				if err != nil {
					return err
				}
				encoded := base64.RawURLEncoding.EncodeToString(data)
				out.EntriesNextCursor = &encoded
				break
			}
			out.Entries = append(out.Entries, e)
		}
		if q.EntryID != "" && len(out.Entries) == 0 {
			return mcpIssue("entry_not_found", "没有匹配的流水，请核对作废状态及筛选条件", "select_existing", "entry_id")
		}
		return nil
	})
	return out, err
}
