#!/usr/bin/env bash
# ============================================================================
# convo · Redis 灾难恢复完整验收链路
#
# 覆盖 README「可靠性边界」里关于离线重建的全部结论：
#   1. 建帖并投票（多用户 / 多帖子）
#   2. 等 Outbox 与 RabbitMQ 排空，确认 MySQL 已追平
#   3. 记录投票方向、版本、票数
#   4. FLUSHDB 模拟 Redis 派生状态全丢
#   5. 重启应用 -> 预期因索引缺失「拒绝启动」
#   6. go run ./cmd/rebuild-redis --confirm-maintenance
#   7. 再次启动应用
#   8. 验证：帖子列表恢复 / 详情票数与 MySQL 一致 / 投票方向恢复 /
#            version 恢复 / 老帖改票后 version 连续增长 / MySQL 再次收敛 /
#            DLQ 无版本冲突消息
#
# 最关键的断言是第 8 条里的「version 连续增长」：
# 如果重建只恢复票数、不恢复版本号，旧用户再投票时 Redis 会从 version=1
# 重来，被 MySQL 判为 stale 而静默丢弃，造成永久性单向不一致。
#
# 用法：
#   cd convo && ./scripts/verify_recovery.sh
#
# 依赖：MySQL / Redis / RabbitMQ 已启动（见 docker-compose.yaml），
#       且容器名与下面的 CONTAINER_* 变量一致。
# ============================================================================
set -uo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_URL="${APP_URL:-http://127.0.0.1:9090/api/v1}"
APP_PORT_FROM_URL="${APP_URL##*:}"; APP_PORT_FROM_URL="${APP_PORT_FROM_URL%%/*}"
RABBITMQ_URL="${CONVO_RABBITMQ_URL:-amqp://convo:convo@127.0.0.1:35672/}"

REDIS_CONTAINER="${REDIS_CONTAINER:-convo-redis507-1}"
MYSQL_CONTAINER="${MYSQL_CONTAINER:-convo-mysql8019-1}"
RABBIT_CONTAINER="${RABBIT_CONTAINER:-convo-rabbitmq-1}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:-123456}"

APP_LOG="$(mktemp /tmp/convo_recovery.XXXXXX.log)"
FAILURES=0

# ---------------------------------------------------------------- 工具函数
say()  { printf '\n\033[1m%s\033[0m\n' "$*"; }
ok()   { printf '  \033[32m✅ %s\033[0m\n' "$*"; }
bad()  { printf '  \033[31m❌ %s\033[0m\n' "$*"; FAILURES=$((FAILURES + 1)); }
info() { printf '     %s\n' "$*"; }

mysql_q() { docker exec "$MYSQL_CONTAINER" mysql -uroot -p"$MYSQL_PASSWORD" -N -e "USE convo; $1" 2>/dev/null; }
redis_c() { docker exec "$REDIS_CONTAINER" redis-cli "$@" 2>/dev/null; }
jqget()   { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1"; }

api() { # api METHOD PATH TOKEN [JSON]
  local method="$1" path="$2" token="${3:-}" body="${4:-}"
  local args=(-sS -m 15 -X "$method" "$APP_URL$path" -H 'Content-Type: application/json')
  [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
  [ -n "$body" ] && args+=(-d "$body")
  curl "${args[@]}"
}

start_app() { # 启动预编译好的二进制；返回后 APP_PID 可用
  ( cd "$REPO_DIR" && CONVO_RABBITMQ_URL="$RABBITMQ_URL" "$APP_BIN" >"$APP_LOG" 2>&1 ) &
  APP_PID=$!
  for _ in $(seq 1 60); do
    curl -sS -m 2 "http://127.0.0.1:${APP_PORT_FROM_URL}/ping" 2>/dev/null | grep -q pong && return 0
    sleep 1
  done
  return 1
}

stop_app() {
  if [ -n "${APP_PID:-}" ]; then
    kill "$APP_PID" 2>/dev/null
    pkill -P "$APP_PID" 2>/dev/null
    wait "$APP_PID" 2>/dev/null
  fi
  for _ in $(seq 1 20); do
    curl -sS -m 1 "http://127.0.0.1:${APP_PORT_FROM_URL}/ping" 2>/dev/null | grep -q pong || return 0
    sleep 1
  done
  pkill -f "$(basename "$APP_BIN")" 2>/dev/null
  sleep 2
  return 0
}

wait_drained() { # 等 Outbox 与 MQ 队列排空
  for _ in $(seq 1 30); do
    local xlen q
    xlen=$(redis_c XLEN convo:outbox:vote); xlen=${xlen:-1}
    q=$(docker exec "$RABBIT_CONTAINER" rabbitmqctl list_queues name messages 2>/dev/null \
        | awk '/convo\.vote/{s+=$2} END{print s+0}')
    [ "$xlen" = "0" ] && [ "$q" = "0" ] && return 0
    sleep 1
  done
  return 1
}

# ---------------------------------------------------------------- 预编译
say "预编译（避免反复 go run）"
APP_BIN="$(mktemp /tmp/convo_app.XXXXXX)"
REBUILD_BIN="$(mktemp /tmp/convo_rebuild.XXXXXX)"
trap 'rm -f "$APP_BIN" "$REBUILD_BIN"' EXIT
( cd "$REPO_DIR" && go build -o "$APP_BIN" . && go build -o "$REBUILD_BIN" ./cmd/rebuild-redis ) \
  && ok "convo_app / convo_rebuild_redis 编译完成" || { bad "编译失败"; exit 1; }

# ---------------------------------------------------------------- 前置检查
say "前置检查"
for c in "$REDIS_CONTAINER" "$MYSQL_CONTAINER" "$RABBIT_CONTAINER"; do
  docker inspect -f '{{.State.Running}}' "$c" 2>/dev/null | grep -q true \
    && ok "容器 $c 运行中" || { bad "容器 $c 未运行"; exit 1; }
done

# ---------------------------------------------------------------- 0. 确保应用在跑
say "0. 确保应用在运行"
if api GET /ping >/dev/null 2>&1 && curl -sS -m 3 "http://127.0.0.1:${APP_PORT_FROM_URL}/ping" 2>/dev/null | grep -q pong; then
  ok "应用已在运行，复用现有实例"
  APP_PID=""
else
  info "应用未运行，由本脚本启动"
  if start_app; then ok "应用启动成功"; else bad "应用启动失败"; tail -5 "$APP_LOG"; exit 1; fi
fi

# ---------------------------------------------------------------- 1. 建帖并投票
say "1. 建帖并投票（多用户）"
STAMP=$(date +%s%N)
USERS=()
snapshot_dir="$(mktemp -d)"
for i in 0 1 2 3 4; do
  u="rec_${STAMP}_$i"
  api POST /signup "" "{\"username\":\"$u\",\"password\":\"password123\",\"re_password\":\"password123\"}" >/dev/null
  USERS+=("$u")
done
TOKEN0=$(api POST /login "" "{\"username\":\"${USERS[0]}\",\"password\":\"password123\"}" | jqget "['data']['token']")
POST_ID=$(api POST /post "$TOKEN0" "{\"community_id\":1,\"title\":\"recovery $(date +%s)\",\"content\":\"recovery\"}" | jqget "['data']['post_id']")
[ -n "$POST_ID" ] && [ "$POST_ID" != "None" ] && ok "建帖 post_id=$POST_ID" || { bad "建帖失败"; exit 1; }

# 每个用户在同一帖上做出不同方向的投票，把 version 推到 2
for i in 1 2 3 4; do
  t=$(api POST /login "" "{\"username\":\"${USERS[$i]}\",\"password\":\"password123\"}" | jqget "['data']['token']")
  api POST /vote "$t" "{\"post_id\":\"$POST_ID\",\"direction\":\"1\"}" >/dev/null
  echo "$t" > "$snapshot_dir/token_$i"
done
# 用户0 反复改票，制造较高的 version
for d in 1 1 -1 1; do api POST /vote "$TOKEN0" "{\"post_id\":\"$POST_ID\",\"direction\":\"$d\"}" >/dev/null; done
echo "$TOKEN0" > "$snapshot_dir/token_0"
ok "5 个用户投票完成（用户0 反复改票）"

# ---------------------------------------------------------------- 2. 排空
say "2. 等 Outbox 与 RabbitMQ 排空"
if wait_drained; then ok "Outbox 与 MQ 队列已排空"; else bad "排空超时"; fi

POST_COUNT=$(mysql_q "SELECT COUNT(*) FROM post WHERE status=1;")
VOTE_ROWS=$(mysql_q "SELECT COUNT(*) FROM post_vote;")
ok "MySQL 已追平：帖子 $POST_COUNT 条，投票状态 $VOTE_ROWS 行"

# ---------------------------------------------------------------- 3. 记录基线
say "3. 记录投票方向、版本、票数"
AUTHOR_ID=$(mysql_q "SELECT user_id FROM post_vote WHERE post_id=$POST_ID ORDER BY version DESC LIMIT 1;")
BASE_DIR=$(mysql_q "SELECT direction FROM post_vote WHERE post_id=$POST_ID AND user_id=$AUTHOR_ID;")
BASE_VER=$(mysql_q "SELECT version FROM post_vote WHERE post_id=$POST_ID AND user_id=$AUTHOR_ID;")
BASE_UP=$(mysql_q "SELECT COUNT(*) FROM post_vote WHERE post_id=$POST_ID AND direction=1;")
BASE_UP="${BASE_UP:-0}"
REDIS_UP_BEFORE=$(redis_c ZCOUNT "convo:post:voted:$POST_ID" 1 1)
info "样本用户 user_id=$AUTHOR_ID  direction=$BASE_DIR  version=$BASE_VER"
info "赞成票：MySQL=$BASE_UP  Redis=$REDIS_UP_BEFORE"
[ "$BASE_UP" = "$REDIS_UP_BEFORE" ] && ok "丢数据前 Redis 与 MySQL 一致" || bad "丢数据前就不一致"

# ---------------------------------------------------------------- 4. FLUSHDB
say "4. 停写 + FLUSHDB（模拟 Redis 派生状态全丢）"
stop_app; ok "应用已停止"
redis_c FLUSHDB >/dev/null
info "Redis 键数 = $(redis_c DBSIZE)，post:time = $(redis_c ZCARD convo:post:time)"

# ---------------------------------------------------------------- 5. 拒绝启动
say "5. 重启应用 -> 预期「拒绝启动」且退出码非 0"
( cd "$REPO_DIR" && timeout 60 env CONVO_RABBITMQ_URL="$RABBITMQ_URL" "$APP_BIN" >"$APP_LOG.refuse" 2>&1 )
REFUSE_CODE=$?
if grep -q "needs an offline rebuild" "$APP_LOG.refuse"; then
  ok "应用拒绝启动，并给出了修复命令"
  info "$(grep -o 'redis post/vote state needs an offline rebuild.*' "$APP_LOG.refuse" | head -1 | cut -c1-140)"
else
  bad "退出码 $REFUSE_CODE，但日志里没有状态检查报错"
  tail -3 "$APP_LOG.refuse"
fi
if [ "$REFUSE_CODE" -ne 0 ]; then
  ok "退出码 = $REFUSE_CODE（非 0，编排与监控可感知）"
else
  bad "退出码为 0 —— 启动校验失败必须以非 0 退出，否则会被当成正常结束"
fi

# ---------------------------------------------------------------- 6. 重建
say "6. 离线重建"
REBUILD_OUT=$(cd "$REPO_DIR" && CONVO_RABBITMQ_URL="$RABBITMQ_URL" "$REBUILD_BIN" --confirm-maintenance 2>&1)
echo "$REBUILD_OUT" | tail -1 | sed 's/^/     /'
echo "$REBUILD_OUT" | grep -q "redis rebuild completed" && ok "重建命令成功" || bad "重建失败"

# ---------------------------------------------------------------- 7. 再次启动
say "7. 再次启动应用"
if start_app; then ok "应用启动成功"; else bad "应用启动失败"; tail -5 "$APP_LOG"; fi

# ---------------------------------------------------------------- 8. 验证
say "8. 验证恢复结果"

# 8.1 帖子列表恢复
LIST_N=$(api GET "/posts2?order=score&page=1&size=20" | jqget "['data']" 2>/dev/null | grep -c "id" || echo 0)
LIST_CODE=$(api GET "/posts2?order=score&page=1&size=20" | jqget "['code']")
if [ "$LIST_CODE" = "1000" ]; then
  ok "帖子列表恢复（code=1000，含目标帖：$(api GET "/posts2?order=score&page=1&size=50" | grep -c "$POST_ID" || echo 0) 处匹配）"
else
  bad "帖子列表未恢复（code=$LIST_CODE）"
fi

# 8.2 详情票数与 MySQL 一致
DETAIL_UP=$(api GET "/post/$POST_ID" | jqget "['data']['vote_num']")
[ "$DETAIL_UP" = "$BASE_UP" ] && ok "详情票数 $DETAIL_UP 与 MySQL 一致" || bad "详情票数 $DETAIL_UP != MySQL $BASE_UP"

# 8.3 投票方向恢复
REDIS_DIR=$(redis_c ZSCORE "convo:post:voted:$POST_ID" "$AUTHOR_ID")
[ "$REDIS_DIR" = "$BASE_DIR" ] && ok "投票方向恢复（$REDIS_DIR）" || bad "方向 $REDIS_DIR != $BASE_DIR"

# 8.4 version 恢复
REDIS_VER=$(redis_c HGET "convo:post:vote:version:$POST_ID" "$AUTHOR_ID")
[ "$REDIS_VER" = "$BASE_VER" ] && ok "version 恢复（$REDIS_VER）" || bad "version $REDIS_VER != $BASE_VER"

# 8.5 老帖改票后 version 连续增长  ← 最关键
say "8.5 【关键】老用户对老帖改票，version 必须连续增长而不是从 1 重来"
TOKEN0_NEW=$(api POST /login "" "{\"username\":\"${USERS[0]}\",\"password\":\"password123\"}" | jqget "['data']['token']")
NEW_DIR=$([ "$BASE_DIR" = "1" ] && echo -1 || echo 1)
VOTE_RESP=$(api POST /vote "$TOKEN0_NEW" "{\"post_id\":\"$POST_ID\",\"direction\":\"$NEW_DIR\"}")
VOTE_VER=$(echo "$VOTE_RESP" | jqget "['data']['version']")
if [ "$VOTE_VER" = "$((BASE_VER + 1))" ]; then
  ok "version $BASE_VER -> $VOTE_VER（连续增长）"
else
  bad "version 应为 $((BASE_VER + 1))，实际 $VOTE_VER —— 若为 1 说明 tombstone 丢失"
fi

# 8.6 MySQL 再次收敛
wait_drained >/dev/null
FINAL_DIR=$(mysql_q "SELECT direction FROM post_vote WHERE post_id=$POST_ID AND user_id=$AUTHOR_ID;")
FINAL_VER=$(mysql_q "SELECT version FROM post_vote WHERE post_id=$POST_ID AND user_id=$AUTHOR_ID;")
[ "$FINAL_VER" = "$((BASE_VER + 1))" ] && [ "$FINAL_DIR" = "$NEW_DIR" ] \
  && ok "MySQL 收敛：direction=$FINAL_DIR version=$FINAL_VER" \
  || bad "MySQL 未收敛：direction=$FINAL_DIR version=$FINAL_VER"

FINAL_MYSQL_UP=$(mysql_q "SELECT COUNT(*) FROM post_vote WHERE post_id=$POST_ID AND direction=1;")
FINAL_REDIS_UP=$(redis_c ZCOUNT "convo:post:voted:$POST_ID" 1 1)
[ "${FINAL_MYSQL_UP:-0}" = "${FINAL_REDIS_UP:-x}" ] \
  && ok "Redis 与 MySQL 赞成票再次一致（${FINAL_REDIS_UP}）" \
  || bad "Redis ${FINAL_REDIS_UP} != MySQL ${FINAL_MYSQL_UP}"

# 8.7 DLQ 无冲突消息
DLQ=$(docker exec "$RABBIT_CONTAINER" rabbitmqctl list_queues name messages 2>/dev/null \
      | awk '/dlq/{print $2}')
[ "${DLQ:-1}" = "0" ] && ok "死信队列为空（无版本冲突消息）" || bad "DLQ 有 $DLQ 条消息"

# ---------------------------------------------------------------- 收尾
stop_app
rm -rf "$snapshot_dir"

say "验收结果"
if [ "$FAILURES" -eq 0 ]; then
  printf '  \033[32m全部通过 ✅\033[0m\n'
  exit 0
else
  printf '  \033[31m有 %d 项未通过 ❌\033[0m\n' "$FAILURES"
  exit 1
fi
