# 记账 HTTP 接口

日期：2026-09-06。已实现本机 HTTP 接口及事务层，未部署。产品规则见[需求记录](../../docs/specs/2026-09-06-investment-ledger-requirements.md)，内部持久化契约见[后端设计](../../docs/specs/2026-09-06-investment-ledger-backend-design.md)。

一期目标已确认为公网匿名共享全部读写，任何访客可使用全部功能，不做认证或用户访问控制。当前接口仍默认关闭且回环限定，本轮不开放生产或移除保护；真实历史附件不公开。启动见[前端说明](../../frontend/README.md#本地记账)。

## 1. 启用与访问边界

在 `backend/` 执行：

```bash
LEDGER_ENABLED=true BACKEND_ADDR=127.0.0.1:5051 LIQUOR_AUTO_SYNC=false go run ./cmd/server
```

- 默认 `LEDGER_ENABLED=false`，不注册记账路由，也不创建 `ledger.db`。
- 启用时仅允许回环监听；非回环地址即使配置 token 也拒绝启动。请求的真实 socket peer 也必须是回环 IP，不信任转发头。
- 本机未配置 token 时保留 Host 防重绑定及跨站写保护；配置 `BACKEND_API_TOKEN` 后，所有账本请求还需 Bearer 令牌。
- 仅适合受信任的本机开发，不具备用户账户授权。不要通过其他反向代理或隧道绕过本机边界；无真实数据的公网演示入口另行适配，承载私人账本前仍须有适当访问保护。
- Nginx 仍只代理白酒。Vite 默认不转发账本，显式 `LEDGER_DEV_PROXY=true` 才启用本机代理，目标须为回环 HTTP 地址，校验实际 peer、Host 和跨源请求。服务令牌仅在开发进程注入，客户端不持有令牌；不启用时记账请求返回 `local_proxy_disabled` JSON 404。
- 账本保存于 `BACKEND_DATA_DIR/ledger.db`，不写入白酒或微博库。默认 `backend/data/` 已忽略；其他目录需自行核验访问权限、Git 及镜像排除规则。
- 已实现汇率、当前总资产、导入初始化、人工维护及 T04 资金加权收益；T05 周任务与只读 UI 覆盖所有账户。fresh schema 将所有当前记录统一到 account_records、所有追溯统一到 audit_log，分析不接受 track。T07 收益曲线/TWR 已实现并开展本地合成验证，完整验收及发布待执行；历史回填、增量导入和批次撤销仍取消。

## 2. 通用契约

前缀：`/api/platform/ledger`。所有成功响应直接返回 JSON，无 `success/data` 包装。错误见第 6 节。GET 均支持 HEAD，无响应正文。响应包含 `Cache-Control: no-store` 和 `X-Content-Type-Options: nosniff`。

- 请求体必须为未压缩 `application/json`，最多 64 KiB，只接受一个对象。
- 拒绝未知字段、重复字段、大小写或 Unicode 字段别名、无效 UTF-8 和尾随第二个 JSON；字段名为精确的 snake_case ASCII。
- 金额、股数、价格、汇率使用十进制字符串；序号、版本也使用正整数字符串，不接受 JSON number。例：`"amount":"100.00"`、`"sequence":"1"`。
- 日期为真实日历日期 `YYYY-MM-DD`。持仓操作写入不能晚于服务当前北京时间日期，不能早于账户期初。独立账户级记录不套用此限制，见第 8 节。
- 对象 ID、幂等键使用 1..128 位 ASCII 字母、数字、下划线或连字符。操作日内序号在整个账本同一日期内唯一，不按账户分别编号，不自动猜测历史先后。
- 写接口不接受查询参数。列表默认 30 条，`limit` 允许 1..100；拒绝未知、重复、空值及非法筛选参数。
- `fee` 未提供或 null 表示未录入，按零计算；显式 `"0.00"` 表示录入为零。期初 `cost`、`diluted_basis` 为 null 表示未知，不等于零。
- HTTP DTO 与已有数据库快照分开，客户端使用 snake_case；不改变已保存 Record、revision 或幂等摘要的内部 JSON 格式。

账户响应新增 `accounting_mode`：已有及普通新建账户为 `holdings`，由导入原子新建的总资产账户为 `reported`。reported 的 `opening_cash` 和详情 `cash` 为 null，数据库零值仅为旧表约束占位，不是已知现金。这些账户不进入持仓 Replay，交易、转账、持仓查询及当前估值返回不支持，不生成虚构的零资产历史。已有持仓账户接收账户级导入不改变其模式。

## 3. 接口清单

| 方法及相对路径 | 行为 |
| --- | --- |
| `GET /fx` | 查询腾讯最新或历史汇率；GET/HEAD，不写账本，参数见下文 |
| `POST /instruments` | 登记证券；完全相同的重试无副作用，不允许改写已有证券身份或币种；201 |
| `GET /instruments` | 证券按 ID 升序分页，参数 `limit`、`cursor` |
| `POST /accounts` | 原子创建账户及期初现金、持仓；201；已存在的账户 ID 返回 409，不重新初始化 |
| `POST /reported-accounts` | 无需 Excel 创建总资产账户，持久幂等回执；201 |
| `GET/POST /accounts/{id}/records` | 账户级有效记录列表 / 手工新增，独立于持仓；GET/HEAD 200，POST 201 |
| `GET/PUT/DELETE /accounts/{id}/records/{recordID}` | 当前版本 / 全量更正 / 作废；200，GET 支持 HEAD |
| `GET /accounts/{id}/records/{recordID}/revisions` | 不可变版本历史，GET/HEAD 200 |
| `GET /accounts/{id}/effective-summary` | 全日期的当前有效统计，不改变原始导入摘要；GET/HEAD 200 |
| `GET /accounts/{id}/analysis-basis` | 单账户全部有效记录、资产图与同快照收益；不接受 track；GET/HEAD 200 |
| `GET /audit`、`GET /audit/{auditID}` | 统一操作审计列表/详情，只读；见第 12 节 |
| `GET /accounts` | 账户元信息按 ID 升序分页，参数 `limit`、`cursor`；列表不声称是当前总资产 |
| `GET /accounts/{id}` | 账户元信息及重放得到的本位币 `cash`；200 |
| `GET /accounts/{id}/valuation` | 只读当前估值预览；GET/HEAD 均不写记录、审计或回执，不返回 history_id，无查询参数 |
| `POST /accounts/{id}/valuation` | 显式重新计算并保存总资产；application/json 空对象 `{}`、Idempotency-Key 必填，成功 200 |
| `GET /accounts/{id}/valuations` | 已保存估值摘要，按保存 ID 倒序分页，可按 as_of 日期筛选；GET/HEAD |
| `GET /accounts/{id}/valuations/{historyID}` | 已保存完整快照及当时账户/证券身份；GET/HEAD，账户隔离 |
| `GET /weekly-status` | 周六任务启用状态、配置和下个计划时刻；GET/HEAD 只读 |
| `GET /accounts/{id}/weekly-jobs` | 账户周六任务倒序分页，状态及历史记录链接；GET/HEAD 只读 |
| `GET /accounts/{id}/weekly-jobs/{jobID}` | 同账户任务详情；GET/HEAD 只读，不提供写入/重试接口 |
| `POST /imports/youzhiyouxing/preview` | XLSX 账户导入预览，不写库 |
| `POST /accounts/{id}/imports/youzhiyouxing` | 校验预览并原子确认导入，可同时新建总资产账户 |
| `GET /accounts/{id}/import-summary` | 导入来源元数据、金额/记录摘要；未导入为 404 |
| `GET /accounts/{id}/imported-records` | 账户级导入记录，日期筛选和游标分页；GET/HEAD |
| `GET /accounts/{id}/positions` | 按证券 ID 分页的当前周期持仓、两种成本及周期损益，参数 `limit`、`cursor` |
| `POST /operations` | 创建资金、买卖、组合录入、分红或转账操作；201 |
| `POST /accounts/{id}/operations` | 创建的账户路径入口；可省略 `operation.account_id`，提供时必须与路径相符；201 |
| `POST /transfers` | 仅接受 `kind=transfer` 的同币种同日关联转账；201 |
| `GET /operations` | 按业务日期、序号倒序分页，可筛选账户、日期和状态 |
| `GET /accounts/{id}/operations` | 同上，账户范围由路径固定，包括该账户作为转出或转入方的转账 |
| `GET /operations/{id}` | 当前已提交版本，作废后仍可读取；200 |
| `PUT /operations/{id}` | 整笔替换并验证完整历史；200；body 的操作 ID 必须与路径相同 |
| `DELETE /operations/{id}` | 作废而非物理删除，保留原字段及修订；200，返回作废版本 |
| `GET /operations/{id}/revisions` | 按版本升序分页，参数 `limit`、`cursor` |

账户初始化与期初在本轮合为一次 `POST /accounts`，没有另行暴露可覆盖期初的 `/opening` 路由。证券/账户初始化不采用操作的幂等回执协议，以上重试规则分别适用。

## 4. 录入示例

以下均为合成数据，示例是请求体，不会自动执行交易或获取行情。

### 证券与期初账户

`POST /instruments`：

```json
{"id":"sample-stock","market":"TEST","code":"001","name":"Synthetic stock","currency":"CNY"}
```

`POST /accounts`：

```json
{
  "id":"sample-account",
  "name":"Synthetic account",
  "currency":"CNY",
  "opening_date":"2026-01-01",
  "opening_cash":"1000.00",
  "positions":[]
}
```

`opening_cash` 必须显式提供，可为零。期初持仓最多 200 项，每项为 `instrument_id`、`quantity`、可选 `cost`、可选 `diluted_basis`；成本和摊薄基数以账户本位币表示，二者独立。证券必须先登记。

### 保存、重试与更正

操作 POST/PUT/DELETE 必须携带一个 `Idempotency-Key`。例如 `sample-create-1` 对应以下 `POST /operations`：

```json
{
  "operation":{
    "id":"sample-buy-1",
    "account_id":"sample-account",
    "date":"2026-01-02",
    "sequence":"1",
    "kind":"deposit_buy",
    "amount":"2000.00",
    "instrument_id":"sample-stock",
    "quantity":"100.000000",
    "price":"10.000000",
    "fee":"5.00"
  },
  "note":"Synthetic deposit and purchase",
  "reason":"Initial entry"
}
```

此例期初现金 1000、转入 2000、买入含费支出 1005，剩余现金 1995。外部投入是 2000，不再把买入额算一遍。响应为 `operation`、`note`、`version:"1"`、`created_at`、`updated_at`，Location 指向操作详情。

PUT 整笔替换时使用新幂等键，body 结构同上并添加 `"expected_version":"1"`；操作 ID 与路径一致，成功返回版本 `"2"`。省略可选费用会变成未录入，不是 PATCH 保留原值；完整 body 必须包含仍需要的字段。

DELETE 使用新键，body 仅包含：

```json
{"expected_version":"2","reason":"Synthetic void"}
```

成功返回版本 `"3"`，`operation.voided=true`。不得在写入 body 传 `voided`，也不能用 PUT 恢复作废记录。

- 创建/更正 body 的 `reason` 必填；`note` 可省略，最多 8192 字节。创建不传 `expected_version`，更正和作废必须是正整数字符串。
- 同键同规范化命令重试，返回首次成功时的 Record 和相同状态码：POST 仍为 201，即使当前操作已经是版本 3，旧 POST 重试仍返回版本 1。
- 同键不同操作内容、动作、预期版本、备注或原因返回 409。想查看最新值调用 GET，不把幂等重试结果当成当前版本。
- 更正或作废造成后续现金不足、超卖或周期冲突时整笔回滚；失败不占用键。重试须保留 ID、序号、版本及 FX 快照，不重新取汇率后沿用原键。
- 全局 POST、账户 POST 在补齐相同账户身份后使用同一 Store 契约，不会为不同路径创建两套独立幂等域。

### 操作字段

| kind | 额外字段及金额含义 |
| --- | --- |
| `deposit`、`withdrawal` | 正的本位币 `amount`；不传股票、费用或 FX |
| `buy`、`sell` | 股票、正股数、正成交价、可选费用；amount 省略或零；不产生外部投入 |
| `deposit_buy`、`sell_withdraw` | 在买卖字段外另填正的本位币 amount，独立表示转入或转出金额 |
| `dividend` | 股票、正的原币到账 amount、明确 `cycle_id`，外币时加 FX；不增加外部投入 |
| `transfer` | 源账户、`to_account_id`、正 amount；同币种同日完成，包含双方的单条操作，不能单改一条腿 |

所有操作需稳定的 id、account_id、date、sequence、kind。未使用的数值字段可以省略，但传入非零无关值会被拒绝。操作序号即使作废也保留，不自动复用。

外币买卖的 price、fee 按证券原币输入；额外传入：

```json
{"fx":{
  "rate":"7.00000000",
  "date":"2026-01-02",
  "source":"synthetic-reference",
  "fetched_at":"2026-01-02T12:00:00Z"
}}
```

汇率方向是“一单位证券原币折多少账户本位币”。调用方先自动查询或手工填写并确认快照，写入时仍明确提供四个字段；示例汇率为合成值。本位币成交不得传任意 FX。跨币种账户转账目前返回 422，不暗中选择差额规则。

### 腾讯汇率获取

```text
GET /api/platform/ledger/fx?base=USD&quote=CNY&mode=latest
GET /api/platform/ledger/fx?base=HKD&quote=CNY&mode=historical&date=2026-09-05
```

- `base` 是证券原币，`quote` 是账户本位币。支持 CNY/HKD/USD 六个方向；反向币对对腾讯直接报价取倒数，以精确整数运算按八位小数舍入，同币种返回 1 且不访问上游。
- `latest` 不接受 date，返回最新可用市场参考报价；`historical` 必须提供不晚于北京时间今天的日期，查询截至该日向前 30 天范围，取最近有效日线收盘价。窗口内无数据则失败，不以今天价格补齐。
- 返回 `{base,quote,mode,requested_date,rate,date,source,fetched_at,quoted_at?}`。rate 为八位十进制字符串；date 为实际汇率业务日期，不强行改成请求日期。最新报价可能跨午夜，quoted_at 与业务日期并非同一概念；日收盘不提供 quoted_at。
- 来源为 `Tencent/spot/USDCNY` 或 `Tencent/close/HKDCNY` 等；倒数追加 `/inverse`。来源不是银行/券商结算汇率。只读取腾讯固定 HTTPS 地址，不切换 ECB 或其他备用供应商。
- 表单以北京时间今天的操作查询 latest，较早日期查询 historical；未来日期不能提交。编辑默认保留原快照，只有显式重新获取或改变币种/日期才重新选择汇率。
- 操作 POST/PUT 的 fx **只接受 rate/date/source/fetched_at 四字段**，不能将整个查询结果直接写入；quoted_at 等是查询展示元数据。固定快照随操作、修订和幂等回执保存，刷新行情不会重算旧交易。
- 汇率查询本身不保存账本、不自动执行交易，也不形成历史资产估值；当前股票估值使用同一腾讯 provider 的最新参考汇率，不使用历史成交汇率。
- 上游请求最多 8 秒（含并发等待）、最多四个并发、256 KiB 响应上限，禁止重定向。不设独立后台汇率任务或静默供应商回退；显式启用的 T05 总资产任务按需获取最新汇率。
- 失败返回 `502 fx_unavailable` 或 `504 fx_timeout`，不返回上游正文；未支持币种为 `400 unsupported_currency`，非法查询为 `400 invalid_query`。最新报价保留实际日期，不承诺盘中实时；不设置未经交易日历核验的固定过期截止。
- 页面提供重新获取与手工录入。切换手工会清除自动快照和来源，要求正汇率、不晚于业务日期的实际汇率日期及来源；获取时间默认本次手工记录时间，可修改。结果不确定的写入仍锁定原请求，不重新抓取汇率后沿用原幂等键。

## 5. 查询、分页与成本

列表结构统一为 `{"items":[],"next_cursor":"..."}`，无下一页时省略 next_cursor。账户/证券/持仓游标为最后一条 ID，修订游标为最后版本字符串。部分列表在刚好满页时也会给出游标，下一页可能为空。

操作列表支持：

```text
GET /operations?account_id=sample-account&from=2026-01-01&to=2026-12-31&status=all&limit=30
GET /operations?cursor=2026-01-02%3A1&limit=30
```

- `from/to` 包含边界；默认不限制日期。
- `status` 为 `all`（默认）、`active`、`voided`，不会默认隐藏作废记录。
- 操作游标为 `日期:序号`，按日期及序号降序排他翻页；路径固定账户时，不能通过 account_id 查询参数改看另一账户。
- 这些是当前记录的游标，不是跨多个请求冻结的历史快照。翻页期间若有人更正记录日期或序号，客户端应重新刷新，不能承诺绝不重复或遗漏。

账户详情的 cash 是当前本位币现金，不是总资产。账户 version 仅是期初元信息版本（目前为 1），不能拿它替代操作 expected_version。

持仓列表字段包括 quantity、remaining_cost、moving_average、diluted_basis、diluted_cost、realized_profit、dividends；成本及盈亏以账户本位币表达。每个证券只展示最新持仓周期，已清仓时数量为零、单位成本为 null；未知成本也为 null，不冒充零。旧周期分红和旧成交仍可从操作历史追溯，当前没有独立周期列表接口。

### 当前股票估值

`GET /api/platform/ledger/accounts/{id}/valuation` 不接受查询参数（不支持传日期请求历史估值），返回：

```text
{
  account_id, currency, as_of, ledger_at, calculated_at, ledger_revision, history_id?,
  cash, known_positions_value, positions_value, total_assets, complete,
  items: [{instrument_id, quantity, quote, fx, market_value, status, error_code?}]
}
```

- 金融数字均为字符串；无法完成估值时 `positions_value`、`total_assets` 为 null，`complete=false`。`known_positions_value` 只包含成功估值的持仓，不含现金，不能冒充总资产。市场数据缺失时仍返回 HTTP 200 的不完整结果。
- cash、quantity、证券身份来自一次数据库事务；网络在事务结束后执行。`ledger_at` 是账本读取时刻，`as_of` 是当次重放截止的北京时间日期，`calculated_at` 是计算完成时刻，不保证期间无人修改账本。
- 每项先用数量乘原币价格舍入到原币分，再按最新参考汇率折算到本位币分，最后加总现金；复用精确整数/大整数舍入，不从持仓成本反推市值。溢出按 `invalid_precision` 失败，不返回截断值。
- quote 包含 `symbol/price/currency/source/date/quoted_at/fetched_at`，来源 Tencent。fx 为现有完整 FXQuote；同币种为 null，不请求汇率。同一估值请求内按币对复用汇率，包括失败结果。
- status 为 `current`（当日参考）、`prior_date`（报价或汇率不是当日）、`unavailable` 或 `closed`。closed 数量为零、市值为零，不调用行情。无持仓账户完整总资产等于现金。
- 支持 SH 的 `600/601/603/605/688/689` CNY 与 `900` USD，SZ 的 `000/001/002/003/300/301` CNY 与 `200` HKD，均为六位代码；HK 为五位代码、HKD。其他市场/代码不自动推断。市场字段使用大写 SH/SZ/HK，不改写既有证券身份。
- 校验腾讯返回代码、币种、正价格及实际时间；退市标记和当日无成交占位报价不可用于估值。缺失、未来时间、重复异常记录不替换为成本或零。
- 逐项 error_code 为 `unsupported_instrument`、`currency_mismatch`、`quote_unavailable`、`quote_timeout`、`quote_inactive`、`fx_unavailable` 或 `fx_timeout`。账户不存在、非法参数、数据库错误等仍使用统一 HTTP 错误。
- 股票请求按 50 个代码分批，响应上限 1 MiB、最多四个股票上游请求同时进行，每次网络预算 6 秒，估值总预算 12 秒；固定 HTTPS 来源、禁止重定向。默认启动不抓取；除显式启用的 T05 周六任务外，由读取账户估值触发，不改写交易或成交 FX。
- GET/HEAD 只计算预览，不改变 canonical 记录或审计，不返回 history_id。选择账户、打开页面、刷新和操作成功后的预览读取均不自动保存。保存必须明确 POST；周任务继续使用内部原子保存，不伪造 GET。

### 历史估值保存与查询

- 当前金额只在 account_records，冻结估值及其历史版本只在 audit_log；不创建 valuation_history 表或兼容视图。schema-1 冻结 JSON 原字节由审计与 account_records.quote_audit_id 关联，列表和详情 API 直接从该统一模型读取。
- `POST /accounts/{id}/valuation` 只接受 `{}`，拒绝客户端金额、未知字段、查询参数及错误媒体类型。服务端重新取得现金/持仓和报价，网络在事务外；不完整结果返回 422 incomplete_valuation，不保存。完整结果和原始回执在同一事务保存，成功后返回完整估值及 history_id。
- Idempotency-Key 与账户和固定 save-valuation 意图绑定，不可与操作、导入或人工回执键复用。先查询已提交回执，再获取报价；并发首次请求在最终事务再次检查。已提交的重试不重新获取行情、不新增记录或审计，返回原响应，不恢复后续更正或作废的当前金额。回执核对原始报价及版本 1 记录审计，而非当前业务版本。
- 新的不同键代表一次新的明确保存，可在同日追加；不能用新键重试未知结果。前端 PendingWrite 锁住原请求、账户导航与其他写入，直到原键确认或得到明确失败。GET/HEAD 和审计/历史查询没有保存副作用。
- 快照包含现金、股数、逐项市值、原始报价及汇率的币种/来源/日期/时间、账户名和证券身份。历史详情不重新连接行情，也不使用现在的证券名称代替保存时的名称。
- `ledger_revision` 是读取同一事务中的全账本期初、证券和有效/作废记录版本后生成的 SHA-256 指纹。其他账户变化也可能使其改变；它用来标识计算依据，不是每日收益版本或历史重算结果。
- 更正或作废交易不会覆盖历史快照；未来收益分析必须结合资金流、原始日期和当时账本依据识别需重算的区间，不能直接把相邻采样点涨幅当成收益率。
- `as_of` 是当次账本截止日期，不是所有证券的收盘日期。保存的是请求时参考估值，可能包含非当日报价，不是对过去交易日的收盘估值回填。

```text
GET /api/platform/ledger/accounts/{id}/valuations?from=2026-09-01&to=2026-09-30&limit=30
GET /api/platform/ledger/accounts/{id}/valuations?cursor=123&limit=30
GET /api/platform/ledger/accounts/{id}/valuations/123
```

列表为 `{items:[{id,account_id,currency,as_of,ledger_at,calculated_at,saved_at,ledger_revision,cash,positions_value,total_assets}],next_cursor?}`。ID、游标及金额均为字符串，limit 默认 30、最大 100；from/to 按 as_of 含首尾筛选，游标按 ID 降序排他翻页，不按非固定长度的时间字符串排序。

详情为 `{id,saved_at,schema_version:1,account_name,valuation,instruments}`。valuation 是保存时完整估值，不含 history_id；instruments 是当时的 `{id,market,code,name,currency}` 列表。非法参数返回 400，账户不存在或跨账户读取记录返回 404；存储快照形状、数值合计或索引元数据不一致时失败关闭。后续修改持久化快照结构必须显式演进格式版本，不能直接改变旧 JSON 的解释。

### 有知有行 XLSX 导入

两个 POST 使用 `multipart/form-data`，不是通用 JSON body。预览只接受 file；确认接受 file、preview_digest 和 create_account（严格字符串 true/false），并要求 `Idempotency-Key`。确认时重新解析文件并核对摘要，不信任客户端传回的预览行。

```text
POST /api/platform/ledger/imports/youzhiyouxing/preview
POST /api/platform/ledger/accounts/{stable-account-id}/imports/youzhiyouxing
GET /api/platform/ledger/accounts/{id}/import-summary
GET /api/platform/ledger/accounts/{id}/imported-records?from=2026-01-01&to=2026-12-31&limit=30
```

- 预览返回 `{digest,metadata,rows,summary,warnings}`。metadata 包含 name/goal/expected_return/expected_return_raw/investment_horizon/currency/money_bucket；expected_return 保留或按简单百分比样式展示，raw 保留原始单元格值。
- 每行是 `{source_row,kind,date,flow,total_assets,note,source_created_at,detail,date_raw,created_raw}`。kind 为 asset/cash_flow，flow 是有符号金额或 null，total_assets 是非负金额或 null；二者可以同在一行。来源行序号不作为持仓操作 sequence。
- summary 包含 row_count/asset_count/flow_count/from/to/total_in/total_out/latest_assets/latest_asset_date；流出合计为正的绝对额，asset_count 按非空资产字段计，latest 按业务日期、来源行号选择。第一笔资产不是自动入金，缺失值不补齐。
- 确认成功为 HTTP 200 `{account_id,batch_id,imported_count,duplicate}`。create_account=true 时新账户名/币种取自文件，模式 reported；false 要求目标已存在且币种一致。不更改现有账户的现金/持仓/期初或自动估值历史。
- 同键同内容/目标/创建意图返回原始结果；异键但同内容、同账户复用批次。首版每账户仅一个批次，不同规范内容返回 409 import_already_exists，不做增量合并。重复行在文件内部仍保留。
- 规范摘要包含解析格式版本、来源元数据与行内容，不含文件名。当前解析版本为 youzhiyouxing-account-v2；解析器升级后须重新预览，不静默复用不同解析规则的摘要。
- 摘要 GET 返回 `{batch_id,metadata,summary,imported_at}`；记录 GET 返回 `{items:[行字段加字符串id],next_cursor?}`。from/to 含首尾，limit 默认 30、最大 100，按日期倒序和来源行号倒序；cursor 原样传回，完整末页可能产生一个空下一页。
- 读取与导入不调用行情，不猜测买卖、分红或账户关联。导入只初始化空业务账户，直接写 account_records；原始行及元数据在 audit_log，imported_account_records 是只读审计投影。不改变持仓现金或股数。
- 当前只有 fresh `001_init.sql`，不新增迁移或上述旧表。确认的新账户、account_records、audit_log 导入清单/原行及幂等回执同事务。失败不残留半个新账户，已提交但响应丢失时按原请求重试确认。
- 文件/解压限制分别 8 MiB/32 MiB，最多 1,024 ZIP 项及 10,000 个数据行位置。标准库解析 ZIP/XML，无额外 XLSX 依赖，不执行公式或跟随外链，不把原文件写入磁盘。仅支持已核验的工作表及表头结构。
- 金额按原始十进制和科学记数法精确解析，超过分精度拒绝；Excel 1900 虚构闰日序号 60 拒绝，1904 日期系统支持。无时区创建时间按北京时间解释，数值日期的小数转换到纳秒并截断，原值仍保留；延后补录不改写业务日期。
- 错误为 `400 invalid_import`（detail_code，可带 row/column）、`400 preview_mismatch`、`409 import_already_exists/currency_mismatch/idempotency_conflict`、`413 upload_too_large`，数据库忙沿用 503 storage_busy。错误不含原单元格、文件名或正文；尚未导入的账户摘要为 404。

## 6. 统一错误映射

错误主体为 `{"code":"...","message":"..."}`。历史冲突可增加 `operation_id`、`date`，指向导致失败的记录；不返回请求正文、密钥、SQL 或内部异常文本。

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | `invalid_body` | 非法 JSON、未知/重复/非规定字段、错误数值类型 |
| 400 | `invalid_query` | 查询或资源 ID 不合法、写请求携带查询参数 |
| 400 | `unsupported_currency` | 汇率查询币种不在 CNY/HKD/USD 范围 |
| 400 | `invalid_idempotency_key` | 缺少、重复或非法 Idempotency-Key |
| 400 | `invalid_version`、`invalid_version_or_id` | 版本未按正整数字符串提供，或更正路径与操作 ID 不同 |
| 400 | `invalid_operation`、`invalid_precision` | 业务字段、历史结构、精度、范围不合法 |
| 401/403 | `unauthorized`、`invalid_host`、`cross_origin`、`local_only` | 应用层的令牌、Host、跨站写或实际 peer 检查失败 |
| 404 | `not_found` | 不存在的资源或记账路由 |
| 405 | `method_not_allowed` | 方法不支持，提供 Allow |
| 408 | `request_canceled` | 事务请求取消，写入按原键重试确认结果 |
| 409 | `idempotency_conflict` | 同键不同命令 |
| 409 | `version_conflict` | 预期版本不匹配 |
| 409 | `operation_voided` | 已作废记录不允许继续修改 |
| 409 | `conflict` | 账户/证券/操作身份或唯一性冲突 |
| 413 | `body_too_large` | 请求体超过 64 KiB |
| 415 | `content_type` | Content-Type 或内容编码不支持 |
| 422 | `insufficient_cash`、`insufficient_position` | 重放后透支或超卖 |
| 422 | `unsupported_operation` | 当前不支持的业务，例如跨币种转账 |
| 500 | `data_integrity`、`internal_error` | 存储不一致或内部失败，不泄露原始异常 |
| 502/504 | `fx_unavailable`、`fx_timeout` | 腾讯汇率缺失、数据校验失败、上游失败或超时；可重试查询或手工录入 |
| 503 | `storage_busy` | SQLite 忙或锁定，附 Retry-After: 1 |
| 504 | `request_timeout` | 事务超时，按原键重试确认结果 |

5xx 仅记录请求方法和错误分类，不打印路径标识、查询参数、业务正文、幂等键或 SQLite 错误携带的数据。

## 7. 验证与限制

使用合成数据和临时 SQLite 测试完整 HTTP 工作流、严格 JSON 校验、HEAD、错误映射、分页、修改回滚、旧幂等回执和 DTO 不破坏内部快照。真实进程测试覆盖默认关闭、有/无 token 的本机接入、创建/更正/作废及重启后回执一致。

T01 验证命令为 `make check && make test && env -u LIQUOR_E2E_LIVE make test-e2e` 及前端 `npm test && npm run build && npm run format:check`。本机合成浏览器演示不等于真实数据验收或发布授权。

## 8. 总资产账户持续记账（T01）

新增接口沿用第 1、2 节严格 JSON、资源限制与本机访问边界。没有公开代理或新增持仓联动。账户级接口同时可用于 reported 和 holdings 账户；后者的现金、持仓、操作与估值完全不变，不自动合计两类数据。

### 创建账户

`POST /reported-accounts`，必须提供 `Idempotency-Key`：

```json
{"id":"synthetic","name":"Synthetic","currency":"CNY","opening_date":"2020-01-01"}
```

返回 201 账户元信息，`accounting_mode="reported"`、`opening_cash=null`、`version="1"`。账户详情的 `cash=null`。名称非空、最多 512 UTF-8 字节；币种 CNY/HKD/USD；不接收 opening_cash/positions，不生成期初资产。opening_date 只是账户元数据，不作为账户级记录日期下界。已存在 ID 使用不同键创建返回 409，不会重新初始化；同键重试返回初始回执。

### 新增、更正、作废

`POST /accounts/{id}/records`，必须提供 `Idempotency-Key`：

```json
{"id":"manual-synthetic","entry":{"kind":"cash_flow","date":"2020-01-02","flow":"-100.01","total_assets":null,"note":"Synthetic log"},"reason":""}
```

- 新建 ID 必须以 `manual-` 开头，总长仍最多 128；导入记录由服务端使用 `import-{原始记录ID}`，两者不冲突。同日期、同金额不会自动去重，只有同键同请求才复用回执；不同键重复 ID 为 409。
- `entry.kind=asset` 要求 total_assets 非空、flow 为空；`cash_flow` 要求 flow 非空、total_assets 可空；`log` 要求非空 note，两个金额必须为空。
- 金额为精确分尺度 Money 十进制字符串，范围 int64 分；资产非负，flow 可正、负、零。可选金额省略/null 表示未记录，`"0"` 为明确零。日志最多 16,384 UTF-8 字节；更正/作废原因必填、最多 512 UTF-8 字节。
- date 接受 0001 至 9999 年的真实日历日期，不按服务器今天拒绝未来日期，也不按来源创建时间猜先后。日期事实原样存储，不生成收益率或“当前估值”。
- `PUT /accounts/{id}/records/{recordID}` 提交完整 entry、`expected_version` 正整数字符串及 reason。body 的 id 可省略，提供时必须匹配路径。不能更改记录身份、账户、来源或原始快照；可更改日期、类型及内容。
- `DELETE` body 仅接受 `expected_version`、reason，保留内容并递增版本、标为 voided；不是物理删除。作废记录不能再更正或作废。CAS 不符为 `409 version_conflict`，作废为 `409 operation_voided`。

返回完整 `AccountRecord`：id、account_id、kind/date/flow/total_assets/note、origin（import/manual）、original（原始 ImportedRow 或 null）、version（字符串）、voided、created_at/updated_at（服务端 UTC 时间）。导入初版 version=1，created_at 为导入时间，来源创建时间仍在 original 中。手工记录初版也是 1。

### 有效查询与统计

- `/records` 返回 `{items,next_cursor?}`，包含导入和手工记录的最新版本，默认包括已作废记录。T02 当前记录增加正整数字符串 sequence；支持 from/to（含端点）、status=all/active/voided、limit=1..100（默认30）、cursor=`日期:sequence`，按日期、数值序号倒序。升级后旧列表游标须从首页刷新；原始持久回执和旧修订仍可没有 sequence，不改写历史。更正保留序号；并发改期时分页不是冻结快照。
- `/records/{recordID}/revisions` 返回 `{items:[{record,reason}],next_cursor?}`，版本升序，cursor 为上页末版本，limit 同上。尚未编辑的导入初版由不可变来源行投影；首次更正时将初版和新版本一并固化，此后只追加。
- `/effective-summary` 不接受查询参数，统计全部有效记录日期，不是某日 as-of 或实时估值。row_count 不含作废，voided_count 单列；asset_count/flow_count/log_count 按记录 kind 计数。from/to 无记录为 null；total_in/total_out 是正流入及负流出绝对值合计，以任意精度分累加后返回两位字符串，不因合计超过 int64 而溢出。
- latest_asset_date 为有效 total_assets 非空记录的最大日期，latest_asset_count 为该日期明确资产记录数量，包括组合行。T02 已确认同日按稳定 sequence 取最后明确资产，latest_assets 不再因多条而返回 null。全部无资产时 date/assets=null、count=0。此原始字段摘要不生成沿用点，截止日与沿用分析见第 9 节。
- 原 `/import-summary` 和 `/imported-records` 仍只返回不可变原始导入内容。当前有效统计单独暴露，前端明确分区。原文件同内容重传只返回原导入回执，不删除覆盖层或手工记录、不重复资金流；不同导出版本合并仍未实现。

### 回执和前端恢复

新建 reported 账户及账户级记录写入共用持久化 `account_record_receipts` 键空间，独立于既有持仓操作和导入回执。规范化请求含操作/账户/记录身份；同键异请求 409。回执与变更及修订同事务提交，读取回执先于当前版本检查。即使稍后记录已更正/作废或服务已重启，原请求重试仍返回最初成功结果，不再次写入。失败事务不占用键。

前端复用 PendingWrite 固定字节和 key；408/5xx/网络失败后保持不确定状态、锁定写入与账户切换，按原请求重试。成功回执可能是旧版本，只表示该次写入成功，随后独立读取最新列表、摘要和修订。读取失败不冒充写入失败。请求只保留页面内存，不将私有数据写入 localStorage；强制关闭页面后无法自动恢复请求，服务端已保存回执仍持久存在。CAS 冲突不自动改成最新版本重试，用户应重新选择当前记录核对后更正。

## 9. 分析依据与失效追踪（T02）

`GET/HEAD /accounts/{id}/analysis-basis?from=2020-01-01&to=2020-12-31&since_revision=0`

- 不接受 track 参数，传入返回 400。两种账户能力模式均使用同一 account_records 当前有效记录，不筛选来源、不设置切换日期、不从审计重建金额。
- from 默认 0001-01-01，to 默认服务器北京时间今天。都为有效日期，from<=to<=今天；未来 to 拒绝 400，未来存储记录保留但不参与投影。所有日期按 Asia/Shanghai 业务日解释。净流入含 [from,to]，opening 严格在 from 之前，closing 在 to 及之前；没有前置期初即 null，不生成虚构初始入金。T04 收益边界单独见 returns，不改变这些 T02 字段。
- 返回 `{account_id,currency,from,to,timezone,revision,change_revision,points,opening,closing,net_flow,changes,previous_basis_affected,status,returns}`。金额和序号/版本为字符串，net_flow 用任意精度分累加。没有资产为 null，显式零为 "0.00"。
- points 包含 date/sequence/record_id/version/flow/assets/status/source_id/source_version/source_date/selected，附当前完整 record（含 original、origin、quote_audit_id、manual_assertion、carried_from 等来源信息）；操作记录另附 operation。每天按稳定 sequence 取最后明确非空 total_assets，后置资金流行不覆盖该资产或降级为参考。weekly_carry 未经人工确认不算新观察；没有明确资产时才取最后非日志记录作为沿用/缺值依据。当天其余逐条明细及来源均保留，日志不会抹除资产。
- reported 的同行资产已含同行资金进出。明确非空资产为 reported；缺资产资金流只沿用 date+sequence 之前最近明确资产原额为 carried，不加现金流；无前置资产为 unavailable，日志为 log。carried 的 source_* 指向明确资产记录，record.total_assets 仍为 null，不保存伪观察。
- 稳定顺序直接保存在 account_records.stable_sequence（API 为 sequence）：新导入按来源行序，新记录按创建事务分配，更正/改期保留序号。不创建 account_record_order、兼容视图或升级旧 005 的迁移。统一记录不按来源重排，不从更正时间推断业务顺序；日终聚合与逐条展示是两个层次。
- 所有 flow 只汇总 canonical 记录一次。操作写入时按操作 ID + 账户腿生成确定金额；转入/转出/组合操作只计外部金额，普通买卖及留存分红为 NULL。操作详情不二次增加 flow。observed/stale/untracked 只表达自动记录的来源可信状态，不改变所存金额。
- revision 是本次投影（含来源和边界）的 SHA-256；change_revision 是该账户 account_record 审计最大 ID（空为 "0"）。since_revision 为该账户上次水位，非负规范整数字符串且不能超当前水位。changes 返回其后的 source_id/source_version/revision/from/to/reason；to=null 表示 [from,+∞) 潜在下游依赖范围，**不等于每个日期都改变或已完成重算**。创建、导入、更正、补记、作废同事务追踪；旧/新日期取最早，转账旧/新参与账户分别追踪，不污染无关账户。
- previous_basis_affected 只有显式给 since_revision 且后续变更起点不晚于请求 to 时为 true；首次查询没有旧结果，不宣称过期。账户级本次结果始终重新投影，历史观察金额不变。status 为 current/unavailable/untracked_history/pending_recalculation；缺少 opening 时即使 closing 已知也不能据此计算完整区间收益。T06 已取消，pending_recalculation 仍表示旧持仓总额受影响，不表示已安排历史价格回填；不能把它自动改为有效。账户级收益直接用有效总额/资金流重新计算，后续收益曲线归入 T07。
- GET/HEAD 不采集行情、不写分析缓存或资产观察。一次一致事务，最多 10,000 条截至 to 的原始记录/观察和 10,000 条游标后的变更；超出返回 400 invalid_query，不截断冒充完整分析。目前非大规模分页分析服务。列表明细仍使用原有有界 API。

### T03 图表与同日明细快照

- 复用 analysis-basis，不增加第二个事件请求或跨请求分页拼接。reported 的 record 已包含同次事务中的当前日志和原始导入明细；holdings 的 points 现在包含区间内全部有效持仓操作，包括非资金操作，附 `operation: {operation,note,version,created_at,updated_at}`，结构与操作查询的 public Record 相同，所有金额/序号/版本仍是字符串。
- 操作腿已写入同一 account_records，外部资金事件遵循统一缺值沿用规则，普通买卖、留存分红为 log、flow=null；不再使用独立 flow/operation 轨道。deposit_buy / sell_withdraw 只计独立 amount，转账只计当前账户腿。原始操作 amount 不是有符号账户流，不能拿它直接合计净投入。无备注的操作也保留，作废操作不进入有效图表；修订继续走原 API。
- 所有操作及备注和观察/状态均在同一个 sql.Tx 读取，并纳入 revision 指纹。修订后的备注不会与旧资金流拼接。同账户所有来源统一分析，按日期和稳定序号展示，不推断真实日内资金/采样时刻顺序。
- 新增区间合并 points 的 10,000 条上限，超限同样返回 400 invalid_query，绝不静默截断。原始记录/观察和变更各自的已有上限仍适用；缩短 from 不保证解决前置历史超限，缩短 to 或后续分页分析才可能解决。持仓输入仍沿用现有全账本读取与重放，尚未实现大账本的增量处理。
- Vue 只将 selected 日终资产画为主要点；carried 独立空心菱形，不进入实际观察线；同日其他明确观察保留小点及全部明细。相邻自然日同类观察可连线；缺日以单个 null 断点阻断，不生成每日空样本，不把期初/截止参考补成新观察。只有资金/日志的日期仍能从独立事件带和日期表进入详情，没有伪造的资产纵坐标。
- 图表使用原币种资产金额，不计算收益、收益率或 T04 指标。仅坐标使用 Number，轴标明近似；tooltip 使用 Canvas richText，只呈现日历日期、固定状态和精确原金额，任意投资日志和标签仅经 Vue 文本插值显示。日期表和同日明细各按 30 条本地分页，键盘和手机无需依赖 Canvas 即可访问全部记录。
- 读取失败清图；切换账户或日期中止并丢弃迟到响应，核对账户、显式区间和嵌套记录身份。没有轨道选择器。图表、页面选择及当前预览 GET 均不写记录；仅明确保存成功后刷新记录/历史，刷新不会再次 POST。

T03 合成验证与限制见[本机验收记录](../../docs/validation/2026-09-07-ledger-t03.md)，不代表真实账本或生产验收。

## 10. 同快照收益（T04，2026-09-07）

沿用上述 GET/HEAD analysis-basis 和严格查询校验，不增加接受客户端金额的接口。响应新增 `returns`，由同一次事务得到的完整 Basis 在内存计算；不再次读库、不请求行情、不写估值或缓存，无迁移。事务之后发生更正时，此响应仍是读取时的一致版本，刷新才读取新版本。

```text
returns: {
  revision, requested_from, requested_to, start_mode,
  effective_from, effective_to, days, opening, closing, net_flow, denominator,
  profit, modified_dietz, xirr, twr, twr_annualized, curve, warnings, flows, investor_flows
}
metric: { value: string|null, percentage: string|null,
          status: available|reference|unavailable, reason: string }
flows: [{ date, record_id, version, flow, weight_days, period_days }]
investor_flows: [{ date, amount }]
curve: [{ date, record_id, baseline, profit, modified_dietz, twr }]
```

- `returns.revision` 等于外层 revision，覆盖图、来源明细、收益计算。指纹现包含是否省略 from；省略与显式 0001-01-01 不再有相同指纹。since_revision 仍不参与指纹。外层 `from/to/opening/closing/net_flow/status` 保留 T02 意义，不能拿它们替代收益对象中的边界或净流入。
- `requested_from/requested_to` 保留调用者传入的日期，省略为 `""`；外层 to 给出本次解析后的默认截止。`start_mode=baseline` 表示未传 from：以首个已知日终资产为基准，排除该日全部资金流，不推断此前初始财富。首行只有 total_assets、flow=null 是有效基准，不是缺少本金，不生成初始入金。同日选最后明确资产，不被后置资金流行替换；原行金额及顺序不变。
- `start_mode=custom`：opening 为 from 之前最近资产，计算边界 effective_from 为 from 前一日日终；资金流包含 from 当天。没有此前资产或显式 from=0001-01-01 时 missing_opening，不补零、不反推入金。
- effective_to 为截止日及之前最后资产/沿用记录日期，不用空白截止日、导出日、后续日志或今天延长年化。所有账户均按统一规则处理缺资产资金流；carried 日期可作为终点，原額不加流量，reference 明示入金后可能显示账面亏损。若输入存在最后 closing 之后的非零资金流则 closing_before_flow，不悄悄排除它们，即使同日净额为零。空区间或同一基准点为 no_interval；明确 from=to 且有前置资产可形成一天区间。
- 日期使用北京时间业务日和公历整数日差，支持 0001..9999，不使用可能溢出的 time.Duration。`days` 是两端日终之间的天数；无有效区间时可能为零或负，指标均不可用。opening/closing 是完整 BasisPoint，带声明日期、source 日期/ID/版本及估值采样元数据，不重新解释原报价日期。
- profit = close - open - net_flow。仅账户边界的外部资金流为正流入、负流出；持仓买卖/分红不再计投入，组合交易只计独立 amount，单账户转账计对应腿。同行资产是 post-flow，绝不重复加款。
- Dietz = profit / (open + Σ flow × weight_days / days)，流量按日终，weight_days=effective_to-date，期末日权重为零。金额合计与加权分子用 big.Int，分母和收益率用 big.Rat；`denominator` 是本位币单位的精确有理数字符串（例如 `120` 或 `100/3`），不大于零仅使 Dietz 不可用。
- `profit.value` 是两位金额；两种 rate.value 是十二位小数的比率（0.1 = 10%），属于舍入后的输出，不作为后续计算输入。percentage 为两位百分数数值字符串，不带 `%`；直接从未舍入计算值产生，避免从十二位 value 再舍入造成双重舍入。收益金额和不可用指标的 percentage=null。全部 half-away-from-zero，舍入为零不带负号，数值不回写原始事实。浮点只用于 XIRR 数值求解及既有图坐标。
- `flows` 保留纳入计算的每笔精确金额、版本和权重分子/分母；`investor_flows` 是加入 -opening、-deposits、+withdrawals、+closing 后按日期以任意精度分合并的净额，零额也可见。默认基准日流量不再次出现。端点缺失、过期或其他前置条件失败时，明细可能为空/不完整，不能当成完成的对账。
- `available` 仅表示依据足以计算，不是审计背书或价格是收盘价。carried 或 opening 来源早于计算边界时为 reference，三项分别带状态。任一端点 stale/untracked 阻止三项指标；无关中间过期观察不阻止只依赖端点和资金流的计算，因此外层 status 仍可能 pending_recalculation 而 returns 可算。端点观察日期已追踪不等于价格/FX 均为当日收盘，holdings 始终提示 sampled_valuation_not_daily_close。

### XIRR 求解与状态

XIRR 对投资者现金流求 `Σ amount / (1+r)^(actual_days/365) = 0`，独立于 Dietz，不把其机械年化。不足 365 天添加 short_period_extrapolation，仍展示可解结果；未将参考产品“超过半年才展示”的规则当作已确认需求。

- 同日精确净额后无正负变号：no_solution（不包括把 -100% 作为有限根）；全部为零：indeterminate_all_zero，不能显示信息量为零的年化。
- 一次符号变化的指数多项式在完整定义域具有唯一根；多次符号变化返回 possible_multiple_roots，仅表示未证明唯一，**不是宣称已证明多根或无根**。经典 -100,+230,-132 的两根案例也不选择任意根。当前不实现完整非传统现金流根隔离。
- 精确路径：仅一次变号且精确净额为零时 XIRR=0；仅两笔非零现金流且相隔恰好 365 天时，使用 `r=-last/first-1` 的精确有理数，不经过浮点。此路径无浮点搜索范围限制；例如整数分 `[-20000,20001]` 和 `[-20000,19999]` 返回比率 `±0.000050000000`、percentage `±0.01`，不因浮点落在数学中点一侧而错误归零。输出 value 仍是十二位舍入值；真实比率严格大于 -1 时，也可能舍入显示为 `-1.000000000000`，不能把显示值重新作为求解输入。
- 其他情况在 y=log(1+r) 的 [-32,32] 内二分，最多 256 轮；指数缩放、归一化残差、补偿加总，近零使用精确净额与 expm1 保留超大资产下的分级差值。归一化残差 <1e-12、利率区间宽度 <2e-14、利率浮点 ULP <1e-14 同时通过后，只产生一个**候选显示值**，不是舍入正确的证明。范围不足/边界消失为 out_of_solver_range，未满足搜索精度/收敛为 not_converged；极端非精确路径年化仍可能不可用。
- 候选结果还必须通过独立舍入认证：将十二位 value 和两位 percentage 对应的比率舍入区间取交集，以精确十进制有理数表达两端。在每个端点令 `q=(1+r)^(-1/365)`，用 192 位 `big.Float` 向外舍入包围 q，并以整数日数幂计算 NPV 的上下界。所有运算为有向舍入的加减乘除/整数幂，不依赖 math.Exp/Expm1 的误差估计。上下端 NPV 必须严格异号且符合唯一根方向，证明真实根严格处于两种舍入区间内；靠近 -1 时以定义域开边界处理下界。
- 若区间仍包含零、候选舍入边界无法认证或精确路径之外的真实根恰落在中点，返回 `precision_unresolved`，value/percentage 均 null，前端说明“数值不确定性跨越舍入边界”。不加固定 epsilon、不相信单纯浮点括区、不将近零残差当成精确根，也不从十二位 value 二次舍入百分数。730 天 `[-400000000,400040001]` 等真实年化 `+0.00005` 的案例目前保守不可用；这不表示该根无解。其他恰好十二位比率中点、三笔或更多现金流同样受此守卫，不承诺所有数学可解根均能展示。
- 前置不可用原因：missing_opening、missing_closing、no_interval、stale_endpoint、untracked_endpoint、closing_before_flow；Dietz 特有 nonpositive_denominator。warnings 为 carried_assets_unchanged、sampled_valuation_not_daily_close、short_period_extrapolation 的适用组合。
- 沿用 T02/T03 各 10,000 条限制；纯计算最多 10,000 points，XIRR 最多 10,002 个分组日期（包括端点），超限拒绝不截断。循环检查 ctx 取消，浮点搜索最多 256 轮；每次舍入认证最多两个端点，每端 q 根区间固定 [0.5,2]、最多 192 次二分，NPV 整数幂用平方求幂，复杂度 O(分组日期数 × log(日差))，精度固定 192 位，无自适应无界计算。认证范围不足同样 precision_unresolved。未改善 T02 已有全账本重放的大账本限制。

Vue 同请求展示三张收益卡及每页 30 条详情。账户、日期或刷新变化中止旧读，校验 revision 和账户身份；无来源轨道选择器，不另取资金流拼接。周任务只读面板及统一审计面板已实现；T07 见下，基准仍未实现，T06 仍取消。旧阶段证据保留。

### T07 曲线与 TWR（2026-09-08）

- 仅扩展同一 returns 快照，无额外数据库读取、API、网络、缓存或写入。curve 是一枚明确 baseline 锚点加其后实际 selected 日期，最多 10,001 点；不补齐自然日。锚点有效时三项为零（零本金不是其含义），无正日数区间时摘要仍 no_interval。custom 锚点在 effective_from，保留真实 opening 记录身份及参考状态。
- 每点 profit 和 modified_dietz 都从共同 opening 计算。以 big.Int 累计净流量 S 和资金流日数矩 M，前缀加权分母为 `n*(opening+S)-M`，每点 O(1) 次大数运算，整段 O(n) 遍历；不逐点调用 calculateReturns 或 XIRR。最终曲线点与摘要的值、状态、原因相同。
- TWR 日终约定：`factor=(当日 post-flow 总资产-当日净资金流)/上一必需资金边界的 post-flow 资产`。仅在包含非零外部资金事件的日期永久链乘，非零进出即使净额抵消也要求当天边界。普通观察点从上一必需边界计算展示值，但不永久链乘，避免无关旧观察污染后续结果。
- 必需边界缺资产为 missing_flow_boundary；stale/untracked 阻断该点及后续链，普通无资金日 stale/untracked 仅该点不可用。carried 边界让必需链持续 reference；没有实际新记录的日期不造观察。周沿用不是新观察，人工确认后才按明确资产参与每日选择。
- 分母零为 zero_twr_base；负因子为 negative_twr_factor；因子零合法表示 -100%，年化同为 -100%。若之后需要除以零资金边界才阻断。TWR 不借用 Dietz 代替缺失边界，摘要资金加权指标仍保留自身规则。
- TWR 精确 big.Rat 链乘，自动约分后分子/分母各最多 32,768 位，整数部分最多 320 位（给百分比留出 100 位十进制契约空间），超限 twr_product_limit。不将舍入结果送回乘积；遍历检查取消信号，保留 10,000 原始点限制。
- 复合年化为 `(1+TWR)^(365/days)-1`。零增长、365/days 为正整数时使用精确有理数路径；其他路径把精确增长 N/D 转成合成现金流 `-D,+N`，直接传 big.Int 给既有 XIRR 唯一根求解及双舍入区间认证，数学上完全等价，不转 int64、不使用未经认证的 math.Pow。仍受 [-32,32] 对数搜索、256 轮、192 位向外舍入认证限制；边界/中点无法认证时返回 precision_unresolved 等既有原因，不承诺所有数学可解年化都展示。
- UI 视角仅本地内存切换：个人为 profit/Dietz/XIRR，基金经理为 profit/TWR/TWR 复合年化。趋势可切换收益率/累计收益；实线实点可计算、虚线空心参考、不可用真实断线并在表中说明原因。连线明确表示已知采样间连接，不是逐日观察。事件带用外层同快照 points，连摘要不可用或基准日的真实进出也保留，转入红/转出绿。
- 仅坐标使用 Number，精确字符串用于 Canvas 文本 tooltip 和 Vue 表格；任意来源 ID/备注不进入富文本解释。表格每页 30 条，原生按钮/选择器可键盘操作；账户/日期切换清空旧读和图，ResizeObserver/dispose 清理沿用现有模式。无基准、年度表、回撤、汇总、设置或从零起始新 UX。
- 实际离线验证及未执行项见 [T07 本机记录](../../docs/validation/2026-09-08-ledger-t07.md)。完整用户验收、浏览器验收与发布均未宣称完成。

### 历史阶段验证记录

以下是此前阶段记录，测试数量不代表 T01 当前总数。app 测试验证配置拒绝非回环监听、实际 peer 限制、CSRF、默认不建库、双库生命周期与初始化失败清理。Nginx 配置只有静态检查；本机没有 Nginx/Docker，未做本次代理运行时验收，更未部署到生产。

本轮已通过 `make check`、`make test`、未启用真实来源的 `make test-e2e`，以及白酒前端的 14 项测试、构建和格式检查。未使用用户财务附件作为公开测试数据。

网页阶段再次通过上述后端验证及前端 44 项测试、构建、格式检查。使用真实 Go 进程和临时合成账本完成浏览器创建、交易、更正、分红/作废和错误拒绝联调；现有接口数值、迁移和持久化契约未改变。

腾讯汇率阶段：合成上游测试覆盖最新/历史、周末、重复/越界日期、币对、倒数精度、超时及并发限制，并验证查询新汇率不修改已保存快照、修订或幂等回执。真实本机接口核验了 USD/CNY 最新、HKD/CNY 周末历史、CNY/USD 历史倒数；浏览器完成历史自动取值保存、编辑保留原快照，以及模拟获取失败后手工更正。只使用临时合成账本，不使用真实财务附件。

历史阶段验证：002 升级与重开、相同日期/时间追加、并发、超时/取消/保存失败回滚、旧账更正后快照不变、分页/账户隔离及损坏拒绝通过。真实进程验证现金账户历史跨两次重启保留；浏览器使用临时合成港股持仓和真实行情，确认 GET 追加 #1/#2、HEAD 不追加、历史显示冻结名称和 FX。前端 76 项测试、构建/格式，后端检查/race/非真实来源进程测试通过。未部署。

## 11. 周六总资产任务（T05 后端）

2026-09-07 用户确认默认每周六北京时间 08:00，先完成后端任务及数据结构。全局 `LEDGER_WEEKLY_ENABLED=false`，不因启用账本、访问页面或读取状态自动打开；开启须同时 `LEDGER_ENABLED=true`。`LEDGER_WEEKLY_TIME=08:00` 是默认值，可配置为严格 HH:MM，时区固定 `Asia/Shanghai`，不提供浏览器开关或手动任务重试写接口。当前回环和令牌保护不变，未修改生产环境配置。

### 时间与重试

- 执行窗口是每周六配置时刻至 24:00（默认 `[08:00,24:00)`）。窗口内启动/重启可以补执行当日任务，并纳入之后新建的账户；不枚举漏掉的历史周六。
- 关闭时 worker 不建任务、不恢复中断状态、不访问行情。启用后旧日 pending/running/待重试 failed 转为 `skipped/expired`，不在周日用当前价格补写周六；第三次已失败且无后续尝试的记录保留原失败原因。
- 每次 tick 最多新建 20 个任务、串行执行 20 次尝试；旧任务过期/中断恢复各最多 20 条。轮次结束后等一分钟，临近下次计划时刻提前唤醒；不是每个账户都能保证在 08:00:00 同时完成。
- 同账户/周六最多三次尝试。第一次失败后至少等 5 分钟，第二次后至少等 30 分钟，第三次不再重试；实际执行还受下一轮和当天窗口约束。running 重启后标 `failed/interrupted`，保留已消耗次数并从恢复时刻计算退避。数据库独占文件锁证明旧进程已退出；运行中有 72 秒租约供失败清理失败时恢复。
- 每次估值有 12 秒总预算，生产股票/FX 仍受各自限流及网络超时约束；失败状态清理另限 3 秒。账户失败不妨碍其他账户任务或手动记账。数据库整体错误也不会热循环；停止时取消 provider，等待 worker 退出后关闭数据库。

### 成功与来源

- fresh schema 的 `weekly_jobs` 以 `(account_id,scheduled_business_date)` 唯一，只约束计划任务，不对普通总资产记录做账户/日期去重。每个任务最多链接一个 history_id，历史记录也只能链接一个任务。
- 所有账户均按 `pending → running → succeeded/failed` 处理；failed 在上限内按退避重新 running。无自动来源且没有任何历史总资产时才 `skipped/no_source`，跨日为 `skipped/expired`。成功/跳过终态及任务身份不可更新或删除，尝试次数用于完成 CAS fencing，迟到的旧尝试不能覆盖新尝试或已成功记录。
- `holdings_current` 使用当前 holdings 现金/持仓；`account_record_carry` 复制计划日期及以前最近一笔有效、非沿用总资产。沿用记录保存原始记录 ID、稳定序号、版本、日期及金额，不能解释为新报价、现金或持仓。明确的纯现金零值可自动估值且不请求行情；任一非零持仓缺必要股票/FX 时 `failed/incomplete_valuation`，不保存现金小计冒充总额。
- 两种成功路径都不通过内部 HTTP。自动估值事务插入 account_records 和报价审计；沿用事务插入 `weekly_carry` 记录和不可变来源快照；随后原子更新任务及任务审计。history_id、账户、计划日期、记录来源及对应审计类型由约束核对，任一步失败全回滚；失败状态落库时单独审计安全错误码。
- 采样前捕获账户 holdings 变更水位；提交事务内若出现水位后的相关变更（from_date<=as_of），返回 `failed/basis_changed`，不保存为成功周更新。无关账户、未持有证券新增、reported 轨道写入不会误触发；沿用现有业务层不可变账户/证券身份约束。
- 保存实际 quote/FX 的业务日期、quoted_at、fetched_at、当次 ledger_at/calculated_at/saved_at。校验证券身份、币种、正价格/汇率、最新腾讯币对来源、实际获取时间；旧日期仍为 prior_date，不强行改成周六收盘，不新增固定过期天数或历史交易日历。采价或提交准备跨北京午夜时过期并回滚，不能 backdate。
- 自动计算和沿用总额写同一 account_records，flow=NULL；冻结来源在 audit_log，周任务成功状态与相关审计同事务。真实估值继续使用 schema-1 冻结 JSON；沿用使用独立 schema-1 来源快照，不混入估值历史。GET 估值只读；任务详情按 source 返回对应冻结证据。

### 只读接口

全部支持 GET/HEAD，HEAD 没有正文，不调度、不采集、不保存观察。`/weekly-status` 不接受查询参数，返回：

```text
{enabled, timezone:"Asia/Shanghai", weekday:"Saturday", time:"08:00",
 next_scheduled_at: RFC3339带+08:00|null, window_open, max_attempts:3}
```

next_scheduled_at 是严格在当前时刻之后的周计划时刻，不是某账户的重试时间；处于当日窗口时指向下周六，当日补执行由 window_open 与任务状态说明。关闭时 next_scheduled_at=null、window_open=false，历史任务仍可读取。

```text
GET /api/platform/ledger/accounts/{id}/weekly-jobs?status=failed&limit=30
GET /api/platform/ledger/accounts/{id}/weekly-jobs?cursor=123&limit=30
GET /api/platform/ledger/accounts/{id}/weekly-jobs/123
```

列表返回 `{items:[WeeklyJob],next_cursor?}`，按任务 ID 降序；limit 默认 30、范围 1..100，cursor 是最后一条 ID，均为规范正整数字符串。status 可省略或为 pending/running/succeeded/failed/skipped，省略表示全部（不接受 `all`）。拒绝未知、重复、空值、空 `?`、非规范数字、跨账户 query 参数；详情不接受查询。查询格式错误 400，不存在账户/任务或另一账户的任务 404，非 GET/HEAD 为 405。翻页不是冻结快照。

```text
WeeklyJob: {
 id:string, account_id, scheduled_business_date:"YYYY-MM-DD",
 source:"holdings_current"|"account_record_carry", status, attempts:0..3,
 created_at, started_at:string|null, finished_at:string|null,
 next_attempt_at:string|null, error_code, history_id:string|null,
 carry?: {id, saved_at, schema_version:1, account_name, record_id,
          account_id, currency, as_of, total_assets,
          source_record:{id,account_id,sequence,version,date,origin,total_assets,
                         manual_assertion?}}
}
```

任务时间为固定纳秒精度 UTC RFC3339。started_at/finished_at 为最近一次尝试/终态时间，attempts 是已开始次数，不是完整逐次日志；重试后旧错误会被本次结果替代。next_attempt_at 只表示最早允许时间，是否启用及是否仍在窗口须同时检查。history_id 只有 succeeded 非空。`carry` 仅在成功沿用任务的详情中返回，列表不携带大快照。错误只输出固定分类，不含 provider 正文、SQL、密钥或私有异常：

| error_code | 含义 |
| --- | --- |
| 空字符串 | pending/running 或已成功 |
| `no_source` | 无自动来源，且计划日期及以前没有可沿用的有效总资产 |
| `expired` | 错过当日窗口或跨午夜，不能以今日值补历史 |
| `interrupted` | 重启或运行租约到期回收中断尝试 |
| `canceled` / `timeout` | 上下文取消 / 本次总预算超时 |
| `basis_changed` | 网络期间账户相关持仓依据改变 |
| `incomplete_valuation` | 必要报价/FX 缺失或校验未通过，完整总额不可用 |
| `invalid_valuation` | 精度溢出、来源/时间或冻结快照校验失败 |
| `storage_error` | 原子保存失败；若连状态清理也失败，暂留 running 等待租约恢复 |

### 前端只读用法

`/ledger` 的“周六调度”使用独立账户选择器查询全局状态、状态筛选的 30 条任务页和任务详情。`holdings_current` 成功任务另读 `accounts/{id}/valuations/{history_id}` 冻结估值；`account_record_carry` 的冻结来源直接随任务详情返回，不请求估值接口。上述操作只做 GET，不调用当前 `valuation`，不改变记账账户、不触发采价或保存。没有浏览器启用、立即执行或任务重试写接口。显示的是查询时状态而非 worker 心跳；关闭时旧 running / next_attempt_at 仍可读取，不表示正在执行或承诺重试。金额和 ID 保持字符串，沿用项明确显示原始记录日期，不把旧金额作为当前行情证明。[前端用法](../../frontend/README.md#t05-周六调度只读)。

T05 后端与只读 UI 已覆盖所有账户：有持仓来源时自动估值，其它账户沿用最近有效总资产，没有历史总资产才 skipped/no_source。沿用是明确标记的新周记录，不是新行情采样。启用仍仅由服务端配置控制；没有历史回填、事件日任务、认证改动或生产启用。本轮仅完成定向测试，完整验收后统一补充验证记录。

## 12. 统一操作审计与记录来源

fresh schema 只有 `001_init.sql`。所有来源直接写同一 account_records 并分配稳定 sequence；更正保留原 sequence，不使用 updated_at 或修订时间重排。导入原行、操作版本、冻结估值、周沿用来源及记录修订全部进入同一 audit_log，不另建兼容视图或第二历史表。

人工记录回执按原 account/id/version/action/after_json 校验审计；账户创建回执验证创建审计或旧迁移捕获；导入回执验证批次、digest、元数据、原行计数及每条版本 1 创建审计。缺失/损坏返回 data_integrity，无额外写入。后续正常更正不使旧回执失效，同文件新键确认只验证原批次，不新增导入审计。

- `GET/HEAD /audit?account_id=a&entity_type=account_record&action=replace&limit=30&cursor=123`：可选账户、对象类型和动作精确筛选，ID 倒序；默认分页规则同其他列表，最多 100 条，UI 固定 30。摘要只返回 id/correlation_id/action/entity_type/entity_id/account_id/version/recorded_at/source，不加载大 JSON。
- `GET/HEAD /audit/{auditID}`：返回同一摘要及 before_json/after_json/metadata_json 字符串，保留原始 JSON 字节，浏览器不能用浮点重新编码。详情最多 8 MiB。无修改/删除接口，方法不符为 405。只读查询不会新增审计或触发估值。
- source 为 human/system，表示操作来源而非用户身份；不保存令牌、Cookie、认证头。
- account_records 的 origin 为 import/manual/currentrefresh/weekly/weekly_carry/operation；可带 operation_id、quote_audit_id、manual_assertion 和 carried_from。来源是追溯信息，不是分析筛选条件；weekly_carry 保留原始记录身份、日期和金额。
- 关联持仓操作的记录由原操作整体管理，普通记录 PUT/DELETE 返回 422 `source_managed_record`。修正/作废转账同时维护双方，换账户使旧腿作废，不遗留有效资金流。普通买卖和留存分红只记日志。
- 自动资产可更正或作废；更正后 manual_assertion=true、原报价引用保留，金额不再由旧报价背书。周任务原成功记录/回执仍指向冻结证据，不能重试恢复作废记录。
- 初次导入在同事务内检查无 canonical 记录和持仓操作，否则 409 `initialization_requires_empty_account`。原回执重试在空账户检查之前返回，不因之后写入而失败。不能用删除用户数据重新初始化。
- 所有已提交业务变化与 audit_log 同事务。幂等重试无新审计，写库失败无成功审计；周任务失败状态确实持久化时有状态审计。标准请求诊断不属于业务审计。
