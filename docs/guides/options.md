# 美股卖出期权权利金工具（Put / Call）

正式页面为 `/options`，API 为 `/api/platform/options/`。行情由富途官方 OpenD 和 Python SDK 提供，Go 负责精确计算及独立 `options.db`，Vue 负责交互。

## 使用

- 顶部切换“卖 Put／卖 Call”。Put 按现金担保测算，Call 按每份 100 股的备兑策略测算；两者都用 Bid 作为卖出参考价。`/options?strategy=CALL` 可直接进入 Call 视图。
- 添加股票支持中文名称、英文名称和代码。优先查询已保存的证券名称，未匹配时按用户的搜索请求查询富途静态目录；英文名称必要时补充 Nasdaq 公开证券目录。最终可添加的证券身份以富途为准，名称检索不拉取期权报价。
- 添加、切换股票、打开网页、筛选、查看收藏和服务启动均不采集行情。首次无数据时点击“更新行情”。
- 当前股票刷新当前类型的该股期权；“全部自选”刷新当前类型的全部启用股票。Put／Call 目录、最新快照、历史摘要和任务回执按类型隔离，切换视图只读缓存。任务结束即停止进度轮询。相同类型和目标合并；不同类型不合并，失败保留原快照。
- 默认未来 365 天、每轮最多 5,000 份合约，限额在整轮股票间共享。达到上限明确显示不完整覆盖，可以缩短范围或单独刷新股票。
- 主表按 1 份和 Bid 展示。Put 的全额基数是行权价对应资金；Call 的全额基数是股票参考市值。可切换净投入，设置费用，筛选、排序、分页并比较最多 3 份同类型合约。详情醒目显示每股权利金、快照估值时剩余天数和年化。
- “股票设置”与默认筛选方案分别保存 Put／Call 条件。Put 提供行权价上限、接货成本上限；Call 提供行权价下限、含权利金卖出价下限。切换股票或类型清空临时条件，显式恢复方案时只恢复当前类型。
- 每行可直接收藏；详情自定义价格、份数、费用、利息、到期及平仓情景后，也可另存。收藏保存原行情、输入、结果、费用版本和估值日期，移出股票池不会删除收藏。
- 浏览器本地存储不是正式版的业务数据源。自选、设置、收藏、快照和任务状态都在服务器 `options.db`。

## 计算与数据口径

金额 API 使用十进制字符串，后端使用 `math/big.Rat` 计算，筛选和排序使用未舍入结果。当前计算版本为 `option-premium-act365-v2`，旧收藏保留原版本及结果。

```text
毛权利金 = 每股拟成交价 × 100 × 份数
净权利金 = 毛权利金 − 已确认的开仓费用
Put 全额资金 = 行权价 × 100 × 份数
Call 全额基数 = 正股快照参考价 × 100 × 份数
净投入参考值 = 全额资金 − 本次权利金收入
单利年化（百分比）= 本次权利金收入 ÷ 所选资金基数 × 365 ÷ 美东日历天数 × 100
```

费用未知时净权利金、费用保持空值，主指标明确采用税费前毛权利金；未知不填零。零／缺失 Bid、倒挂盘口、未确认的合约属性不生成报价收入。0 天、已过期或非正分母不生成年化。

### 备兑 Call

- 每份对应 100 股。显示备兑所需股数、股票参考市值，以及被指派时按行权价卖出股票的金额；股票交付金额不是需要额外预留的现金。
- 含权利金卖出价／股＝行权价＋本次净权利金÷对应股数，费用未知则按毛权利金；未含指派费用。
- 收权利金后持股参考成本／股＝正股快照参考价−本次权利金收入÷对应股数。这是统一比较基准，不是用户的历史持仓成本。
- Call 内在价值＝`max(正股参考价−行权价, 0)`；Put 为 `max(行权价−正股参考价, 0)`。时间价值为所选成交假设下的毛权利金减参考内在价值，不扣交易费。
- Call 价外是行权价高于正股，价内是行权价低于正股。价内不直接等同于会被指派，也不按“内在价值占权利金70%”定义深度价内。
- 到期情景首先列出期权部分盈亏：`净权利金−max(到期股价−行权价,0)×股数−额外费用＋情景利息`。
- 填写“假设持股成本／股”后，另算持股＋卖 Call 合计：`净权利金＋(min(到期股价,行权价)−持股成本)×股数−额外费用＋情景利息`。未填写成本不补零、不计算合计；情景参数不改变主表的权利金年化。
- Call 的可选现金利息只针对用户另外填写的现金，不默认把股票市值作为计息现金。

备兑结构及到期收益关系参考 [OIC Covered Call (Buy/Write)](https://www.optionseducation.org/strategies/all-strategies/covered-call-buy-write)。主列是权利金收入率，和股票涨跌合计收益分开。

纽约日期通过 `America/New_York` 转换，日期差采用 civil days，跨夏令时不会按 23／25 小时的一天误算。旧行情和收藏保留原估值日期，操作时间按北京时间显示。候选列表另按当前美东日期排除到期当日及更早合约，不用旧快照的剩余天数判断今天是否已过期；收藏不随该排除改写。

富途 `update_time` 是当前价更新时间，不是 Bid／Ask 的更新时间；快照未提供盘口时间时返回 `null`。`option_premium` 是溢价指标，不当作卖 Put 收入。`option_settlement_mode` 是 AM／PM 时间分类，不是现金／实物交割标志；本模块根据美国股票／ETF 身份、标准期权类型、100 股单位判定标准股票期权，并重新核对快照中的合约单位与乘数。指数及调整合约不套用固定 100 股计算。

## 本地启动

先在富途官方可视化 OpenD 登录，完成 API 问卷及协议确认后重新登录。默认监听 `127.0.0.1:11111`。本模块只创建 `OpenQuoteContext`，无需解锁交易。

适配服务使用独立 Python 环境：

```bash
python3 -m venv services/option-quotes/.venv
services/option-quotes/.venv/bin/pip install -r services/option-quotes/requirements.txt
services/option-quotes/.venv/bin/python services/option-quotes/app.py
```

另一个终端启动 Go：

```bash
cd backend
OPTIONS_ENABLED=true go run ./cmd/server
```

再启动前端：

```bash
cd frontend
npm ci
npm run dev
```

打开 `http://127.0.0.1:5173/options`。已有服务占用默认端口时分别设置 `QUOTES_PORT`、`OPTIONS_QUOTES_URL`、`BACKEND_ADDR`、`BACKEND_PROXY_URL` 和 Vite `--port`。服务令牌只用于服务端开发代理，不能放到 `VITE_*`。

## 配置

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `OPTIONS_ENABLED` | `false` | 启用独立模块 |
| `OPTIONS_QUOTES_URL` | `http://127.0.0.1:5053` | Python 适配服务 |
| `OPTIONS_MAX_WATCH` | `20` | 自选上限，允许 1–100 |
| `OPTIONS_MAX_CONTRACTS` | `5000` | 一次手动任务共享合约上限，允许 1–20000 |
| `OPTIONS_TASK_TIMEOUT` | `20m` | 单次任务时限 |
| `OPTIONS_DIRECTORY_TTL` | `24h` | 在手动任务中检查目录缓存 |
| `OPTIONS_HISTORY_BUDGET_MB` | `256` | 普通历史负载容量，允许 1–4096 MiB |
| `FUTU_HOST` / `FUTU_PORT` | `127.0.0.1` / `11111` | 适配层连接 OpenD |
| `QUOTES_HOST` / `QUOTES_PORT` | `127.0.0.1` / `5053` | 适配层监听 |

没有自动采集开关或采集周期。SDK 登录连接维护与行情采集分开。

## 服务器部署

2026-11-17：用户指出未要求网页登录密码，已移除 Basic Auth 与密码文件挂载。需求审计交付后，用户要求先推送部署，现恢复发布；这不代表用户逐项确认了助手自拟默认值。当前代码是共享工作区，访问者可通过入口读写自选和收藏；内部平台令牌不是用户认证。

当前配置使用 `docker-compose.options.yml` 叠加 Compose，原 HTTP 5052 拒绝 `/options` 和 options API，独立 `options-entry` 提供无网页登录密码的 HTTPS 页面。平台端继续验证内部服务令牌和同源写入。

准备仓库外私有 `/opt/weibospider-private/options/compose.env`（目录权限700、文件600），内容形状如下，替换为实际值。登录密码只输入官方OpenD，不写入此文件：

```dotenv
FUTU_LOGIN_ACCOUNT=<富途登录帐号>
OPTIONS_PRIVATE_DIR=/opt/weibospider-private/options
OPTIONS_HTTPS_PORT=443
```

`OPTIONS_PRIVATE_DIR` 位于仓库外，目录权限 700，保存：

- `fullchain.pem`：与最终 HTTPS 域名或 IP 匹配的有效证书链。
- `privkey.pem`：证书私钥，权限 600，Nginx master 可读。

OpenD 与 SDK 均固定为 `10.12.7208`。OpenD 使用官方 Ubuntu 压缩包并校验 SHA-256，Dockerfile 只复制命令行发行目录，不把 GUI 带入运行镜像。官方原始下载包约 469 MB，需要先确认服务器构建空间。

首次登录在服务器交互终端进行，账号、密码和验证码只输入官方 OpenD：

```bash
source scripts/compose-command.sh
"${COMPOSE[@]}" build futu-opend option-quotes options-entry platform
"${COMPOSE[@]}" run --rm --no-deps futu-opend -console=1
```

在官方交互中选择记住密码，完成验证后退出这次进程。随后以记住密码方式启动：

```bash
"${COMPOSE[@]}" up -d
```

`futu-login` 卷保存整个 OpenD 用户目录，账号状态和日志不进镜像、不进 Git；11111 与适配层 5053 均不映射到宿主机公网端口。现有 `platform-data` 卷中新增 `options.db`，不复用账本或税务库。

`update.sh` / `install.sh` 通过 `scripts/compose-command.sh` 检查仓库外私有环境文件，可用 `OPTIONS_ENV_FILE` 指定其他路径；兼容旧 `.env.options`。文件存在时使用同一 overlay，后续发布仍更新期权服务。平台重建后重新启动两个 Nginx 入口以刷新上游地址。没有期权环境文件时仍使用原三服务部署。

当前容器内存上限是初始保护边界（OpenD 512 MiB、适配层 384 MiB、私有入口 64 MiB），不是已经验收的常驻占用。上线前应在目标 Linux 主机验证官方二进制、首次登录、证书及常驻／峰值内存；本地 macOS 浏览器验收不能替代 Linux 容器运行验收。

## 存储、保留及恢复

- 独立迁移：001 业务结构，002 历史容量状态，003 手动任务去重回执，004 Put／Call 类型隔离。004 将原数据归入 PUT，原始快照与收藏 JSON 不改写，旧 Put 请求编号仍可重放。升级后的库需使用认识 004 的版本打开。
- 每只股票整轮报价校验完成后，在事务中发布 snapshot 和 latest 指针；失败保留旧指针。
- 普通完整快照最多保留 7 天；每天最后一次手动快照摘要保留 180 天；任务日志保留 30 天。达到容量上限时清理较旧普通历史并提示，不扩大历史占用。这里的容量指历史 JSON 负载，SQLite 页面／索引和最新快照有额外占用。
- 收藏复制必要行情、参数、费用方案和计算结果，与短期快照不设删除级联。行情清理、服务重启、删除自选不改变收藏。
- 相同快照、合约和规范化输入去重。请求编号回执优先读取；取消收藏后重试旧请求不会恢复已取消记录。
- 重复手动刷新合并到运行任务，并持久化合并请求的回执。服务启动把未结束任务标记为中断，等待用户再次点击。
- `GET /candidates`、`GET /history` 和 `POST /scans` 支持 `option_type=PUT|CALL`，未指定时兼容旧调用默认 PUT；不接受 ALL。指定合约的刷新从已存合约身份确定类型。
- 备份使用 SQLite 一致性备份机制；不要删除平台数据卷。OpenD 登录目录单独备份并限制访问。

## 测试

```bash
cd backend
make check
make test
make test-e2e
```

```bash
services/option-quotes/.venv/bin/python -m unittest discover -s services/option-quotes/tests -p 'test_*.py' -v
```

浏览器 E2E 使用独立 SQLite、完整 Go API、Vue 和 Python 适配层；只有外部 SDK 返回值替换为可控制的 fixture。不会连接真实 OpenD 或生产数据库：

```bash
cd frontend
npm ci
npx playwright install chromium
OPTIONS_PYTHON="$(pwd)/../services/option-quotes/.venv/bin/python" npm run test:options-e2e
npm test
npm run build
npm run format:check
```

可设置 `OPTIONS_E2E_ROOT` 指向仓库外私有临时目录。截图和 trace 写入该目录，测试 Go 数据目录由启动器创建并清理。

已安装 Nginx 时还可直接验证正式入口模板：

```bash
python3 scripts/check-options-entry.py
```

该检查生成一次性本地证书和测试内部令牌，验证无网页登录密码的HTTPS页面／API、旧HTTP路由和内部令牌注入，结束后清理。真实富途取数由用户明确触发；参见 [首版验收](../validation/2026-11-13-options.md)及 [卖 Call 验收](../validation/2026-11-16-options-call.md)。
