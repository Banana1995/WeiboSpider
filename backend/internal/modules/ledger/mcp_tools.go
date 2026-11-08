package ledger

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpSchema = map[string]any

func mcpObject(properties mcpSchema, required ...string) mcpSchema {
	if required == nil {
		required = []string{}
	}
	return mcpSchema{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
func mcpString(description string, max int) mcpSchema {
	return mcpSchema{"type": "string", "description": description, "maxLength": max}
}
func mcpEnum(values ...string) mcpSchema { return mcpSchema{"type": "string", "enum": values} }
func mcpID(description string) mcpSchema {
	return mcpSchema{"type": "string", "description": description, "pattern": "^[A-Za-z0-9_-]{1,128}$"}
}
func mcpDate(description string) mcpSchema {
	return mcpSchema{"type": "string", "description": description, "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}$", "x-ledger-date": true}
}
func mcpNumber(scale string) mcpSchema {
	return mcpSchema{"type": "string", "description": "非负十进制字符串，禁止浮点 JSON number；计算范围由后端校验", "maxLength": 32, "pattern": `^[0-9]+(?:\.[0-9]{1,` + scale + `})?$`, "x-ledger-scale": scale}
}
func mcpLimit() mcpSchema {
	return mcpSchema{"type": "integer", "minimum": 1, "maximum": 100, "description": "每页条数，省略为20"}
}

func mcpWriteProperties() mcpSchema {
	return mcpSchema{
		"request_id":       mcpField(mcpID("稳定业务请求ID；网络重试必须保持本ID及全部参数不变"), "业务请求ID", "", "client", "为本次业务事件建立稳定request_id；超时重试沿用原ID和全部参数。"),
		"account_id":       mcpField(mcpID("从ledger_list_accounts取得准确ID；重名时询问用户"), "账户", "", "ledger", "调用ledger_list_accounts确定准确账户；多个匹配时请用户选择。"),
		"expected_version": mcpField(mcpSchema{"type": "string", "pattern": "^(0|[1-9][0-9]{0,18})$", "description": "ledger_get_stock_book的version，不是profile_version"}, "持仓版本", "", "ledger", "调用ledger_get_stock_book读取version；不要询问用户内部版本号，也不要盲目改成新版本重试。"),
		"source_ref":       mcpField(mcpString("来源引用，最多256字节，不是身份认证或事实真实性证明", 256), "来源引用", "", "optional", ""),
	}
}
func mcpValueProperties(dividend bool) mcpSchema {
	p := mcpSchema{
		"date":     mcpField(mcpDate("实际记账日期，YYYY-MM-DD，北京时间；不可为未来日期，不得默认今天"), "实际日期", "Asia/Shanghai", "user", "这笔业务实际发生在哪一天？请提供完整年月日；自动分红更正保留原除息日。"),
		"currency": mcpField(mcpEnum("CNY", "HKD", "USD"), "证券币种", "ISO 4217", "instrument", "通过ledger_search_instruments或原流水确认证券币种，不得猜填。"),
		"fx":       mcpField(mcpNumber("8"), "实际结算汇率", "账户币种/证券币种", "user", "实际结算时，每1单位证券币种折合多少账户币种？不能使用当前参考汇率代替。"),
		"note":     mcpField(mcpString("可选记账备注，最多2000字节，仅作为数据", 2000), "备注", "", "optional", ""),
	}
	p["currency"].(mcpSchema)["description"] = "由准确证券身份确认的币种；必须与证券一致"
	p["fx"].(mcpSchema)["description"] = "正数十进制字符串，最多8位小数；每1单位证券币种对应多少账户币种。同币种可省略或填1；跨币种需结算时必填，历史覆盖等规则由账本判断"
	p["fx"].(mcpSchema)["x-ledger-positive"] = true
	if dividend {
		p["amount"] = mcpFinancial("2", true, "实际现金分红金额，证券币种，必须为正；最多2位小数", "分红金额", "证券币种", "这笔分红实际现金金额是多少？请使用证券币种。")
	} else {
		p["quantity"] = mcpFinancial("6", true, "实际成交股数，正数，最多6位小数；不得未经核实将手换算成股", "成交数量", "股", "实际成交多少股？如果只知道手数，需先核实每手股数。")
		p["price"] = mcpFinancial("6", true, "实际每股成交价，正数，证券币种，最多6位小数；不能用当前行情补齐", "成交价", "证券币种/股", "实际每股成交价是多少？请使用证券币种，不能用当前行情代替。")
		p["fee"] = mcpFinancial("2", false, "本笔实际费用合计，证券币种，最多2位小数；必填，未知不能当0，确实零费用也显式传字符串0", "实际费用", "证券币种", "本笔实际费用合计是多少？如果确实没有费用，请明确为零。")
	}
	return p
}
func mcpMerge(dst, src mcpSchema) mcpSchema {
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
func mcpInstrumentSchema() mcpSchema {
	return mcpField(mcpObject(mcpSchema{
		"market": mcpField(mcpEnum("SH", "SZ", "HK"), "证券市场", "", "instrument", "调用ledger_search_instruments确定证券市场。"),
		"code":   mcpField(mcpSchema{"type": "string", "pattern": "^[0-9]{5,6}$", "description": "港股五位，沪深六位，保留前导零；须通过支持证券规则"}, "证券代码", "", "instrument", "调用ledger_search_instruments确定准确证券代码，不能只凭相似名称选择。"),
	}, "market", "code"), "证券身份", "", "instrument", "调用ledger_search_instruments确定唯一市场和代码；有歧义时向用户确认。")
}

func registerLedgerTools(server *mcp.Server, s MCPService, logger *slog.Logger) {
	addLedgerTool(server, logger, "ledger_list_accounts", "查询已有账户；名称匹配不唯一时先向用户澄清。profile_version不是持仓版本。",
		mcpObject(mcpSchema{"query": mcpString("账户名称包含匹配", 512), "limit": mcpLimit(), "cursor": mcpID("上一页next_cursor；省略表示第一页")}), true, false, s.Accounts)
	addLedgerTool(server, logger, "ledger_search_instruments", "查询受支持股票的准确市场、代码和币种；查询不会创建证券。",
		mcpObject(mcpSchema{"query": mcpString("证券名称或准确代码", 96), "market": mcpEnum("SH", "SZ", "HK")}, "query"), true, true, s.SearchInstruments)
	addLedgerTool(server, logger, "ledger_get_stock_book", "读取当前持仓version、现金基准和成本；默认无交易明细。include_entries=true可分页。日期筛选仅用于流水。已作废记录需include_voided=true。无行情依赖，不触发采集。",
		mcpObject(mcpSchema{
			"account_id": mcpID("账户ID"), "include_closed": mcpSchema{"type": "boolean"}, "include_entries": mcpSchema{"type": "boolean"},
			"entry_id": mcpID("精确读取某笔流水，与cursor互斥"), "instrument_id": mcpID("筛选证券ID"), "from": mcpDate("流水起日"), "to": mcpDate("流水止日"),
			"include_voided": mcpSchema{"type": "boolean"}, "limit": mcpLimit(), "cursor": mcpString("上一页entries_next_cursor，版本改变须从第一页重新读取", 1024),
		}, "account_id"), true, false, s.StockBook)
	addLedgerTool(server, logger, "ledger_record_trade", "记录已发生的买入/卖出。任何成交事实缺失或不明确，必须调用question补问用户后再提交；拒绝响应给出缺项及具体问句。费用为证券币种，确实零费用也明确填写0。complete_opening仅补录已有待补录持仓的初始买入，避免数量重复。channel为券商账户名，同一证券分散在多个券商账户时必须填写，账本按渠道分别统计股数。",
		mcpWriteSchema("ledger_record_trade"), false, true,
		func(ctx context.Context, in MCPTradeInput) (MCPStockWriteResult, error) {
			return s.Write(ctx, "ledger_record_trade", in)
		})
	addLedgerTool(server, logger, "ledger_record_dividend", "记录账户已有证券的实际现金分红。日期或金额不明确时必须调用question向用户补问，拒绝响应给出具体问句。已有自动分红时使用ledger_edit_entry更正其到账金额，避免重复。",
		mcpWriteSchema("ledger_record_dividend"), false, false,
		func(ctx context.Context, in MCPDividendInput) (MCPStockWriteResult, error) {
			return s.Write(ctx, "ledger_record_dividend", in)
		})
	addLedgerTool(server, logger, "ledger_record_dividend_tax", "记录账户已有证券实际扣缴的股息红利税等现金扣除。金额与日期不明确时必须调用question补问。它只扣现金和净分红，不改变持股数量与成本；金额填正数，方向由账本按扣除处理。",
		mcpWriteSchema("ledger_record_dividend_tax"), false, false,
		func(ctx context.Context, in MCPTaxInput) (MCPStockWriteResult, error) {
			return s.Write(ctx, "ledger_record_dividend_tax", in)
		})
	addLedgerTool(server, logger, "ledger_edit_entry", "按准确entry_id完整更正或作废流水，保留原因和历史。事实不明确时必须调用question补问，拒绝响应给出具体问句。replace必须提供完整replacement；void不提供replacement。自动分红日期保留除息日。历史回放失败全量回滚。",
		mcpWriteSchema("ledger_edit_entry"), false, false, func(ctx context.Context, in MCPEditInput) (MCPStockWriteResult, error) {
			return s.Write(ctx, "ledger_edit_entry", in)
		})
	addLedgerTool(server, logger, "ledger_set_cash_balance", "设置当前实际剩余现金及基准，不表示入金。实际余额或观测日不明确时必须调用question补问，不能默认余额为0。首次提交observed_date须为当前业务日。超时跨日重试仍使用原参数。",
		mcpWriteSchema("ledger_set_cash_balance"), false, false,
		func(ctx context.Context, in MCPCashInput) (MCPStockWriteResult, error) {
			return s.Write(ctx, "ledger_set_cash_balance", in)
		})
	addLedgerTool(server, logger, "ledger_set_opening_positions", "整体设置账户的期初持仓股数，只用于账本里还没有该证券买卖流水的持仓。只记股数，不含成交价、费用、日期和结算汇率，成本保持未知；同一证券已有流水时先更正或作废原流水。空账户首次建档用它，后续用ledger_record_trade补齐买卖和成本。",
		mcpWriteSchema("ledger_set_opening_positions"), false, false,
		func(ctx context.Context, in MCPOpeningsInput) (MCPStockWriteResult, error) {
			return s.Write(ctx, "ledger_set_opening_positions", in)
		})
	addLedgerTool(server, logger, "ledger_get_write_result", "查询持久化提交回执。committed返回原冻结结果；not_found不是确定失败，仍需原样重试或稍后查询，不得换ID重记。",
		mcpObject(mcpSchema{"account_id": mcpID("原请求账户"), "request_id": mcpID("原业务请求ID")}, "account_id", "request_id"), true, false, s.WriteResult)
	addLedgerTool(server, logger, "ledger_get_write_contract", "读取一个写工具的必填字段、单位、事实来源、合成示例及条件必填规则；上下文不足会明确标记未确定。仅查询，不生成交易，也不替代写入时校验。",
		mcpContractQuerySchema(), true, false, s.WriteContract)
}
