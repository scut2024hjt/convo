package redis

import (
	_ "embed"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis"
)

//go:embed ratelimit.lua
var voteRateLimitLua string

var voteRateLimitScript = redis.NewScript(voteRateLimitLua)

// 同一毫秒内会有多个请求并发进入 Lua，用自增序号保证 ZSET member 唯一，
// 否则同分同成员的 ZADD 会把两次请求合并成一次，导致实际放行数偏多。
var rateLimitSequence uint64

// VoteRateLimitResult 描述一次限流判定的结果。
type VoteRateLimitResult struct {
	Allowed    bool
	Count      int64
	RetryAfter time.Duration
}

// AllowVoteRequest 用「精确滑动窗口」判定某个用户在当前窗口内是否还有投票额度。
//
// 之所以用 ZSET 记录每次请求的时间戳，而不是固定窗口计数：
// 固定窗口在两个窗口的交界处会放过接近 2 倍限额的突发流量，
// 滑动窗口只统计最近 window 内的请求，边界更严格。
func AllowVoteRequest(userID string, window time.Duration, limit int64) (*VoteRateLimitResult, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("vote rate limit must be positive, got %d", limit)
	}
	now := time.Now().UnixMilli()
	member := strconv.FormatInt(now, 10) + "-" +
		strconv.FormatUint(atomic.AddUint64(&rateLimitSequence, 1), 10)

	raw, err := voteRateLimitScript.Run(client,
		[]string{getRedisKey(KeyVoteRateLimitPrefix + userID)},
		now, window.Milliseconds(), limit, member,
	).Result()
	if err != nil {
		return nil, err
	}
	values, ok := raw.([]interface{})
	if !ok || len(values) < 3 {
		return nil, fmt.Errorf("unexpected vote rate limit script result: %#v", raw)
	}
	allowed, err := redisInt64(values[0])
	if err != nil {
		return nil, err
	}
	count, err := redisInt64(values[1])
	if err != nil {
		return nil, err
	}
	retryAfterMs, err := redisInt64(values[2])
	if err != nil {
		return nil, err
	}
	return &VoteRateLimitResult{
		Allowed:    allowed == 1,
		Count:      count,
		RetryAfter: time.Duration(retryAfterMs) * time.Millisecond,
	}, nil
}
