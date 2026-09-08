# AGENTS.md

本项目工作指引。任何 agent 在此仓库工作时，先读本文件。

## 项目概述

- **项目**：微博管理器（WeiboSpider）
- **定位**：原微博/雪球内容管理服务，加上独立 Go 白酒行情后端和 Vue 单品趋势页；逐步演进为多数据源平台。
- **技术栈**：微博使用 Python 3.9 / Scrapy 2.5 / Flask 2.3 / Waitress / APScheduler / SQLite / Headless Chrome（PDF）；白酒使用 Go 1.26 / CGO SQLite / Vue 3 / TypeScript / Vite / ECharts / Nginx。
- **状态更新**：微博前端每 3s 普通轮询 `/api/crawl/status`（**不用 SSE 或长轮询**）；白酒页面仅在加载、手动刷新时读取同步状态，刷新不触发采集。
- **License**：MIT
- **仓库**：https://github.com/Banana1995/WeiboSpider（公有仓库，master 分支）
- **前端**：原微博 `weibospider/static/index.html` 由 Flask 提供；新增 `frontend/` 由 Nginx 提供构建产物。暂未统一导航或迁移微博到 Vue。

## 账本公开访问（2026-09-08 用户授权）

- 用户明确授权账本公网匿名共享全部读写，不设登录或用户权限；入口 <http://43.130.247.183:5052/ledger>。任何人均可读取、导入、修改全部账本数据，不应上传私人财务信息。
- Compose 设置 `LEDGER_ENABLED=true`、`LEDGER_WEEKLY_ENABLED=false`；Go 5051 仍仅内部开放，`platform-data` 中 `ledger.db` 与 `liquor.db` 独立。不得删除数据卷或重置数据库。
- 仅精确 `/api/platform/ledger` 及其子路径免服务令牌；保留同源写保护、请求限制、校验、幂等和审计。Nginx 账本代理清除 Authorization；白酒仍仅公开 GET/HEAD，并使用内部服务令牌。
- 此授权取代历史文档的账本仅限本机、等待开放条件，不代表提供私人数据保护或已完成完整功能验收。本次不运行 E2E；生产仅做读取和无效请求校验，不创建测试数据、不导入用户附件、不触发采集。

## 白酒历史状态（2026-09-05 验收快照）

- **版本**：白酒功能已合入远端 `master`，本次生产版本 `e2c5476`。工作前仍需检查实际分支、工作树和未提交修改，不把此快照当作实时状态。
- **部署**：根 Compose 统一管理 `weibospider`、`platform`、`market-entry` 三个独立容器；push `master` 触发共同部署和邮件通知，不是按模块独立发布。
- **入口**：原微博 `http://43.130.247.183:5050/`；白酒 `http://43.130.247.183:5052/liquor`；Go 的 5051 仅容器内部开放。
- **已验收**：白酒自动采集 11 款酒、341 条报价；公网浏览器列表、单品趋势、日期筛选、明细及移动视口验证通过。数据量是当日样本，不是固定约束。
- **未实现**：多品对比、归一化涨跌幅、微博 Vue 迁移、统一导航、完整登录、独立发布流水线。
- **HTTPS**：当前未配置，HTTP 和服务令牌不能视为完整用户认证。白酒仅开放行情读取；账本按上述授权公开共享，不承诺私人财务数据保密。
- **文档**：[当前架构](docs/specs/2026-09-05-multi-source-architecture.md)、[部署运维](docs/deploy-prep.md)、[生产验收记录](docs/validation/2026-09-05-liquor-production.md)。

历史数据快照（2026-08-14）：微博 4954 条（已删 2449）、评论 109083 条，当时已有定时抓取、热度评论、PDF、批量管理、图片代理及灯箱；不代表当前库的统计。

## 目录结构

```
weibospider/
├── app.py              # Flask Web 应用、API 路由、PDF 导出、图片代理
├── db.py               # SQLite 数据库操作 (TweetDB)
├── run.py              # 启动入口（--dev 热更新 / 生产 waitress）
├── scheduler.py        # 北京时间 [07:00,22:00)，微博62/评论47分钟 + 0至5分钟抖动
├── keepalive.py        # Cookie 保活（Set-Cookie 刷新）
├── settings.py         # Scrapy 全局配置
├── pipelines.py        # Scrapy 数据管道（写 SQLite）
├── middlewares.py      # Scrapy 中间件（代理、UA）
├── spiders/
│   ├── tweet_by_user_id.py  # 微博抓取爬虫
│   ├── comment.py           # 评论抓取爬虫（热度排序 flow=0）
│   └── common.py            # 解析工具（parse_tweet_info 等）
├── static/index.html   # 前端 SPA 页面
├── start.sh / stop.sh  # 后台启停脚本
└── data.db             # 本地 SQLite（gitignore，不提交）
backend/                # Go 模块、独立库、API、采集；内部测试和 e2e/
frontend/               # Vue 单品行情、Vitest、Dockerfile、nginx.conf.template
tests/                  # 仓库根目录的 Python pytest 测试
docs/                   # 当前指南、架构、历史方案、验收记录
Dockerfile              # python:3.9-slim + chromium + Noto CJK 字体
docker-compose.yml      # 三服务：5050微博、5052白酒、5051内部；两份独立持久化
install.sh / update.sh  # 服务器一键部署/更新
.github/workflows/deploy.yml  # push master 自动部署
```

## 开发环境

- **微博开发**：在 `weibospider/` 执行 `python run.py --dev --host 127.0.0.1`（默认端口 5050）；`--dev` 不改变默认端口。定时调度开启时才运行上述间隔任务，重启不立即抓取。
- **虚拟环境**：项目根目录 `venv/`（Python 3.9）
- **测试**：`source venv/bin/activate && python -m pytest tests/ -q`
- **已知旧环境问题**：曾在本机 `venv` 观察到 Werkzeug 缺少 `__version__`，导致 Flask test client fixture 报错；这是历史环境记录，不是当前测试数量或必然失败的承诺，需按实测判断。
- **白酒后端**：在 `backend/` 执行 `go run ./cmd/server`，默认回环 5051、本地自动采集关闭；验证用 `make check`、`make test`、`make test-e2e`，真实来源需显式 `make test-e2e-live`。生产 Compose 另设 `LIQUOR_AUTO_SYNC=true`。
- **白酒前端**：在 `frontend/` 执行 `npm ci`、`npm run dev`，访问回环 5173；`npm test`、`npm run build`、`npm run format:check`。不要把服务令牌放入 `VITE_*`。
- **Git 约束**：仅在用户授权时提交、推送或部署，push `master` 会触发三服务的共同发布；检查工作树，保留其他人的修改，**不要随意 force push**。

## 服务器与部署

### 服务器信息

| 项 | 值 |
|----|-----|
| 公网 IP | 43.130.247.183 |
| SSH 用户 | deploy |
| 部署目录 | /opt/weibospider |
| 服务端口 | 5050 微博；5052 白酒；5051 仅容器内部 |
| 微博数据 | /opt/weibospider/data/（绑定挂载，容器内 /app/weibospider/data） |
| 白酒数据 | Compose 卷 platform-data（当前实际名 weibospider_platform-data），容器内 /app/data/liquor.db |
| 服务配置 | /opt/weibospider/.env.platform（600，deploy 可读，禁止提交或打印令牌） |
| 容器 | weibospider、weibospider-platform-1、weibospider-market-entry-1（同一 Compose 项目） |
| 业务容器用户 | Python 和 Go 均 UID 1000（app），**deploy 用户 UID 是 1002** |

### 本机 SSH 连接（别名 `weibo`）

已在 `~/.ssh/config` 配置，本地直接：

```bash
ssh weibo                    # 登录服务器
ssh weibo 'docker compose --project-directory /opt/weibospider ps'
```

配置内容：HostName 43.130.247.183 / User deploy / IdentityFile ~/.ssh/weibospider_deploy

### 自动部署

- **触发**：push 到 master → GitHub Actions（deploy.yml）SSH 到服务器跑 `update.sh` → 快进拉取并重新执行新版脚本 → 初始化缺失的 `.env.platform` → 在服务器构建三个镜像并 `docker compose up -d --build` → 双入口健康检查 → 邮件通知。
- **发布边界**：镜像、依赖和数据独立，但流水线统一；镜像或配置变化会重建对应容器，本次发布曾重建微博容器。未实现 CI 预构建分发或按路径选择服务发布。
- **GitHub Secrets**（9 个，已配置）：SSH_HOST / SSH_USER / SSH_PORT / SSH_KEY / SMTP_HOST / SMTP_PORT / SMTP_USER / SMTP_PASS / MAIL_TO
- **服务器上 git 已设置**：`git branch --set-upstream-to=origin/master` 且 `pull.ff only`

### 服务器权限坑（重要）

- **数据目录属主**：必须 `chown -R 1000:1000 /opt/weibospider/data/`，否则容器内 UID 1000 无法写库，抓取报 `PermissionError: data.db.plock`。
- **deploy 无 sudo**：deploy 用户不在 sudoers。需要 root 权限时用 `sudo -u deploy` 反向（root 执行）或先 root 授权目录再 su deploy。
- **git 所有权**：目录属主须为 deploy，否则 `git pull` 报 `dubious ownership`。
- **白酒持久化**：命名卷挂载目录由 Go 镜像按 UID 1000 初始化；不要挂载微博数据，也不要执行 `docker compose down -v` 或删除生产卷。备份须使用 SQLite 一致性机制或经确认停写后复制，详见部署运维文档。

## 已踩过的坑（排查经验）

### 1. 图片不显示（已修复）——新浪图床防盗链 + ORB 拦截

**现象**：本地开发图片正常，部署到线上（IP:5050）图片全部不显示。

**根因**：新浪图床 `wx1.sinaimg.cn` 防盗链规则按组合生效：

| User-Agent | Referer | 结果 |
|------------|---------|------|
| 浏览器 UA | weibo.com | 200 |
| 浏览器 UA | 无（no-referrer） | **403** |
| 浏览器 UA | 非微博域名（如服务器 IP） | **403** |
| 非浏览器 UA（curl 默认） | 无 | 200 |

浏览器把图片当 img 加载时，若新浪返回 403 HTML 错误页，浏览器还会触发 `net::ERR_BLOCKED_BY_ORB` 拦截（Opaque Response Blocking）。

**曾走弯路**：第一版修复用 `<meta name="referrer" content="no-referrer">` + `<img referrerpolicy="no-referrer">`，反而让浏览器不带 Referer → 403，**无效且更糟**。

**正确修复（当前方案）**：
1. 后端 `app.py` 新增 `/api/img?url=...` 代理接口：服务端用 `User-Agent: Mozilla/5.0` + `Referer: https://weibo.com/` 抓图，内存缓存（最多 200 条），只允许 sinaimg.cn 域名。
2. 前端 `index.html` 新增 `proxyImg(u)` 函数：sinaimg.cn 的 URL 一律替换为 `/api/img?url=...`。
3. 两处图片渲染（`renderCard` 和 `renderRetweet`）都用 `proxyImg` 包一层。

**排查方法**（同类问题复用）：
```bash
# 对比 UA 和 Referer 组合
UA='Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36'
curl -s -o /dev/null -w "%{http_code}\n" -A "$UA" -H "Referer: https://weibo.com/" "<图片URL>"
# 浏览器端看 Network：ERR_BLOCKED_BY_ORB / 403 都指向防盗链
```

### 2. 历史泄露的 Cookie 已清理（勿再提交敏感文件）

仓库历史曾包含真实微博 Cookie（`weibospider/cookie.txt`、`.github/cookie.png`），已用 `git filter-branch` 重写历史并 force push。**后续严禁提交任何 Cookie / 密钥 / data.db**。不要假定所有敏感路径均已忽略：提交前检查实际文件清单和忽略规则，特别是截图、备份及服务器残留未跟踪文件。

### 3. 服务器首次部署流程

```bash
# 服务器（root）
sudo useradd -m -s /bin/bash deploy
sudo mkdir -p /opt/weibospider && sudo chown deploy:deploy /opt/weibospider
curl -fsSL https://raw.githubusercontent.com/Banana1995/WeiboSpider/master/install.sh | sudo bash
sudo chown -R 1000:1000 /opt/weibospider/data/   # 容器写库权限
# 服务器 git 上游（重建 git 后必须做）
su - deploy -c 'cd /opt/weibospider && git branch --set-upstream-to=origin/master master'
```

### 4. 服务器日常排查命令

```bash
ssh weibo
cd /opt/weibospider
docker compose ps                 # 容器状态
docker compose logs -f            # 实时日志
docker compose logs --tail 100    # 最近 100 行
docker compose up -d --build      # 手动重建
# 查数据库（服务器无 sqlite3，用 python）
python3 -c "import sqlite3; c=sqlite3.connect('data/data.db'); print(c.execute('SELECT COUNT(*) FROM tweets').fetchone())"
```

### 5. GitHub Actions 排查

```bash
gh run list -R Banana1995/WeiboSpider --limit 5        # 最近运行
gh run view <run_id> -R Banana1995/WeiboSpider --log   # 完整日志
```
- 邮件通知步骤失败会导致整体标红，但**部署本身可能已成功**（看 Deploy via SSH 步骤是否 `✓ 更新成功`）。
- Secrets 在 push 之前才配置时，本次运行会因拿不到值失败，重新 push 即可。

### 6. 图片灯箱（已实现）——点击缩略图看大图

纯前端功能，位于 `weibospider/static/index.html`：

- **CSS**：`.lightbox` 全屏遮罩（`rgba(0,0,0,0.9)`，z-index 10000），`.lb-close` 关闭按钮，`.lb-loading` 加载提示。
- **HTML**：`<div id="lightbox">` 内含 `#lightbox-img` 和 `#lightbox-loading`，位于页面底部（toast 之后）。
- **JS 函数**：
  - `toFullSize(u)`：把 sinaimg URL 的尺寸后缀 `orj960/wap720/mw690/bmiddle/thumbnail` 替换为 `large`（原图，清晰度更高）。
  - `openLightbox(thumbUrl)`：先加载 `large` 原图（经 `proxyImg` 代理），失败自动回退到缩略图，成功则隐藏 loading。
  - `closeLightbox()`：移除 `show` class 并清空 `img src`。
  - 事件：`document` 委托 click（`e.target.closest('.card-images img')`）打开灯箱，从 `src` 中解析 `url=` 参数还原原始图片 URL；keydown Esc 关闭。
- **注意**：缩略图 `src` 是代理地址（`/api/img?url=...`），打开灯箱时必须用 `decodeURIComponent` 从 `url=` 参数还原真实 sinaimg URL 再传给 `openLightbox`。

### 7. 页面"加载中"排查（经验）

**现象**：页面能打开但卡在"加载中"，列表不渲染。

**排查流程**（区分服务端 vs 浏览器端）：
1. 先验证服务端：`curl -s -o /dev/null -w "%{http_code}" http://43.130.247.183:5050/api/tweets` — 200 说明后端正常。
2. 看容器日志：`ssh weibo 'docker compose --project-directory /opt/weibospider logs --tail 50 weibospider'` — 确认抓取/调度正常。
3. 用浏览器 DevTools 检查：Network 里 `/api/tweets` 是否 200；Console 是否有 JS 异常。
4. **常见误判**：`favicon.ico` 的 404 是正常的，与崩溃无关；"加载中"可能只是瞬间状态或浏览器缓存，先强刷（Cmd/Ctrl+Shift+R）再判断。

### 8. 标签页"喔唷，崩溃啦"（已修复）——SSE 全量日志重灌 + O(n²) 前端拼接

**现象**：页面长时间开着后 Chrome 标签页直接崩溃（Aw, Snap），刷新/重开标签页又能用。

**根因**（2026-08-15 修复）：
1. `crawl.log` 无限增长（线上曾达 2.4MB+/2.8 万行；只有手动"全量抓取"会清空，定时增量抓取只追加）。
2. 旧 SSE 端点 `/api/crawl/events` 每次连接/重连都从 `last_log_pos=0` 把**整个文件**重推给浏览器。
3. 前端 `log` 事件处理用 `body.textContent += e.data` 逐行追加 —— **O(n²)** 字符串重建 + 每行强制 reflow，行数越多主线程卡得越久，最终 renderer OOM 崩溃。

**修复方案**：
1. **SSE 改轮询（根治）**：删除 `/api/crawl/events`，前端 `setInterval(pollCrawlStatus, 3000)` 每 3s 轮询 `/api/crawl/status`（该接口返回 `SCHEDULER.status` + `logs` 尾部 200 行）。**不用 SSE 也避免 waitress 线程被长连接占满**（waitress 是同步服务器，每条 SSE 长连接永久占一个 worker 线程，`threads=8` 下开几个标签页就耗尽）。
2. `_make_log_helpers` 每次抓取开始时清空 `crawl.log`，防止无限增长。
3. 前端日志面板用 `body.textContent = data.logs.join('\n')` 整体替换（只在内容变化时写），不再逐行 `+=`。

**排查方法**（同类问题复用）：`curl -N http://IP:5050/api/crawl/events` 数一次连接收到多少字节——旧版会立刻灌入全部历史日志（几 MB），说明连接级日志回放有问题。

## 安全约定

- 永不提交 Cookie / 密码 / 密钥 / 数据库文件。
- 私有信息仅放已核验被忽略且受权限保护的数据目录、服务器 `.env.platform`、环境变量或 GitHub Secrets；不要打印令牌或把它带入浏览器构建。
- 改公开仓库前，先确认 git 历史无敏感内容（`git log --all -p | grep <敏感词>`）。
