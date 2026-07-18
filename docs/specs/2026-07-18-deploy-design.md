# WeiboSpider 一键部署与自动更新设计

日期：2026-07-18
状态：待实现

## 目标

为 WeiboSpider 添加一键部署和自动更新能力，满足两个场景：

1. **首次部署**：在全新 Ubuntu 服务器上一行命令拉起服务
2. **持续更新**：开发者 push 到 `master` 分支后，自动部署到服务器，邮件通知结果

## 整体架构

```
开发机                     GitHub                      服务器
─────                     ──────                      ──────
git push master ────────► Actions workflow ─────────► SSH 执行 update.sh
                          (deploy.yml)                  ├─ git pull
                          ├─ SSH 连服务器                ├─ docker compose up --build
                          └─ 发邮件通知结果              └─ 健康检查
```

首次部署：
```
curl -fsSL <raw-url>/install.sh | sudo bash
  ├─ 安装 Docker + Docker Compose
  ├─ git clone 仓库到 /opt/weibospider
  └─ docker compose up -d --build
```

## 容器化方案

### 镜像设计
- 基础镜像：`python:3.9-slim`
- 内置 Chromium（headless，用于 PDF 导出）
- 工作目录：`/app`
- 容器内用户：非 root（`app` 用户，UID 1000）
- 暴露端口：5050
- 数据目录：`/app/weibospider/data`（挂载到宿主机）

### Dockerfile
```dockerfile
FROM python:3.9-slim

# 安装 Chromium（用于 PDF 导出）和必要系统包
RUN apt-get update && apt-get install -y --no-install-recommends \
    chromium \
    fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# 先装依赖（利用 Docker 层缓存）
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt waitress

# 复制项目
COPY . .

# 创建非 root 用户和 data 目录
RUN useradd -r -u 1000 app && \
    mkdir -p /app/weibospider/data && \
    chown -R app:app /app

USER app

EXPOSE 5050

# 生产模式启动（waitress）
CMD ["python", "weibospider/run.py", "--host", "0.0.0.0", "--port", "5050"]
```

### docker-compose.yml
```yaml
version: "3.8"
services:
  weibospider:
    build: .
    container_name: weibospider
    ports:
      - "5050:5050"
    volumes:
      - ./data:/app/weibospider/data
    restart: unless-stopped
    environment:
      - CHROME_PATH=/usr/bin/chromium
```

## 数据持久化

- 宿主机 `/opt/weibospider/data/` 挂载到容器 `/app/weibospider/data/`
- 包含内容：
  - `data.db` — SQLite 数据库（含微博数据 + 配置）
  - `server.log` — 运行日志
  - `crawl.log` — 抓取日志

**关键改动：** `db.py:14` 当前数据库路径固定在模块目录下。挂载 `./data` 到该目录后，路径自动指向挂载卷，无需改代码。

## 代码改动

### 1. Chrome 路径适配（app.py:828）
当前硬编码 macOS 路径，改为环境变量优先 + 平台探测：

```python
def _get_chrome_path():
    # 环境变量优先（容器内用这个）
    env_path = os.environ.get('CHROME_PATH')
    if env_path and os.path.exists(env_path):
        return env_path
    # macOS
    mac_path = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
    if os.path.exists(mac_path):
        return mac_path
    # Linux 本地开发
    for p in ['google-chrome', 'chromium', 'chromium-browser']:
        import shutil
        found = shutil.which(p)
        if found:
            return found
    raise RuntimeError('未找到 Chrome/Chromium，请设置 CHROME_PATH 环境变量')

chrome_path = _get_chrome_path()
```

### 2. requirements.txt 补充
添加 `waitress`（生产模式 WSGI 服务器，run.py 已依赖但 requirements 里缺失）。

## 部署脚本

### install.sh（首次部署）
```bash
#!/bin/bash
set -e
INSTALL_DIR="/opt/weibospider"

# 1. 检查并安装 Docker
if ! command -v docker &>/dev/null; then
    echo "安装 Docker..."
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker
fi

# 2. 检查 Docker Compose（v2 内置或独立二进制）
if ! docker compose version &>/dev/null && ! command -v docker-compose &>/dev/null; then
    echo "安装 Docker Compose..."
    apt-get update && apt-get install -y docker-compose-plugin
fi

# 3. 克隆仓库
if [ -d "$INSTALL_DIR/.git" ]; then
    echo "目录已存在，执行更新..."
    cd "$INSTALL_DIR" && git pull
else
    git clone https://github.com/Banana1995/WeiboSpider.git "$INSTALL_DIR"
    cd "$INSTALL_DIR"
fi

# 4. 创建数据目录
mkdir -p data

# 5. 构建并启动
docker compose up -d --build

# 6. 等待并健康检查
echo "等待服务启动..."
for i in $(seq 1 30); do
    if curl -sf http://localhost:5050/ >/dev/null 2>&1; then
        echo "✓ 服务已启动"
        echo "访问: http://$(hostname -I | awk '{print $1}'):5050"
        exit 0
    fi
    sleep 2
done
echo "✗ 服务启动超时，查看日志: docker compose logs"
exit 1
```

### update.sh（更新部署，幂等）
```bash
#!/bin/bash
set -e
cd /opt/weibospider

echo "拉取最新代码..."
git pull

echo "重建并重启容器..."
docker compose up -d --build

echo "健康检查..."
for i in $(seq 1 30); do
    if curl -sf http://localhost:5050/ >/dev/null 2>&1; then
        echo "✓ 更新成功"
        exit 0
    fi
    sleep 2
done
echo "✗ 更新后服务未恢复，查看日志: docker compose logs"
exit 1
```

## CI/CD 自动部署

### .github/workflows/deploy.yml
```yaml
name: Deploy to Server

on:
  push:
    branches: [master]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Deploy via SSH
        uses: appleboy/ssh-action@v1.0.0
        with:
          host: ${{ secrets.SSH_HOST }}
          username: ${{ secrets.SSH_USER }}
          key: ${{ secrets.SSH_KEY }}
          port: ${{ secrets.SSH_PORT || 22 }}
          script: |
            cd /opt/weibospider
            ./update.sh

      - name: Send email notification
        if: always()
        uses: dawidd6/action-send-mail@v3
        with:
          server_address: ${{ secrets.SMTP_HOST }}
          server_port: ${{ secrets.SMTP_PORT }}
          username: ${{ secrets.SMTP_USER }}
          password: ${{ secrets.SMTP_PASS }}
          subject: "[WeiboSpider] 部署 ${{ job.status == 'success' && '成功' || '失败' }}"
          to: ${{ secrets.MAIL_TO }}
          from: ${{ secrets.SMTP_USER }}
          body: |
            分支: master
            提交: ${{ github.sha }}
            结果: ${{ job.status }}
            时间: ${{ github.event.head_commit.timestamp }}
            提交者: ${{ github.actor }}
```

## 需要配置的 GitHub Secrets

| Secret | 说明 |
|--------|------|
| `SSH_HOST` | 服务器 IP |
| `SSH_USER` | SSH 用户名 |
| `SSH_KEY` | SSH 私钥（完整内容） |
| `SSH_PORT` | SSH 端口（可选，默认 22） |
| `SMTP_HOST` | SMTP 服务器地址 |
| `SMTP_PORT` | SMTP 端口（465/587） |
| `SMTP_USER` | SMTP 用户名（发件邮箱） |
| `SMTP_PASS` | SMTP 密码/授权码 |
| `MAIL_TO` | 通知收件邮箱 |

## 服务器端准备步骤

部署前用户需在服务器上操作一次：

1. 创建专用部署用户：
   ```bash
   sudo useradd -m -s /bin/bash deploy
   sudo usermod -aG docker deploy
   ```

2. 配置 SSH 公钥免密登录：
   ```bash
   sudo mkdir -p /home/deploy/.ssh
   sudo cp ~/.ssh/authorized_keys /home/deploy/.ssh/
   sudo chown -R deploy:deploy /home/deploy/.ssh
   ```

3. 预创建目录并授权：
   ```bash
   sudo mkdir -p /opt/weibospider
   sudo chown deploy:deploy /opt/weibospider
   ```

4. 首次部署：
   ```bash
   curl -fsSL https://raw.githubusercontent.com/Banua1995/WeiboSpider/master/install.sh | sudo bash
   ```

5. 将 SSH 私钥配置到 GitHub Secrets

## 首次部署流程

```bash
# 在服务器上
curl -fsSL <raw-url>/install.sh | sudo bash
# → 自动装 Docker、clone 代码、构建镜像、启动容器
# → 输出访问地址
```

## 更新部署流程

```bash
# 在开发机上
git push origin master
# → GitHub Actions 自动触发
# → SSH 到服务器执行 update.sh
# → 邮件通知结果
```

## 错误处理

| 场景 | 处理 |
|------|------|
| `git pull` 冲突 | `update.sh` 失败退出，邮件通知，需人工 `ssh` 处理 |
| 构建失败 | `docker compose up --build` 失败，旧容器继续运行（`--build` 失败不影响已运行容器），邮件通知 |
| 健康检查失败 | `update.sh` 退出非 0，邮件报失败 |
| SSH 连接失败 | Actions 步骤失败，邮件通知 |

## 测试要点

1. `Dockerfile` 能在 Linux 构建成功
2. 容器内 Chromium 能生成 PDF（中文正常）
3. `install.sh` 在全新 Ubuntu 22.04 上跑通
4. `update.sh` 幂等，多次执行无副作用
5. 数据持久化：更新后数据库和配置不丢
6. GitHub Actions 触发后服务器实际更新
7. 邮件通知成功/失败两种情况都发

## 不做的事

- 不做 Nginx 反代 / HTTPS（用户选 IP+端口访问）
- 不做多容器编排（只有一个 Python 服务）
- 不做镜像仓库推送（每次在服务器本地构建，简化流程）
- 不做零停机部署（秒级停机可接受）
- 不做回滚自动化（失败时旧容器仍在，可手动 `docker compose up -d` 回退）
