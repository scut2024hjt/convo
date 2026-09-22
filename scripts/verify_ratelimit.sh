#!/usr/bin/env bash
# ============================================================================
# convo · 限流验收（含多实例 member 冲突）
#
# 验证：
#   1. 同一用户并发打两个实例，总放行数不超过配置额度
#   2. 另一个用户不受影响（按用户维度隔离）
#   3. 窗口结束后额度恢复
#
# 用法：
#   cd convo && ./scripts/verify_ratelimit.sh
#
# 额度与窗口读自 conf/config.yaml（ratelimit.vote_max_requests /
# ratelimit.vote_window_milliseconds），可用 RL_LIMIT / RL_WINDOW_MS 覆盖。
# ============================================================================
set -uo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RABBITMQ_URL="${CONVO_RABBITMQ_URL:-amqp://convo:convo@127.0.0.1:35672/}"
PORT_A="${PORT_A:-9090}"
PORT_B="${PORT_B:-9091}"
URL_A="http://127.0.0.1:${PORT_A}/api/v1"
URL_B="http://127.0.0.1:${PORT_B}/api/v1"

LOG_A="$(mktemp /tmp/convo_rl_a.XXXXXX.log)"
LOG_B="$(mktemp /tmp/convo_rl_b.XXXXXX.log)"
BIN="$(mktemp /tmp/convo_app.XXXXXX)"
PID_A=""; PID_B=""

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
ok()  { printf '  \033[32m✅ %s\033[0m\n' "$*"; }
info(){ printf '     %s\n' "$*"; }

cleanup() {
  for p in "$PID_A" "$PID_B"; do
    [ -n "$p" ] && kill "$p" 2>/dev/null
  done
  sleep 1
  pkill -f "$(basename "$BIN")" 2>/dev/null
  rm -f "$BIN" "$LOG_A" "$LOG_B"
}
trap cleanup EXIT

serving() { curl -sS -m 2 "http://127.0.0.1:$1/ping" 2>/dev/null | grep -q pong; }

# 启动一个实例。注意两点：
#   1. 用 exec 让子 shell 被应用替换，$! 就是应用自身的 PID，便于精确回收
#   2. 绝不能用 $(...) 取 PID —— 后台进程会持有命令替换的管道导致永久阻塞
STARTED_PID=""
start_instance() { # start_instance <port> <machine_id> <logfile>
  local port="$1" machine="$2" log="$3"
  STARTED_PID=""
  if serving "$port"; then
    info "端口 $port 已有实例在跑，复用"
    return 0
  fi
  ( cd "$REPO_DIR" && exec env CONVO_RABBITMQ_URL="$RABBITMQ_URL" \
      CONVO_SNOWFLAKE_MACHINE_ID="$machine" CONVO_APP_PORT="$port" \
      "$BIN" >"$log" 2>&1 ) &
  STARTED_PID=$!
}

say "编译"
( cd "$REPO_DIR" && go build -o "$BIN" . ) && ok "convo_app 编译完成" || { echo "编译失败"; exit 1; }

say "启动两个实例"
start_instance "$PORT_A" 1 "$LOG_A"; PID_A="$STARTED_PID"
for _ in $(seq 1 60); do serving "$PORT_A" && break; sleep 1; done
serving "$PORT_A" && ok "实例 A 就绪（:$PORT_A）" || { echo "实例 A 启动失败"; tail -5 "$LOG_A"; exit 1; }

start_instance "$PORT_B" 2 "$LOG_B"; PID_B="$STARTED_PID"
for _ in $(seq 1 60); do serving "$PORT_B" && break; sleep 1; done
serving "$PORT_B" && ok "实例 B 就绪（:$PORT_B）" || { echo "实例 B 启动失败"; tail -5 "$LOG_B"; exit 1; }

say "限流验收"
( cd "$REPO_DIR/scripts/verify" && APP_A="$URL_A" APP_B="$URL_B" go run ./ratelimit )
RC=$?

say "结果"
[ "$RC" -eq 0 ] && printf '  \033[32m通过 ✅\033[0m\n' || printf '  \033[31m未通过 ❌\033[0m\n' >&2
exit "$RC"
