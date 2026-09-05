#!/bin/bash
set -e

INSTALL_DIR="/opt/weibospider"
cd "$INSTALL_DIR"

echo "=== WeiboSpider 更新部署 ==="

# 拉取后重新执行仓库中的新版脚本，避免当前进程继续运行旧内容。
if [ "${1:-}" != "--after-pull" ]; then
    echo "→ 拉取最新代码..."
    git pull --ff-only
    exec "$0" --after-pull
fi

# 新平台服务令牌仅保存在服务器，不进入仓库或前端构建产物。
if [ ! -s .env.platform ]; then
    echo "→ 初始化平台服务配置..."
    umask 077
    printf 'BACKEND_API_TOKEN=%s\n' "$(openssl rand -hex 32)" > .env.platform
fi

# 1. 重建并重启容器（--build 失败不影响已运行容器）
echo "→ 重建镜像并重启容器..."
docker compose up -d --build

# 2. 健康检查
echo "→ 健康检查微博服务..."
for i in $(seq 1 30); do
    if curl -sf http://localhost:5050/ >/dev/null 2>&1; then
        echo ""
        echo "✓ 微博服务正常"
        break
    fi
    printf "."
    sleep 2
done

if ! curl -sf http://localhost:5050/ >/dev/null 2>&1; then
    echo ""
    echo "✗ 更新后微博服务未恢复"
    echo "查看日志: docker compose logs"
    exit 1
fi

echo "→ 健康检查白酒行情服务..."
for i in $(seq 1 30); do
    if curl -sf http://localhost:5052/healthz >/dev/null 2>&1 \
        && curl -sf http://localhost:5052/api/platform/liquor/latest >/dev/null 2>&1; then
        echo ""
        echo "✓ 更新成功"
        SERVER_IP=$(hostname -I | awk '{print $1}')
        echo "微博: http://$SERVER_IP:5050"
        echo "白酒行情: http://$SERVER_IP:5052/liquor"
        exit 0
    fi
    printf "."
    sleep 2
done

echo ""
echo "✗ 更新后白酒行情服务未恢复"
echo "查看日志: docker compose logs"
exit 1
