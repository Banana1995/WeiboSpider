# 投资账本 MCP 严格记账验收

日期：2026-11-06。实施工作区：`feat/ledger-mcp`。

## 变更

- 接续已有八工具实现、共用事务内核及 008 迁移。
- 新增字段契约查询，服务 1.1.0 共九工具。
- Schema、字段说明和补问模板同源，写接口强制验证并返回完整缺项。
- 不明确的金融事实要求 AI 调用 question 补问；内部元数据指导读取工具。
- 保留成功回执格式、幂等身份和确定性财务计算。

## 已执行验证

| 命令／场景 | 结果 |
| --- | --- |
| 开始修改前：`go test -race ./internal/modules/ledger ./internal/app -run 'TestMCP\|TestLedgerMCP\|TestStock\|TestAutomaticDividend\|TestSchema' -count=1` | 通过，记录原工作区基线 |
| 完成协议和字段校验后：`go test -race ./internal/modules/ledger ./internal/app -run 'TestMCP\|TestLedgerMCP' -count=1` | 通过 |
| `make check` | 通过，gofmt 及 `go vet -tags=e2e ./...` |
| `make test` | 通过，全部普通测试包 race/shuffle 检查；账本包约20.6秒 |
| `make build` | 通过，正式 server 二进制构建 |
| `git diff --check` | 通过 |

测试使用合成数据、固定时钟、隔离数据库及真实 MCP SDK 到 httptest 的调用链。逐项缺失/null/空值测试比较 accounts、account_records、instruments、stock_entries、stock_journals、current_holdings、stock_dividend_status、audit_log、idempotency_receipts、sqlite_sequence 的完整逻辑内容，并验证失败请求没有唤醒分红任务。

其他覆盖包括：多缺项一次返回、准确 question 问句和单位、补齐后提交与重试、显式零费用/零余额、金额类型/精度/溢出、非法日期/币种、嵌套更正字段、分支互斥、32项诊断上限、字段查询与工具 Schema 一致、查询不访问证券来源、FX 当前/历史/同日覆盖及条件缺失后的恢复。

已有 V1 测试继续覆盖历史回放、并发、自动分红、更正/作废、各持久化节点故障回滚、旧回执兼容、迁移和重启恢复。

## 集成与发布

已通过 SQLite backup API 为生产 ledger.db 建立一致性备份，时间 `2026-11-06T11:34:07Z`；备份位于平台数据卷内受限的 `mcp-release-backups` 目录。`integrity_check=ok`，无外键违规，迁移为001至007；生产原提交为 `fb6146c`。备份没有下载到本地或提交到仓库。

生产检出目录原有未提交 `/mcp` 代理和未跟踪历史档案服务，账本 MCP 使用既有账本子路径；集成时保留这些既有工作。

### 主线与部署

- 功能提交：`25d1b1d`。最新主线合并提交：`864b22c`。
- 主线集成后，`backend/` 和 `docker-compose.yml` 与已通过检查、测试、构建的功能提交完全一致；合并保留最新微博／雪球及前端修改。
- [GitHub Actions 42211479826](https://github.com/Banana1995/WeiboSpider/actions/runs/42211479826) 的 SSH 部署与邮件通知均成功，总时长约1分53秒。
- 服务器检出提交核对为 `864b22c`；platform 和微博容器已重建运行，入口容器已重启。

### 公网协议与业务读取

在 `http://43.130.247.183:5052/api/platform/ledger/mcp` 执行：

1. initialize 返回 `investment-ledger`、`1.1.0`，instructions 含明确 question 补问要求。
2. tools/list 返回九工具，交易 Schema 的 required 包含 fee。
3. ledger_get_write_contract 返回契约版本 `2026-11-06`。
4. ledger_list_accounts 及选定账户的 ledger_get_stock_book 只读调用成功，未在记录中公开账户或持仓内容。
5. 发送刻意缺少账户、请求身份、日期、价格、费用等要素的无效交易请求，返回 `isError=true`、`incomplete_entry`、完整缺项、`question_tool=question`、`next_action=ask_user_with_question_then_resubmit` 和 `write_effect=none`。该请求在字段校验阶段被拒绝，没有创建生产测试流水。

### 持久化与同级入口

- 以只读数据卷挂载及 SQLite mode=ro 核对，最新迁移为 `008_mcp_receipts.sql`，共8个迁移；`integrity_check=ok`，无外键违规。
- 微博 `/api/stats`、5052 `/healthz`、`/ledger`、`/liquor`、`/tax`、白酒 latest API 均为200。
- 既有历史档案 `/mcp` 未授权 POST 仍为401。
- 生产验收为读取和确定缺项请求，不导入附件、不新建测试账务、不额外触发采集；完整有效写入与故障场景由本地合成测试验证。
