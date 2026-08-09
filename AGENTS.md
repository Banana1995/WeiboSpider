# AGENTS.md

本项目工作指引。任何 agent 在此仓库工作时，先读本文件。

## 项目概述

- **项目**：微博管理器（WeiboSpider）
- **定位**：基于 Scrapy + Flask 的微博内容抓取与管理系统。支持定时抓取、热度排序评论、PDF 导出、Web 管理界面。
- **技术栈**：Python 3.9 / Scrapy 2.5 / Flask 2.3 / Waitress / APScheduler / SQLite / SSE / Headless Chrome（PDF）
- **License**：MIT
- **仓库**：https://github.com/Banana1995/WeiboSpider（公有仓库，master 分支）

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

## 安全约定

- 永不提交 Cookie / 密码 / 密钥 / 数据库文件。
- 私有信息只放 `data/`（gitignore）或环境变量 / GitHub Secrets。
- 改公开仓库前，先确认 git 历史无敏感内容（`git log --all -p | grep <敏感词>`）。
