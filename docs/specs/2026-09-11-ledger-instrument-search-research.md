# 账本证券代码查询：调研与接口契约

日期：2026-09-11。范围：证券身份查询、前端自动回填及账户持仓展示位置调整，不改数据库或估值规则，不运行 E2E，不访问或写入生产账本。

## 结论

采用腾讯官网当前使用的 JSON smartbox 搜索作为唯一候选来源，再调用现有腾讯 `batch(ctx, symbols)` 行情接口核验身份、解码名称并读取实际币种。不使用中文模糊搜索，不执行 JavaScript，不增加备用源。

搜索与估值分离：新增 `InstrumentSearchProvider.Search(ctx, code)` 和 `Handler.InstrumentSearch` 注入点，原 `QuotesProvider` 接口不变。应用启动时将同一个 `NewTencentQuotes()` 实例注入两个接口；不产生启动网络请求。查询本身不读写账本，不创建证券、不保存行情或触发后台采集。

## 调研依据

先检索接口文档，再检查腾讯官网的网络请求与 JS 字段解析；实现时另以只读 HTTPS GET 复查了 `900901` 的 JSON 搜索及下表八个 symbol 的行情字段。未将价格快照作为测试断言，也未在单元测试中访问真实腾讯服务。

| 来源 | 观察与用途 |
| --- | --- |
| [腾讯贵州茅台页面](https://gu.qq.com/sh600519/gp) | 官网实际使用下面的 JSON 搜索接口。 |
| [JSON smartbox：600519](https://proxy.finance.qq.com/cgi/cgi-bin/smartbox/search?stockFlag=1&fundFlag=1&app=official_website&c=1&query=600519) | 顶层含 `stock`、`fund` 等数组；股票候选含 `code`、`name`、`type`，也可能含 `reportInfo.match_level`。 |
| [JSON smartbox：900901](https://proxy.finance.qq.com/cgi/cgi-bin/smartbox/search?stockFlag=1&fundFlag=1&app=official_website&c=1&query=900901) | 本次确认 `sh900901` 为 `GP-B`，不是 A 股。 |
| [腾讯批量行情](https://qt.gtimg.cn/?q=sh600519,sz000001,sh900901,sz200012,hk00700,hk80700,sh688001,sz300001) | GBK 编码的 `v_<symbol>="...~...";` 文本；名称、代码、类型、币种均从字段读取。 |
| [腾讯行情 JS bundle](https://st.gtimg.com/quotes/hs-fund/bundle.13362df9.js) | 先前调研通过 `adaptHS` / `adaptHK` 核实字段索引；构建文件名可能随发布变化。 |
| [旧 smartbox](https://smartbox.gtimg.cn/s3/?q=600519&t=all) | 先前调研确认仍可用，但本次不接入、不做双源兼容。 |
| [Go Text Decoder 文档](https://pkg.go.dev/golang.org/x/text/encoding#Decoder) | 无效编码可能被替换为 `U+FFFD` 而不返回 error，因此解码后还必须拒绝替换字符。 |

Context7 在先前调研中未找到腾讯接口文档。本次查询 Go Text 的 Context7 条目也未取得所需 Decoder 细节，转用上述 Go 官方包文档。当前后端原本未依赖 `golang.org/x/text`，本次新增 `v0.42.0`，与项目 Go 1.26 要求一致。

### 已知搜索陷阱

- `000001` 会同时出现 `sh000001`（`ZS` 指数）与 `sz000001`（`GP-A` 股票）；不能选首项，也不能只看代码形状判断类型。
- `00700` 可能包含 `hk00700` 和关联的 `hk80700`；仅保留去前缀后的代码逐字符相等的候选。
- 直接以 `hk00700` 作为上游 query 曾返回空；实现先剥离前缀、发送 `00700`，再在本地限定 HK。
- 不依赖 `reportInfo.match_level` 替代本地精确比较，也不依赖上游排序做唯一性判断。
- 正常无匹配时 `stock`、`fund` 为数组且为空。缺少这两个数组、`null`、错误对象或 JSON 损坏不能解释成无匹配。

### 行情字段

索引从 0 开始，先将整段行情从 GBK 解码，再按 `~` 分割，不求值 assignment。不能直接拆原始字节，因为合法 GBK 双字节字符可能以 `0x7E`（ASCII `~`）结尾：

| 字段 | SH / SZ | HK |
| --- | --- | --- |
| 名称（GBK） | 1 | 1 |
| 裸代码 | 2 | 2 |
| 类型 | 61 | 63 |
| 币种 | 82 | 75 |

本次只读复查结果：

| Symbol | 行情类型 | 行情币种 |
| --- | --- | --- |
| `sh600519` | `GP-A` | `CNY` |
| `sz000001` | `GP-A` | `CNY` |
| `sh688001` | `GP-A-KCB` | `CNY` |
| `sz300001` | `GP-A-CYB` | `CNY` |
| `sh900901` | `GP-B` | `USD` |
| `sz200012` | `GP-B` | `HKD` |
| `hk00700` | `GP` | `HKD` |
| `hk80700` | `GP` | `CNY` |

这说明不能硬编码“沪深都是人民币”或“港股都是港币”。HK USD 分支有合成单元测试，但不是本次已验证的真实个股样本。

## 新端点契约

```http
GET /api/platform/ledger/instruments/search?code=600519
```

同时支持 `HEAD`，其状态码与响应头同 GET，但无响应体。其他方法返回 405，`Allow: GET, HEAD`。该路由仅在账本启用时注册，沿用应用现有账本访问边界与请求保护，不另加登录或权限规则。

### 参数

只允许且必须有一个非空 `code` 参数：

- 裸代码：完整的 5 或 6 位 ASCII 数字，保留前导零；5 位对应 HK 候选，6 位对应 SH / SZ 候选。
- 可选小写市场前缀：`sh` + 6 位、`sz` + 6 位、`hk` + 5 位，例如 `sh600519`、`sz000001`、`hk00700`。
- 不接受大写前缀、空格、短代码、超长代码、中文名称、多个代码或任何模糊匹配表达式。
- 重复参数、未知参数、空值、非法 URL 编码均为 400，不访问上游。正常 URL 百分号解码后再验证代码。

### 成功响应

HTTP 200，`Content-Type: application/json; charset=utf-8`，`Cache-Control: no-store`：

```json
{
  "items": [
    {
      "name": "贵州茅台",
      "market": "SH",
      "code": "600519",
      "currency": "CNY"
    }
  ]
}
```

精确类型：

```ts
{
  items: Array<{
    name: string;
    market: "SH" | "SZ" | "HK";
    code: string;
    currency: "CNY" | "HKD" | "USD";
  }>;
}
```

- 始终有 `items` 数组，无匹配为 `{"items":[]}`，不返回 `null` 或 404。
- 不包含数据库证券 ID、价格、分页或来源字段。`code` 不带市场前缀。
- 同一 symbol 去重；保留上游顺序，但该顺序不是接口的排序保证。跨市场多个精确股票候选全部返回，不擅自选首项。
- 前端可在 `items.length === 1` 时自动回填，多项时让用户选择；零项表示无受支持的精确匹配。
- 输出名称取自通过核验的行情 GBK 名称。搜索展示名称也进行安全校验，但两源显示空格等可能不同，不强制名称字符串完全相等。

### 错误响应

沿用 `httpapi` 平铺格式，不在响应中泄露上游正文或内部错误：

```json
{
  "code": "instrument_search_unavailable",
  "message": "instrument search is temporarily unavailable"
}
```

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | `invalid_query` | 查询参数不符合契约。 |
| 405 | `method_not_allowed` | 非 GET / HEAD。 |
| 408 | `request_canceled` | 请求 context 取消，沿用账本已有错误。客户端断连时通常无法接收响应。 |
| 502 | `instrument_search_unavailable` | provider 未注入、网络故障、非 200、重定向、超限、响应损坏或候选身份不能完整核验。 |
| 504 | `instrument_search_timeout` | 整体、单次网络请求或等待共享并发槽位超时。 |

前端以 `code` 映射文案，不依赖英文 `message`。新加入的错误码只有两个 `instrument_search_*`。

## 核验与资源边界

- 允许的搜索股票类型：SH 为 `GP-A` / `GP-A-KCB` / `GP-B`；SZ 为 `GP-A` / `GP-A-CYB` / `GP-B`；HK 为 `GP`。指数、基金、未知类型和其他市场被过滤。
- smartbox 仅提供候选，不直接作为返回值。先按完整代码及可选市场精确过滤，再调用已有 `batch`。
- 行情 assignment symbol 必须在请求集合内且唯一，内部代码必须相等，类型必须与已允许的搜索候选一致。字段短缺、额外未知 assignment、重复 assignment 或缺失任意候选均失败，不返回部分成功。
- `v_pv_none_match="1";` 出现在第二阶段表示已命中的候选无法核验，返回 502，而非空数组。
- 币种必须实际读取为 CNY / HKD / USD。沪深还核验 A 股 CNY、SH B 股 USD、SZ B 股 HKD；HK 接受上述三种实际币种，不按市场推断。
- 名称必须是有效非空文本、UTF-8 不超过 512 字节；拒绝控制/格式字符、替换字符和协议/脚本危险分隔符。GBK 解码出现替换字符也失败。
- 搜索 JSON 必须有效 UTF-8、无重复键、嵌套深度不超过 16，无尾随脚本；只解析数据，绝不执行上游 JS。
- 固定 HTTPS 上游 URL，不接受用户 URL，不跟随重定向，不转发客户端 Authorization / Cookie。
- 搜索总预算 12 秒，每次上游请求含槽位等待和响应读取最多 6 秒；client 另有 6 秒超时。沿用调用者更早的截止时间。
- smartbox 响应最多 256 KiB，行情响应最多 1 MiB，读取上限多 1 字节用于检测超限。
- 搜索与估值、不同 `TencentQuotes` 实例共享同一个进程级 4 槽并发限制，持有到响应体读取及关闭结束。没有逐候选 goroutine。
- 无匹配只调用一次 smartbox；存在候选时再调用一次批量行情。没有重试、缓存或备用源。并发限制不是每 IP 限流，公网请求仍可能消耗共享额度。

## 限制与非目标

这些腾讯接口没有找到可依赖的公开契约或 SLA，字段位置、类型值、搜索召回和可用性都可能变化。当前类型白名单刻意保守，未来合法但未知的股票类型也可能返回空；已选候选的行情结构漂移则返回不可用。没有实现完整证券主数据同步，空数组不等同于“全球不存在该证券”。

本功能只查身份，不保证可交易、行情新鲜、当前有成交或估值可用，不对查询做估值价格/时间检查。

**原 `quoteSymbol` 代码白名单和币种支持完全不变。** 例如 `hk80700` 可返回 `market: "HK", currency: "CNY"`，但现有估值仅支持 HKD 港股，仍报 `currency_mismatch`。搜索成功不能被前端解释为估值一定支持；其他不在原估值代码白名单内的身份也不自动获得估值支持。

不扩展中文模糊搜索、美股、北交所、基金、指数、备用接口或数据库迁移。

## 前端行为

- 账户主页面按收益曲线、账户持仓、账户记录的顺序展示。复用同一个 `LedgerHoldings` 组件保留原管理页入口、交易管理、手工持仓及显式估值功能；页面加载仅读取持仓，不自动查询行情或保存总资产。
- 登记证券弹窗先展示代码查询，唯一精确结果自动回填名称、市场、代码与币种，多结果必须选择。支持输入大写前缀，前端先转为小写再请求后端。
- 查询无结果、超时或不可用时允许重试或手工填写；查询词变化取消旧请求并清除旧自动回填，查询期间不允许提交。查询不写入账本，明确点击登记后才创建证券。
- 真实币种在登记时保留，不再按市场重新覆盖；重复证券提示使用已有登记，写入仍遵循原幂等请求与结果不确定时的锁定机制。

## 验证

新增 `backend/internal/modules/ledger/instrument_search_test.go`，通过 Handler fake 与包内 RoundTripper 注入构造响应，不连接真实来源。覆盖正常身份、空数组、前缀剥离、指数/基金/未知类型过滤、HK 关联条目、跨市场歧义、去重、GBK、B 股、HK CNY / USD、原 HK CNY 估值拒绝、参数及响应注入、缺失/损坏数据、状态码、重定向、响应上限、读取失败、网络错误、超时、取消和共享并发（含响应读取阶段）。

本次运行结果：

```text
go test -race -count=1 ./internal/modules/ledger -run 'TestInstrumentSearch|TestStock'
PASS

make test
# go test -race -shuffle=on -count=1 -timeout=5m ./...
PASS: internal/app, internal/database, internal/modules/ledger, internal/modules/liquor

go vet ./...
PASS
```

未运行 `make test-e2e` / `make test-e2e-live`，未执行生产验证或部署，未提交代码。

前端定向测试 `LedgerInstrumentDialog.test.ts`、`Ledger.formal.test.ts`、`AccountRecords.test.ts` 共 40 项通过，新增查询测试 14 项及页面顺序测试 1 项；`npm run build` 通过。全量测试为 213 通过、46 失败；隔离导出修改前 HEAD `fab9593` 复跑得到 198 通过、同样 46 失败，旧失败集中于 Ledger UI、FX、导入、CurrentHoldings 和 prototype 测试，未纳入本次修复范围。

本地浏览器以合成账户、持仓和查询响应检查桌面与 390px 手机视口，确认持仓紧邻账户记录上方、查询字段回填、放弃草稿确认及无横向溢出。拦截全部写请求，不访问生产；此检查不代表真实来源到数据库的 E2E 验收。新增组件、查询测试及拆分后的管理组件通过 Prettier 检查；全仓 `format:check` 仍报告 25 个文件，不做无关的批量格式化。
