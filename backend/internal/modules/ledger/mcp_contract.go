package ledger

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
)

const mcpContractVersion = "2026-11-06"

// These annotations travel with the actual input schema: help text, diagnostics
// and field discovery cannot maintain a different list of required fields.
func mcpField(schema mcpSchema, label, unit, source, question string) mcpSchema {
	schema["x-ledger-label"], schema["x-ledger-unit"] = label, unit
	schema["x-ledger-source"], schema["x-ledger-question"] = source, question
	if schema["description"] == nil {
		schema["description"] = label
	}
	return schema
}

func mcpFinancial(scale string, positive bool, description, label, unit, question string) mcpSchema {
	s := mcpField(mcpNumber(scale), label, unit, "user", question)
	s["description"], s["x-ledger-positive"] = description, positive
	return s
}

func mcpWriteSchema(tool string) mcpSchema {
	p := mcpWriteProperties()
	required := []string{"request_id", "account_id", "expected_version"}
	switch tool {
	case "ledger_record_trade":
		mcpMerge(p, mcpValueProperties(false))
		p["instrument"] = mcpInstrumentSchema()
		p["side"] = mcpField(mcpEnum("buy", "sell"), "买卖方向", "", "user", "这笔交易是买入还是卖出？")
		p["mode"] = mcpField(mcpEnum("trade", "complete_opening"), "记账模式", "", "user", "这是普通成交，还是补录已有待补录持仓的初始买入？")
		p["mode"].(mcpSchema)["description"] = "省略表示trade；complete_opening仅用于已有待补录持仓，必须有完整实际成交事实"
		return mcpObject(p, append(required, "instrument", "side", "date", "quantity", "price", "fee", "currency")...)
	case "ledger_record_dividend":
		mcpMerge(p, mcpValueProperties(true))
		p["instrument"] = mcpInstrumentSchema()
		return mcpObject(p, append(required, "instrument", "date", "amount", "currency")...)
	case "ledger_set_cash_balance":
		p["observed_date"] = mcpField(mcpDate("当前实际现金的北京时间日期，首次提交必须为当前业务日"), "余额观测日", "Asia/Shanghai", "user", "请确认当前业务日的实际余额，不能把过去余额当作当前余额。")
		p["amount"] = mcpFinancial("2", false, "当前实际剩余现金，账户币种，非负，最多2位小数；不是入金；明确零余额合法", "实际现金余额", "账户币种", "当前实际剩余现金是多少？如果余额确实为零，请明确为零。")
		p["currency"] = mcpField(mcpEnum("CNY", "HKD", "USD"), "账户币种", "ISO 4217", "ledger", "调用ledger_list_accounts确认账户币种。")
		return mcpObject(p, append(required, "observed_date", "amount", "currency")...)
	case "ledger_edit_entry":
		p["entry_id"] = mcpField(mcpID("ledger_get_stock_book取得的准确流水ID"), "流水ID", "", "ledger", "调用ledger_get_stock_book定位准确流水，不能直接按最近一笔猜选。")
		p["action"] = mcpField(mcpEnum("replace", "void"), "修改操作", "", "user", "需要完整更正这笔流水，还是将其作废？")
		p["reason"] = mcpField(mcpString("更正或作废原因，非空，最多512字节", 512), "修改原因", "", "user", "更正或作废这笔记录的原因是什么？")
		p["reason"].(mcpSchema)["minLength"] = 1
		p["reason"].(mcpSchema)["x-ledger-nonblank"] = true
		trade, div := mcpValueProperties(false), mcpValueProperties(true)
		trade["kind"] = mcpField(mcpEnum("buy", "sell"), "交易类型", "", "user", "更正后的交易是买入还是卖出？")
		div["kind"] = mcpField(mcpEnum("dividend"), "分红类型", "", "ledger", "从原流水确认是否为分红；不能将买卖转换成分红。")
		p["replacement"] = mcpField(mcpSchema{"type": "object", "x-ledger-discriminator": "kind", "oneOf": []any{
			mcpObject(trade, "kind", "date", "quantity", "price", "fee", "currency"),
			mcpObject(div, "kind", "date", "amount", "currency"),
		}}, "完整替换数据", "", "user", "请提供与原记录业务类型一致的完整更正内容；缺失金融字段不能沿用或猜填。")
		schema := mcpObject(p, append(required, "entry_id", "action", "reason")...)
		schema["x-ledger-discriminator"] = "action"
		schema["oneOf"] = []any{
			mcpSchema{"properties": mcpSchema{"action": mcpSchema{"const": "replace"}}, "required": []string{"replacement"}},
			mcpSchema{"properties": mcpSchema{"action": mcpSchema{"const": "void"}}, "not": mcpSchema{"required": []string{"replacement"}}},
		}
		return schema
	}
	return nil
}

type MCPContractQuery struct {
	ToolName        string                 `json:"tool_name"`
	AccountID       string                 `json:"account_id,omitempty"`
	Instrument      *MCPInstrumentSelector `json:"instrument,omitempty"`
	EntryID         string                 `json:"entry_id,omitempty"`
	Date            string                 `json:"date,omitempty"`
	Mode            string                 `json:"mode,omitempty"`
	Action          string                 `json:"action,omitempty"`
	ReplacementKind string                 `json:"replacement_kind,omitempty"`
}

func mcpContractQuerySchema() mcpSchema {
	return mcpObject(mcpSchema{
		"tool_name":  mcpEnum("ledger_record_trade", "ledger_record_dividend", "ledger_edit_entry", "ledger_set_cash_balance"),
		"account_id": mcpID("可选准确账户ID，用于解释当前上下文；不提供则显示尚未确定的规则"),
		"instrument": mcpInstrumentSchema(), "entry_id": mcpID("更正或作废的原流水ID"),
		"date": mcpDate("拟记账日期，用于解释现金基准覆盖"), "mode": mcpEnum("trade", "complete_opening"),
		"action": mcpEnum("replace", "void"), "replacement_kind": mcpEnum("buy", "sell", "dividend"),
	}, "tool_name")
}

type MCPContractField struct {
	Path        string   `json:"path"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	When        string   `json:"when,omitempty"`
	Unit        string   `json:"unit,omitempty"`
	ValueSource string   `json:"value_source,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
	MaxLength   int      `json:"max_length,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Question    string   `json:"question,omitempty"`
}

type MCPConditionalRequirement struct {
	Field  string `json:"field"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type MCPWriteContract struct {
	ToolName                string                      `json:"tool_name"`
	ContractVersion         string                      `json:"contract_version"`
	BusinessDate            string                      `json:"business_date"`
	Timezone                string                      `json:"timezone"`
	InputSchema             mcpSchema                   `json:"input_schema"`
	Fields                  []MCPContractField          `json:"fields"`
	ConditionalRequirements []MCPConditionalRequirement `json:"conditional_requirements"`
	KnownContext            map[string]any              `json:"known_context"`
	UnresolvedContext       []string                    `json:"unresolved_context"`
	Examples                []map[string]any            `json:"examples"`
}

func mcpSchemaText(schema mcpSchema, key string) string {
	s, _ := schema[key].(string)
	return s
}

func mcpContractFields(schema mcpSchema, prefix, when string) []MCPContractField {
	fields := []MCPContractField{}
	properties, _ := schema["properties"].(mcpSchema)
	required, _ := schema["required"].([]string)
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		p := properties[key].(mcpSchema)
		path := prefix + key
		// Branch discriminators with only const are already described by the parent.
		if p["type"] == nil {
			continue
		}
		field := MCPContractField{Path: path, Label: mcpSchemaText(p, "x-ledger-label"), Description: mcpSchemaText(p, "description"), Type: mcpSchemaText(p, "type"), Required: slices.Contains(required, key), When: when,
			Unit: mcpSchemaText(p, "x-ledger-unit"), ValueSource: mcpSchemaText(p, "x-ledger-source"), Pattern: mcpSchemaText(p, "pattern"), Question: mcpSchemaText(p, "x-ledger-question")}
		field.Enum, _ = p["enum"].([]string)
		field.MaxLength, _ = p["maxLength"].(int)
		fields = append(fields, field)
		fields = append(fields, mcpContractFields(p, path+".", when)...)
	}
	branches, _ := schema["oneOf"].([]any)
	for _, value := range branches {
		branch := value.(mcpSchema)
		discriminator := mcpSchemaText(schema, "x-ledger-discriminator")
		condition := when
		if props, ok := branch["properties"].(mcpSchema); ok {
			if prop, ok := props[discriminator].(mcpSchema); ok {
				values, _ := prop["enum"].([]string)
				if constant, ok := prop["const"].(string); ok {
					values = []string{constant}
				}
				if condition != "" {
					condition += "; "
				}
				condition += prefix + discriminator + "=" + strings.Join(values, "|")
			}
		}
		fields = append(fields, mcpContractFields(branch, prefix, condition)...)
	}
	return fields
}

func (q MCPContractQuery) valid() bool {
	if mcpWriteSchema(q.ToolName) == nil || q.AccountID != "" && !validID(q.AccountID) || q.Date != "" && !validDate(q.Date) || q.Instrument != nil && !q.Instrument.valid() {
		return false
	}
	switch q.ToolName {
	case "ledger_record_trade":
		return q.EntryID == "" && q.Action == "" && q.ReplacementKind == "" && (q.Mode == "" || q.Mode == "trade" || q.Mode == "complete_opening")
	case "ledger_record_dividend":
		return q.EntryID == "" && q.Mode == "" && q.Action == "" && q.ReplacementKind == ""
	case "ledger_set_cash_balance":
		return q.Instrument == nil && q.EntryID == "" && q.Date == "" && q.Mode == "" && q.Action == "" && q.ReplacementKind == ""
	case "ledger_edit_entry":
		return q.Instrument == nil && q.Mode == "" && (q.EntryID == "" || validID(q.EntryID)) && (q.Action == "" || q.Action == "replace" || q.Action == "void") &&
			(q.ReplacementKind == "" || slices.Contains([]string{"buy", "sell", "dividend"}, q.ReplacementKind)) && (q.Action != "void" || q.Date == "" && q.ReplacementKind == "")
	}
	return false
}

func (s MCPService) WriteContract(ctx context.Context, q MCPContractQuery) (MCPWriteContract, error) {
	out := MCPWriteContract{ToolName: q.ToolName, ContractVersion: mcpContractVersion, Timezone: "Asia/Shanghai", KnownContext: map[string]any{}, UnresolvedContext: []string{}, ConditionalRequirements: []MCPConditionalRequirement{}}
	if !q.valid() {
		return out, mcpIssue("invalid_input", "请提供该写工具相关且格式合法的上下文字段", "fix_input", "tool_name", "account_id", "instrument", "entry_id", "date", "mode", "action", "replacement_kind")
	}
	out.InputSchema = mcpWriteSchema(q.ToolName)
	out.Fields = mcpContractFields(out.InputSchema, "", "")
	out.Examples = []map[string]any{mcpContractExample(q)}
	var err error
	out.BusinessDate, _, err = s.Store.cutoff()
	if err != nil {
		return out, err
	}
	var st stockState
	var identity *Instrument
	var previous *StockEntry
	if q.AccountID == "" {
		out.UnresolvedContext = append(out.UnresolvedContext, "account_id")
	} else {
		err = s.Store.db.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			st, err = readStockState(ctx, tx, q.AccountID)
			if err != nil {
				return err
			}
			out.KnownContext["account"], out.KnownContext["version"], out.KnownContext["cash_anchor"] = mcpAccount(st.account), st.version(), st.journal.Cash
			if q.EntryID != "" {
				for _, e := range st.entries {
					if e.ID == q.EntryID {
						copy := e
						previous = &copy
						i := st.instruments[e.InstrumentID]
						identity = &i
					}
				}
				if previous == nil {
					return mcpIssue("entry_not_found", "账户中不存在该流水", "select_existing", "entry_id")
				}
				if previous.Voided {
					return ErrVoided
				}
				out.KnownContext["entry"] = previous
			}
			if q.Instrument != nil {
				for _, i := range st.instruments {
					if i.Market == q.Instrument.Market && i.Code == q.Instrument.Code {
						if identity != nil {
							return ErrCorrupt
						}
						copy := i
						identity = &copy
					}
				}
				if identity == nil {
					var i Instrument
					err := tx.QueryRowContext(ctx, `SELECT id,market,code,name,currency FROM instruments WHERE market=? AND code=? ORDER BY id LIMIT 1`, q.Instrument.Market, q.Instrument.Code).Scan(&i.ID, &i.Market, &i.Code, &i.Name, &i.Currency)
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return err
					}
					if err == nil {
						identity = &i
					}
				}
			}
			if identity != nil {
				if _, code := quoteSymbol(*identity); code != "" || !validID(identity.ID) || !validText(identity.Name) {
					return ErrCorrupt
				}
				out.KnownContext["instrument"] = instrumentJSON{identity.ID, identity.Market, identity.Code, identity.Name, identity.Currency}
			}
			return nil
		})
		if err != nil {
			return out, err
		}
	}
	if q.ToolName == "ledger_set_cash_balance" {
		out.ConditionalRequirements = append(out.ConditionalRequirements, MCPConditionalRequirement{"observed_date", "required", "首次提交必须为当前北京时间业务日；已提交重试先恢复原回执"})
		return out, nil
	}
	fxPath := "fx"
	if q.ToolName == "ledger_edit_entry" {
		fxPath = "replacement.fx"
		status, reason := "undetermined", "先明确replace或void；replace完整提供replacement，void禁止该字段"
		switch q.Action {
		case "replace":
			status, reason = "required", "完整更正，必需金融字段不能省略"
		case "void":
			status, reason = "forbidden", "作废操作禁止replacement"
		default:
			out.UnresolvedContext = append(out.UnresolvedContext, "action")
		}
		out.ConditionalRequirements = append(out.ConditionalRequirements, MCPConditionalRequirement{"replacement", status, reason})
		if previous == nil {
			out.UnresolvedContext = append(out.UnresolvedContext, "entry_id")
		} else if q.ReplacementKind != "" && (q.ReplacementKind == "dividend") != (previous.Kind == "dividend") {
			return out, mcpInvalid("replacement_kind")
		}
		if q.Action == "void" {
			return out, nil
		}
	}
	status, reason := "undetermined", "上下文不足；跨币种需要结算时必填，历史覆盖等分支需读取账户及原流水；写入时重新校验"
	if identity == nil {
		out.UnresolvedContext = append(out.UnresolvedContext, "instrument")
	}
	if q.AccountID != "" && identity != nil && (q.ToolName != "ledger_edit_entry" || q.Action == "replace") {
		if identity.Currency == st.account.Currency {
			status, reason = "optional", "同币种确定使用1；显式提供非1值将拒绝"
		} else if q.Date == "" {
			out.UnresolvedContext = append(out.UnresolvedContext, "date")
		} else {
			required, why := mcpNeedsFX(st, identity.Currency, q.Date, q.Mode == "complete_opening", previous)
			status, reason = "optional", why
			if required {
				status = "required"
			}
		}
	}
	out.ConditionalRequirements = append(out.ConditionalRequirements, MCPConditionalRequirement{fxPath, status, reason})
	return out, nil
}

// Shared by contract discovery and the transactional write check. No prices or
// exchange rates are fetched here, and this explanation never authorizes a write.
func mcpNeedsFX(st stockState, currency Currency, date string, opening bool, previous *StockEntry) (bool, string) {
	if currency == st.account.Currency {
		return false, "同币种确定使用1"
	}
	if previous != nil && previous.Event != nil {
		return true, "跨币种自动分红更正需要实际汇率"
	}
	if st.journal.Cash == nil {
		return false, "未设置现金基准；保存原币事实，不把未知汇率当作已结算汇率"
	}
	if opening || previous != nil && previous.Opening {
		return false, "初始买入补录已由现金基准覆盖"
	}
	a := st.journal.Cash
	if date < a.Date || date == a.Date && previous != nil && previous.Sequence <= a.Sequence {
		return false, "该历史流水已被现金基准覆盖"
	}
	return true, "跨币种且需要现金结算，必须提供实际汇率，方向为每1单位证券币种对应多少账户币种"
}

func mcpContractExample(q MCPContractQuery) map[string]any {
	in := map[string]any{"request_id": "message-demo-001", "account_id": "account-demo", "expected_version": "0"}
	trade := map[string]any{"kind": "buy", "date": "2026-09-06", "quantity": "100", "price": "10", "fee": "0", "currency": "CNY"}
	switch q.ToolName {
	case "ledger_record_trade":
		for k, v := range trade {
			in[k] = v
		}
		delete(in, "kind")
		in["side"], in["instrument"] = "buy", MCPInstrumentSelector{"SH", "600036"}
	case "ledger_record_dividend":
		in["instrument"], in["date"], in["amount"], in["currency"] = MCPInstrumentSelector{"SH", "600036"}, "2026-09-06", "100.00", "CNY"
	case "ledger_set_cash_balance":
		in["observed_date"], in["amount"], in["currency"] = "2026-09-06", "1000.00", "CNY"
	case "ledger_edit_entry":
		in["entry_id"], in["action"], in["reason"] = "entry-demo", "replace", "核对实际成交记录"
		in["replacement"] = trade
		if q.ReplacementKind == "dividend" {
			in["replacement"] = map[string]any{"kind": "dividend", "date": "2026-09-06", "amount": "100.00", "currency": "CNY"}
		}
		if q.Action == "void" {
			in["action"] = "void"
			delete(in, "replacement")
		}
	}
	return in
}
