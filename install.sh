#!/bin/bash
set -e

INSTALL_DIR="/opt/weibospider"
REPO_URL="https://github.com/Banana1995/WeiboSpider.git"

echo "=== WeiboSpider 一键部署 ==="

# 1. 检查 root
if [ "$EUID" -ne 0 ]; then
    echo "请用 sudo 运行此脚本"
    exit 1
fi

# 2. 安装 Docker
if ! command -v docker &>/dev/null; then
    echo "→ 安装 Docker..."
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker
    echo "✓ Docker 已安装"
else
    echo "✓ Docker 已存在"
fi

# 3. 检查 Docker Compose
if ! docker compose version &>/dev/null; then
    echo "→ 安装 Docker Compose 插件..."
    apt-get update && apt-get install -y docker-compose-plugin
    echo "✓ Docker Compose 已安装"
else
    echo "✓ Docker Compose 已存在"
fi

# 4. 克隆或更新仓库
if [ -d "$INSTALL_DIR/.git" ]; then
    echo "→ 目录已存在，拉取最新代码..."
    cd "$INSTALL_DIR"
    git pull --ff-only
else
    echo "→ 克隆仓库到 $INSTALL_DIR..."
    git clone "$REPO_URL" "$INSTALL_DIR"
    cd "$INSTALL_DIR"
fi

# 5. 创建数据目录
mkdir -p data
chown -R 1000:1000 data

# 6. 构建并启动
echo "→ 构建镜像并启动容器（首次可能需 2-3 分钟）..."
docker compose up -d --build

# 7. 健康检查
echo "→ 等待服务启动..."
for i in $(seq 1 30); do
    if curl -sf http://localhost:5050/ >/dev/null 2>&1; then
        echo ""
        echo "✓ 服务已启动"
        SERVER_IP=$(hostname -I | awk '{print $1}')
        echo "访问: http://$SERVER_IP:5050"
        echo "日志: docker compose logs -f"
        exit 0
    fi
    printf "."
    sleep 2
done

echo ""
echo "✗ 服务启动超时"
echo "查看日志: cd $INSTALL_DIR && docker compose logs"
exit 1
