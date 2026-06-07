#!/bin/bash
# 停止微博管理器
cd "$(dirname "$0")"
if [ -f server.pid ]; then
    kill $(cat server.pid) 2>/dev/null && echo "已停止" || echo "进程已不存在"
    rm -f server.pid
else
    kill $(lsof -t -i :5000) 2>/dev/null && echo "已停止" || echo "没有运行中的服务"
fi
