#!/usr/bin/env bash
# P0-2/P0-3 故障场景验证：
#  1. MQ 宕机时投票接口是否仍然可用（写链路解耦）
#  2. 事件是否留在 Redis Stream 中不丢
#  3. MQ 恢复后是否自动补偿落库，且 Redis 与 MySQL 最终一致
set -uo pipefail

BASE="http://127.0.0.1:9090/api/v1"
STAMP=$(date +%s%N)
MYSQL="docker exec convo-mysql8019-1 mysql -uroot -p123456 -N -e"
REDIS="docker exec convo-redis507-1 redis-cli"

jqget() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1"; }

signup_login() { # $1 = username -> prints token
  curl -sS -X POST "$BASE/signup" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"password123\",\"re_password\":\"password123\"}" >/dev/null
  curl -sS -X POST "$BASE/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"password123\"}" | jqget "['data']['token']"
}

vote() { # $1 token, $2 post, $3 dir
  curl -sS -X POST "$BASE/vote" -H 'Content-Type: application/json' -H "Authorization: Bearer $1" \
    -d "{\"post_id\":\"$2\",\"direction\":\"$3\"}"
}

TOKEN=$(signup_login "fault_$STAMP")
POST=$(curl -sS -X POST "$BASE/post" -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" \
  -d '{"community_id":1,"title":"fault post","content":"fault content"}' | jqget "['data']['post_id']")
echo "post_id=$POST"

echo
echo "--- 阶段1：MQ 正常，先投 3 票 ---"
for i in 1 2 3; do
  T=$(signup_login "fault_${STAMP}_a$i")
  vote "$T" "$POST" 1 >/dev/null
done
sleep 3
echo "Redis 计数 = $($REDIS ZCOUNT "convo:post:voted:$POST" 1 1)"
echo "MySQL 计数 = $($MYSQL "USE convo; SELECT COUNT(*) FROM post_vote WHERE post_id=$POST AND direction=1;" 2>/dev/null)"

echo
echo "--- 阶段2：停掉 RabbitMQ，再投 5 票 ---"
docker stop convo-rabbitmq-1 >/dev/null
echo "rabbitmq stopped"
for i in 4 5 6 7 8; do
  T=$(signup_login "fault_${STAMP}_b$i")
  START=$(date +%s%N)
  RESP=$(vote "$T" "$POST" 1)
  END=$(date +%s%N)
  CODE=$(echo "$RESP" | jqget "['code']")
  printf "  投票 code=%s 延迟=%sms\n" "$CODE" "$(( (END-START)/1000000 ))"
done
sleep 3
echo "Redis 计数      = $($REDIS ZCOUNT "convo:post:voted:$POST" 1 1)   <- 期望 8"
echo "MySQL 计数      = $($MYSQL "USE convo; SELECT COUNT(*) FROM post_vote WHERE post_id=$POST AND direction=1;" 2>/dev/null)   <- 期望 3（MQ 已停）"
echo "Redis Stream 积压 = $($REDIS XLEN "convo:outbox:vote")   <- 期望 >0（事件未丢，等 MQ 恢复）"
echo "Stream pending   = $($REDIS XPENDING "convo:outbox:vote" vote-relay 2>/dev/null | head -1)"

echo
echo "--- 阶段3：恢复 RabbitMQ，等待自动补偿 ---"
docker start convo-rabbitmq-1 >/dev/null
for i in $(seq 1 24); do
  sleep 5
  M=$(docker exec convo-mysql8019-1 mysql -uroot -p123456 -N -e "USE convo; SELECT COUNT(*) FROM post_vote WHERE post_id=$POST AND direction=1;" 2>/dev/null)
  S=$(docker exec convo-redis507-1 redis-cli XLEN "convo:outbox:vote")
  printf "  t=%02ds MySQL=%s Stream=%s\n" "$((i*5))" "$M" "$S"
  if [ "$M" = "8" ] && [ "$S" = "0" ]; then echo "  ==> 已收敛"; break; fi
done

echo
echo "--- 最终状态 ---"
echo "Redis 计数 = $($REDIS ZCOUNT "convo:post:voted:$POST" 1 1)"
echo "MySQL 计数 = $($MYSQL "USE convo; SELECT COUNT(*) FROM post_vote WHERE post_id=$POST AND direction=1;" 2>/dev/null)"
echo "DLQ 消息数 = $($MYSQL "USE convo; SELECT COUNT(*) FROM mq_consumed_message;" 2>/dev/null)"
