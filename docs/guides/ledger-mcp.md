# 投资账本 MCP 接入指南

实现契约见[设计与路线图](../specs/2026-10-25-ledger-mcp.md)、[严格字段契约](../specs/2026-11-06-ledger-mcp-strict.md)，验证结果见[本轮验收记录](../validation/2026-11-06-ledger-mcp.md)。

## 启用和连接

传输：**remote Streamable HTTP**，服务名 `investment-ledger`，版本 1.1.0，共九个工具。

- 本地入口：`http://127.0.0.1:5051/api/platform/ledger/mcp`。
- 公网入口：`http://43.130.247.183:5052/api/platform/ledger/mcp`（2026-11-06 已部署验证）。
- 账本沿用公开共享授权，MCP 不要求 OAuth 或账本令牌；跨站浏览器写保护仍有效。
- `LEDGER_MCP_ENABLED=true` 且 `LEDGER_ENABLED=true` 时注册路由。本地默认关闭，目标 Compose 已配置开启。
- 回执与客户端会话无关；不需要保存 Mcp-Session-Id。

本地开发先把 `LEDGER_DEV_DATA_DIR` 设置为独立本地测试目录，再在 backend 下运行：

```bash
LEDGER_ENABLED=true LEDGER_MCP_ENABLED=true \
BACKEND_DATA_DIR="${LEDGER_DEV_DATA_DIR:?请先设置隔离的本地数据目录}" \
go run ./cmd/server
```

此配置使用现有账本服务生命周期。读工具本身不触发采集或后台结算。

客户端选择其“远程 MCP / Streamable HTTP”接入方式，填入对应 URL。具体客户端的私有配置格式应按该客户端文档使用。

## 推荐记账顺序

1. `ledger_list_accounts` 确定准确 account_id。重名账户先询问，不自动选第一项。
2. `ledger_search_instruments` 确定证券市场、代码与币种。
3. `ledger_get_stock_book` 读取 version、现金基准和已有流水。
4. 按需调用 `ledger_get_write_contract` 了解该写工具的字段、单位及条件必填规则。成交事实缺失或含糊时，**必须调用客户端的 `question` 工具向用户补问具体数据**，得到明确答案后使用稳定 request_id 提交。客户端没有 question 工具时，停在向用户提问环节。
5. 向用户说明提交结果中的数量、金额、现金变化和提示。
6. 如需当前状态，再读 get_stock_book；回执可能来自过去的成功提交。

所有示例均为合成数据；账户 ID、流水 ID、持仓版本和业务日期必须以实际读取结果及用户事实为准。

## 严格字段与 question 补问

六个写工具都必须提供 `request_id`、`account_id`、`expected_version`。其他必填业务字段如下；精确 JSON Schema 可通过 `tools/list` 或字段契约查询获取。

| 工具 | 必填业务字段 |
| --- | --- |
| `ledger_record_trade` | `instrument.market`、`instrument.code`、`side`、`date`、`quantity`、`price`、`fee`、`currency` |
| `ledger_record_dividend` | `instrument.market`、`instrument.code`、`date`、`amount`、`currency` |
| `ledger_set_cash_balance` | `observed_date`、`amount`、`currency` |
| `ledger_set_opening_positions` | `positions[].instrument.market`、`positions[].instrument.code`、`positions[].quantity` |
| `ledger_record_dividend_tax` | `instrument.market`、`instrument.code`、`date`、`amount`、`currency` |
| `ledger_edit_entry` | `entry_id`、`action`、`reason`；replace 还要求完整 `replacement`，void 禁止该字段 |

买卖更正的 replacement 必填 `kind`、`date`、`quantity`、`price`、`fee`、`currency`；分红更正必填 `kind`、`date`、`amount`、`currency`。跨币种需要现金结算时 `fx` 条件必填，同币种省略确定为 1；具体现金基准覆盖与分红规则由字段查询解释，写入事务重新验证。

- 数量和价格最多 6 位小数，金额和费用最多 2 位，汇率最多 8 位；全部使用十进制字符串。
- 缺失、null、空字符串、估计值、未知金额不能转成零。**零手续费也必须由用户明确。**
- 未提供日期不能默认今天，实际成交价不能用现价代替，手数未经核实不能当成股数。
- 账户 ID、证券身份、币种及版本可从查询工具取得；不要向用户索取内部版本号。
- 备注和来源引用可选；`mode` 默认 trade，初始买入补录明确使用 complete_opening。

例如只说“买了100股腾讯”，AI 应通过 question 补问账户（如不明确）、实际日期、每股成交价和实际费用；不能先写一笔不完整的账。

字段查询示例：

```json
{"tool_name":"ledger_record_trade","account_id":"account-demo","instrument":{"market":"HK","code":"00700"},"date":"2026-11-05"}
```

输出包含 `input_schema`、`fields`、`conditional_requirements`、`known_context`、`unresolved_context`、`examples`。上下文不全时 FX 要求标记 `undetermined`，不能解释为不需要 FX。查询仅使用账本中已知证券身份，不会为了展示字段而创建证券或访问行情来源。

缺项拒绝的核心字段示例：

```json
{
  "ok": false,
  "write_effect": "none",
  "error": {
    "code": "incomplete_entry",
    "missing_fields": ["date", "price", "fee"],
    "question_tool": "question",
    "next_action": "ask_user_with_question_then_resubmit",
    "questions": [
      {"field":"date","question":"这笔业务实际发生在哪一天？请提供完整年月日。","next_action":"question"},
      {"field":"price","question":"实际每股成交价是多少？请使用证券币种。","next_action":"question"},
      {"field":"fee","question":"本笔实际费用合计是多少？如果确实没有费用，请明确为零。","next_action":"question"}
    ]
  }
}
```

完整错误还返回拒绝原因、字段格式、单位、错误位置和恢复策略。`invalid_fields` 解释非法类型、空值、格式或分支冲突；嵌套字段使用 `replacement.fee` 等准确路径。错误问题最多 32 项，超过时 `truncated=true`。

`question_tool=question` 是对 AI 的明确调用指引，由宿主 AI 调用其 question 工具；MCP 服务本身不代替用户回答。AI 应按问句补问，不能只是向用户重复一个错误码然后自行猜填。`write_effect=none` 表示本次调用没有产生账务写入，不证明此前的同一业务事件从未提交。

## 查询示例

账户搜索：

```json
{"query":"港股","limit":20}
```

证券搜索：

```json
{"query":"腾讯","market":"HK"}
```

当前持仓：

```json
{"account_id":"account-demo"}
```

分页交易：

```json
{
  "account_id":"account-demo",
  "include_entries":true,
  "from":"2026-01-01",
  "to":"2026-10-25",
  "limit":20
}
```

下一页保持筛选条件，并传回 entries_next_cursor。stale_cursor 表示持仓版本、业务日或条件变化，应从第一页重新读取。

精确读取需要更正的流水：

```json
{"account_id":"account-demo","entry_id":"entry-demo","include_voided":true}
```

## 买入／卖出

工具：`ledger_record_trade`。

```json
{
  "request_id":"msg-demo-001-item-1",
  "account_id":"account-demo",
  "expected_version":"12",
  "instrument":{"market":"HK","code":"00700"},
  "side":"buy",
  "date":"2026-10-23",
  "quantity":"100.000000",
  "price":"500.000000",
  "fee":"10.00",
  "currency":"HKD",
  "note":"示例成交"
}
```

卖出使用 side=sell。quantity 为股数，不能直接把“手”当作股。价格和费用使用证券币种。费用未知时先补问，零费用明确传 0。

账户和证券同币种时 FX 可省略；不同币种需要结算时，例如每 1 港币对应 0.9 元账户人民币，使用 `fx:"0.90000000"`。

已有待补录持仓的初始买入使用 `mode:"complete_opening"`，数量必须与待补录数量一致。只有当前数量、缺少成交事实时不应猜测交易。

## 实际现金分红

工具：`ledger_record_dividend`。

```json
{
  "request_id":"msg-demo-002-item-1",
  "account_id":"account-demo",
  "expected_version":"13",
  "instrument":{"market":"HK","code":"00700"},
  "date":"2026-06-01",
  "amount":"480.00",
  "currency":"HKD"
}
```

dividend_conflict 会给出现有自动分红的 entry_id，应更正该记录，而不是再录一笔。分红工具不接受交易数量、成交价或费用字段。

## 更正与作废

工具：`ledger_edit_entry`。

更正必须提供完整替换字段：

```json
{
  "request_id":"msg-demo-003-item-1",
  "account_id":"account-demo",
  "expected_version":"14",
  "entry_id":"entry-demo",
  "action":"replace",
  "reason":"核对成交价",
  "replacement":{
    "kind":"buy",
    "date":"2026-10-23",
    "quantity":"100.000000",
    "price":"499.000000",
    "fee":"10.00",
    "currency":"HKD"
  }
}
```

更正分红的 replacement 使用 kind=dividend、date、amount、currency 及所需 FX。自动分红 date 保留原流水中的除息日，不擅自改为派息日。

作废保留历史：

```json
{
  "request_id":"msg-demo-004-item-1",
  "account_id":"account-demo",
  "expected_version":"15",
  "entry_id":"entry-demo",
  "action":"void",
  "reason":"重复录入"
}
```

历史更正或作废造成后续超卖、现金不足时整次回滚，错误 details 可指出失败流水和日期。

## 期初持仓（只有股数）

工具：`ledger_set_opening_positions`。

```json
{
  "request_id":"msg-demo-006-item-1",
  "account_id":"account-demo",
  "expected_version":"0",
  "positions":[
    {"instrument":{"market":"SH","code":"600036"},"quantity":"1300"},
    {"instrument":{"market":"HK","code":"00700"},"quantity":"1900"}
  ]
}
```

用于账户里还没有该证券买卖流水、只知道当前实际持股数量的情况：整体替换期初持仓清单，同一证券只能出现一次，最多 200 项。它只写股数——不写成交价、费用、日期和结算汇率，成本保持未知（结果提示 `unknown_opening_cost`）。同一证券已有流水时整笔拒绝（`opening_conflict`），要先更正或作废原流水，避免数量重复。

后续补齐真实买入时，用 `ledger_record_trade` 的 `mode=complete_opening`：数量必须与该证券的期初股数一致，提交后该证券变成真实买入流水并带上成本，其余期初持仓保持不变。

## 股息红利税

工具：`ledger_record_dividend_tax`。

```json
{
  "request_id":"msg-demo-007-item-1",
  "account_id":"account-demo",
  "expected_version":"12",
  "instrument":{"market":"SH","code":"601225"},
  "date":"2026-11-02",
  "amount":"90.90",
  "currency":"CNY",
  "note":"股息红利税补缴"
}
```

红利税是现金扣除：`amount` 填正数，账本按流出处理，现金台账减少同样的金额。分红金额保持税前口径，扣税单独计入该证券的 `metrics.tax`，个股累计盈亏按税前分红减红利税结算。它不改变持股数量、成交价和成本；也不需要费用字段。多笔同日补缴分多笔提交，各自使用稳定 request_id。

## 当前实际现金

工具：`ledger_set_cash_balance`。

```json
{
  "request_id":"msg-demo-005-item-1",
  "account_id":"account-demo",
  "expected_version":"16",
  "observed_date":"2026-10-25",
  "amount":"29990.00",
  "currency":"HKD"
}
```

这是当前剩余现金基准，不是新增投入本金。observed_date 首次提交须为当前北京时间业务日。明确的零余额合法；cash_anchor=null 则表示未设置基准。

## 结果说明

业务成功返回 structuredContent：

```json
{"ok":true,"data":{"schema_version":1,"request_id":"...","version_before":"12","version_after":"13"}}
```

以上只是结果结构节选。完整 data 还包含主流水、receipt_id、audit_id、数量／成本前后状态、实际现金变化、现金基准、关联变更与提示码。

- entry.amount 是主交易的证券原币净金额。
- cash.delta 是该事务实际改变的账户币种现金，可能受到基准覆盖和分红对账影响。
- cash_effect_reason 说明为何结算、已覆盖、无基准或重新计算。
- 成本未知为 null；不能把它当作成本 0。
- positions_changed 的 delta=0 也可能表示成本、待补录或分红指标发生变化。

## 超时和重复请求

request_id 是业务身份，不是每次调用随机生成的 ID。有券商成交编号时使用“来源系统＋成交编号”；否则使用“消息 ID＋条目序号”。同日期同价格同股数可能是两笔真实成交，不能据此自动去重。

超时、断线或不确定错误之后调用 `ledger_get_write_result`：

```json
{"account_id":"account-demo","request_id":"msg-demo-001-item-1"}
```

- committed：返回原成功回执，可向用户说明已经记账。
- not_found、definitive=false：暂未找到成功证据，不代表原请求不能继续提交。保持原 request_id、版本及所有参数重试，或再次查询。
- idempotency_conflict：同一请求 ID 已对应其他内容，先查原回执，明确究竟是重试还是新的业务操作。
- version_conflict：重新读取持仓并核对本次意图，不能自动覆盖成最新版本写入。

不同账户的同名 request_id 分开处理；同一账户的不同写工具不能重用同一个 request_id。

## 运维与回退

- `/healthz` 是健康检查，MCP 地址本身的 GET 返回 405 是无状态传输的正常行为。
- 日志记录工具名、耗时、结果码和哈希关联 ID；审计中可按命令来源定位正式变更。
- 008 迁移扩展回执类型，历史回执保留。迁移后用 `LEDGER_MCP_ENABLED=false` 关闭功能时仍需运行了解 008 的兼容程序。
- 生产验证使用初始化、工具列表和读工具。写入协议验证在合成数据库完成。
- 当前仓库的共同部署和 SQLite 备份／恢复规则见[运维说明](../deploy-prep.md)。
