#!/bin/bash
# 微博管理器启动脚本
cd "$(dirname "$0")"
LOGFILE="./server.log"
echo "$(date) 启动服务..." >> "$LOGFILE"
nohup python3 run.py "$@" >> "$LOGFILE" 2>&1 &
PID=$!
echo "PID: $PID, 日志: $LOGFILE"
echo "http://localhost:5050"
# 写入 PID 文件方便停止
echo $PID > ./server.pid
