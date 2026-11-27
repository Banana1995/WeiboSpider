package ledger

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const mcpReceiptKind = "mcp_stock_command"

type MCPWriteFields struct {
	RequestID       string `json:"request_id"`
	AccountID       string `json:"account_id"`
	ExpectedVersion string `json:"expected_version"`
	SourceRef       string `json:"source_ref,omitempty"`
}

type MCPInstrumentSelector struct {
	Market string `json:"market"`
	Code   string `json:"code"`
}

type MCPTradeInput struct {
	MCPWriteFields
	Instrument MCPInstrumentSelector `json:"instrument"`
	Channel    string                `json:"channel,omitempty"`
	Side       string                `json:"side"`
	Mode       string                `json:"mode,omitempty"`
	Date       string                `json:"date"`
	Quantity   string                `json:"quantity"`
	Price      string                `json:"price"`
	Fee        string                `json:"fee"`
	Currency   Currency              `json:"currency"`
	FX         string                `json:"fx,omitempty"`
	Note       string                `json:"note,omitempty"`
}

type MCPDividendInput struct {
	MCPWriteFields
	Instrument MCPInstrumentSelector `json:"instrument"`
	Channel    string                `json:"channel,omitempty"`
	Date       string                `json:"date"`
	Amount     string                `json:"amount"`
	Currency   Currency              `json:"currency"`
	FX         string                `json:"fx,omitempty"`
	Note       string                `json:"note,omitempty"`
}

type MCPTaxInput struct {
	MCPWriteFields
	Instrument MCPInstrumentSelector `json:"instrument"`
	Channel    string                `json:"channel,omitempty"`
	Date       string                `json:"date"`
	Amount     string                `json:"amount"`
	Currency   Currency              `json:"currency"`
	FX         string                `json:"fx,omitempty"`
	Note       string                `json:"note,omitempty"`
}

type MCPReplacement struct {
	Kind     string   `json:"kind"`
	Channel  string   `json:"channel,omitempty"`
	Date     string   `json:"date"`
	Quantity string   `json:"quantity,omitempty"`
	Price    string   `json:"price,omitempty"`
	Fee      string   `json:"fee,omitempty"`
	Amount   string   `json:"amount,omitempty"`
	Currency Currency `json:"currency"`
	FX       string   `json:"fx,omitempty"`
	Note     string   `json:"note,omitempty"`
}

type MCPEditInput struct {
	MCPWriteFields
	EntryID     string          `json:"entry_id"`
	Action      string          `json:"action"`
	Reason      string          `json:"reason"`
	Replacement *MCPReplacement `json:"replacement,omitempty"`
}

type MCPCashInput struct {
	MCPWriteFields
	ObservedDate string   `json:"observed_date"`
	Amount       string   `json:"amount"`
	Currency     Currency `json:"currency"`
}

type MCPOpeningsInput struct {
	MCPWriteFields
	Positions []MCPOpeningPosition `json:"positions"`
}

type MCPClosedFlowInput struct {
	Kind     string `json:"kind"`
	Date     string `json:"date"`
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
	Fee      string `json:"fee"`
	Amount   string `json:"amount"`
	FX       string `json:"fx,omitempty"`
	Note     string `json:"note,omitempty"`
}

type MCPClosedInput struct {
	MCPWriteFields
	Instrument  MCPInstrumentSelector `json:"instrument"`
	Channel     string                `json:"channel,omitempty"`
	OpenedOn    string                `json:"opened_on"`
	ClosedOn    string                `json:"closed_on"`
	Quantity    string                `json:"quantity"`
	Income      string                `json:"income"`
	Expense     string                `json:"expense"`
	Fees        string                `json:"fees"`
	Profit      string                `json:"profit"`
	ProfitRate  string                `json:"profit_rate"`
	HoldingDays int                   `json:"holding_days,omitempty"`
	Currency    Currency              `json:"currency"`
	Flows       []MCPClosedFlowInput  `json:"flows"`
	Source      string                `json:"source,omitempty"`
	Note        string                `json:"note,omitempty"`
}

type MCPOpeningPosition struct {
	Instrument MCPInstrumentSelector `json:"instrument"`
	Channel    string                `json:"channel,omitempty"`
	Quantity   string                `json:"quantity"`
}

// This versioned wire shape is immutable. Provider names, client identity and
// JSON-RPC ids are deliberately excluded from the business request identity.
type mcpStockIntent struct {
	SchemaVersion int                   `json:"schema_version"`
	Tool          string                `json:"tool"`
	Write         MCPWriteFields        `json:"write"`
	Instrument    MCPInstrumentSelector `json:"instrument"`
	EntryID       string                `json:"entry_id"`
	Action        string                `json:"action"`
	Mode          string                `json:"mode"`
	ObservedDate  string                `json:"observed_date"`
	Reason        string                `json:"reason"`
	Value         MCPReplacement        `json:"value"`
	Openings      []MCPOpeningPosition  `json:"openings,omitempty"`
	Closed        *StockClosed          `json:"closed,omitempty"`
}

var mcpUnsignedDecimal = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
var mcpSignedDecimal = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
var mcpRateString = regexp.MustCompile(`^-?[0-9]{1,20}(?:\.[0-9]{1,2})?$`)

// mcpChannel is the broker account a trade belongs to. It is data, not an
// instruction, and an empty value keeps entries recorded before channels existed.
func mcpChannel(name string) (string, bool) {
	name = strings.TrimSpace(name)
	return name, name == "" || validChannelName(name)
}

func mcpText(s string, limit int, required bool) bool {
	return len(s) <= limit && utf8.ValidString(s) && (!required || strings.TrimSpace(s) != "") &&
		strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' || unicode.Is(unicode.Cf, r) }) < 0
}

func mcpDecimal(s string, digits int, positive bool) (string, error) {
	if !mcpUnsignedDecimal.MatchString(s) {
		return "", ErrPrecision
	}
	v, err := parseFixed(s, digits)
	if err != nil || v < 0 || positive && v == 0 {
		return "", ErrPrecision
	}
	return formatFixed(v, digits), nil
}

func mcpFixed(s string, digits int) int64 {
	v, _ := parseFixed(s, digits)
	return v
}

func (i MCPInstrumentSelector) valid() bool {
	currency := CNY
	if i.Market == "HK" || i.Market == "SZ" && strings.HasPrefix(i.Code, "200") {
		currency = HKD
	} else if i.Market == "SH" && strings.HasPrefix(i.Code, "900") {
		currency = USD
	}
	_, code := quoteSymbol(Instrument{Market: i.Market, Code: i.Code, Currency: currency})
	return code == ""
}

func normalizeMCPWrite(tool string, input any) (mcpStockIntent, error) {
	in := mcpStockIntent{SchemaVersion: 1, Tool: tool}
	switch v := input.(type) {
	case MCPTradeInput:
		if tool != "ledger_record_trade" || v.Side != "buy" && v.Side != "sell" {
			return in, mcpInvalid("side")
		}
		in.Write, in.Instrument, in.Action, in.Mode = v.MCPWriteFields, v.Instrument, "create", v.Mode
		if in.Mode == "" {
			in.Mode = "trade"
		}
		if in.Mode != "trade" && in.Mode != "complete_opening" || in.Mode == "complete_opening" && v.Side != "buy" {
			return in, mcpInvalid("mode")
		}
		in.Value = MCPReplacement{Kind: v.Side, Channel: v.Channel, Date: v.Date, Quantity: v.Quantity, Price: v.Price, Fee: v.Fee, Currency: v.Currency, FX: v.FX, Note: v.Note}
	case MCPDividendInput:
		if tool != "ledger_record_dividend" {
			return in, ErrOperation
		}
		in.Write, in.Instrument, in.Action = v.MCPWriteFields, v.Instrument, "create"
		in.Value = MCPReplacement{Kind: "dividend", Channel: v.Channel, Date: v.Date, Amount: v.Amount, Currency: v.Currency, FX: v.FX, Note: v.Note}
	case MCPTaxInput:
		if tool != "ledger_record_dividend_tax" {
			return in, ErrOperation
		}
		in.Write, in.Instrument, in.Action = v.MCPWriteFields, v.Instrument, "create"
		in.Value = MCPReplacement{Kind: "tax", Channel: v.Channel, Date: v.Date, Amount: v.Amount, Currency: v.Currency, FX: v.FX, Note: v.Note}
	case MCPClosedInput:
		if tool != "ledger_record_closed" || !validDate(v.OpenedOn) || !validDate(v.ClosedOn) || v.OpenedOn > v.ClosedOn || len(v.Flows) == 0 || len(v.Flows) > 1000 {
			return in, mcpInvalid("opened_on", "closed_on", "flows")
		}
		if !v.Instrument.valid() || !v.Currency.valid() || !mcpText(v.Source, 200, false) || !mcpText(v.Note, 2000, false) || v.HoldingDays < 0 {
			return in, mcpInvalid("instrument", "currency", "source", "note", "holding_days")
		}
		channel, ok := mcpChannel(v.Channel)
		if !ok {
			return in, mcpInvalid("channel")
		}
		quantity, err := mcpDecimal(v.Quantity, 6, true)
		if err != nil {
			return in, mcpIssue("invalid_precision", "股数格式或范围不合法", "fix_input", "quantity")
		}
		income, err := mcpDecimal(v.Income, 2, false)
		if err != nil {
			return in, mcpIssue("invalid_precision", "累计收入格式或范围不合法", "fix_input", "income")
		}
		expense, err := mcpDecimal(v.Expense, 2, false)
		if err != nil {
			return in, mcpIssue("invalid_precision", "累计支出格式或范围不合法", "fix_input", "expense")
		}
		fees, err := mcpDecimal(v.Fees, 2, false)
		if err != nil {
			return in, mcpIssue("invalid_precision", "费用格式或范围不合法", "fix_input", "fees")
		}
		if !mcpSignedDecimal.MatchString(v.Profit) {
			return in, mcpIssue("invalid_precision", "总盈亏格式不合法", "fix_input", "profit")
		}
		profit, err := parseFixed(v.Profit, 2)
		if err != nil {
			return in, mcpIssue("invalid_precision", "总盈亏格式不合法", "fix_input", "profit")
		}
		if !mcpRateString.MatchString(v.ProfitRate) {
			return in, mcpIssue("invalid_precision", "收益率格式不合法", "fix_input", "profit_rate")
		}
		closed := &StockClosed{
			OpenedOn: v.OpenedOn, ClosedOn: v.ClosedOn, Quantity: Quantity(mcpFixed(quantity, 6)),
			Income: Money(mcpFixed(income, 2)), Expense: Money(mcpFixed(expense, 2)), Fees: Money(mcpFixed(fees, 2)),
			Profit: Money(profit), ProfitRate: v.ProfitRate, HoldingDays: v.HoldingDays,
			Source: strings.TrimSpace(v.Source),
		}
		for _, f := range v.Flows {
			if !validDate(f.Date) || f.Date < v.OpenedOn || f.Date > v.ClosedOn || !mcpText(f.Note, 500, false) {
				return in, mcpInvalid("flows.date", "flows.note")
			}
			price, err := mcpDecimal(f.Price, 6, false)
			if err != nil {
				return in, mcpIssue("invalid_precision", "流水价格格式或范围不合法", "fix_input", "flows.price")
			}
			flowQty, err := mcpDecimal(f.Quantity, 6, false)
			if err != nil {
				return in, mcpIssue("invalid_precision", "流水股数格式或范围不合法", "fix_input", "flows.quantity")
			}
			fee, err := mcpDecimal(f.Fee, 2, false)
			if err != nil {
				return in, mcpIssue("invalid_precision", "流水费用格式或范围不合法", "fix_input", "flows.fee")
			}
			amount, err := mcpDecimal(f.Amount, 2, false)
			if err != nil {
				return in, mcpIssue("invalid_precision", "流水金额格式或范围不合法", "fix_input", "flows.amount")
			}
			fx := Rate(0)
			if f.FX != "" {
				rate, err := mcpDecimal(f.FX, 8, true)
				if err != nil {
					return in, mcpIssue("invalid_precision", "流水汇率格式或范围不合法", "fix_input", "flows.fx")
				}
				fx = Rate(mcpFixed(rate, 8))
			}
			if f.Kind != "buy" && f.Kind != "sell" && f.Kind != "dividend" {
				return in, mcpInvalid("flows.kind")
			}
			if f.Kind == "dividend" && mcpFixed(flowQty, 6) != 0 {
				return in, mcpInvalid("flows.quantity")
			}
			if f.Kind != "dividend" && (mcpFixed(flowQty, 6) == 0 || mcpFixed(price, 6) == 0) {
				return in, mcpInvalid("flows.quantity", "flows.price")
			}
			closed.Flows = append(closed.Flows, StockClosedFlow{
				Kind: f.Kind, Date: f.Date, Price: Price(mcpFixed(price, 6)), Quantity: Quantity(mcpFixed(flowQty, 6)),
				Fee: Money(mcpFixed(fee, 2)), Amount: Money(mcpFixed(amount, 2)), FX: fx, Note: strings.TrimSpace(f.Note),
			})
		}
		in.Write, in.Instrument, in.Action = v.MCPWriteFields, v.Instrument, "create"
		in.Value = MCPReplacement{Channel: channel, Currency: v.Currency, Note: strings.TrimSpace(v.Note)}
		in.Closed = closed
	case MCPEditInput:
		if tool != "ledger_edit_entry" || !validID(v.EntryID) || !mcpText(v.Reason, 512, true) {
			return in, mcpInvalid("entry_id", "reason")
		}
		in.Write, in.EntryID, in.Action, in.Reason = v.MCPWriteFields, v.EntryID, v.Action, strings.TrimSpace(v.Reason)
		if v.Action == "replace" && v.Replacement != nil {
			in.Value = *v.Replacement
		} else if v.Action != "void" || v.Replacement != nil {
			return in, mcpInvalid("action", "replacement")
		}
	case MCPOpeningsInput:
		if tool != "ledger_set_opening_positions" || len(v.Positions) == 0 || len(v.Positions) > maxStockOpenings {
			return in, mcpInvalid("positions")
		}
		in.Write, in.Action = v.MCPWriteFields, "openings"
		seen := map[string]bool{}
		for _, p := range v.Positions {
			if !p.Instrument.valid() {
				return in, mcpIssue("unsupported_instrument", "证券市场或代码不受支持", "fix_input", "positions")
			}
			if key := p.Instrument.Market + "/" + p.Instrument.Code + "/" + strings.TrimSpace(p.Channel); seen[key] {
				return in, mcpInvalid("positions")
			} else {
				seen[key] = true
			}
			quantity, err := mcpDecimal(p.Quantity, 6, true)
			if err != nil {
				return in, mcpIssue("invalid_precision", "期初股数格式或范围不合法", "fix_input", "positions")
			}
			in.Openings = append(in.Openings, MCPOpeningPosition{p.Instrument, p.Channel, quantity})
		}
	case MCPCashInput:
		if tool != "ledger_set_cash_balance" || !validDate(v.ObservedDate) {
			return in, mcpInvalid("observed_date")
		}
		in.Write, in.Action, in.ObservedDate = v.MCPWriteFields, "cash", v.ObservedDate
		in.Value = MCPReplacement{Amount: v.Amount, Currency: v.Currency}
	default:
		return in, ErrOperation
	}
	if !validID(in.Write.RequestID) || !validID(in.Write.AccountID) || !mcpText(in.Write.SourceRef, 256, false) {
		return in, mcpInvalid("request_id", "account_id", "source_ref")
	}
	if _, err := stockVersion(in.Write.ExpectedVersion); err != nil {
		return in, mcpInvalid("expected_version")
	}
	if in.Action == "openings" {
		// Opening positions carry quantities only: no price, fee, date or
		// settlement currency exists, so the value checks do not apply.
		for n := range in.Openings {
			channel, ok := mcpChannel(in.Openings[n].Channel)
			if !ok {
				return in, mcpInvalid("positions")
			}
			in.Openings[n].Channel = channel
		}
		return in, nil
	}
	if in.Closed != nil {
		// Closed history is an immutable broker snapshot. It has no trade
		// replacement fields and never touches current quantity or cash.
		return in, nil
	}
	channel, ok := mcpChannel(in.Value.Channel)
	if !ok {
		return in, mcpInvalid("channel")
	}
	in.Value.Channel = channel
	if in.Action == "create" && !in.Instrument.valid() {
		return in, mcpIssue("unsupported_instrument", "证券市场或代码不受支持", "fix_input", "instrument")
	}
	if in.Action == "void" {
		return in, nil
	}
	if !in.Value.Currency.valid() || !mcpText(in.Value.Note, 2000, false) {
		return in, mcpInvalid("currency", "note")
	}
	in.Value.Note = strings.TrimSpace(in.Value.Note)
	if in.Action != "cash" && !validDate(in.Value.Date) {
		return in, mcpInvalid("date")
	}
	v := &in.Value
	type decimalField struct {
		name     string
		value    *string
		digits   int
		positive bool
	}
	fields := []decimalField{}
	if in.Action == "cash" || v.Kind == "dividend" || v.Kind == "tax" {
		if v.Quantity != "" || v.Price != "" || v.Fee != "" {
			return in, mcpInvalid("quantity", "price", "fee")
		}
		fields = append(fields, decimalField{"amount", &v.Amount, 2, in.Action != "cash"})
	} else if v.Kind == "buy" || v.Kind == "sell" {
		if v.Amount != "" {
			return in, mcpInvalid("amount")
		}
		fields = append(fields, decimalField{"quantity", &v.Quantity, 6, true}, decimalField{"price", &v.Price, 6, true}, decimalField{"fee", &v.Fee, 2, false})
	} else {
		return in, mcpInvalid("kind")
	}
	if v.FX != "" {
		fields = append(fields, decimalField{"fx", &v.FX, 8, true})
	}
	for _, field := range fields {
		value, err := mcpDecimal(*field.value, field.digits, field.positive)
		if err != nil {
			return in, mcpIssue("invalid_precision", "金额、数量、价格或汇率格式及范围不合法", "fix_input", field.name)
		}
		*field.value = value
	}
	return in, nil
}

func mcpWriteKey(account, request string) string {
	raw, _ := json.Marshal([]string{"ledger-mcp-stock-v1", account, request})
	return "mcp-stock-" + receiptDigest(string(raw))
}

type MCPPositionChange struct {
	Instrument     instrumentJSON `json:"instrument"`
	QuantityBefore Quantity       `json:"quantity_before"`
	QuantityAfter  Quantity       `json:"quantity_after"`
	Delta          Quantity       `json:"delta"`
	CostBefore     *Price         `json:"cost_before"`
	CostAfter      *Price         `json:"cost_after"`
	OpeningBefore  bool           `json:"opening_before"`
	OpeningAfter   bool           `json:"opening_after"`
}

type MCPCashChange struct {
	Currency Currency `json:"currency"`
	Before   Money    `json:"before"`
	After    Money    `json:"after"`
	Delta    Money    `json:"delta"`
}

type MCPRelatedChanges struct {
	Count     int      `json:"count"`
	EntryIDs  []string `json:"entry_ids"`
	Truncated bool     `json:"truncated"`
}

type MCPStockWriteResult struct {
	SchemaVersion    int                 `json:"schema_version"`
	RequestID        string              `json:"request_id"`
	AccountID        string              `json:"account_id"`
	Tool             string              `json:"tool"`
	Action           string              `json:"action"`
	ReceiptID        string              `json:"receipt_id"`
	AuditID          string              `json:"audit_id"`
	RecordedAt       string              `json:"recorded_at"`
	BusinessDate     string              `json:"business_date"`
	VersionBefore    string              `json:"version_before"`
	VersionAfter     string              `json:"version_after"`
	Entry            *StockEntry         `json:"entry"`
	PositionsChanged []MCPPositionChange `json:"positions_changed"`
	Cash             MCPCashChange       `json:"cash"`
	CashAnchorBefore *CashAnchor         `json:"cash_anchor_before"`
	CashAnchorAfter  *CashAnchor         `json:"cash_anchor_after"`
	CashEffectReason string              `json:"cash_effect_reason"`
	RelatedChanges   MCPRelatedChanges   `json:"related_changes"`
	Notices          []string            `json:"notices"`
}
