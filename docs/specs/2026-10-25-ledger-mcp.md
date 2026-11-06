# 投资账本 MCP：设计、契约与实施路线图

日期：2026-10-25。本文描述本分支的 V1 实现及后续规划；发布状态以部署记录为准。

## 1. 目标与交付边界

AI 将已经发生的交易整理成结构化事实，后端完成校验、精确计算和持久化。MCP 与网页共用账本数据与交易内核，覆盖已有账户的完整单股记账闭环。

V1 交付八个工具：账户查询、证券搜索、持仓和流水查询、买卖记账、现金分红、更正／作废、现金余额设置、提交回执查询。提供实际协议调用、数据库迁移、异常恢复、审计和自动化验证。

沿用现有 SH/SZ/HK 股票白名单，包括明确币种的沪深 B 股。账户支持 USD 不等于已经支持美股，证券搜索结果中出现基金也不等于支持基金交易。

批量持仓初始化、原子批量记账、外部资金流、公司行动和更多市场见后续路线图。这些扩展具有独立账务语义。

## 2. 四阶段实施

| 阶段 | 工作 | 交付与完成条件 |
| --- | --- | --- |
| 一：事务内核 | 抽取 applyStockCommandTx；保留 REST 回执格式；增加 MCP 意图规范化、独立回执类别、审计来源 | 原 REST 测试通过；迁移保留旧字节；交易、投影、审计和回执原子提交；持久化失败可回滚 |
| 二：协议接入 | 官方 Go SDK、无状态 HTTP、八个工具 Schema、结果类型、入口及配置 | 实际 SDK 客户端可列举并调用八工具；非法参数无副作用；访问边界与开关有效 |
| 三：AI 使用闭环 | 无行情依赖的查询、版本绑定分页、证券解析、变更摘要、超时恢复、所有写入分支 | 持仓与现金语义清晰；离线可操作已有证券；历史回执可恢复；并发翻页不跨版本拼接 |
| 四：验证交付 | 迁移、并发、故障、历史回放及协议测试；接入与运维文档 | 后端检查、竞态全量测试、构建及 diff 检查；记录实测结果与发布边界 |

逐阶段执行的审阅版完整设计由 OpenChamber 项目 Plans 管理；本规格记录稳定实现契约。测试与实际执行结果见[验收记录](../validation/2026-10-25-ledger-mcp.md)。

## 3. 接入架构

```text
AI 客户端 → Streamable HTTP → Nginx 账本代理
                              ↓ /api/platform/ledger/mcp
                       MCPService + typed tools
                              ↓
网页 → REST stock-book → applyStockCommandTx
                              ↓
          ledger.db：证券、流水、journal、持仓快照、审计、回执
```

- MCP 位于现有 platform 进程内，共享已打开的 Store 和数据库所有权。
- 官方 SDK 固定为 `github.com/modelcontextprotocol/go-sdk v1.8.0`；Schema 使用 `github.com/google/jsonschema-go v0.4.3`。
- 使用 `Stateless=true`、`JSONResponse=true`。业务恢复不依赖 MCP session 或 JSON-RPC id。
- SDK 负责协议版本协商与生命周期；本地测试覆盖实际 SDK 客户端和 2025-11-25 初始化协议。
- 入口精确为 `/api/platform/ledger/mcp`。已有 Nginx 账本规则直接代理，不占用历史档案服务的 `/mcp`。
- 新配置 `LEDGER_MCP_ENABLED` 默认 false，开启要求 `LEDGER_ENABLED=true`；本分支 Compose 配置为 true。
- 账本仍采用既有公开共享访问规则，保留同源写保护，原生无 Origin 客户端与有 Origin 浏览器请求按既有边界处理。
- 请求体上限 64 KiB，HTTP/工具调用预算 20 秒。GET/DELETE 在无状态 SDK 传输下返回 405。

## 4. 代码组织

| 文件 | 职责 |
| --- | --- |
| `backend/internal/modules/ledger/stock_store.go` | 原 REST WriteStock、共用事务内核、正式投影 |
| `backend/internal/modules/ledger/stock_math.go` | 精确回放；为现金／数量错误保留相关流水上下文 |
| `backend/internal/modules/ledger/mcp_types.go` | typed 输入、规范化请求、稳定业务键、冻结结果 DTO |
| `backend/internal/modules/ledger/mcp_commands.go` | 两次回执检查、命令映射、共用内核调用、提交结果与审计 |
| `backend/internal/modules/ledger/mcp_receipts.go` | 严格回执校验、只读恢复查询 |
| `backend/internal/modules/ledger/mcp_instruments.go` | 证券身份解析、本地目录及公开来源查询 |
| `backend/internal/modules/ledger/mcp_queries.go` | 账户筛选、持仓摘要、流水筛选与游标 |
| `backend/internal/modules/ledger/mcp_tools.go` | 八个工具的输入 Schema、说明与绑定 |
| `backend/internal/modules/ledger/mcp_server.go` | SDK HTTP handler、严格 JSON、typed 输出 Schema、工具日志 |
| `backend/internal/modules/ledger/mcp_errors.go` | 稳定业务错误及恢复建议 |
| `backend/internal/modules/ledger/migrations/008_mcp_receipts.sql` | 新增回执 kind，保留旧数据与不可变约束 |
| `backend/internal/app/config.go`、`app.go` | 开关验证与生命周期内挂载 |

## 5. 与现有账本模型的关系

### 5.1 正式数据

`stock_entries` 保存交易和分红，`stock_journals` 保存旧持仓数量与现金基准，`current_holdings` 保存当前正式快照。`audit_log` 和 `idempotency_receipts` 保存不可变证据。

账户外部资金流和历史总资产的正式输入仍是 `account_records`。单股买卖不会生成第二笔入金／出金，也不会自动写一份账户总资产估值。

### 5.2 金融数值

输入和输出使用十进制字符串，内部继续使用定点数：金额与费用 2 位，数量与价格 6 位，FX 8 位。输入允许精度范围内的短格式，规范化后固定小数位；拒绝 JSON number、科学计数法、负数量、超精度和溢出。

价格、费用和成交金额是证券币种；现金与现金差额是账户币种。FX 为“每 1 单位证券币种对应多少账户币种”。同币种省略 FX 时用 1，明确提供时也只能为 1。

输入的 FX 省略与显式值分别保存在意图中；网络重试应保持原参数。数值字符串短格式与等值固定精度格式会规范化为同一请求。

### 5.3 日期与现金基准

交易日期使用北京时间业务日，显式 `YYYY-MM-DD`，不允许未来交易。查询返回业务日期和时区以帮助 AI 解析“今天”。

未设置现金时返回现金 0 与 `cash_anchor:null`；明确设置零余额会返回非空的零金额基准。基准日前和同日已覆盖的交易不会重复扣减余额。需要结算的跨币种交易缺 FX 时拒绝；已有规则允许不结算的历史补录可以保持未知 FX。

现金工具表示“现在实际剩余多少钱”，不表示入金。首次提交的 observed_date 必须为本次事务业务日，已提交的跨日重试先回放原回执。

### 5.4 初始持仓、分红与更正

- `mode=complete_opening` 只允许买入，并要求数量与已有待补录数量相同；以初始买入替代数量基准，避免重复增加持仓。
- 未知历史成本保持 null，不从数量或行情猜测成交。
- 手工分红针对账户已有证券；存在匹配自动分红时返回现有 entry_id，使用更正工具处理实际到账金额。
- 自动分红更正保留事件与除息日、派息日；作废或更正会保留人工覆盖，worker 不会重新覆盖。
- replace 提供完整替换字段，void 提供原因；不能跨证券身份替换，不能在买卖与分红之间转换。
- 更正历史交易触发完整回放，后续超卖或现金不足会使整笔更正回滚。

## 6. 工具契约

调用示例和使用顺序见[接入指南](../guides/ledger-mcp.md)。所有输入对象拒绝未知字段。

### 6.1 公共写入字段

| 字段 | 规则 |
| --- | --- |
| request_id | 必填；1..128 位 ASCII 字母、数字、下划线或连字符；同一业务事件的重试保持不变 |
| account_id | 必填；准确账户 ID |
| expected_version | 必填；持仓 version 字符串，初始为 0；不是 profile_version |
| source_ref | 可选；最多 256 字节的来源引用，作为数据记录 |

文本进行 UTF-8、长度和非法控制字符检查。note 最多 2000 字节，reason 最多 512 字节且非空；不默认保存 AI 对话或附件正文。

### 6.2 查询工具

**ledger_list_accounts**：query 可选，按账户名称包含匹配；limit 默认 20、最多 100；cursor 为上一页 next_cursor。查询先筛选再分页，百分号和下划线按字面匹配。返回账户 ID、名称、币种、开户日期、profile_version、业务日期、时区和 next_cursor。多账户匹配由 AI 向用户澄清。

**ledger_search_instruments**：query 必填，market 可选 SH/SZ/HK。返回明确 market/code/name/currency。精确代码优先读取本地目录，外部查询只发送公开名称／代码。查询本身不新增证券。

**ledger_get_stock_book**：account_id 必填。可选 include_closed、include_entries、entry_id、instrument_id、from、to、include_voided、limit、cursor。默认返回当前持仓摘要，交易数组为空；include_entries=true 才分页返回流水，指定 entry_id 自动启用单条读取。日期只筛选流水，不把当前持仓改成历史持仓。include_voided 用于读取作废记录。

返回账户、version、业务日期、ledger_saved_at、现金、现金基准、证券数量与成本/分红指标，以及有限交易和 entries_next_cursor。行情盈亏可能为 null；查询不等待行情、不触发采集或结算。

**ledger_get_write_result**：account_id、request_id 必填。committed 返回冻结结果；not_found 返回 definitive=false，并指导原样重试／再次查询。账户删除和证据损坏返回错误，不冒充“从未提交”。

### 6.3 写入工具

**ledger_record_trade**：公共字段加 instrument={market,code}、side=buy/sell、date、quantity、price、fee、currency；可选 mode=trade/complete_opening、fx、note。金额由后端计算，手续费为 0 也显式传入；首次证券与首笔交易在一个事务创建。

**ledger_record_dividend**：公共字段加 instrument、date、amount、currency；可选 fx、note。amount 必须为正，不接受股数、成交价和费用字段。

**ledger_edit_entry**：公共字段加 entry_id、action=replace/void、reason。replace 必须给 replacement；void 必须不提供 replacement。replacement 的 kind 为 buy/sell 时需要 date/quantity/price/fee/currency，为 dividend 时需要 date/amount/currency；可选 fx/note。证券身份来自原流水。

**ledger_set_cash_balance**：公共字段加 observed_date、amount、currency。余额非负，currency 与账户一致。返回新的现金基准及实际现金变化。

## 7. 请求规范化与身份解析

规范化请求以 schema_version=1 固定：记录工具名、公共写字段、动作、证券选择、目标流水、余额观察日、原因及业务值。协议 request id、客户端信息、网络地址和外部证券名称不进入幂等请求身份。

身份解析先找当前账户准确 market/code，再查本地目录，最后才查公开提供方。外部响应必须准确匹配市场、代码和受支持币种；新证券 ID 稳定派生。网络位于事务外，事务内再次核对账户版本和证券一致性。

已有回执的恢复先于证券解析，因此外部来源离线不会破坏已提交请求的恢复。已有证券也能在来源离线时继续记账。

## 8. 幂等与事务执行顺序

内部键为 `mcp-stock-` 加规范数组 `[ledger-mcp-stock-v1, account_id, request_id]` 的 SHA-256。新交易 ID 再由内部键稳定派生。

同一账户、同一 request_id 在所有写工具之间共享身份，不同工具／不同业务内容冲突。不同账户同名 request_id 相互独立。source_ref 仅是审计引用，不能替代稳定业务键或被当成跨任意新 ID 自动去重的保证。

执行顺序：

1. 严格解析与规范化请求。
2. 查找既有回执，验证后直接回放。
3. 对新请求进行必要的外部身份解析。
4. 打开既有数据库事务，再次查回执。
5. 验证账户及持仓 expected_version。
6. 调用共用内核：证券新增、流水变化、分红缓存对账、历史回放、现金和持仓投影。
7. 在同一事务内构造变更摘要，写命令审计及回执。
8. 提交成功后唤醒分红 worker；worker 唤醒不是第二次记账结果。

两次回执检查解决并发竞态。事务内先回执、后版本，使旧成功请求在当前账户已有更高版本时仍可恢复。

## 9. 提交结果

`MCPStockWriteResult` 冻结如下信息：

- schema_version、request_id、account_id、tool、action。
- receipt_id、audit_id、recorded_at、business_date。
- version_before、version_after。
- 主流水 entry（现金工具为 null）。
- positions_changed：证券身份、数量前后值／差额、成本前后值、待补录状态。
- cash：币种、before、after、delta。
- cash_anchor_before、cash_anchor_after。
- cash_effect_reason：no_anchor、covered_by_anchor、settled、opening_completion、balance_reset、recalculated。
- related_changes：同事务关联交易变化数量、最多 100 个标识及截断标志。
- notices：未知成本、未知 FX、现金基准覆盖等提示码。

前后状态来自同一事务，不能在写入后通过普通 GET 拼装。主交易金额与整个事务的现金差额分别表达，后者可能同时包含自动分红调整。

重复调用返回同一冻结业务结果；查询当前状态使用 get_stock_book。网页的已有缓存通过重新读取／刷新获取 MCP 写入后的状态。

## 10. 审计与迁移

新增 `008_mcp_receipts.sql`，为回执 kind 加入 mcp_stock_command。迁移复制所有旧行，保留原请求／响应／摘要／审计 ID 字节并恢复不可变触发器，历史 001..007 不改写。

原 REST WriteStock 保留请求序列化、StockWriteResult 和 stock_command 回执；MCP 用独立类型存丰富结果。两入口只共享金融内核。

MCP 命令审计 entity_type=mcp_stock_command，source=human，metadata 记录 schema_version、transport=mcp、tool、request_id、source_ref、reason。自动分红的生成和调整继续使用 source=system；子变更通过 correlation_id 串联。

命令审计需要在不可变 after_json 中包含自身 ID，因此在同一 IMMEDIATE 事务内读取 AUTOINCREMENT 下一个值并核对实际插入 ID。数据库单一所有者、单连接及事务锁是该做法的前提。

恢复校验请求摘要、严格 JSON 形状、规范化意图、结果身份、版本递增、金额差额、主流水、审计关联和来源元数据。损坏返回 data_integrity，不从当前持仓推断成功。

## 11. 分页与一致性

流水按日期降序、同日 sequence 降序。游标绑定账户、持仓版本、业务日期、筛选摘要及上一页末条日期／sequence。

后续页发现版本、业务日期或筛选改变，返回 stale_cursor；应从第一页重新读取。它提供版本不变时的稳定分页，不是无限期持久化快照。游标是有界、严格校验的数据，不是权限凭证。

## 12. 错误和恢复策略

成功为 `{ok:true,data:...}`；业务失败为 `{ok:false,error:{code,message,fields,retry_policy,details}}`，并设置 MCP isError=true。structuredContent 与文本中的 JSON 同源。SDK 对协议及 Schema 的验证错误遵循其 MCP 规范响应。

| 错误 | 处理 |
| --- | --- |
| invalid_input / invalid_precision / currency_mismatch | 修正明确错误的输入 |
| account_not_found / entry_not_found | 重新选择准确账户或流水 |
| instrument_ambiguous / unsupported_instrument | 澄清市场身份或受支持范围 |
| instrument_unavailable / storage_busy | 保留原请求恢复 |
| version_conflict | 重读持仓并核对原意 |
| idempotency_conflict | 查询原 request_id 的回执 |
| stock_quantity / stock_cash | 核对历史交易或实际余额；整个事务已拒绝 |
| fx_required | 提供实际结算汇率 |
| dividend_conflict | 更正返回的现有分红流水 |
| operation_voided | 查看已作废记录及历史 |
| invalid_observed_date | 获取当前业务日的实际余额事实 |
| stale_cursor | 从第一页重新读取 |
| request_timeout / write_status_unknown | 查询回执并原样重试，不自动换 ID |
| data_integrity | 停止写入并报告证据异常 |

“暂未查到回执”不意味着正在执行的原请求不可能提交。客户端不能把传输超时当作确定失败。更正后的新意图与网络重试应明确区分。

## 13. 验证与运维

定向验证覆盖分数股与精确舍入、清仓重买、基准历史补录、显式零余额、跨币种、初始持仓补录、自动分红冲突／覆盖／作废、历史更正失败、同键并发、CAS、不同账户业务键、各持久化节点回滚、重启和响应丢失恢复、在途缺失回执、来源离线、分页失效、回执损坏及账户删除。

协议验证使用真实 SDK 客户端和本地 httptest，检查全部工具、严格输入、结构化输出与文本一致性、旧协议初始化、Origin、请求体限制和模块开关。数据使用隔离临时数据库、固定时钟和合成行情。

执行 `make check`、`make test`、`make build` 和 `git diff --check`，记录实际结果。独立 E2E、真实来源抓取及生产写入不属于本次验证。

迁移器拒绝比程序已知更多的迁移。008 升级后优先关闭 MCP 开关，继续运行了解 008 的兼容程序；不能直接换回只知道 007 的旧程序。备份／恢复使用 SQLite 一致性机制，保留生产卷和历史迁移证据。

## 14. 后续路线图

1. **V1.1：初始化与原子批量。** 单独表达未知成本的当前数量，已有交易账户继续由流水管理；批量限定同一账户、有序条目、全成全败、行级错误与统一幂等。
2. **V1.2：外部资金流。** 接入 account_records，明确入金／出金是否同时更新现金，以及跨两类记录的原子命令；账户内部渠道调拨不重复计入外部资金流。
3. **V1.3：公司行动与更多市场。** 建立拆股、送转、合股、转托管事件；证券范围扩展同时补齐行情、币种、费用与分红来源。
4. **V1.4：账单与对账。** 稳定来源编号、跨批次去重、差异报告；对账结果通过确定性的记账命令落账。

## 15. 参考

- [既有单股账本规则](2026-09-25-stock-journal.md)
- [账户渠道规则](2026-09-25-ledger-channels.md)
- [官方 Go MCP SDK](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp)
- [MCP HTTP 传输规范](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)
