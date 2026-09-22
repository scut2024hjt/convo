//go:build integration
// +build integration

package redis

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVoteRateLimitSlidingWindow(t *testing.T) {
	initIntegrationRedis(t, 32)

	base := time.Now().UnixNano()
	user := strconv.FormatInt(base, 10)
	t.Cleanup(func() {
		client.Del(getRedisKey(KeyVoteRateLimitPrefix + user))
	})

	window := 500 * time.Millisecond
	const limit = 5

	for i := 0; i < limit; i++ {
		result, err := AllowVoteRequest(user, window, limit)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Allowed {
			t.Fatalf("request %d should be allowed, got %#v", i, result)
		}
		if result.Count != int64(i+1) {
			t.Fatalf("request %d: count=%d want=%d", i, result.Count, i+1)
		}
	}

	blocked, err := AllowVoteRequest(user, window, limit)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Allowed {
		t.Fatalf("request over the limit should be rejected, got %#v", blocked)
	}
	if blocked.RetryAfter <= 0 || blocked.RetryAfter > window {
		t.Fatalf("retry after %v is outside (0, %v]", blocked.RetryAfter, window)
	}

	// 另一个用户不受影响：限流必须按用户维度隔离。
	other := strconv.FormatInt(base+1, 10)
	t.Cleanup(func() { client.Del(getRedisKey(KeyVoteRateLimitPrefix + other)) })
	allowed, err := AllowVoteRequest(other, window, limit)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed.Allowed {
		t.Fatalf("a different user must not be limited, got %#v", allowed)
	}

	// 窗口滑过之后额度恢复。
	time.Sleep(window + 100*time.Millisecond)
	recovered, err := AllowVoteRequest(user, window, limit)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.Allowed || recovered.Count != 1 {
		t.Fatalf("window should have slid: %#v", recovered)
	}
}

// 并发打同一用户：Lua 脚本必须让「判重—计数—写入」整体原子，
// 放行数应恰好等于 limit，不能因为并发而超发。
func TestVoteRateLimitConcurrentNoOverIssue(t *testing.T) {
	initIntegrationRedis(t, 64)

	user := strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() { client.Del(getRedisKey(KeyVoteRateLimitPrefix + user)) })

	const (
		limit   = 50
		callers = 300
	)
	window := 10 * time.Second

	var allowedCount int64
	var group sync.WaitGroup
	for i := 0; i < callers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := AllowVoteRequest(user, window, limit)
			if err != nil {
				t.Errorf("rate limit call failed: %v", err)
				return
			}
			if result.Allowed {
				atomic.AddInt64(&allowedCount, 1)
			}
		}()
	}
	group.Wait()

	if allowedCount != limit {
		t.Fatalf("allowed=%d, want exactly %d", allowedCount, limit)
	}
	stored, err := client.ZCard(getRedisKey(KeyVoteRateLimitPrefix + user)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if stored != limit {
		t.Fatalf("zset size=%d, want %d", stored, limit)
	}
	ttl, err := client.PTTL(getRedisKey(KeyVoteRateLimitPrefix + user)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 {
		t.Fatalf("rate limit key must expire, got ttl=%v", ttl)
	}
}
