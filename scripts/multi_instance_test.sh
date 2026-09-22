#!/usr/bin/env bash
# 多实例验证：
#  1. 两个实例生成的 用户ID / 帖子ID 不冲突（Snowflake machine_id 隔离）
#  2. 实例 A 发的帖，在实例 B 上投票，计数与落库正确（共享 Redis/MySQL/MQ）
set -uo pipefail
A="http://127.0.0.1:9090/api/v1"
B="http://127.0.0.1:9091/api/v1"
STAMP=$(date +%s%N)
MYSQL="docker exec convo-mysql8019-1 mysql -uroot -p123456 -N -e"
REDIS="docker exec convo-redis507-1 redis-cli"

jqget() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1"; }

signup_login() { # $1 base, $2 username
  curl -sS -X POST "$1/signup" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$2\",\"password\":\"password123\",\"re_password\":\"password123\"}" >/dev/null
  curl -sS -X POST "$1/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$2\",\"password\":\"password123\"}" | jqget "['data']['token']"
}

echo "=== 1. 两个实例各建 5 个用户，检查 ID 是否冲突 ==="
IDS=""
for i in 1 2 3 4 5; do
  T=$(signup_login "$A" "mi_${STAMP}_a$i")
  UID_A=$(curl -sS -X POST "$A/login" -H 'Content-Type: application/json' -d "{\"username\":\"mi_${STAMP}_a$i\",\"password\":\"password123\"}" | jqget "['data']['user_id']")
  T2=$(signup_login "$B" "mi_${STAMP}_b$i")
  UID_B=$(curl -sS -X POST "$B/login" -H 'Content-Type: application/json' -d "{\"username\":\"mi_${STAMP}_b$i\",\"password\":\"password123\"}" | jqget "['data']['user_id']")
  IDS="$IDS$UID_A\n$UID_B\n"
done
TOTAL=$(printf "$IDS" | grep -c .)
UNIQ=$(printf "$IDS" | sort -u | grep -c .)
echo "生成的 user_id 总数=$TOTAL 去重后=$UNIQ  -> $([ "$TOTAL" = "$UNIQ" ] && echo '无冲突 OK' || echo '存在冲突 FAIL')"
printf "$IDS" | sort -u | head -3 | while read -r id; do echo "  样例 id=$id (二进制高位=${id:0:6})"; done

echo
echo "=== 2. 实例 A 发帖，实例 B 投票 ==="
TOKEN_A=$(signup_login "$A" "mi_${STAMP}_author")
POST=$(curl -sS -X POST "$A/post" -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN_A" \
  -d '{"community_id":1,"title":"multi instance","content":"cross instance"}' | jqget "['data']['post_id']")
echo "post_id=$POST (由实例 A 生成)"

for i in 1 2 3 4; do
  TB=$(signup_login "$B" "mi_${STAMP}_v$i")
  CODE=$(curl -sS -X POST "$B/vote" -H 'Content-Type: application/json' -H "Authorization: Bearer $TB" \
    -d "{\"post_id\":\"$POST\",\"direction\":\"1\"}" | jqget "['code']")
  printf "  实例 B 投票 code=%s\n" "$CODE"
done
sleep 4
echo "Redis 计数 = $($REDIS ZCOUNT "convo:post:voted:$POST" 1 1)  (期望 4)"
echo "MySQL 计数 = $($MYSQL "USE convo; SELECT COUNT(*) FROM post_vote WHERE post_id=$POST AND direction=1;" 2>/dev/null)  (期望 4)"

echo
echo "=== 3. 跨实例读到一致的帖子详情 ==="
echo "实例 A 读: $(curl -sS "$A/post/$POST" | jqget "['data']['vote_num']")"
echo "实例 B 读: $(curl -sS "$B/post/$POST" | jqget "['data']['vote_num']")"
