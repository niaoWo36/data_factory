#!/usr/bin/env bash
# data_factory 启动脚本 (macOS / Linux)
# 用法: ./start.sh [端口]   默认端口 8080
#       PORT=9090 ./start.sh

PORT=${1:-${PORT:-8080}}
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo ""
echo "╔══════════════════════════════════╗"
echo "║    data_factory  启动中          ║"
echo "╚══════════════════════════════════╝"
echo "  端口 : $PORT"
echo "  地址 : http://localhost:$PORT"
echo ""

# ── 检查并释放被占用的端口 ─────────────────────────────────────
check_and_kill_port() {
    local port=$1
    local pids=""

    if command -v lsof >/dev/null 2>&1; then
        pids=$(lsof -ti tcp:"$port" 2>/dev/null || true)
    elif command -v fuser >/dev/null 2>&1; then
        pids=$(fuser "${port}/tcp" 2>/dev/null | xargs -r || true)
    fi

    if [ -n "$pids" ]; then
        echo "  ⚠️  端口 $port 已被占用 (PID: $pids)，正在终止..."
        echo "$pids" | tr ' ' '\n' | while read -r p; do
            [ -n "$p" ] && kill -9 "$p" 2>/dev/null && echo "    已终止 PID $p" || true
        done
        sleep 1
        echo "  ✅  端口 $port 已释放"
        echo ""
    fi
}

check_and_kill_port "$PORT"

exec "$SCRIPT_DIR/data_factory" -port "$PORT"
