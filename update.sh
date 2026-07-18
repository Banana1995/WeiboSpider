#!/bin/bash
set -e

INSTALL_DIR="/opt/weibospider"
cd "$INSTALL_DIR"

echo "=== WeiboSpider 更新部署 ==="

# 1. 拉取最新代码
echo "→ 拉取最新代码..."
git pull --ff-only

# 2. 重建并重启容器（--build 失败不影响已运行容器）
echo "→ 重建镜像并重启容器..."
docker compose up -d --build

# 3. 健康检查
echo "→ 健康检查..."
for i in $(seq 1 30); do
    if curl -sf http://localhost:5050/ >/dev/null 2>&1; then
        echo ""
        echo "✓ 更新成功"
        SERVER_IP=$(hostname -I | awk '{print $1}')
        echo "访问: http://$SERVER_IP:5050"
        exit 0
    fi
    printf "."
    sleep 2
done

echo ""
echo "✗ 更新后服务未恢复"
echo "查看日志: docker compose logs"
exit 1
