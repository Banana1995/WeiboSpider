# 微博管理器

基于 Scrapy + Flask 的微博内容抓取与管理系统，支持增量同步、热度排序评论、搜索、笔记和 PDF 导出。同仓库新增 Go 白酒行情后端与 Vue 趋势图页面，原微博业务和数据库保留。

## 当前状态（2026-09-05）

白酒采集、Vue 单品趋势图和 Nginx 入口已部署，功能已合入 `master`，本次上线版本为 `e2c5476`。目前是**同一套自动部署、三个独立容器、两个网页入口**，不是两套独立发布流水线。

| 入口 | 内容 |
| --- | --- |
| [微博管理器](http://43.130.247.183:5050/) | 原网页和 Python 后端，公网端口 5050 |
| [白酒行情](http://43.130.247.183:5052/liquor) | Vue 行情列表、搜索、单品趋势和历史明细，公网端口 5052 |
| Go 行情 API | 经白酒入口的 `/api/platform/liquor/*` 访问，5051 仅容器内部开放 |

白酒页面支持近 7/30/90/365 天筛选，明确显示实际覆盖范围，不补齐缺失报价；刷新只读取已入库数据。服务开启自动同步时，每天北京时间 09:00、21:00 各抓取一次，不在启动时补采或在失败后频繁重试。9 月 5 日验收时为 11 款酒、341 条报价。多品对比、涨跌幅归一化、统一导航、微博 Vue 迁移及完整登录尚未实现。

- [Go 后端开发与 API](backend/README.md)
- [Vue 前端开发与测试](frontend/README.md)
- [多数据源平台整体架构说明](docs/specs/2026-09-05-multi-source-architecture.md)
- [投资记账需求与讨论记录](docs/specs/2026-09-06-investment-ledger-requirements.md)
- [投资记账后续计划（优先级、顺序与验收）](docs/specs/2026-09-06-investment-ledger-roadmap.md)
- [投资记账首批后端设计](docs/specs/2026-09-06-investment-ledger-backend-design.md)
- [投资记账 HTTP 接口（默认关闭，仅限本机）](backend/docs/ledger-api.md)
- [投资记账网页与本地启动（尚未部署）](frontend/README.md#本地记账)
- [当前部署与后续方案](docs/specs/2026-09-05-multi-source-deployment.md)
- [生产部署与公网端到端验收记录](docs/validation/2026-09-05-liquor-production.md)

下文安装、配置和使用指南针对原微博服务；白酒开发请使用上面的独立子项目指南。

## 微博功能特性

- **定时抓取**：开启调度后，在北京时间 07:00 至 22:00 前执行增量抓取；微博每 62 分钟、评论每 47 分钟，另加 0 至 5 分钟随机延迟
- **热度评论**：按微博官方热度排序抓取评论，本地保持相同排序
- **实时日志**：抓取过程日志通过轮询实时刷新到前端
- **PDF 导出**：一键导出微博内容为 PDF，嵌入中文字体，中文完美显示
- **批量管理**：支持批量删除/恢复微博
- **分页浏览**：默认每页 100 条，支持页码导航和任意页跳转
- **Web 管理界面**：SPA 单页应用，瀑布流卡片展示
- **信息管理**：雪球内容、笔记、选文注解、图片上传、全局搜索和评论命中定位

## 环境要求

- Python 3.9（当前开发和容器运行基线）
- Google Chrome（PDF 导出功能需要）
- macOS / Linux

## 安装

```bash
# 克隆仓库
git clone https://github.com/Banana1995/WeiboSpider.git
cd WeiboSpider

# 创建虚拟环境（推荐）
python3 -m venv venv
source venv/bin/activate

# 安装依赖
pip install -r requirements.txt
```

## 配置

### 1. 获取微博 Cookie

1. 用 Chrome 浏览器打开 [weibo.com](https://weibo.com) 并登录
2. 按 F12 打开开发者工具 → Application → Cookies → https://weibo.com
3. 将所有 Cookie 拼接成字符串（格式：`key1=value1; key2=value2; ...`）

### 2. 首次启动

首次启动时会自动生成数据库和默认配置。启动后通过 Web 界面配置：

- **Cookie**：粘贴你获取的 Cookie 字符串
- **用户 UID**：添加你要监控的博主 UID（在博主主页 URL 中可找到）
- **时间范围**：设置抓取微博的时间范围（留空 = 不限时间）

## 启动服务

```bash
cd weibospider

# 开发模式（改代码自动热更新，默认端口 5050）
python run.py --dev

# 生产模式（多线程，更稳定）
python run.py

# 指定端口
python run.py --port 8080
```

或使用脚本后台启动：

```bash
./start.sh              # 后台启动
./start.sh --port 8080  # 指定端口
./stop.sh               # 停止服务
```

两种模式默认均访问 http://localhost:5050 。`run.py` 默认监听所有网卡；仅本机开发时可加 `--host 127.0.0.1`。

## 使用指南

### 抓取微博

1. 在界面中配置好 Cookie 和要监控的博主 UID
2. 设置时间范围（可选，留空表示不限时间）
3. 点击 **"立即抓取"** 按钮
4. 查看实时日志了解抓取进度
5. 抓取完成后页面自动刷新显示新数据

开启定时调度后，任务按上述 62/47 分钟间隔运行，超出北京时间 `[07:00, 22:00)` 的触发会跳过。重启或部署不会立即触发微博抓取；Go 白酒服务同样不会在启动时补采，而是等待每天北京时间 09:00 或 21:00。

### 查看评论

- 每条微博卡片底部显示评论数
- 点击 **"评论 N"** 展开查看热度排序的评论
- 热度排序使用微博官方 `flow=0` 接口，按综合热度降序排列

### 导出 PDF

1. 点击工具栏 **"导出 PDF"** 按钮
2. 在弹出的模态框中选择导出时间范围（留空 = 全部）
3. 点击 **"确认导出"**
4. 浏览器自动下载 PDF 文件

PDF 使用嵌入的 Noto Sans SC 中文字体，适合打印和存档。

### 管理微博

- **删除**：点击卡片右上角 × 按钮，微博进入回收站
- **批量删除**：勾选多条微博后点击 "删除选中"
- **恢复**：切换到 "回收站" 标签，勾选后点击 "撤回选中"
- **按博主筛选**：点击配置区的博主名，只看该博主的微博

## 项目结构

```
WeiboSpider/
├── weibospider/
│   ├── app.py              # Flask Web 应用、API 路由、PDF 导出
│   ├── db.py               # SQLite 数据库操作
│   ├── run.py              # 启动入口
│   ├── scheduler.py        # 定时抓取调度器
│   ├── settings.py         # Scrapy 全局配置
│   ├── pipelines.py        # Scrapy 数据管道
│   ├── middlewares.py      # Scrapy 中间件（代理、UA）
│   ├── start.sh / stop.sh  # 后台启动/停止脚本
│   ├── static/
│   │   └── index.html      # 原微博 SPA 页面
│   └── spiders/
│       ├── tweet_by_user_id.py  # 微博抓取爬虫
│       ├── comment.py           # 评论抓取爬虫（热度排序）
│       └── common.py            # 公共工具函数
├── backend/                # Go 模块化后端、独立 SQLite、采集与 API
├── frontend/               # Vue + TypeScript + ECharts、Nginx 配置
├── tests/                  # Python 测试（仓库根目录）
├── docs/                   # 架构、部署、历史方案和验收记录
├── docker-compose.yml      # 三服务统一编排，数据分别持久化
├── install.sh / update.sh  # 统一安装与发布脚本
├── requirements.txt        # 原 Python 依赖
└── .gitignore
```

## 微博 API 说明

以下为原服务部分端点，仍从 5050 访问；白酒端点见 [Go API 契约](backend/README.md#api-契约)。当前白酒 Nginx 尚未提供 `/api/weibo/*` 映射。

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/tweets` | GET | 获取微博列表（支持分页、筛选、回收站） |
| `/api/tweets/<id>` | GET | 获取单条微博及评论 |
| `/api/tweets/<id>` | DELETE | 删除微博（软删除，进入回收站） |
| `/api/tweets/batch-delete` | DELETE | 批量删除 |
| `/api/tweets/restore` | POST | 批量恢复 |
| `/api/export` | GET | 导出数据（`?format=pdf` 下载 PDF，`?start=&end=` 筛选时间） |
| `/api/crawl` | POST | 手动触发抓取（可选 `{"user_id":"xxx"}` 抓取指定用户） |
| `/api/crawl/cancel` | POST | 取消正在进行的抓取 |
| `/api/crawl/status` | GET | 获取抓取状态（`tweet`/`comment` 运行状态 + 最近日志尾部），前端每 3s 轮询 |
| `/api/config` | GET/POST | 读取/修改配置（cookie, user_ids, start_date, end_date） |
| `/api/stats` | GET | 获取数据统计（微博总数、评论总数等） |

## 技术栈

- **爬虫框架**：Scrapy 2.5
- **Web 框架**：Flask 2.3 + Waitress
- **定时任务**：APScheduler 3.10
- **数据库**：SQLite
- **PDF 生成**：Headless Chrome + Google Fonts (Noto Sans SC)
- **微博状态更新**：前端每 3s 普通定时轮询 `/api/crawl/status`，不是长轮询或 SSE，避免 Waitress 线程被长连接占满
- **白酒后端**：Go 1.26、`net/http`、`database/sql`、CGO SQLite
- **白酒前端**：Vue 3 + TypeScript + Vite + ECharts；生产由 Nginx 提供静态文件

## 一键部署到服务器

详见 [docs/deploy-prep.md](docs/deploy-prep.md) 完整准备步骤。

### 首次部署

在服务器上执行：

```bash
curl -fsSL https://raw.githubusercontent.com/Banana1995/WeiboSpider/master/install.sh | sudo bash
```

### 自动更新

push 到 `master` 分支后，GitHub Actions 自动 SSH 到服务器执行 `update.sh`，邮件通知部署结果。脚本快进拉取后重新执行新版自身，在服务器构建三个镜像并运行 `docker compose up -d --build`，随后检查微博和白酒入口。

各服务的依赖、构建上下文和数据独立，但发布流程目前统一；镜像或配置变化可能重建对应容器，不能保证只改白酒就完全不触碰微博发布。尚未实现按模块独立发布或 CI 预构建镜像后分发。

需在仓库 Secrets 配置 `SSH_HOST`、`SSH_USER`、`SSH_PORT`、`SSH_KEY`、`SMTP_*`、`MAIL_TO`。脚本会在服务器缺少配置时生成私有 `.env.platform`，供 Go 和 Nginx 使用；令牌不进入前端构建产物。安全组需按需要放行 TCP 5050、5052，不开放 Go 的 5051。

### 手动更新

```bash
ssh deploy@<服务器IP>
cd /opt/weibospider
./update.sh
```

### 数据持久化

| 服务 | 持久化位置 |
| --- | --- |
| 微博 | `/opt/weibospider/data/` 绑定挂载到容器 `/app/weibospider/data` |
| 白酒 | Compose 命名卷 `platform-data` 挂载到 Go 容器 `/app/data`，库为 `liquor.db` |

正常重建保留这些存储，但持久化不是备份。禁止使用 `docker compose down -v` 删除生产卷；不要直接复制运行中的 SQLite 主文件或数据目录作为一致性备份。备份和权限要求见 [部署准备与运维](docs/deploy-prep.md)。

当前入口使用 HTTP，未实现完整用户登录。白酒 API 代理只允许 GET/HEAD，服务令牌不等于用户认证；未来敏感模块上线前需补齐访问控制及 HTTPS 或受控 VPN。

## License

MIT
