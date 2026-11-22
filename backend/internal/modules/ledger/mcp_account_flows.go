package ledger

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
)

const flowCreateTool = "ledger_record_account_cash_flow"
const flowEditTool = "ledger_edit_account_cash_flow"
const flowListTool = "ledger_list_account_cash_flows"

type MCPFlowInput struct {
	RequestID       string   `json:"request_id"`
	AccountID       string   `json:"account_id"`
	Currency        Currency `json:"currency"`
	SourceRef       string   `json:"source_ref,omitempty"`
	RecordID        string   `json:"record_id,omitempty"`
	ExpectedVersion string   `json:"expected_version,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Date            string   `json:"date,omitempty"`
	Direction       string   `json:"direction,omitempty"`
	Amount          string   `json:"amount,omitempty"`
	Note            *string  `json:"note,omitempty"`
	Channel         *string  `json:"channel,omitempty"`
}

func (in MCPFlowInput) mcpWriteIdentity() MCPWriteFields {
	return MCPWriteFields{AccountID: in.AccountID, RequestID: in.RequestID, ExpectedVersion: in.ExpectedVersion, SourceRef: in.SourceRef}
}

type mcpFlowIntent struct {
	Tool  string       `json:"tool"`
	Input MCPFlowInput `json:"input"`
}

func (i mcpFlowIntent) check() error {
	in := i.Input
	if !validID(in.AccountID) || !validID(in.RequestID) || !in.Currency.valid() || !mcpText(in.SourceRef, 256, false) {
		return mcpInvalid("account_id", "request_id", "currency", "source_ref")
	}
	if in.Date != "" && !validDate(in.Date) {
		return mcpInvalid("date")
	}
	if in.Note != nil && !mcpText(*in.Note, 16384, false) {
		return mcpInvalid("note")
	}
	if in.Channel != nil && *in.Channel != "" && !validChannelName(*in.Channel) {
		return mcpInvalid("channel")
	}
	if in.Amount != "" || in.Direction != "" {
		n, err := ParseMoney(in.Amount)
		if err != nil || n <= 0 {
			return mcpInvalid("amount")
		}
		if in.Direction != "in" && in.Direction != "out" {
			return mcpInvalid("direction")
		}
	}
	switch i.Tool {
	case flowCreateTool:
		if in.RecordID != "" || in.ExpectedVersion != "" || in.Reason != "" {
			return mcpInvalid("record_id", "expected_version", "reason")
		}
		if in.Date == "" || in.Amount == "" {
			return mcpInvalid("date", "amount", "direction")
		}
	case flowEditTool:
		if !validID(in.RecordID) || !mcpText(in.Reason, 512, true) {
			return mcpInvalid("record_id", "reason")
		}
		if _, err := positiveInteger(in.ExpectedVersion); err != nil {
			return mcpInvalid("expected_version")
		}
		if in.Date == "" && in.Amount == "" && in.Note == nil && in.Channel == nil {
			return mcpInvalid("date", "amount", "note", "channel")
		}
	default:
		return ErrOperation
	}
	return nil
}

// Edits project only requested fields onto the record's actual version. Other
// observations (especially total_assets and channel breakdown) stay intact.
func (i mcpFlowIntent) command(key string, previous *AccountRecord) (AccountRecordCommand, error) {
	in := i.Input
	c := AccountRecordCommand{Action: CreateOperation, AccountID: in.AccountID, ID: "manual-" + receiptDigest(key), Reason: in.Reason}
	entry := AccountEntry{Kind: "cash_flow"}
	if i.Tool == flowEditTool {
		if previous == nil || previous.AccountID != in.AccountID || previous.ID != in.RecordID || previous.Kind != "cash_flow" {
			return c, mcpIssue("record_not_found", "没有匹配的账户资金流水", "select_existing", "record_id")
		}
		if previous.Version != in.ExpectedVersion {
			return c, mcpIssue("version_conflict", "该笔账户流水已变化，请重新读取并核对", "reload_state", "expected_version")
		}
		if previous.Voided {
			return c, ErrVoided
		}
		c.Action, c.ID, c.ExpectedVersion = ReplaceOperation, in.RecordID, in.ExpectedVersion
		entry = previous.AccountEntry
	}
	if in.Date != "" {
		entry.Date = in.Date
	}
	if in.Amount != "" {
		n, err := ParseMoney(in.Amount)
		if err != nil || n <= 0 {
			return c, mcpInvalid("amount")
		}
		if in.Direction == "out" {
			n = -n
		}
		entry.Flow = &n
	}
	if in.Note != nil {
		entry.Note = *in.Note
	}
	if in.Channel != nil {
		entry.FlowChannel = *in.Channel
	}
	if !entry.valid() {
		return c, ErrOperation
	}
	c.Entry = &entry
	return c, nil
}

type MCPFlowWriteResult struct {
	Reason    string        `json:"reason,omitempty"`
	Tool      string        `json:"tool"`
	RequestID string        `json:"request_id"`
	ReceiptID string        `json:"receipt_id"`
	Currency  Currency      `json:"currency"`
	Record    AccountRecord `json:"record"`
}

func (s MCPService) writeFlow(ctx context.Context, tool string, in MCPFlowInput) (out MCPFlowWriteResult, err error) {
	intent := mcpFlowIntent{tool, in}
	if err = intent.check(); err != nil {
		return
	}
	key := mcpWriteKey(in.AccountID, in.RequestID)
	raw, err := s.Store.accountReceipt(ctx, key, "account_record", intent, func(tx *sql.Tx) (any, int64, error) {
		info, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, in.AccountID))
		if err != nil {
			return nil, 0, err
		}
		if info.Currency != in.Currency {
			return nil, 0, mcpInvalid("currency")
		}
		day, _, err := s.Store.cutoff()
		if err != nil {
			return nil, 0, err
		}
		if in.Date > day {
			return nil, 0, mcpInvalid("date")
		}
		var prev *AccountRecord
		if tool == flowEditTool {
			r, err := scanAccountRecord(tx.QueryRowContext(ctx, accountRecordSelect+` WHERE account_id=? AND id=?`, in.AccountID, in.RecordID))
			if err != nil && !errors.Is(err, ErrNotFound) {
				return nil, 0, err
			}
			if err == nil {
				prev = &r
			}
		}
		c, err := intent.command(key, prev)
		if err != nil {
			return nil, 0, err
		}
		return s.Store.applyAccountRecordTx(ctx, tx, key, c, &mcpAuditMetadata{SchemaVersion: 1, Transport: "mcp", Tool: tool, RequestID: in.RequestID, SourceRef: in.SourceRef, Reason: in.Reason})
	})
	if err != nil {
		return out, err
	}
	return intent.result(key, string(raw))
}

func (i mcpFlowIntent) result(key, raw string) (MCPFlowWriteResult, error) {
	out := MCPFlowWriteResult{Tool: i.Tool, RequestID: i.Input.RequestID, ReceiptID: key, Currency: i.Input.Currency, Reason: i.Input.Reason}
	err := decodeReceipt(raw, &out.Record)
	return out, err
}

func validateMCPFlowReceipt(ctx context.Context, tx *sql.Tx, i mcpFlowIntent, raw, key string, auditID int64) error {
	if i.check() != nil || key != mcpWriteKey(i.Input.AccountID, i.Input.RequestID) {
		return ErrCorrupt
	}
	var before sql.NullString
	var meta string
	var currency Currency
	if err := tx.QueryRowContext(ctx, `SELECT a.before_json,a.metadata_json,c.currency FROM audit_log a JOIN accounts c ON c.id=a.account_id WHERE a.id=? AND a.account_id=?`, auditID, i.Input.AccountID).Scan(&before, &meta, &currency); err != nil || currency != i.Input.Currency {
		return ErrCorrupt
	}
	var stamp recordAuditMetadata
	if decodeReceipt(meta, &stamp) != nil || stamp.Transport != "mcp" || stamp.Tool != i.Tool || stamp.RequestID != i.Input.RequestID || stamp.SourceRef != i.Input.SourceRef {
		return ErrCorrupt
	}
	var prev *AccountRecord
	if before.Valid {
		prev = new(AccountRecord)
		if decodeReceipt(before.String, prev) != nil {
			return ErrCorrupt
		}
	}
	if (prev == nil) != (i.Tool == flowCreateTool) {
		return ErrCorrupt
	}
	c, err := i.command(key, prev)
	if err != nil {
		return ErrCorrupt
	}
	return validateAccountReceipt(ctx, tx, c, raw, key, auditID)
}

type MCPFlowQuery struct {
	IncludeSummary bool   `json:"include_summary,omitempty"`
	AccountID      string `json:"account_id"`
	RecordID       string `json:"record_id,omitempty"`
	From           string `json:"from,omitempty"`
	To             string `json:"to,omitempty"`
	Status         string `json:"status,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	Cursor         string `json:"cursor,omitempty"`
}

type MCPFlowPage struct {
	RecordReason *string             `json:"record_reason,omitempty"`
	Summary      *MCPFlowPerformance `json:"summary,omitempty"`
	Account      MCPAccount          `json:"account"`
	Items        []AccountRecord     `json:"items"`
	NextCursor   *string             `json:"next_cursor"`
}

// Account-wide effective returns for the requested dates, not page subtotals.
type MCPFlowPerformance struct {
	From    string       `json:"effective_from"`
	To      string       `json:"effective_to"`
	NetFlow string       `json:"net_flow"`
	Profit  ReturnMetric `json:"profit"`
	Dietz   ReturnMetric `json:"modified_dietz"`
	XIRR    ReturnMetric `json:"xirr"`
}

func (s MCPService) AccountFlows(ctx context.Context, q MCPFlowQuery) (out MCPFlowPage, err error) {
	var basis AnalysisBasis
	out.Items = []AccountRecord{}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Status == "" {
		q.Status = "active"
	}
	if !validID(q.AccountID) || q.RecordID != "" && (!validID(q.RecordID) || q.Cursor != "") || q.Status != "active" && q.Status != "voided" && q.Status != "all" || q.Limit < 1 || q.Limit > 100 || q.From != "" && !validDate(q.From) || q.To != "" && (!validDate(q.To) || q.From > q.To) {
		return out, mcpInvalid("account_id", "record_id", "from", "to", "status", "limit", "cursor")
	}
	filter := q
	filter.Cursor, filter.Limit = "", 0
	data, _ := json.Marshal(filter)
	hash := receiptDigest(string(data))
	var cursor mcpEntryCursor
	if q.Cursor != "" {
		if len(q.Cursor) > 1024 {
			return out, mcpInvalid("cursor")
		}
		raw, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		if e != nil || decodeReceipt(string(raw), &cursor) != nil || !validDate(cursor.Date) || cursor.Sequence <= 0 {
			return out, mcpInvalid("cursor")
		}
	}
	err = s.Store.db.WithTx(ctx, func(tx *sql.Tx) error {
		info, e := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, q.AccountID))
		if e != nil {
			return e
		}
		out.Account = mcpAccount(info)
		var rev string
		if e := tx.QueryRowContext(ctx, `SELECT CAST(coalesce(max(id),0) AS TEXT) FROM audit_log WHERE entity_type='account_record' AND account_id=?`, q.AccountID).Scan(&rev); e != nil {
			return e
		}
		sqlq := accountRecordSelect + ` WHERE account_id=? AND kind='cash_flow'`
		args := []any{q.AccountID}
		if q.Status != "all" {
			sqlq += ` AND voided=?`
			args = append(args, q.Status == "voided")
		}
		if q.RecordID != "" {
			sqlq += ` AND id=?`
			args = append(args, q.RecordID)
		}
		if q.From != "" {
			sqlq += ` AND business_date>=?`
			args = append(args, q.From)
		}
		if q.To != "" {
			sqlq += ` AND business_date<=?`
			args = append(args, q.To)
		}
		if q.Cursor != "" {
			if cursor.Account != q.AccountID || cursor.Filter != hash || cursor.Version != rev {
				return mcpIssue("stale_cursor", "账户记录或筛选条件已变化，请重新分页", "restart_pagination", "cursor")
			}
			sqlq += ` AND (business_date,sequence)<(?,?)`
			args = append(args, cursor.Date, cursor.Sequence)
		}
		rows, e := tx.QueryContext(ctx, sqlq+` ORDER BY business_date DESC,sequence DESC LIMIT ?`, append(args, q.Limit+1)...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			if len(out.Items) == q.Limit {
				r := out.Items[q.Limit-1]
				seq, _ := strconv.ParseInt(r.Sequence, 10, 64)
				b, _ := json.Marshal(mcpEntryCursor{Account: q.AccountID, Version: rev, Filter: hash, Date: r.Date, Sequence: seq})
				next := base64.RawURLEncoding.EncodeToString(b)
				out.NextCursor = &next
				break
			}
			r, e := scanAccountRecord(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, r)
		}
		if e := rows.Err(); e != nil {
			return e
		}
		if e := rows.Close(); e != nil {
			return e
		}
		if q.RecordID != "" && len(out.Items) == 0 {
			return mcpIssue("record_not_found", "没有匹配的账户资金流水，请核对筛选条件", "select_existing", "record_id")
		}
		if q.RecordID != "" {
			var reason string
			e := tx.QueryRowContext(ctx, `SELECT json_extract(metadata_json,'$.reason') FROM audit_log WHERE entity_type='account_record' AND account_id=? AND entity_id=? AND version=?`, q.AccountID, q.RecordID, out.Items[0].Version).Scan(&reason)
			if e != nil {
				return e
			}
			out.RecordReason = &reason
		}
		if q.IncludeSummary {
			basis, e = s.Store.analysisBasis(ctx, tx, q.AccountID, q.From, q.To, 0)
			return e
		}
		return nil
	})
	if err == nil && q.IncludeSummary {
		var r Returns
		r, err = calculateReturns(ctx, basis, q.From, q.To)
		if err == nil {
			out.Summary = &MCPFlowPerformance{r.EffectiveFrom, r.EffectiveTo, r.NetFlow, r.Profit, r.Dietz, r.XIRR}
		}
	}
	return
}
