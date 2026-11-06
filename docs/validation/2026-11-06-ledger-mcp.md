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

生产检出目录原有未提交 `/mcp` 代理和未跟踪历史档案服务，账本 MCP 使用既有账本子路径；集成时保留这些既有工作。本轮部署与只读验收结果完成后追加。
