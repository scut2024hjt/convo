package redis

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis"
)

//go:embed ratelimit.lua
var voteRateLimitLua string

var voteRateLimitScript = redis.NewScript(voteRateLimitLua)

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
	// A process-local sequence is not unique across multiple application
	// instances. A random nonce keeps ZSET members distinct even when two
	// instances handle requests for the same user in the same millisecond.
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("generate vote rate-limit member: %w", err)
	}
	member := strconv.FormatInt(now, 10) + "-" + hex.EncodeToString(nonce[:])

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
