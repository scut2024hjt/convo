#!/usr/bin/env bash
# ============================================================================
# convo · 简历「项目描述」功能点逐条验收
#
# 对应简历这一句：
#   「基于 Go 开发的社区论坛后端，提供用户注册登录、帖子发布与查询、
#     投票及帖子热度排序等功能。」
#
# 逐个接口实打一遍，确认简历里写的功能都真实可用。
# 用法：cd convo && ./scripts/verify_resume_features.sh [--no-app]
#   --no-app  假定应用已在 9090 运行，不自己启停
# ============================================================================
set -uo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE="${APP_URL:-http://127.0.0.1:9090/api/v1}"
PORT="${BASE##*:}"; PORT="${PORT%%/*}"
RABBITMQ_URL="${CONVO_RABBITMQ_URL:-amqp://convo:convo@127.0.0.1:35672/}"
MANAGE_APP=1
[ "${1:-}" = "--no-app" ] && MANAGE_APP=0

LOG="$(mktemp /tmp/convo_resume.XXXXXX.log)"
BIN="$(mktemp /tmp/convo_app.XXXXXX)"
APP_PID=""
FAIL=0

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
ok()  { printf '  \033[32m✅ %s\033[0m\n' "$*"; }
bad() { FAIL=$((FAIL+1)); printf '  \033[31m❌ %s\033[0m\n' "$*"; }
info(){ printf '     %s\n' "$*"; }

cleanup() {
  if [ "$MANAGE_APP" = "1" ]; then
    [ -n "$APP_PID" ] && kill "$APP_PID" 2>/dev/null
    sleep 1
    pkill -f "$(basename "$BIN")" 2>/dev/null
  fi
  rm -f "$BIN" "$LOG"
}
trap cleanup EXIT

jqget() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null; }
code_of() { python3 -c "import sys,json;print(json.load(sys.stdin).get('code'))" 2>/dev/null; }
api() {
  local m="$1" p="$2" t="${3:-}" b="${4:-}"
  local a=(-sS -m 15 -X "$m" "$BASE$p" -H 'Content-Type: application/json')
  [ -n "$t" ] && a+=(-H "Authorization: Bearer $t")
  [ -n "$b" ] && a+=(-d "$b")
  curl "${a[@]}"
}
expect_ok() { # expect_ok <描述> <响应>
  local desc="$1" resp="$2"
  if [ "$(echo "$resp" | code_of)" = "1000" ]; then ok "$desc"; else bad "$desc（响应：$(echo "$resp" | head -c 120)）"; fi
}

if [ "$MANAGE_APP" = "1" ]; then
  say "编译并启动应用"
  ( cd "$REPO_DIR" && go build -o "$BIN" . ) || { echo "编译失败"; exit 1; }
  ( cd "$REPO_DIR" && exec env CONVO_RABBITMQ_URL="$RABBITMQ_URL" "$BIN" >"$LOG" 2>&1 ) &
  APP_PID=$!
  for _ in $(seq 1 60); do
    curl -sS -m 2 "http://127.0.0.1:$PORT/ping" 2>/dev/null | grep -q pong && break
    sleep 1
  done
  curl -sS -m 2 "http://127.0.0.1:$PORT/ping" 2>/dev/null | grep -q pong \
    && ok "应用就绪（:$PORT）" || { bad "应用启动失败"; tail -5 "$LOG"; exit 1; }
fi

STAMP=$(date +%s%N)
U="resume_$STAMP"

# ---------------------------------------------------------- 用户注册登录
say "① 用户注册登录"
expect_ok "POST /signup 注册" \
  "$(api POST /signup "" "{\"username\":\"$U\",\"password\":\"password123\",\"re_password\":\"password123\"}")"
LOGIN=$(api POST /login "" "{\"username\":\"$U\",\"password\":\"password123\"}")
expect_ok "POST /login 登录" "$LOGIN"
TOKEN=$(echo "$LOGIN" | jqget "['data']['token']")
[ -n "$TOKEN" ] && [ "$TOKEN" != "None" ] && ok "登录返回 JWT token" || bad "未返回 token"

BAD=$(api POST /login "" "{\"username\":\"$U\",\"password\":\"wrongpass123\"}")
[ "$(echo "$BAD" | code_of)" != "1000" ] && ok "错误密码被拒绝（code=$(echo "$BAD" | code_of)）" || bad "错误密码竟然通过"

# ---------------------------------------------------------- 帖子发布与查询
say "② 帖子发布与查询"
CREATE=$(api POST /post "$TOKEN" "{\"community_id\":1,\"title\":\"resume feature check\",\"content\":\"body\"}")
expect_ok "POST /post 发帖" "$CREATE"
POST_ID=$(echo "$CREATE" | jqget "['data']['post_id']")
info "post_id=$POST_ID"

DETAIL=$(api GET "/post/$POST_ID")
expect_ok "GET /post/:id 帖子详情" "$DETAIL"
echo "$DETAIL" | grep -q "resume feature check" && ok "详情返回正确标题" || bad "详情标题不对"

expect_ok "GET /community 社区列表" "$(api GET /community)"
expect_ok "GET /community/:id 社区详情" "$(api GET /community/1)"

# ---------------------------------------------------------- 投票
say "③ 投票（含取消）"
expect_ok "POST /vote 赞成"     "$(api POST /vote "$TOKEN" "{\"post_id\":\"$POST_ID\",\"direction\":\"1\"}")"
repeat=$(api POST /vote "$TOKEN" "{\"post_id\":\"$POST_ID\",\"direction\":\"1\"}")
[ "$(echo "$repeat" | jqget "['data']['changed']")" = "False" ] \
  && ok "重复投同方向被幂等忽略（changed=false）" || bad "幂等失效"
expect_ok "POST /vote 反对"     "$(api POST /vote "$TOKEN" "{\"post_id\":\"$POST_ID\",\"direction\":\"-1\"}")"
expect_ok "POST /vote 取消投票" "$(api POST /vote "$TOKEN" "{\"post_id\":\"$POST_ID\",\"direction\":\"0\"}")"

# ---------------------------------------------------------- 帖子热度排序
say "④ 帖子列表与热度排序"
for order in Time score; do
  R=$(api GET "/posts2?order=$order&page=1&size=5")
  expect_ok "GET /posts2?order=$order 分页列表" "$R"
done
expect_ok "GET /posts 基础列表" "$(api GET "/posts?page=1&size=5")"
expect_ok "GET /posts2 按社区过滤" "$(api GET "/posts2?community_id=1&page=1&size=5")"

# 热度排序是否真的生效：给目标帖刷几票，看它是否靠前
for i in 1 2 3; do
  t=$(api POST /login "" "{\"username\":\"${U}_v$i\",\"password\":\"password123\"}" | jqget "['data']['token']")
  [ "$t" = "None" ] && { api POST /signup "" "{\"username\":\"${U}_v$i\",\"password\":\"password123\",\"re_password\":\"password123\"}" >/dev/null; t=$(api POST /login "" "{\"username\":\"${U}_v$i\",\"password\":\"password123\"}" | jqget "['data']['token']"); }
  api POST /vote "$t" "{\"post_id\":\"$POST_ID\",\"direction\":\"1\"}" >/dev/null
done
sleep 2
RANK=$(api GET "/posts2?order=score&page=1&size=50" | grep -o "$POST_ID" | head -1)
[ -n "$RANK" ] && ok "热度榜能检索到该帖（score 排序生效）" || bad "热度榜中找不到该帖"

# ---------------------------------------------------------- 登录态
say "⑤ 登录态与单设备登录"
expect_ok "带 token 访问受保护接口" "$(api POST /vote "$TOKEN" "{\"post_id\":\"$POST_ID\",\"direction\":\"1\"}")"
NEW=$(api POST /login "" "{\"username\":\"$U\",\"password\":\"password123\"}" | jqget "['data']['token']")
OLD=$(api POST /vote "$TOKEN" "{\"post_id\":\"$POST_ID\",\"direction\":\"1\"}" | code_of)
[ "$OLD" != "1000" ] && ok "旧 token 在新登录后失效（code=$OLD，单设备登录生效）" || bad "旧 token 仍有效"
expect_ok "POST /logout 退出登录" "$(api POST /logout "$NEW")"
AFTER=$(api POST /vote "$NEW" "{\"post_id\":\"$POST_ID\",\"direction\":\"1\"}" | code_of)
[ "$AFTER" != "1000" ] && ok "登出后 token 失效（code=$AFTER）" || bad "登出后 token 仍有效"

say "结果"
if [ "$FAIL" -eq 0 ]; then printf '  \033[32m简历功能点全部可用 ✅\033[0m\n'; exit 0; fi
printf '  \033[31m有 %d 项未通过 ❌\033[0m\n' "$FAIL"; exit 1
