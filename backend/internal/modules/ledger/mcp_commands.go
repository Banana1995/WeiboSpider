package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
)

type MCPService struct {
	Store       *Store
	Instruments InstrumentSearchProvider
	Dividends   *DividendWorker
}

func (s MCPService) Write(ctx context.Context, tool string, args any) (MCPStockWriteResult, error) {
	var result MCPStockWriteResult
	in, err := normalizeMCPWrite(tool, args)
	if err != nil {
		return result, err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return result, err
	}
	key := mcpWriteKey(in.Write.AccountID, in.Write.RequestID)
	var found bool
	err = s.Store.db.WithTx(ctx, func(tx *sql.Tx) error {
		result, found, err = readMCPReceipt(ctx, tx, in.Write.AccountID, in.Write.RequestID, string(raw))
		return err
	})
	if err != nil || found {
		return result, err
	}
	// Resolve identities outside the transaction and only after receipt lookup.
	var identity *Instrument
	var openings []Instrument
	if in.Action == "openings" {
		for _, p := range in.Openings {
			resolved, err := s.resolveInstrument(ctx, in.Write.AccountID, p.Instrument, true)
			if err != nil {
				return result, err
			}
			openings = append(openings, *resolved)
		}
	} else if in.Action == "create" {
		identity, err = s.resolveInstrument(ctx, in.Write.AccountID, in.Instrument, in.Value.Kind != "dividend")
		if err != nil {
			return result, err
		}
	}
	committed := false
	err = s.Store.db.WithTx(ctx, func(tx *sql.Tx) error {
		result, found, err = readMCPReceipt(ctx, tx, in.Write.AccountID, in.Write.RequestID, string(raw))
		if err != nil || found {
			return err
		}
		command, err := s.prepareCommand(ctx, tx, in, identity, openings)
		if err != nil {
			return err
		}
		change, err := s.Store.applyStockCommandTx(ctx, tx, in.Write.AccountID, key, command)
		if err != nil {
			return err
		}
		if in.Action == "cash" && change.Date != in.ObservedDate {
			return mcpIssue("invalid_observed_date", "请提供当前业务日的实际现金余额", "obtain_current_balance", "observed_date")
		}
		result, err = mcpMutationResult(in, key, command, change)
		if err != nil {
			return err
		}
		// Audit rows are immutable: reserve the next AUTOINCREMENT value under the
		// same immediate transaction, so the frozen result can contain its proof id.
		var auditID int64
		if err := tx.QueryRowContext(ctx, `SELECT max(coalesce((SELECT seq FROM sqlite_sequence WHERE name='audit_log'),0),coalesce((SELECT max(id) FROM audit_log),0))+1`).Scan(&auditID); err != nil || auditID <= 0 {
			return ErrCorrupt
		}
		result.AuditID = strconv.FormatInt(auditID, 10)
		metadata := mcpAuditMetadata{1, "mcp", in.Tool, in.Write.RequestID, in.Write.SourceRef, in.Reason}
		actual, err := appendAudit(ctx, tx, key, command.Action, mcpReceiptKind, key, in.Write.AccountID, 1, change.Stamp, "human", nil, result, metadata)
		if err != nil {
			return err
		}
		if actual != auditID {
			return ErrCorrupt
		}
		response, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err := saveReceipt(ctx, tx, key, mcpReceiptKind, string(raw), string(response), auditID); err != nil {
			return err
		}
		committed = true
		return nil
	})
	if err != nil {
		return MCPStockWriteResult{}, err
	}
	if committed && s.Dividends != nil {
		s.Dividends.Trigger()
	}
	return result, nil
}

func (s MCPService) prepareCommand(ctx context.Context, tx *sql.Tx, in mcpStockIntent, identity *Instrument, openings []Instrument) (StockCommand, error) {
	c := StockCommand{Action: in.Action, ExpectedVersion: in.Write.ExpectedVersion, ID: in.EntryID, ReplaceOpening: in.Mode == "complete_opening"}
	st, err := readStockState(ctx, tx, in.Write.AccountID)
	if err != nil {
		return c, err
	}
	if st.version() != c.ExpectedVersion {
		e := mcpToolFailure(ErrVersion)
		e.Details["observed_version"] = st.version()
		return c, e
	}
	today, _, err := s.Store.cutoff()
	if err != nil {
		return c, err
	}
	if in.Action == "openings" {
		if len(openings) != len(in.Openings) {
			return c, ErrCorrupt
		}
		c.Openings = []StockOpening{}
		for n, p := range in.Openings {
			i := openings[n]
			if _, code := quoteSymbol(i); code != "" {
				return c, mcpIssue(code, "现有证券身份不符合当前支持的市场和币种规则", "fix_input", "positions")
			}
			if slices.ContainsFunc(st.entries, func(e StockEntry) bool { return !e.Voided && e.InstrumentID == i.ID }) {
				problem := mcpIssue("opening_conflict", "该证券已有买卖流水，期初持仓会与流水重复；请更正或作废原流水", "edit_existing_entry", "positions")
				problem.Details = map[string]string{"instrument_id": i.ID, "code": i.Code, "market": i.Market}
				return c, problem
			}
			quantity, err := ParseQuantity(p.Quantity)
			if err != nil {
				return c, err
			}
			c.Openings = append(c.Openings, StockOpening{instrumentJSON{i.ID, i.Market, i.Code, i.Name, i.Currency}, quantity})
		}
		return c, nil
	}
	if in.Action == "cash" {
		if in.ObservedDate != today {
			return c, mcpIssue("invalid_observed_date", "余额日期必须为当前业务日", "obtain_current_balance", "observed_date")
		}
		if in.Value.Currency != st.account.Currency {
			return c, mcpIssue("currency_mismatch", "余额币种必须与账户一致", "fix_input", "currency")
		}
		amount, err := ParseMoney(in.Value.Amount)
		c.Cash = &amount
		return c, err
	}
	var previous *StockEntry
	if in.Action != "create" {
		n := slices.IndexFunc(st.entries, func(e StockEntry) bool { return e.ID == in.EntryID })
		if n < 0 {
			return c, mcpIssue("entry_not_found", "该账户中不存在此流水", "select_existing", "entry_id")
		}
		previous = &st.entries[n]
		if previous.Voided {
			return c, ErrVoided
		}
		i := st.instruments[previous.InstrumentID]
		identity = &i
		if in.Action != "void" && (previous.Kind == "dividend") != (in.Value.Kind == "dividend") {
			return c, mcpInvalid("replacement.kind")
		}
	} else {
		c.ID = "mcp-entry-" + receiptDigest(mcpWriteKey(in.Write.AccountID, in.Write.RequestID))
	}
	if identity == nil {
		return c, ErrCorrupt
	}
	if _, code := quoteSymbol(*identity); code != "" {
		return c, mcpIssue(code, "现有证券身份不符合当前支持的市场和币种规则", "fix_input", "instrument")
	}
	if in.Action == "void" {
		return c, nil
	}
	if in.Value.Currency != identity.Currency {
		return c, mcpIssue("currency_mismatch", "成交币种必须与证券一致", "fix_input", "currency")
	}
	if in.Value.Date > today {
		return c, mcpInvalid("date")
	}
	v := in.Value
	e := &StockInput{InstrumentID: identity.ID, Kind: v.Kind, Date: v.Date, Note: v.Note}
	if v.Kind == "dividend" {
		e.Amount, err = ParseMoney(v.Amount)
	} else {
		var qErr, pErr, fErr error
		e.Quantity, qErr = ParseQuantity(v.Quantity)
		e.Price, pErr = ParsePrice(v.Price)
		e.Fee, fErr = ParseMoney(v.Fee)
		err = errors.Join(qErr, pErr, fErr)
	}
	if err != nil {
		return c, err
	}
	if v.FX != "" {
		e.FX, err = ParseRate(v.FX)
		if err != nil {
			return c, err
		}
	}
	if identity.Currency == st.account.Currency {
		if e.FX != 0 && e.FX != 100000000 {
			return c, mcpInvalid("fx")
		}
		e.FX = 100000000
	}
	needsFX, _ := mcpNeedsFX(st, identity.Currency, e.Date, c.ReplaceOpening, previous)
	if e.FX == 0 && needsFX {
		return c, mcpIssue("fx_required", "请提供每单位证券币种对应账户币种的结算汇率", "supply_fx", "fx")
	}
	if previous != nil && previous.Event != nil && e.Date != previous.Event.ExDate {
		return c, mcpInvalid("replacement.date")
	}
	if e.Kind == "dividend" && (previous == nil || previous.Event == nil) {
		for _, existing := range st.entries {
			if !existing.Voided && existing.ID != c.ID && existing.InstrumentID == e.InstrumentID && existing.Event != nil && (existing.Date == e.Date || existing.Event.PayDate == e.Date) {
				problem := mcpIssue("dividend_conflict", "已有自动分红，请更正现有流水的实际金额", "edit_existing_entry")
				problem.Details = map[string]string{"entry_id": existing.ID, "date": existing.Date, "pay_date": existing.Event.PayDate}
				return c, problem
			}
		}
	}
	c.Entry = e
	if c.Action == "create" {
		if _, exists := st.instruments[identity.ID]; !exists {
			c.Security = &instrumentJSON{identity.ID, identity.Market, identity.Code, identity.Name, identity.Currency}
		}
	}
	return c, nil
}

func mcpMutationResult(in mcpStockIntent, key string, command StockCommand, change stockMutation) (MCPStockWriteResult, error) {
	before, after := change.Before, change.After
	out := MCPStockWriteResult{SchemaVersion: 1, RequestID: in.Write.RequestID, AccountID: in.Write.AccountID, Tool: in.Tool, Action: in.Action,
		ReceiptID: key, RecordedAt: change.Stamp, BusinessDate: change.Date, VersionBefore: before.version(), VersionAfter: after.version(),
		CashAnchorBefore: before.journal.Cash, CashAnchorAfter: after.journal.Cash, PositionsChanged: []MCPPositionChange{}, Notices: []string{},
		RelatedChanges: MCPRelatedChanges{EntryIDs: []string{}}}
	out.Cash.Currency = after.account.Currency
	if before.current.Snapshot != nil {
		out.Cash.Before = before.current.Snapshot.Cash
	}
	out.Cash.After = after.current.Snapshot.Cash
	var err error
	out.Cash.Delta, err = SubMoney(out.Cash.After, out.Cash.Before)
	if err != nil {
		return out, err
	}
	left, err := replayStocks(before.journal, before.entries, change.Date)
	if err != nil {
		return out, err
	}
	right, err := replayStocks(after.journal, after.entries, change.Date)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for id := range after.instruments {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		l, r := left[id], right[id]
		if l == nil {
			l = &stockPosition{}
		}
		if r == nil {
			r = &stockPosition{}
		}
		lm, err := metricsFor(l, nil)
		if err != nil {
			return out, err
		}
		rm, err := metricsFor(r, nil)
		if err != nil {
			return out, err
		}
		lo := slices.ContainsFunc(before.journal.Openings, func(p CurrentPosition) bool { return p.InstrumentID == id })
		ro := slices.ContainsFunc(after.journal.Openings, func(p CurrentPosition) bool { return p.InstrumentID == id })
		if l.quantity == r.quantity && reflect.DeepEqual(lm, rm) && lo == ro {
			continue
		}
		i := after.instruments[id]
		out.PositionsChanged = append(out.PositionsChanged, MCPPositionChange{instrumentJSON{i.ID, i.Market, i.Code, i.Name, i.Currency}, l.quantity, r.quantity, r.quantity - l.quantity, lm.Cost, rm.Cost, lo, ro})
	}
	old := map[string]StockEntry{}
	for _, e := range before.entries {
		old[e.ID] = e
	}
	for _, e := range after.entries {
		if e.ID == command.ID {
			saved := e
			out.Entry = &saved
			continue
		}
		if previous, ok := old[e.ID]; !ok || !reflect.DeepEqual(previous, e) {
			out.RelatedChanges.Count++
			if len(out.RelatedChanges.EntryIDs) < 100 {
				out.RelatedChanges.EntryIDs = append(out.RelatedChanges.EntryIDs, e.ID)
			}
		}
	}
	out.RelatedChanges.Truncated = out.RelatedChanges.Count > len(out.RelatedChanges.EntryIDs)
	switch {
	case in.Action == "cash":
		out.CashEffectReason = "balance_reset"
	case in.Action == "openings":
		out.CashEffectReason = "opening_basis"
	case command.ReplaceOpening:
		out.CashEffectReason = "opening_completion"
	case before.journal.Cash == nil:
		out.CashEffectReason = "no_anchor"
	case in.Action == "replace" || in.Action == "void":
		out.CashEffectReason = "recalculated"
	case out.Entry != nil && out.Entry.Date < before.journal.Cash.Date:
		out.CashEffectReason = "covered_by_anchor"
	default:
		out.CashEffectReason = "settled"
	}
	if len(after.journal.Openings) > 0 {
		out.Notices = append(out.Notices, "unknown_opening_cost")
	}
	if out.Entry != nil && out.Entry.FX == 0 {
		out.Notices = append(out.Notices, "fx_unsettled")
	}
	if out.CashEffectReason == "covered_by_anchor" {
		out.Notices = append(out.Notices, "covered_by_cash_anchor")
	}
	return out, nil
}
