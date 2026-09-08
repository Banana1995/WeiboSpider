# T05 前端只读调度验收

日期：2026-09-07。范围：共享工作树中 T05 已有后端的只读前端，不包含启用配置、任务执行写 API 或历史来源衔接。本记录使用合成数据，未提交、推送、部署或导入私人数据。**T05 整体仍部分完成**；本轮不修改路线图、需求记录、后端设计或另建连续性方案，整体状态及衔接设计由主任务维护。

## 1. 实现与边界

- `/ledger` 新增独立“周六调度”区域，沿用现有账本样式、原生按钮/选择器、语义表格及只读冻结详情，不引入 UI、路由或状态管理依赖。
- 全局显示查询时启用状态、`Asia/Shanghai`、每周六配置时刻、窗口是否开放、下一固定计划时刻和响应读取时间。明确不是 worker 心跳；窗口开放时下一固定时刻可以是下周六，不把它当成当日重试时间。
- 独立账户选择器不改变上方记账账户。状态筛选、30 条倒序游标分页、任务详情及关联冻结记录读取不调用父页面 refresh 或当前 `GET /valuation`。无账户时全局状态仍可读；首次加载和手动操作读取，无轮询/SSE，不使用 localStorage/IndexedDB。
- 展示 pending/running/succeeded/failed/skipped 的文字标签、已开始次数、最近开始/结束时间、最早重试记录和固定原因。关闭时历史 running/next_attempt_at 仍可能存在，不能据此承诺正在运行或将重试；第三次失败显示上限耗尽。
- 成功任务只按 `history_id` 读取该账户的冻结记录，复用 `LedgerValuation` 展示保存时账户/证券身份、现金、持仓市值、总资产、实际报价和汇率来源日期。新增可选时间格式回调，仅调度详情使用北京时间，原有估值面板默认展示行为保留。
- 金额/ID 保持字符串。验证每个列表项的账户、任务 ID/状态/来源/次数/时间/游标，以及详情的任务身份和冻结记录的账户、币种、业务日期、来源及时间关系；不在客户端重新计算金额。列表 running 后的新详情可以为 succeeded，已成功任务不能改接另一历史记录。
- 使用 `useLedgerRead` 的取消与代次隔离，账户/筛选/页面/任务变化清空旧详情；失败或无效响应不继续展示上一成功数据。任务详情和全局状态是独立查询，不混成同时刻快照。名称/来源文本正常转义，不使用 `v-html`。
- 保留根页面写入待确认锁，面板选择和按钮在锁定时不可操作；不改变既有账本写入、幂等或离开保护。

## 2. 历史与自动记录说明

页面内明确区分“导入历史 / 手工记录”和“当前持仓生成的总资产”。前者直接提供总资产及转入/转出，不要求反推股数或绑定证券；后者使用当前现金、持仓和报价/汇率。手动刷新及成功记账后的刷新也能保存总资产记录，不能把所有 `valuation_history` 都称为周六任务，只有任务 `history_id` 关联证明周任务来源。

reported 的 `skipped/no_source` 是没有自动持仓/现金来源、尚未更新，不是失败、零资产或要求补建股票；已有导入/手工记录仍独立有效。明确零现金的完整总额可以成功。成功只说明当时已保存，不证明现在新鲜或更正后的分析有效，冻结记录仍可能成为过期参考。

**未实现连续性。** 当前两条轨道不自动相加、不复制最新总额冒充新记录、不直接拼成连续收益曲线。后续仍需明确同账户选源/切换日期及资金流去重，不恢复已取消的历史行情回填；本轮只有简要边界说明，没有未来功能开关或执行入口。

## 3. 文件清单

| 文件 | 本轮变化 |
| --- | --- |
| `frontend/src/LedgerWeekly.vue` | 新只读调度面板和局部响应式样式 |
| `frontend/src/ledgerWeekly.ts` | API 类型、响应/身份校验、固定文案及北京时间格式 |
| `frontend/src/LedgerWeekly.ui.test.ts` | 22 项组件测试 |
| `frontend/src/ledgerWeekly.test.ts` | 31 项契约测试；与组件测试文件名不只差大小写，兼容 macOS 文件系统 |
| `frontend/src/Ledger.vue` | 接入独立面板，更新相邻 T05/T06 范围说明 |
| `frontend/src/Ledger.ui.test.ts` | 全局状态/空任务桩及 1 项父页面读取隔离测试 |
| `frontend/src/LedgerValuation.vue` | 可选时间格式回调，默认行为不变 |
| `frontend/src/LedgerValuationHistory.vue` | 只更新相邻“定时采样未实现/历史回填”旧文案 |
| `frontend/README.md` | T05 只读使用说明和边界 |
| `backend/docs/ledger-api.md` | 第 11 节补充前端 GET 用法及整体仍部分完成说明，无接口改动 |
| 本记录 | 合成验收依据和限制 |

工作前已有大量脏文件/未跟踪文件，包括现有账本前后端；均保留。未修改任何后端业务代码、迁移、生产代理或部署配置。

## 4. 自动验证

最后一次完整前端验证：2026-09-07 14:49 北京时间。后端回归于同日 14:46 后执行。

| 命令 | 实际结果 |
| --- | --- |
| `frontend: npm test` | 16 个测试文件，192 项通过；原有 138 项保留，新增 54 项 |
| `frontend: npm run build` | `vue-tsc --noEmit` 和 Vite 生产构建通过 |
| `frontend: npm run format:check` | 全部匹配文件通过 |
| `frontend: TZ=America/Los_Angeles npm test -- src/ledgerWeekly.test.ts src/LedgerWeekly.ui.test.ts` | 2 个文件，53 项通过，纳秒 UTC/带偏移时间仍显示北京时间 |
| `backend: make check` | gofmt 检查、`go vet -tags=e2e ./...` 通过 |
| `backend: make test` | race/shuffle 非缓存测试通过 |
| `backend: env -u LIQUOR_E2E_LIVE make test-e2e` | 非真实来源进程 e2e 通过，真实来源测试明确跳过 |
| `git diff --check` | 通过；不代表未跟踪文件已被提交 |

新增测试覆盖关闭/启用/窗口开放与下周固定时刻、旧 running、pending/succeeded/failed/skipped、no_source、过期、重试时间与三次上限、零总资产、超安全整数 ID/金额、状态筛选和分页重置、独立选择、详情状态推进、已成功任务错误链接、跨账户/错币种/错日期/错来源冻结记录、迟到成功/失败与取消、全局刷新竞态、读取失败清旧数据、空账户、锁定、无轮询、HTML 字面文本及父页面 GET/写入隔离。

## 5. 浏览器与只读证据

使用独立新启动的真实 Go/Vite 进程和新合成 SQLite 库，未停止既有 `185xx` 进程。

- URL：`http://127.0.0.1:18652/ledger`，后端 `127.0.0.1:18651`。
- 本轮后端配置为 `LEDGER_ENABLED=true`、`LEDGER_WEEKLY_ENABLED=false`、`LIQUOR_AUTO_SYNC=false`；白酒来源指向回环不可达地址。未启用真实 provider 采集，浏览器动作不调用当前估值。
- 合成目录：`/var/folders/xk/9mnrvs4j3ksf_dcp2725mtt80000gn/T/opencode/ledger-t05-frontend/`，不在工作树内。包含数据库、临时 seed/序列化校准/计数脚本、服务日志及截图，只涉及本轮合成数据。
- 3 个合成账户：reported、holdings 和合法零现金；34 条任务，其中 holdings 32 条用于实际 30+2 分页；2 条冻结历史。任务和历史通过临时脚本造数而非真实定时执行，冻结 JSON 按既有 Go schema-1 序列化要求校准后由真实历史 API 校验读取，未改应用代码以放宽校验。
- 桌面 1365×1000、移动模拟 390×844，分别测得 `documentElement.scrollWidth=1365/390`。移动任务表容器 324px / 内容 650px，冻结详情表容器 290px / 内容 938px，宽表内部滚动，页面不横向溢出；长名称在说明/详情正常换行，含 HTML 字符的冻结名称未产生 img 元素。
- 真实 API 验证全局关闭、no_source、所有五种状态的任务详情、失败退避/上限、30+2 翻页、筛选重置、空筛选、成功冻结记录及合法 `0.00` 总资产。股票/FX 实际日期 `2026-09-04` 仍为 prior_date，没有改成周六报价；原始纳秒 UTC 显示成正确北京时间。
- 键盘聚焦“刷新调度状态”后按 Space 触发只读刷新；按钮、选择器、表格说明与错误/加载提示可被辅助树读取。
- **浏览器响应覆盖仅用于无法安全现场执行的状态：**模拟 `enabled=true/window_open=true/next_scheduled_at=下周六`，截图显式标注 SYNTHETIC API 覆盖；另模拟延迟、503 和错误账户响应，验证 loading/error/清旧数据及恢复。真实后端始终关闭，覆盖已移除。不能把这些覆盖当作真实周六 worker 执行验收。
- 控制台未发现应用 warning/error；实际桌面页面空闲 3.5 秒 API 请求数 9→9（单测另验证 180 秒不轮询）。隔离浏览器上下文的 localStorage 和 IndexedDB 均为空。

一组“刷新调度状态 → 任务 #32 → 重新读取任务详情 → 查看冻结记录 #1”实际请求如下，全部 GET：

```text
/api/platform/ledger/weekly-status
/api/platform/ledger/accounts/10-holdings/weekly-jobs?status=succeeded&limit=30
/api/platform/ledger/accounts/10-holdings/weekly-jobs/32
/api/platform/ledger/accounts/10-holdings/weekly-jobs/32
/api/platform/ledger/accounts/10-holdings/valuations/1
```

DevTools Network 对应请求 125–129 均为 200；没有当前 `/valuation`、POST/PUT/DELETE 或 provider 请求，上方记账账户选择保持未选。最初全页面加载另有原有 accounts/instruments/operations GET，不属于调度动作。最终桌面使用实际关闭状态和真实冻结 API，不保留模拟启用。

临时 `counts.mjs` 对每一项执行断言，浏览器操作前后及最终复查一致：

```text
accounts=3
weekly_jobs=34
valuation_history=2
valuation_basis=2
operations=0
operation_revisions=0
imported_account_records=0
account_record_edits=0
account_record_revisions=0
analysis_changes=0
PASS: synthetic database counts unchanged
```

截图均在上述仓库外目录：`desktop-no-source.png`、`desktop-frozen.png`、`mobile-disabled.png`、`mobile-frozen.png`、`mobile-enabled-synthetic-overlay.png`。最后两种冻结详情截图已使用北京时间格式。Chrome 设备模拟不代表真机 Safari 验收。

## 6. 遗留与交接

T05 只读前端本机合成验收通过，仍待主任务独立复核。后端整体保持默认关闭，没有启用/重试写接口，因此不提供浏览器开关或“立即重试”。列表/详情读数不是跨请求一致快照或 worker 心跳；历史详情仅用于原始记录核对，不检查当前分析失效水位。

历史导入与未来自动记录的显式选源、切换日期和资金流衔接未实现，T05 整体不标完成。没有进行真实账本验收、公网发布、生产启用或长时间负载/真机测试。

为后续独立复核保留本轮本机进程：Go PID `29486`（18651），Vite Node PID `29412`（18652）；URL 如上，均为合成临时数据，不应用作长期账本。复核时应先确认进程仍匹配，不停止其他人的 `185xx` 服务。
