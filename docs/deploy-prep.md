# 服务器部署准备与运维

更新：2026-09-08。白酒已上线，账本按用户授权开放公网匿名共享全部读写；[白酒历史验收记录](validation/2026-09-05-liquor-production.md) 保留当时发布结果。实际版本以服务器 HEAD 和部署工作流为准。以下涉及安装、重建、停写或恢复的操作必须获得授权，不因阅读本文自动执行。

## 当前拓扑

同一台服务器、同一根 Compose 项目，三个独立镜像和容器，**统一发布，不是按模块独立部署**：

| Compose 服务 | 入口 | 数据 |
| --- | --- | --- |
| `weibospider` | 公网 5050，原微博网页和 API | `/opt/weibospider/data/` 绑定到 `/app/weibospider/data` |
| `platform` | Go 5051，仅容器内部，不映射宿主机端口 | `platform-data` 命名卷绑定到 `/app/data`，独立 `liquor.db` 和 `ledger.db` |
| `market-entry` | 公网 5052 映射 Nginx 8080，Vue 页面 `/liquor`、`/ledger` | 无业务数据挂载 |

- 微博入口：<http://43.130.247.183:5050/>；白酒入口：<http://43.130.247.183:5052/liquor>。
- 原微博代码、Python 依赖和数据库保留；共同发布过程中仍可能重建其容器，不能把代码不改等同于部署不受影响。
- Go 与 Nginx 通过服务端 `.env.platform` 共享服务令牌；Nginx 只为白酒上游请求注入令牌，账本上游清除 Authorization，浏览器不持有令牌。
- 白酒 `/api/platform/liquor/*` 代理只允许 GET/HEAD，公开 POST 同步会返回 403；账本精确 `/api/platform/ledger` 及子路径开放全部读写方法，其他 platform 路径 JSON 404。不代理微博写接口。
- 账本入口：<http://43.130.247.183:5052/ledger>。Compose 显式 `LEDGER_ENABLED=true`、`LEDGER_WEEKLY_ENABLED=false`，不因公开访问开启自动任务。保留同源写保护、上传限制和超时；不删除卷、不重置数据库。
- 生产 Compose 设置 `LIQUOR_AUTO_SYNC=true`；Go 本地启动默认关闭。采集规则见 [后端指南](../backend/README.md#同步规则)。页面刷新不触发采集。

## 1. 系统要求

- Ubuntu 20.04 / 22.04 / 24.04（Debian 11+ 亦可）
- 公网 IP，按需要在云安全组和宿主机防火墙放行 TCP 5050、5052 和 SSH 端口；不要开放 Go 的 5051
- root 或 sudo 权限
- Docker Compose 插件，以及 `git`、`curl`、`openssl`；安装脚本负责缺失的 Docker/Compose，服务令牌生成依赖 OpenSSL
- 本机持久化磁盘，不将 SQLite 数据目录放到网络共享盘

当前部署仍是 HTTP。账本根据用户明确授权无需登录、匿名共享全部读写，任何人都能查看、导入和修改全部数据，请勿上传私人财务信息。内部 Bearer 令牌只保护其他模块，不代表账本用户认证或私人数据保护。生产验证仅限读取与无效请求校验，不创建测试账户、不导入用户附件、不触发采集。

## 2. 创建部署用户

```bash
sudo useradd -m -s /bin/bash deploy
sudo usermod -aG docker deploy  # 安装 Docker 后执行
```

## 3. 配置 SSH 免密登录

在开发机上：

```bash
ssh-keygen -t ed25519 -f ~/.ssh/weibospider_deploy  # 若已有 key 可跳过
ssh-copy-id -i ~/.ssh/weibospider_deploy.pub deploy@<服务器IP>
```

测试：

```bash
ssh -i ~/.ssh/weibospider_deploy deploy@<服务器IP> 'echo ok'
```

## 4. 授权目录

```bash
sudo mkdir -p /opt/weibospider
sudo chown deploy:deploy /opt/weibospider
```

## 5. 首次部署

```bash
curl -fsSL https://raw.githubusercontent.com/Banana1995/WeiboSpider/master/install.sh | sudo bash
```

当前安装脚本会安装依赖、拉取仓库、初始化微博数据目录和缺失的 `.env.platform`，再构建并启动三个服务。生产构建在服务器进行，需预留镜像、构建缓存、数据库和备份空间；尚未实现 CI 预构建镜像分发。

首次以 root 安装后，由管理员检查以下权限，不能直接假定后续 `deploy` 更新可用：

- 仓库源码和 `.git` 应由 `deploy` 持有，否则拉取或写入可能失败。
- `.env.platform` 应为 `600` 且由 `deploy` 可读；root 创建的该文件需单独调整属主。不要打印或提交内容。
- 微博 `data/` 必须可由容器 UID 1000 写入，不能将其误改为 deploy 的 UID 1002。
- 白酒命名卷初次挂载时使用 Go 镜像初始化的 UID 1000 目录；不要与微博数据目录混用。

安装脚本目前只检查微博页面的健康状态。首次部署还必须执行下面的白酒 API、同步状态和公网检查，不能仅凭安装脚本成功判断全部可用。

## 6. 配置 GitHub Secrets

在仓库 Settings → Secrets and variables → Actions 添加：

| Secret | 示例 |
|--------|------|
| SSH_HOST | 1.2.3.4 |
| SSH_USER | deploy |
| SSH_KEY | （~/.ssh/weibospider_deploy 的完整私钥内容） |
| SSH_PORT | 22 |
| SMTP_HOST | smtp.qq.com |
| SMTP_PORT | 465 |
| SMTP_USER | you@qq.com |
| SMTP_PASS | （邮箱授权码） |
| MAIL_TO | you@qq.com |

## 7. 验证自动部署

```bash
# 在开发机
git push origin master
# 去 GitHub Actions 页面查看 deploy 任务执行情况
# 邮箱应收到部署成功通知
```

## 8. 日常更新

之后每次向 `master` 推送都会触发共同部署。脚本快进拉取后用 `--after-pull` 重新执行新版自身，避免当前进程继续按旧脚本执行；随后初始化缺失的 `.env.platform`、运行 `docker compose up -d --build` 并检查两个入口。不要手动使用 `--after-pull` 跳过正常拉取流程。

Compose 会检查三个服务，按镜像或配置变化重建对应容器；目前没有按目录变化选择服务发布。也可在获得部署授权后手动执行：

```bash
ssh deploy@<服务器IP>
cd /opt/weibospider
./update.sh
```

仅推送 `feat/go-liquor` 等功能分支不会触发当前工作流。不要为测试在生产检出目录切换功能分支，也不要在共享工作树中覆盖其他人的未提交修改。

## 9. 数据备份

两份业务存储需要分别备份，`.env.platform` 及微博的私有配置也需要受权限保护的恢复安排，不能进入公开仓库。当前 Compose 项目下白酒卷名为 `weibospider_platform-data`，项目名改变时前缀也会改变，应先检查实际挂载，而不是硬编码 Docker 的宿主机内部目录。

```bash
docker inspect --format '{{json .Mounts}}' weibospider
docker inspect --format '{{json .Mounts}}' weibospider-platform-1
```

**不能用对运行中数据库执行 `cp`、`cp -r` 或普通文件打包来保证一致性。** 两个业务库均使用 WAL，复制主文件可能遗漏已提交数据，逐个复制整个目录也可能跨越不同事务时刻。

选择并验证其中一种备份方式：

1. 在线备份：使用 SQLite backup API 或经过验证的 `VACUUM INTO` 工具生成一致性快照，再归档。Go 的 `internal/database.Backup` 已有内部 API 和测试，但没有发布为运维 CLI 或 HTTP 备份端点；不要将其当成现成命令。
2. 停写备份：先安排并确认维护窗口，停止对应业务服务及其他写入者，确认已经退出后复制该服务的完整持久化目录或卷。只关闭网页不等于停止调度、保活或数据库写入。

备份使用私有目录和不覆盖已有文件的名称，记录应用提交、数据库迁移版本、备份时间及校验信息。恢复前先在独立目录/卷检查完整性及应用兼容性，再经过确认停止目标服务并恢复正确 UID/GID。不能将旧库覆盖到仍在运行的生产服务，也不能把测试库直接用作生产库。

正常镜像重建保留数据卷，但不等于已经备份。**禁止执行 `docker compose down -v` 或删除生产卷来“修复部署”。** 数据库分别备份不代表跨模块全局事务一致。生产备份/恢复演练和组合负载测试尚未完成。

## 10. 查看日志

```bash
cd /opt/weibospider
docker compose ps
docker compose logs --tail 100 platform market-entry
docker compose logs --tail 100 weibospider
```

不要把完整配置、Cookie、令牌或私有数据日志粘贴到公开 issue。校验 Compose 语法可使用 `docker compose config --quiet`；直接输出完整配置会展开环境变量，可能泄露令牌。

## 11. 发布后检查

以下为只读检查，在服务器执行：

```bash
curl --max-time 10 -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:5050/api/stats
curl --max-time 10 -fsS http://127.0.0.1:5052/healthz
curl --max-time 10 -fsS http://127.0.0.1:5052/api/platform/liquor/latest
curl --max-time 10 -fsS http://127.0.0.1:5052/api/platform/liquor/sync
```

- `5052/healthz` 只证明 Nginx 存活，不能证明 Go、外部来源或数据新鲜度。
- 最新价接口 200 也可能返回空库。核对 `items`、来源报价日期、同步状态和最后成功时间；不要把 11 款/341 条写成永久不变的健康断言。
- 浏览器从公网打开 `/liquor`，检查脚本、样式、图表资源及 API，验证切换、日期筛选和明细；同时检查旧微博入口。
- 若服务器本机正常而公网超时，先检查监听端口、云安全组、宿主机防火墙和网络路径，不能仅凭超时断定是哪一层。
- 查看 GitHub Actions 的 SSH 部署和邮件通知两个步骤；邮件失败可能使整体标红，但服务实际已部署。
- 回滚应先明确受影响服务和兼容版本，不能通过删除持久卷或回灌旧数据库来回滚页面。当前没有自动回滚流水线。
