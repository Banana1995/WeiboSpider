#!/bin/bash
# Run on the operator's Mac. The one-use credential stays out of console output,
# shell history and HTTP URLs/logs (the fragment is consumed by the login page).
set -euo pipefail
name=${1:-gaomanyi}
action=${2:-setup}
[[ "$name" =~ ^[a-z0-9][a-z0-9_.-]{2,31}$ && ( "$action" == setup || "$action" == recover ) ]] || exit 2
origin=https://gaomanyi.site
if ! curl --noproxy '*' -fsS --connect-timeout 3 --max-time 6 "$origin/login" -o /dev/null 2>/dev/null; then
  # The cloud ingress may still be closed. Only this Mac's loopback can reach
  # this temporary forwarding port; the actual network hop is encrypted by SSH.
  port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
  ssh -fT -o BatchMode=yes -o ExitOnForwardFailure=yes -o ControlPath=none \
    -L "127.0.0.1:$port:127.0.0.1:5052" weibo 'sleep 1800'
  origin="http://127.0.0.1:$port"
  echo "公网 HTTPS 尚不可达，使用本机专属 SSH 加密通道设置密码（30 分钟）。" >&2
fi
ssh -o BatchMode=yes weibo "cd /opt/weibospider && docker compose exec -T platform /app/server $action $name" |
  python3 -c 'import sys, webbrowser
path = sys.stdin.read().strip()
if not path.startswith("/login#setup=") or "\n" in path: sys.exit("未能生成设置链接")
if not webbrowser.open(sys.argv[1] + path): sys.exit("无法打开默认浏览器")' "$origin"
