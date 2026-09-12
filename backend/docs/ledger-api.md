# 记账 HTTP 接口

更新：2026-09-12，描述当前开发工作树，不代表生产已部署此版本。账户采用直接当前输入与独立固定历史，不提供旧库迁移、兼容执行路径或自动清库。设计、测试覆盖及验证命令见[当前模型](../../docs/specs/2026-09-12-ledger-current-inputs.md)。[旧接口归档](ledger-api-before-current-inputs.md)仅供历史参考，不可作为调用指南。

## 1. 启用与访问边界

- `LEDGER_ENABLED` 默认关闭；启用后使用独立的 `BACKEND_DATA_DIR/ledger.db`，不读取或修改微博、白酒库。使用新的空数据目录开发；已有结构校验失败时不会删除或重置数据库。
- 账本按既有用户授权匿名共享全部读写，无登录或用户隔离。任何访客都能修改数据，请勿上传私人财务信息。
- 仅规范 `/api/platform/ledger` 及其子路径免服务令牌。保留同源写保护、Host 校验、请求限制、校验、事务、幂等和审计，不提供跨域放行。
- Nginx 清除账本 Authorization 并保留含端口的 Host；Go 5051 仍是内部服务端口。Vite 开发代理需显式 `LEDGER_DEV_PROXY=true`，仅使用回环 HTTP 后端，不是公开部署方案。
- 本次未访问生产、提交、推送或部署，未运行 E2E/真实来源。启动及代理配置见[前端说明](../../frontend/README.md#本地记账)。

## 2. 通用契约

相对路径前缀均为 `/api/platform/ledger`。成功响应直接返回 JSON，不包 `success/data`。所有 GET 支持 HEAD，HEAD 无正文。响应使用 `Cache-Control: no-store` 和 `X-Content-Type-Options: nosniff`。

- JSON 写入必须为未压缩 `application/json`，最多 64 KiB，只接受一个对象。拒绝未知、重复、大小写/Unicode 别名字段、非法 UTF-8、尾随 JSON；键为精确 snake_case ASCII。
- 金额、数量、价格和汇率必须用十进制字符串。金额为 int64 分，数量/价格最多六位小数，汇率最多八位；版本、ID 和游标保持字符串，不能经过浮点数转换。
- ID、幂等键为 1..128 位 ASCII 字母、数字、下划线或连字符。金额为 `null` 与明确 `"0.00"` 不同。
- 日期为真实公历日期 `YYYY-MM-DD`，时间戳为带时区的 RFC3339。独立历史记录可在账户开始日期之前或服务器今天之后；未来记录不会成为今天的资产/收益端点。
- 写入不接受查询参数；列表默认 30、`limit=1..100`。拒绝未知、重复、空值或非法查询参数。分析接口另外有 10,000 条记录与变更上限。
- 账户创建、当前输入、账户记录和导入确认共用全局 `Idempotency-Key` 空间。先检查已提交回执，再检查当前版本；已提交原请求重试返回原结果，不恢复或覆盖后来的修改。
- 未提交失败不占用键；不确定结果必须保持原 body、ID、预期版本和 key 重试。CAS 冲突不能自动改成最新版本覆盖。

## 3. 接口清单

| 方法及相对路径 | 行为 |
| --- | --- |
| `POST /accounts` | 幂等创建账户元信息，201 |
| `GET /accounts`、`GET /accounts/{id}` | 列表 / 元信息与独立当前现金 |
| `GET /instruments/search?code=...` | 查询沪深港完整证券代码身份，只读 |
| `GET /instruments` | 不可变证券身份目录，按 ID 分页 |
| `POST /instruments` | 底层不可变目录插入，201；当前 UI 不使用独立登记流程 |
| `GET/PUT /accounts/{id}/current-holdings` | 当前现金/数量快照；整份 CAS 替换，200 |
| `GET /accounts/{id}/holdings` | 当前持仓与只读参考价格、市值 |
| `GET /accounts/{id}/valuation` | 只读当前参考总估值，不保存 |
| `GET /accounts/{id}/valuations` | 已保存原始估值摘要，按 ID 倒序分页 |
| `GET /accounts/{id}/valuations/{historyID}` | 同账户原始估值证据，不取当前行情 |
| `GET/POST /accounts/{id}/records` | 独立账户记录列表 / 人工新增，POST 201 |
| `GET/PUT/DELETE /accounts/{id}/records/{recordID}` | 当前版本 / 全量更正 / 作废，200 |
| `GET /accounts/{id}/records/{recordID}/revisions` | 严格核验的不可变版本历史 |
| `GET /accounts/{id}/effective-summary` | 全记录统计及截至今天的账本资产投影 |
| `GET /accounts/{id}/analysis-basis` | 单账户同快照资产、资金流和收益 |
| `POST /imports/youzhiyouxing/preview` | XLSX 预览，不写库 |
| `POST /accounts/{id}/imports/youzhiyouxing` | 原子确认，可同时新建账户 |
| `GET /accounts/{id}/import-summary`、`GET /accounts/{id}/imported-records` | 原始导入证据，不受后续人工更正影响 |
| `GET /fx` | 最新/历史参考汇率，只读，不建立资产记录 |
| `GET /weekly-status` | 周计划状态，只读 |
| `GET /accounts/{id}/weekly-jobs`、`GET /accounts/{id}/weekly-jobs/{jobID}` | 周任务列表 / 同账户详情，只读 |
| `GET /audit`、`GET /audit/{auditID}` | 审计列表 / 不可变原始事件，只读 |

`POST /accounts/{id}/valuation` 返回 405。`/reported-accounts`、`/operations`、`/transfers`、账户 `/operations`、`/positions` 及持仓 `/transactions` 路径已删除，返回 404。没有隐藏交易或回放处理器。

## 4. 创建账户

以下为合成请求示例，不自动执行。`POST /accounts`，携带 `Idempotency-Key: synthetic-create`：

```json
{"id":"synthetic","name":"Synthetic account","currency":"CNY","opening_date":"2026-01-01"}
```

必填 ID、非空名称、CNY/HKD/USD 币种及有效日期；不接受 `opening_cash`、期初持仓或账户模式。返回 `id/name/currency/opening_date/opening_cash:null/version:"1"`，Location 为账户详情。

新账户没有虚构的现金、持仓来源、资产或入金。列表/详情返回 `current_holdings_input:"manual_snapshot"`；详情 `cash` 在未配置时为 null，配置后仅取当前快照现金。元信息 `opening_cash` 始终为 null。账户名、币种、开始日期当前不提供修改接口。

账户重试与其他写入一样使用原 POST 和 key，不通过 GET 猜测提交成功。创建/导入成功后即使列表读取失败，前端保留目标账户 ID，重新读取不会选回旧账户或再次提交。

## 5. 只读参考行情

`/holdings` 和 `/valuation` 都不接受查询参数，读取同一事务内的当前现金、数量及证券身份，释放事务后再访问行情。读请求不增加资产、审计或回执；参考估值不是账本总资产。

`/holdings` 返回 `account_id/currency/source/as_of/ledger_at/revision/manual_version/configured/cash/complete/total_assets/items`。未配置时 `configured=false`、版本 `"0"`、金额 null、items 空；不是零现金来源。items 含 `instrument/quantity/price/market_value/account_market_value/weight/quote_status/quote/fx`。删除持仓后不保留伪造的清仓周期行或成本。

`/valuation` 未配置时返回 422；否则返回 `source/current_holdings/account_id/currency/as_of/ledger_at/calculated_at/ledger_revision/cash/known_positions_value/positions_value/total_assets/complete/items`，不返回 `history_id`。source 仅为 `manual_snapshot`，指纹只标识当前来源，不是历史收益失效标志。

- 每项按数量乘原币报价舍入到分，再按最新 FX 折算到账户币种分，最后加现金。金额计算不经过浮点，超范围返回 `invalid_precision`。
- 任一持仓行情/汇率不完整则 `complete=false`，总资产为 null。已知市值小计不能当成总资产。空证券列表加明确现金可完整估值为现金。
- 腾讯股票支持 SH/SZ 白名单股票及 HK 五位 HKD 代码；A/B 股币种需匹配。查询身份支持精确五/六位代码，也接受 sh/sz/hk 前缀；查不到或不支持自动报价时允许手工身份，但不伪造价格。
- 股票批次最多 50 个代码，响应 1 MiB、网络预算 6 秒；最多四个股票请求并发，总估值预算 12 秒。固定 HTTPS 来源，不跟重定向、不静默更换供应商。
- 报价包含实际 `date/quoted_at/fetched_at/source/currency`；较早日期标为 `prior_date`，不是当前成交价或保证的收盘价。
- `/fx?base=USD&quote=CNY&mode=latest` 不带 date；`mode=historical&date=YYYY-MM-DD` 查截至该日向前 30 天最近有效日线。支持 CNY/HKD/USD 六方向，同币种不访问上游；八位精确舍入，响应保留实际日期与来源。汇率查询不会回填历史资产。

## 6. 错误与请求安全

错误为 `{"code":"...","message":"..."}`。导入校验可附 `detail_code/row/column`，不回显文件名、单元格、SQL、幂等键或内部异常。

| HTTP | 主要 code |
| --- | --- |
| 400 | `invalid_body`、`invalid_query`、`invalid_idempotency_key`、`invalid_operation`、`invalid_precision` |
| 403 | 同源/Host 等应用层写保护失败 |
| 404/405 | `not_found` / `method_not_allowed`（含 Allow） |
| 408/504 | `request_canceled` / `request_timeout`，写入保持原请求重试 |
| 409 | `idempotency_conflict`、`version_conflict`、`conflict`、`operation_voided`、`basis_changed` |
| 413/415 | `body_too_large` / `content_type` |
| 422 | `unsupported_operation`，例如尚无当前估值输入 |
| 500 | `data_integrity` / `internal_error` |
| 502/504 | 行情身份或 FX 来源不可用/超时，返回对应固定错误码 |
| 503 | `storage_busy`，附 `Retry-After: 1` |

5xx 日志仅记录方法与固定分类，不记录金融值或请求标识。字段、版本、历史审计形状或稳定序号不一致时失败关闭，不返回部分数据。

## 7. 本地验证

在 `backend/`：`go test -race -shuffle=on -count=1 -timeout=5m ./...`、`go vet ./...`。在 `frontend/`：`npm test`、`npm run build`。结果与覆盖映射见[当前验证](../../docs/specs/2026-09-12-ledger-current-inputs.md#local-verification)。未启用 e2e build tag 或真实来源标志，不属于生产、真实附件或浏览器验收。

## 8. 固定资产与资金流记录

`POST /accounts/{id}/records` 携带幂等键：

```json
{"id":"manual-synthetic","entry":{"kind":"cash_flow","date":"2026-01-02","flow":"2000.00","total_assets":null,"note":"Synthetic flow"}}
```

- 新建 ID 以 `manual-` 开头。`asset` 必须有非负 total_assets 且 flow=null；`cash_flow` 必须有 flow，total_assets 可空；`log` 必须有非空 note 且两金额为空。
- flow 可正、负、零；前端转入/转出表单填写正金额并按方向生成符号。note 最多 16,384 UTF-8 字节。后端支持已有备注维护，不需要交易记录。
- PUT 提交完整 entry、原 `expected_version` 和非空 reason；DELETE 提交原版本和 reason 并作废，不物理删除。reason 最多 512 UTF-8 字节。不能修改账户、ID、来源、稳定序号或原始导入/报价快照；已作废不能继续修改。
- 自动估值记录只能更正为 asset，标记 `manual_assertion=true`，原始报价审计不变。资金流/人工金额不调整当前现金或持仓，也不按交易重新核算。
- 返回包含 `id/account_id/sequence/entry字段/origin/original/version/voided/created_at/updated_at` 与适用的 `quote_audit_id/manual_assertion/carried_from`。没有 operation_id 或关联交易修改路径。
- `/records` 支持含首尾的 from/to、status=all/active/voided（默认 all）、limit、原样传回的 `日期:sequence` 游标，按日期与数值序号倒序；前端默认请求 active。改期保留序号，分页不是跨请求冻结快照。
- 修订按版本升序分页，以末版本为 cursor；严格验证完整版本数、连续版本、原始 JSON 形状、记录身份、创建时间及稳定序号。缺失旧修订不会被当成正常历史。

## 9. 分析依据与资产投影

`GET /accounts/{id}/analysis-basis?from=2024-01-01&to=2024-12-31&since_revision=0`：from 默认 0001-01-01，to 默认北京时间今天，要求 from<=to<=今天，不接受 track。

返回 `account_id/currency/from/to/timezone/revision/change_revision/points/opening/closing/net_flow/changes/previous_basis_affected/status/returns`。只读取 `account_records` 的有效固定事实，不连接行情、不回放交易、不用当前持仓计算历史。

- 同日最后一笔明确资产为日终资产，已包含当天全部资金流，不受当日各行录入先后影响；其余行仍保留原字段。
- 没有明确资产的日期，投影为最近有效明确资产加其后日期累计净流入。raw record.total_assets=null 仍为 null。status=carried 表示未观察到期间市场涨跌，不是新的价格事实。
- 周任务沿用记录不是新锚点，不重复累加、不重置累计流量。源记录更正时更新投影及来源版本，原始沿用证据不变。唯一明确资产作废后不能用旧沿用金额复活它，投影和收益返回缺少依据。
- 明确人工金额为 reported，固定采样为 observed，无依据为 unavailable，备注为 log。当前输入编辑不使旧 observed 失效，不产生历史重算任务。
- `change_revision` 只跟踪该账户的 account_record 审计；记录更正取新旧日期较早者，持仓/证券变化不会改变收益指纹。`since_revision` 不参与 revision 指纹。
- `effective-summary` 的记录数、总流入/流出统计所有有效日期；latest_assets 为截至今天的同一账本投影，latest_asset_date 是最近明确资产日期，不受收益筛选影响。没有明确资产时为 null，未来金额不冒充今天资产。
- 金额与资金流累计精确计算。单个记录及投影资产仍受 int64 分范围限制，超限拒绝而非溢出或补零；累计 total_in/total_out/net_flow 使用大整数。

## 10. 同快照收益

returns 含 `revision/requested_from/requested_to/start_mode/effective_from/effective_to/days/period_days/opening/closing/net_flow/denominator/profit/modified_dietz/xirr/twr/twr_annualized/curve/warnings/flows/investor_flows`。

- 未指定 from 时，以首个已知日终资产为基准并排除该日全部资金流；指定 from 时，取此前最后资产作为 from 前一日日终边界。无前置资产不补零、不反推本金。
- 期末为范围内最后资产/投影记录，不靠后续备注或空白截止日延长年化。profit = close - open - net_flow；所有指标和曲线使用同一投影金额。
- Modified Dietz 分母为 `open + sum(flow * weight_days / period_days)`；`days` 为自然日日差，`period_days=days+1` 为含首尾的权重分母，`weight_days=effective_to-flow_date`。期末流量权重为零；无正区间或非正分母不可用。
- TWR 在资金事件日链接 `(当日资产-当日净流入)/上个资金边界资产`。流量估算依据总额只应用一次，不把已投影的边界再加款。任何估算边界参与后，累计 TWR 仍为 reference。curve 中 twr_estimate 保留原明确记录、日期和累计净流量，供解释而非再次入账。
- 示例：10000 明确资产后先转入 2000、再转入 1000，两日投影分别为 12000、13000；随后明确总资产 14300，收益金额 1300，TWR 为 10%，不是在 13000 上再加 3000。
- XIRR 用投资者符号、同日大整数净额和实际天数/365 独立求解，不机械年化 Dietz。无法证明唯一、多根风险、求解范围或舍入认证不足均返回不可用原因；短区间有外推警告。
- metric 为 `value/percentage/status/reason`；金额两位、比率十二位、百分数两位，直接从未舍入值 half-away-from-zero 输出。不以展示值反向计算或回写事实。
- 详细求根证明保留于历史设计和数学单元测试；本次未改变 XIRR 唯一性证书与精度认证算法。`carried_assets_unchanged` 是现有 warning 标识，当前解释为流量调整投影，不再表示资产原额不加款。

## 11. 周六总资产任务（T05 后端）

`LEDGER_WEEKLY_ENABLED=false` 默认关闭，需启用账本；`LEDGER_WEEKLY_TIME` 默认北京时间每周六 08:00。状态接口不是心跳，不提供浏览器启用或立即执行按钮。

- 每账户、计划日期唯一任务；配置当前来源时保存完整估值，否则沿用截至当日最近明确资产证据。明确的零现金空持仓是有效来源，不等于未配置。
- pending/running/failed/succeeded/skipped 状态保留审计；同日补执行、失败最多三次，重试间隔 5/30 分钟，跨午夜不回填过期周次。
- 行情在数据库事务外获取，保存事务检查持仓版本和任务租约；中途变化返回 basis_changed，不保存过时输入。记录更正或其他账户输入不改变本账户估值来源。
- 成功后资产金额固定，原现金、数量、证券身份、价格、FX、来源时间进入不可变审计。后续持仓修改、资产人工更正/作废都不改写原始估值证据；终态任务不会重新执行以恢复旧金额。
- 无来源且无明确资产时 skipped/no_source，不制造零。沿用不抓行情、不推断现金或持仓，不成为新的市场观察。
- 估值总预算 12 秒，失败清理额外 3 秒，租约 72 秒；取消及服务退出等待 worker 结束再关闭库。
- weekly-status 返回配置、当前窗口及下一固定计划；weekly-jobs 按 ID 倒序、可筛选状态。详情带历史 ID，沿用来源还包含冻结原记录；查询不运行任务。

## 12. 原始导入与统一审计

导入两 POST 使用 multipart。预览只接受 file；确认接受 file、preview_digest、create_account（严格 true/false）及 Idempotency-Key。服务重新解析并核对摘要，不信任客户端回传行。

- 文件 8 MiB、解压 32 MiB、最多 1,024 ZIP 项和 10,000 数据行位置；标准库 ZIP/XML，不执行公式或外链，不落盘用户原附件。
- 创建标志为 true 时账户使用来源名/币种；false 时要求现有账户币种匹配。首次导入仅允许空历史账户，但已配置的当前现金/持仓可以保留不变。
- 原始资金流与总资产可同行，重复行保留，空总资产不补值；日期/数值科学记数法精确解析，Excel 1900/1904 日期系统与来源文本按已支持规则处理。
- 规范摘要包含解析版本、来源元数据与原行，不含文件名。同键同请求复用回执；异键同内容同账户复用原批次；不同批次不增量合并、不撤销或重置已有记录。
- 新账户、所有记录、原始行审计和回执同事务提交。后续人工更正不改变 import-summary/imported-records；重传不会恢复旧金额或重复流量。
- 审计列表支持 account_id/entity_type/action/limit/cursor；详情仅显示固定原始 JSON，前端文本转义。无审计写接口，数据库禁止覆盖/删除审计与回执。

## 13. 同账户当前持仓

“添加持仓”直接进入证券身份查询，确认身份和当前数量后保存整份账户快照，不先登记证券、不生成交易。证券身份修改创建新不可变目录项；其他账户和旧估值中的名称、市场、代码、币种不被改写。

`PUT /accounts/synthetic/current-holdings`，携带独立 Idempotency-Key：

```json
{
  "expected_version":"0",
  "cash":"1000.00",
  "positions":[{"instrument_id":"security-1","quantity":"10.000000"}],
  "securities":[{"id":"security-1","market":"SH","code":"600000","name":"Synthetic security","currency":"CNY"}]
}
```

- expected_version 为当前快照版本，尚未配置时为 `"0"`，不是账户元信息 version。cash 必填非负，positions 必填数组，最多 200 项，数量为正。
- securities 可省略，用于本次新选身份；每项必须被 positions 引用，已有同 ID 身份只能完全一致。当前 UI 查询后使用新 ID，身份/持仓/审计/回执原子提交。
- 同账户市场+代码只能出现一次，编辑也检查，不能通过不同 ID/名称/币种绕过。跨账户可以相同市场+代码。
- 删除某项表示不再持有；空数组表示纯现金，现金零加空数组表示明确空来源。历史目录、审计及固定资产不会删除。
- 不接受基准日期、成本、交易或行情价格。保存不更新历史资产、资金流或收益，不要求历史补交易。
- 当前版本冲突时保留草稿并显式重新读取；不确定结果锁定原请求重试。已提交回执可能是旧版本，确认后另读当前快照，不用旧回执覆盖新状态。
