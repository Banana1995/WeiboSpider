# Go 多数据源后端

首期实现白酒行情后端闭环：酒价内参采集、完整批次校验、SQLite 持久化、最新与历史查询、手动和可选自动同步。2026-09-05 已随 Vue 单品趋势页及 Nginx 接入部署，功能合入 `master`，本次生产版本为 `e2c5476`。原 Python 业务和数据库保留，根 Compose 已扩展为三服务共同发布。

当前生产入口、权限、持久化和备份说明见 [部署运维](../docs/deploy-prep.md)，实测结果见 [生产验收记录](../docs/validation/2026-09-05-liquor-production.md)。

记账模块本地默认关闭，生产 Compose 按用户授权启用公网匿名共享全部读写，见[接口](docs/ledger-api.md)与[前端启动](../frontend/README.md#本地记账)。任何人均可查看和修改全部数据，请勿上传私人财务信息。2026-09-12 开发工作树将当前输入放在 `current_holdings`，固定资产/资金流放在 `account_records`，证据与版本放在 `audit_log`。GET/HEAD 估值只读，估值 POST 和旧交易回放引擎已删除；收益与账本总资产统一投影，不接受 track。周六 08:00 任务默认关闭，启用后使用当前来源保存固定记录，或在无来源时保留明确标记的沿用证据。详见[当前模型与完整离线验证](../docs/specs/2026-09-12-ledger-current-inputs.md)。本次未部署，不提供旧库迁移/兼容回退，不恢复历史行情回填或增量导入。

## 环境与启动

### 当前持仓开发变更

所有账户使用 `GET/HEAD/PUT /accounts/{id}/current-holdings` 维护当前现金和证券数量。证券身份与持仓在同一事务写入；同账户市场+代码唯一，跨账户允许重复。账户没有 holdings/reported 模式，不保留 Replay、旧操作 Store/HTTP 处理器或 operations/opening_positions 表。持仓编辑不写历史资产、资金流或收益失效标记。只有保存进行中检查当前来源版本；已保存的原始估值输入不随持仓修改而改变。详情见 [API 第 13 节](docs/ledger-api.md#13-同账户当前持仓)。

按最新开发阶段授权直接修改 `001_init.sql`，不添加 `002`、迁移、回滚或旧实现 fallback。旧库的迁移校验和检查仍会拒绝不匹配结构，不自动清除任何数据库；本轮没有启动旧库、删除本地/生产数据或部署。单元测试只使用临时合成库。

- Go 1.26+；开发验证版本为 Go 1.26.3。
- Linux/macOS，本地持久化文件系统。当前数据库所有权锁使用 POSIX `flock`。
- CGO 和 C 编译器。SQLite 使用 `github.com/mattn/go-sqlite3` 内附的 SQLite，不要求安装或启动 SQLite 服务。

在本目录执行：

```bash
go mod download
go run ./cmd/server
```

默认监听 `127.0.0.1:5051`，数据库为当前工作目录下的 `data/liquor.db`，**不自动访问外部数据源**。启动完成后显式发起同步：

```bash
curl -i -X POST http://127.0.0.1:5051/api/platform/liquor/sync \
  -H 'Content-Type: application/json' -d '{}'
curl http://127.0.0.1:5051/api/platform/liquor/sync
curl http://127.0.0.1:5051/api/platform/liquor/latest
curl 'http://127.0.0.1:5051/api/platform/liquor/products/1/history?limit=31'
```

第一次请求返回 `202` 和 `run_id`，不是已完成采集。通过 `GET /sync` 等待 `state=succeeded` 后查询数据；重复触发正在运行的任务返回 `409`。最后一次任务的状态保存在数据库中。

以上命令适用于本机开发。生产 Go 端口不映射宿主机，通过 5052 的 Nginx 读取行情；公开入口不允许 POST 同步，生产由 Compose 显式开启自动同步。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `BACKEND_ADDR` | `127.0.0.1:5051` | HTTP 监听地址；端口冲突时自行选择新端口，不使用微博的 5050 |
| `BACKEND_DATA_DIR` | `data` | 本后端专用目录，不使用旧服务的 `DB_PATH` |
| `BACKEND_API_TOKEN` | 空 | 本机模式可不配置；非回环监听必须设置 32 至 512 位无空白 ASCII 令牌，使用随机值 |
| `LIQUOR_AUTO_SYNC` | `false` | 显式设为 `true` 才开启自动同步 |
| `LIQUOR_SOURCE_URL` | `https://business.cj.sina.cn/api/liquor_price` | 运维配置，可替换为测试服务器；不是用户可提交的采集 URL |
| `LIQUOR_REQUEST_INTERVAL` | `1s` | 顺序请求间隔，允许 0 至 1 分钟；真实来源不要设置为 0 |
| `LIQUOR_SYNC_TIMEOUT` | `5m` | 一次完整同步超时，允许 1 秒至 30 分钟 |
| `LEDGER_ENABLED` | `false` | 启用匿名共享记账 API；生产 Compose 显式为 true；非回环监听仍须配置令牌以保护其他模块，但账本不校验令牌 |
| `LEDGER_WEEKLY_ENABLED` | `false` | 只接受精确 `true`/`false`；开启周六总资产任务，必须同时 `LEDGER_ENABLED=true`，不是生产启用授权 |
| `LEDGER_WEEKLY_TIME` | `08:00` | 用户确认的北京时间默认时刻；严格 `HH:MM`、00:00..23:59，时区固定 Asia/Shanghai、每周六；非法值即使关闭也拒绝启动 |

配置令牌后，除只读 `GET/HEAD /healthz` 外，所有请求都需要：

```http
Authorization: Bearer <BACKEND_API_TOKEN>
```

除启用的精确账本命名空间外，无令牌模式仅适合本机开发并拒绝非回环 Host。所有业务请求保留浏览器跨站写保护，不提供跨域放行；同源代理须保留含端口的 Host。账本按用户授权匿名共享全部读写，令牌只保护其他模块，不提供私人账本保密。不要把令牌硬编码进前端构建产物、代码或命令示例。

## API 契约

路径已包含 `/api/platform`，Vite 开发代理和生产 Nginx 均保留该前缀。下表描述 Go 本身的契约，生产代理额外限制为 GET/HEAD；入口拒绝的写请求可能返回 Nginx 的 HTML 403，而不是 Go 的 JSON 错误。

| 方法与路径 | 结果 |
| --- | --- |
| `GET /healthz` | 进程存活检查，不证明外部来源可用或数据已更新 |
| `GET /api/platform/liquor/latest` | 最后接受的报价快照：`source`、`price_basis`、`price_date`、`items` |
| `GET /api/platform/liquor/products/{id}/history` | 产品元信息及历史 `items`，按日期倒序 |
| `POST /api/platform/liquor/sync` | 仅接受 `application/json` 和空对象 `{}`，异步触发同步 |
| `GET /api/platform/liquor/sync` | 最后一次任务状态、日期和失败代码；不是无限任务日志 |

历史参数：`from`、`to` 为 `YYYY-MM-DD`，包含两端；`limit` 默认 30，范围 1 至 366。返回日期范围内最新的最多 `limit` 条；更长历史按日期窗口读取。不存在的产品返回 `404`，非法参数返回 `400`。空库最新列表返回 `items: []` 和空 `price_date`，不能视为采集成功。

最新报价中的每个条目包含：

```json
{
  "id": 1,
  "name": "Sample",
  "specifications": "53/500ml",
  "unit": "元/瓶",
  "sort": 1,
  "price_date": "2026-09-05",
  "price_cents": 179600,
  "change_cents": -200,
  "fetched_at": "2026-09-05T02:00:00Z"
}
```

以上为格式示例，不是实时报价。`price_cents` 与 `change_cents` 是人民币分，除以 100 才是元。`unit` 保留来源展示单位。金额以整数存储，来源当前是四舍五入后的整数元；不把缺失字段或小数元静默舍入。本模块允许金额的上限低于 JavaScript 安全整数范围，不将此约束泛化为未来所有金融数据的精度方案。

`source=sina_jiujia`；`price_basis=terminal_retail_weighted_mean`，表示来源声明的终端零售加权均价，不是批发价，也不是本平台独立验证的逐笔成交价格。

同步状态 `state` 包括 `idle`、`running`、`succeeded`、`failed`、`interrupted`。保留 `run_id`、`started_at`、`finished_at`、`last_success_at`、`last_price_date`、`records` 和 `error_code`。`records` 是该次完整批次写入/更新的报价数，不是净新增数。失败代码包括 `source_unavailable`、`invalid_source_data`、`storage_error`、`timeout`、`cancelled` 和 `process_interrupted`。

API 错误使用 `{"code":"...","message":"..."}`。旧微博 API 由其原服务处理，本后端不会返回或修改微博数据。

## 同步规则

1. 顺序读取来源列表，再按列表中的酒品 ID 获取详情和近一个月历史；不依赖固定的 11 个酒品。
2. 校验来源业务状态、列表数量、必需字段、单位、金额、日期、重复项，以及列表/详情/历史最新点的一致性。拒绝超过限制的响应和重定向。
3. 整批抓取成功后，在一个短事务中更新产品、历史价格和成功状态；网络请求不在写事务内执行。
4. 按 `(source, product_id, price_date)` 幂等更新。同日期允许来源修正；较旧快照和北京时间未来日期整批拒绝。
5. 最新列表反映最后接受快照的成员，已不在当前列表的产品不继续显示，但保留历史价格。
6. 失败不删除已成功采集的数据，也不清空最后成功日期。来源晚更新时，“任务成功”仍可能对应旧报价日期，展示时必须检查 `price_date`。

开启 `LIQUOR_AUTO_SYNC=true` 后，每天北京时间 09:00、21:00 各自动同步一次。服务启动或重启不会立即补采；如果执行时服务未运行，该次自动同步跳过并等待下一个固定时刻。自动同步失败或来源报价未更新时，不额外重试。手动同步仍可主动触发，但不允许并行任务。

任务通过上下文超时和停止信号取消。进程重启后遗留的 `running` 标记转为 `interrupted`，但不会因此立即补采。HTTP 服务和后台任务共用一个进程，不创建额外任务容器。

### 记账周六任务（T05 后端）

这是与白酒独立的 worker，默认关闭；关闭时不建任务、不恢复任务状态、不请求报价。仅显式启用后，在周六北京时间 08:00（可通过上述时刻配置调整）至当日 24:00 的窗口内处理所有账户：有当前持仓来源的账户计算现金加持仓总资产，其它账户复制截至当日最近一笔有效明确总资产并标记沿用。没有有效明确资产时保存 `skipped/no_source`，不会伪造零资产。沿用证据不重置账本的后续净资金流投影。

- 周六窗口内启动可补执行当天任务，窗口内新建账户也会纳入；不生成历史周六任务。周日及以后把遗留 pending/running/待重试任务标为 `skipped/expired`，不能用今天的行情补写上周六。
- 每轮最多新建 20 个账户任务、串行处理 20 次尝试；每轮结束后等待一分钟，临近计划时刻提前唤醒。空队列和失败不会热循环，大量账户可能延迟或错过当天窗口。
- 每账户/周六最多三次尝试，首次失败后至少等 5 分钟，第二次失败后至少等 30 分钟，第三次停止；不提供 HTTP 重试/开关写接口。运行中断也消耗一次尝试，重启按相同退避恢复，跨日则过期。已有任务不因更改时刻重新生成。
- 每次总预算 12 秒（含读库、股票、汇率及成功保存），失败状态清理另有 3 秒预算。运行租约为 72 秒；失败清理也失败时，后续轮次回收过期租约。进程独占数据库文件锁，同一 worker 防并发 Tick，Serve 取消并等待它退出后调用 Close 释放账本库。
- 仅完整、精确且来源/时间有效的结果可保存。采价期间当前输入发生变更时 `failed/basis_changed`；任务成功状态、history_id 和 schema-1 快照同事务，同一任务重试不能重复记录。成功后持仓编辑不使旧记录失效；手动刷新只读，不追加资产。人工资产记录可独立创建、更正或作废。
- 周五或更早的可用参考报价保留实际日期并标 `prior_date`，不制造周六收盘价格；沿用现有来源校验，不新增交易日历或历史回填。纯现金（包括明确零）不请求网络。
- `GET/HEAD /api/platform/ledger/weekly-status`、`/accounts/{id}/weekly-jobs`、`/accounts/{id}/weekly-jobs/{jobID}` 只读，不触发任务。完整字段见[接口第 11 节](docs/ledger-api.md#11-周六总资产任务t05-后端)。

自动计算和明确标记的沿用总额都直接写入同账户 `account_records`，flow 为 NULL；报价、沿用来源和任务状态变更写入唯一 `audit_log`。沿用保留原始记录 ID、版本和业务日期，不把历史金额推造成现金、股数或新行情。历史验证记录反映改动前范围；本轮功能待统一验收，生产配置、认证边界和 Python 未改变。

## 数据库公共层

`internal/database` 提供：

- `Open(ctx, directory, module)`：安全模块名、独立文件、进程所有权锁；同库只允许一个本平台服务拥有者，避免重复调度和并发启动迁移。
- 每个库的 `database/sql` 连接池默认最多一个连接，连接级设置经 DSN 应用于每个新连接：外键开启、WAL、5 秒锁等待、`synchronous=FULL`、立即写事务。
- `WithTx`：统一提交和回滚；调用方不能自行结束传入的事务，不能在事务内反过来借同一连接池执行查询。
- `Migrate`：模块拥有 SQL 文件，公共层按文件名顺序执行，在库内记录名称和 SHA-256，拒绝修改或删除已执行的迁移，以及历史不匹配的应用版本。待执行批次失败时回滚，不静默跳过错误。
- `Backup`：通过 `VACUUM INTO` 创建包含已提交 WAL 数据的一致性快照，同步到磁盘后发布，不覆盖已有目标路径；不是复制活跃 `.db` 文件。目标目录需要已存在。

迁移文件使用 `NNN_name.sql` 命名并嵌入二进制。每个模块独立记录迁移版本；脚本不自行控制事务、不附加其他库、不修改公共迁移历史表。迁移是可信应用代码，不是第三方脚本沙箱。

使用本地 POSIX 文件系统；不支持把库放到网络共享盘，也不应绕过服务所有权约束启动另一写入者。数据库公共层不强制隔离同进程里的不可信代码。未来模块接入时拥有自己的库和 SQL，不直接读写 `liquor.db` 或微博的 `data.db`。

文件默认位于 `data/`，目录权限 0700、新库与锁文件权限 0600。`Close` 等待连接/事务释放再释放所有权锁，调用方应先停止请求和任务，再关闭数据库。不要手动删除仍可能被服务使用的 `.lock` 文件。

备份暂提供内部 Go API和测试，不开放任意服务器文件路径的 HTTP 备份接口。恢复使用已停止的独立目标库，并验证完整性与迁移版本，不能在运行中覆盖文件。不同模块分别备份不代表全局一致快照。

## 验证与构建

```bash
make check
make test
make build
```

测试使用独立临时 SQLite 文件及本机模拟来源，覆盖事务、迁移、备份、任务取消/超时、幂等、过期快照、HTTP 和重启后的数据保留。默认不联网。显式测试真实来源（会低频请求列表和全部详情，数据在测试结束后清理）：

```bash
LIQUOR_LIVE_TEST=1 go test -race -count=1 -timeout=4m \
  -run '^TestLiveSina_SyncAndQuery$' -v ./internal/app
```

在 Go 1.26.3/macOS arm64 上，2026-09-05 已实测经 HTTP 触发、存库、查询获得 11 个酒品、341 条报价和单品 31 条历史；这是样本结果，不是接口长期可用性保证。

另外提供实际服务器子进程的黑盒端到端流程，会从源码构建二进制，经 HTTP 操作，并执行真实停止与重启信号：

```bash
make test-e2e
make test-e2e-live
```

前者使用可控来源且不联系新浪，后者显式开启真实来源并执行两轮同步。覆盖首次使用、查询、幂等、重启留存、上游失败、任务超时、SIGTERM/SIGKILL 恢复和访问保护。详细结果及已修复的问题见 [后端进程端到端记录](docs/e2e-2026-09-05.md)。这些命令仍只测试后端进程，不包含 Vue、Nginx 或 Docker；后续容器与公网浏览器验收单独记录，不能混为同一套自动化测试。

Docker 构建上下文必须是本目录，不能使用根目录微博 Dockerfile：

```bash
docker build -t data-platform-backend:local .
```

镜像使用 CGO 构建和兼容的 Debian 运行环境，不宣称二进制完全静态。镜像不包含数据目录、密钥或开发工具；非 root 用户 UID 1000。容器内监听所有接口，因此启动时必须提供 `BACKEND_API_TOKEN`，并只将宿主机端口映射到回环地址供本地验证。示例使用独立命名卷，不挂载微博数据：

```bash
docker run --rm --name data-platform-backend-test \
  -p 127.0.0.1:5051:5051 \
  -e BACKEND_API_TOKEN \
  -v data-platform-backend-test:/app/data \
  data-platform-backend:local
```

先通过安全方式设置环境令牌，示例不提供默认口令。此步骤仅为独立后端验证，不会启动 Nginx 或微博副本；不作为生产部署命令。2026-09-05 已在 Linux Docker 环境完成镜像构建、隔离容器启动、命名卷重启及 Nginx 读取验证，随后通过根 Compose 部署生产。

生产使用 `platform-data` 独立卷与 `.env.platform`，设置 `LIQUOR_AUTO_SYNC=true`；此配置覆盖本机默认值，但不改变默认配置表。不要在生产目录启动另一进程争用数据库，也不要为调试临时公开 5051。

## 扩展边界

默认只注册白酒模块；显式设置 `LEDGER_ENABLED=true` 时增加独立账本库与匿名共享记账 API。新模块复用数据库基础代码及 HTTP 反馈工具，表结构、业务事务和金融计算留在模块内。代码不读取微博 Cookie、旧数据库或旧服务配置。

Vue 单品页面、Nginx、根 Compose 和共同自动发布已实现；完整登录、微博前端迁移、其他业务模块及按模块独立发布尚未实现。新的私有模块上线前必须重新设计入口访问控制，不能直接沿用当前公开行情的 GET/HEAD 代理策略。
