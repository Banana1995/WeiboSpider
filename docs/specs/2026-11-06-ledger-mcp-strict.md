# 投资账本 MCP 严格字段契约

本轮承接 [V1 设计](2026-10-25-ledger-mcp.md)，保留已实现的账务内核、幂等键和 `schema_version=1` 冻结回执。服务版本为 1.1.0，字段契约版本为 `2026-11-06`，新增 `ledger_get_write_contract`，总计九工具。

## 目标与事实来源

AI 描述已发生的记账事实，服务端强制验证其完整性、精度和账务关系。实际成交日期、股数、价格、费用、分红和余额缺失或不明确时，AI 必须调用客户端 question 工具取得明确数据后再提交。费用未知不能补零，日期未知不能补今天；证券和版本等确定元数据通过查询工具取得。

服务端无法仅凭一个合法数字证明它来自用户真实成交。Server instructions 和四个写工具的 description 明确禁止猜填；来源引用仅用于关联，不能当作真实性证明。

## 同源契约

`mcpWriteSchema` 是各写工具的 Schema 来源。字段定义携带中文说明、单位、事实来源和补问模板，统一用于：

1. `tools/list` 的 `inputSchema`。
2. `ledger_get_write_contract` 的字段列表、Schema 与条件解释。
3. 拒绝响应的 `requirements` 与 `questions`。

必填字段、单位和合成输入详见[接入指南](../guides/ledger-mcp.md#严格字段与-question-补问)。字段查询只读；金融值、回执与账户状态不会因查询而改变。上下文不足时返回 `unresolved_context` 和 `undetermined`，不会默认为无须汇率。

## 校验与错误

- 保留入口原始 JSON 校验：重复键、多顶层对象、非法 UTF-8、内容类型和 64 KiB 上限。
- 使用官方 SDK 原始 `Server.AddTool` handler，在类型解码前显式验证完整 JSON Schema，保留缺失/null/空串/零的差别。
- 已知工具的参数问题返回 `isError=true`、结构化错误以及一致的文本 JSON。
- 字段问题一次汇总可确定的缺项；业务类型未定时只报告确定的共同要求，避免把买卖字段误要求到分红。
- 数值严格使用固定精度解析，金额和费用 2 位、数量/价格 6 位、汇率 8 位；超精度拒绝。整数协议计数精确解析，金融数值不经过 float64。
- 保留 typed 服务层和事务层的第二层业务检查。账户、证券、币种、版本、现金基准、历史超卖及分红规则仍由原记账内核决定。
- 直接验证输出序列化后的数据与公布的 OutputSchema；响应验证失败可能发生于提交之后，此时返回状态未知并指导回执恢复。

固定依赖的 jsonschema-go v0.4.3 在类型检查时把 `json.Number` 视为 string。适配层将协议整数转换为精确 int64，并单独验证真实 JSON 字符串类型，避免数字被当作金额字符串；保留独立输出 Schema 和 JSON number 拒绝测试。

错误保留原有 `code`、`message`、`fields`、`retry_policy`、`details`，补充：

- `tool`、`contract_version`。
- `missing_fields`、`invalid_fields`（字段路径、原因码、解释）。
- `requirements`（字段含义、格式、单位、来源）。
- `questions`（字段、具体问句、执行动作）。
- `question_tool=question`、`next_action=ask_user_with_question_then_resubmit`：要求宿主 AI 使用 question 补问。
- `truncated`：超过 32 个字段问题时明确截断。

技术字段的动作是读取账户或证券上下文，而不是要求用户回答内部 ID/版本号。无 question 工具的客户端停在提问环节，不继续猜填或写入。

## 原子性与恢复

成功入账仍使用既有单事务：证券、流水、journal、现金/持仓、审计、回执全成或全败。失败调用不唤醒分红 worker。

`write_effect` 位于响应外层，不修改冻结回执：

- `none`：可以确定本次调用无账务写入；不代表历史上同请求从未成功。
- `committed`：返回已提交的结果，包括原成功回执重放。
- `unknown`：传输、存储或响应处理存在不确定性，必须查询原回执或原样重试。

同一业务事件沿用原 request_id。回执先于外部解析和版本检查，事务内再次查询；not_found 不证明原请求不会继续提交。

新增字段查询与严格错误不需要额外数据迁移，首次集成仍需要既有 008 MCP 回执迁移。升级与回退规则见[部署运维](../deploy-prep.md)。

## 验证

本轮增加独立的必填业务验收清单，逐项移除/置 null/置空，经真实 SDK 客户端调用并对比全部相关表的逻辑内容、版本、审计、回执和后台唤醒。另验证 question 补问后提交、显式零、Schema 一致性、FX 分支、嵌套字段路径、输出 Schema、未知字段与有界诊断。

实际命令和结果见[本轮验收](../validation/2026-11-06-ledger-mcp.md)。
