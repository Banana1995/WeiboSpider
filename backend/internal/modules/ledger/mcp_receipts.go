package ledger

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"time"
)

// An empty expected request means a read-only lookup; it never claims that a
// missing receipt proves the original operation cannot still commit.
func readMCPReceipt(ctx context.Context, tx *sql.Tx, account, requestID, expected string) (MCPStockWriteResult, bool, error) {
	var out MCPStockWriteResult
	key := mcpWriteKey(account, requestID)
	var request string
	err := tx.QueryRowContext(ctx, `SELECT request_json FROM idempotency_receipts WHERE key=?`, key).Scan(&request)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if expected != "" {
		request = expected
	}
	saved, found, err := loadReceipt(ctx, tx, key, mcpReceiptKind, request)
	if err != nil || !found {
		return out, found, err
	}
	var in mcpStockIntent
	if decodeReceipt(request, &in) != nil || decodeReceipt(saved.Response, &out) != nil || in.SchemaVersion != 1 || out.SchemaVersion != 1 ||
		in.Write.AccountID != account || in.Write.RequestID != requestID || out.AccountID != account || out.RequestID != requestID ||
		out.ReceiptID != key || out.AuditID != strconv.FormatInt(saved.AuditID, 10) || out.Tool != in.Tool || out.Action != in.Action || out.VersionBefore != in.Write.ExpectedVersion {
		return out, true, ErrCorrupt
	}
	if !validMCPIntent(in) {
		return out, true, ErrCorrupt
	}
	before, err := stockVersion(out.VersionBefore)
	after, parseErr := positiveInteger(out.VersionAfter)
	if err != nil || parseErr != nil || after != before+1 || !validDate(out.BusinessDate) || !out.Cash.Currency.valid() || out.Cash.Before < 0 || out.Cash.After < 0 || out.Cash.Delta != out.Cash.After-out.Cash.Before || out.PositionsChanged == nil || out.Notices == nil || out.RelatedChanges.EntryIDs == nil {
		return out, true, ErrCorrupt
	}
	if _, err := time.Parse(time.RFC3339Nano, out.RecordedAt); err != nil {
		return out, true, ErrCorrupt
	}
	if out.Action == "cash" {
		if out.Entry != nil || out.CashAnchorAfter == nil || out.Cash.After.String() != in.Value.Amount || out.BusinessDate != in.ObservedDate || out.Cash.Currency != in.Value.Currency || out.CashAnchorAfter.Amount != out.Cash.After || out.CashAnchorAfter.Date != out.BusinessDate {
			return out, true, ErrCorrupt
		}
	} else {
		id := in.EntryID
		if in.Action == "create" {
			id = "mcp-entry-" + receiptDigest(key)
		}
		if out.Entry == nil || out.Entry.ID != id || !validStockEntry(*out.Entry) || out.Entry.Voided != (in.Action == "void") {
			return out, true, ErrCorrupt
		}
		if out.Entry.UpdatedAt != out.RecordedAt {
			return out, true, ErrCorrupt
		}
		if in.Action != "void" {
			e, v := out.Entry, in.Value
			if e.Kind != v.Kind || e.Date != v.Date || e.Date > out.BusinessDate || e.Note != v.Note {
				return out, true, ErrCorrupt
			}
			if v.Kind == "dividend" {
				if e.Amount.String() != v.Amount || e.Fee != 0 {
					return out, true, ErrCorrupt
				}
			} else if e.Quantity.String() != v.Quantity || e.Price.String() != v.Price || e.Fee.String() != v.Fee {
				return out, true, ErrCorrupt
			}
			rate := Rate(0)
			if v.FX != "" {
				rate, err = ParseRate(v.FX)
				if err != nil {
					return out, true, ErrCorrupt
				}
			} else if v.Currency == out.Cash.Currency {
				rate = 100000000
			}
			if rate != e.FX {
				return out, true, ErrCorrupt
			}
		}
	}
	seen := map[string]bool{}
	for _, p := range out.PositionsChanged {
		i := p.Instrument
		_, code := quoteSymbol(Instrument{i.ID, i.Market, i.Code, i.Name, i.Currency})
		if !validID(i.ID) || seen[i.ID] || code != "" || !validText(i.Name) || p.QuantityBefore < 0 || p.QuantityAfter < 0 || p.Delta != p.QuantityAfter-p.QuantityBefore || p.CostBefore != nil && *p.CostBefore < 0 || p.CostAfter != nil && *p.CostAfter < 0 {
			return out, true, ErrCorrupt
		}
		seen[i.ID] = true
	}
	for _, anchor := range []*CashAnchor{out.CashAnchorBefore, out.CashAnchorAfter} {
		if anchor != nil && (anchor.Amount < 0 || anchor.Sequence < 0 || !validDate(anchor.Date)) {
			return out, true, ErrCorrupt
		}
	}
	if out.RelatedChanges.Count < len(out.RelatedChanges.EntryIDs) || len(out.RelatedChanges.EntryIDs) > 100 || out.RelatedChanges.Truncated != (out.RelatedChanges.Count > len(out.RelatedChanges.EntryIDs)) ||
		!slices.Contains([]string{"no_anchor", "covered_by_anchor", "settled", "opening_completion", "balance_reset", "recalculated"}, out.CashEffectReason) {
		return out, true, ErrCorrupt
	}
	var proof, stamp, action, metadata string
	err = tx.QueryRowContext(ctx, `SELECT after_json,recorded_at,action,metadata_json FROM audit_log WHERE id=? AND entity_type=? AND entity_id=? AND correlation_id=? AND account_id=? AND version=1`, saved.AuditID, mcpReceiptKind, key, key, account).Scan(&proof, &stamp, &action, &metadata)
	if err != nil || proof != saved.Response || stamp != out.RecordedAt || action != out.Action {
		return out, true, ErrCorrupt
	}
	var origin mcpAuditMetadata
	if decodeReceipt(metadata, &origin) != nil || origin != (mcpAuditMetadata{1, "mcp", in.Tool, requestID, in.Write.SourceRef, in.Reason}) {
		return out, true, ErrCorrupt
	}
	return out, true, nil
}

type mcpAuditMetadata struct {
	SchemaVersion int    `json:"schema_version"`
	Transport     string `json:"transport"`
	Tool          string `json:"tool"`
	RequestID     string `json:"request_id"`
	SourceRef     string `json:"source_ref"`
	Reason        string `json:"reason"`
}

func validMCPIntent(in mcpStockIntent) bool {
	v := in.Value
	var args any
	switch in.Tool {
	case "ledger_record_trade":
		args = MCPTradeInput{in.Write, in.Instrument, v.Kind, in.Mode, v.Date, v.Quantity, v.Price, v.Fee, v.Currency, v.FX, v.Note}
	case "ledger_record_dividend":
		args = MCPDividendInput{in.Write, in.Instrument, v.Date, v.Amount, v.Currency, v.FX, v.Note}
	case "ledger_set_cash_balance":
		args = MCPCashInput{in.Write, in.ObservedDate, v.Amount, v.Currency}
	case "ledger_edit_entry":
		var replacement *MCPReplacement
		if in.Action == "replace" {
			replacement = &v
		}
		args = MCPEditInput{in.Write, in.EntryID, in.Action, in.Reason, replacement}
	default:
		return false
	}
	normalized, err := normalizeMCPWrite(in.Tool, args)
	return err == nil && reflect.DeepEqual(in, normalized)
}

type MCPReceiptQuery struct {
	AccountID string `json:"account_id"`
	RequestID string `json:"request_id"`
}

type MCPReceiptResult struct {
	Status     string               `json:"status"`
	Definitive bool                 `json:"definitive"`
	Result     *MCPStockWriteResult `json:"result"`
	NextAction string               `json:"next_action"`
}

func (s MCPService) WriteResult(ctx context.Context, q MCPReceiptQuery) (MCPReceiptResult, error) {
	out := MCPReceiptResult{Status: "not_found", NextAction: "retry_same_request_or_query_again"}
	if !validID(q.AccountID) || !validID(q.RequestID) {
		return out, mcpInvalid("account_id", "request_id")
	}
	err := s.Store.db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := scanAccountInfo(ctx, tx.QueryRowContext(ctx, accountInfoSelect+` WHERE id=?`, q.AccountID)); err != nil {
			return err
		}
		result, found, err := readMCPReceipt(ctx, tx, q.AccountID, q.RequestID, "")
		if err != nil {
			return err
		}
		if found {
			out = MCPReceiptResult{"committed", true, &result, "read_stock_book_for_current_state"}
		}
		return nil
	})
	return out, err
}
