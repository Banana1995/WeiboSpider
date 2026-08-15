# AGENTS.md

本项目工作指引。任何 agent 在此仓库工作时，先读本文件。

## 项目概述

- **项目**：微博管理器（WeiboSpider）
- **定位**：基于 Scrapy + Flask 的微博内容抓取与管理系统。支持定时抓取、热度排序评论、PDF 导出、Web 管理界面。
- **技术栈**：Python 3.9 / Scrapy 2.5 / Flask 2.3 / Waitress / APScheduler / SQLite / Headless Chrome（PDF）。前端每 3s 轮询 `/api/crawl/status` 获取抓取状态与日志（**不用 SSE**，避免 waitress 线程被长连接占满）
- **License**：MIT
- **仓库**：https://github.com/Banana1995/WeiboSpider（公有仓库，master 分支）
- **前端**：原生 JS SPA（无框架，单文件 `weibospider/static/index.html`），Flask 提供静态文件

## 当前状态（2026-08-14 快照）

- **部署**：线上正常运行，容器 Up，服务端一切健康（页面 /api/tweets /api/stats 均 200）
- **GitHub Actions**：自动部署全绿，push master 即自动部署 + 邮件通知
- **数据量**：微博 4954 条（已删 2449），评论 109083 条
- **已有功能**：定时抓取、热度评论、PDF 导出、批量管理、图片代理、**图片灯箱（点击看大图）**
- **HTTPS**：未启用（纯 IP 无法签发可信证书，保留 HTTP；无密码类敏感数据，风险可控）

## 目录结构

```
weibospider/
├── app.py              # Flask Web 应用、API 路由、PDF 导出、图片代理
├── db.py               # SQLite 数据库操作 (TweetDB)
├── run.py              # 启动入口（--dev 热更新 / 生产 waitress）
├── scheduler.py        # 定时抓取调度器（每日凌晨 2:00）
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
├── data.db             # 本地 SQLite（gitignore，不提交）
└── tests/              # pytest 测试
docs/
Dockerfile              # python:3.9-slim + chromium + Noto CJK 字体
docker-compose.yml      # 端口 5050，volume 挂载 ./data
install.sh / update.sh  # 服务器一键部署/更新
.github/workflows/deploy.yml  # push master 自动部署
```

## 开发环境

- **本地开发**：`cd weibospider && python run.py --dev`（端口 5000）或 `python run.py --port <port>`
- **虚拟环境**：项目根目录 `venv/`（Python 3.9）
- **测试**：`source venv/bin/activate && python -m pytest tests/ -q`
  - 注意：`venv` 中 werkzeug 版本过新（缺 `__version__` 属性），`test_app.py` / `test_integration.py` 中依赖 Flask test client 的 fixture 会报 AttributeError，**这是环境问题，与代码改动无关**。其余测试（test_common / test_db / test_spider_params / test_frontend / test_chrome_path 等 93 个）应全部通过。
- **git 仓库状态**：master 分支，push 即触发自动部署，**不要随意 force push**（历史曾重写过一次，见"历史遗留"）。

## 服务器与部署

### 服务器信息

| 项 | 值 |
|----|-----|
| 公网 IP | 43.130.247.183 |
| SSH 用户 | deploy |
| 部署目录 | /opt/weibospider |
| 服务端口 | 5050 |
| 数据目录 | /opt/weibospider/data/（volume 挂载，容器内 /app/weibospider/data） |
| 容器 | weibospider（docker compose） |
| 容器用户 | UID 1000（app），**deploy 用户 UID 是 1002** |

### 本机 SSH 连接（别名 `weibo`）

已在 `~/.ssh/config` 配置，本地直接：

```bash
ssh weibo                    # 登录服务器
ssh weibo 'docker compose ps'  # 服务器上执行命令
```

配置内容：HostName 43.130.247.183 / User deploy / IdentityFile ~/.ssh/weibospider_deploy

### 自动部署

- **触发**：push 到 master → GitHub Actions（deploy.yml）SSH 到服务器跑 `update.sh`（git pull + docker compose up -d --build）→ 邮件通知
- **GitHub Secrets**（9 个，已配置）：SSH_HOST / SSH_USER / SSH_PORT / SSH_KEY / SMTP_HOST / SMTP_PORT / SMTP_USER / SMTP_PASS / MAIL_TO
- **服务器上 git 已设置**：`git branch --set-upstream-to=origin/master` 且 `pull.ff only`

### 服务器权限坑（重要）

- **数据目录属主**：必须 `chown -R 1000:1000 /opt/weibospider/data/`，否则容器内 UID 1000 无法写库，抓取报 `PermissionError: data.db.plock`。
- **deploy 无 sudo**：deploy 用户不在 sudoers。需要 root 权限时用 `sudo -u deploy` 反向（root 执行）或先 root 授权目录再 su deploy。
- **git 所有权**：目录属主须为 deploy，否则 `git pull` 报 `dubious ownership`。

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

仓库历史曾包含真实微博 Cookie（`weibospider/cookie.txt`、`.github/cookie.png`），已用 `git filter-branch` 重写历史并 force push。**后续严禁提交任何 Cookie / 密钥 / data.db**（均已在 .gitignore）。

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
2. 看容器日志：`ssh weibo 'docker compose logs --tail 50'` — 确认抓取/调度正常。
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
- 私有信息只放 `data/`（gitignore）或环境变量 / GitHub Secrets。
- 改公开仓库前，先确认 git 历史无敏感内容（`git log --all -p | grep <敏感词>`）。
