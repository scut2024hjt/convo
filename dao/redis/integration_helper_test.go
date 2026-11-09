//go:build integration
// +build integration

package redis

import (
	"os"
	"strconv"
	"testing"

	"github.com/scut2024hjt/convo/settings"
)

// integrationTestDB 把集成测试隔离到独立的 Redis DB。
//
// 如果测试和本地正在运行的实例共用 db 0，实例里的 relay 会以同一个消费组
// 读到测试写进 outbox stream 的事件，并把它 XACK + XDEL 掉，
// 于是「事件计数」断言会随机失败——这不是产品缺陷，而是测试没有做隔离。
const integrationTestDB = 15

// initIntegrationRedis 连接测试专用 DB，并清空上一次运行的残留数据。
func initIntegrationRedis(t *testing.T, poolSize int) {
	t.Helper()
	if os.Getenv("CONVO_T03_REDIS_INTEGRATION_OK") != "yes" {
		t.Skip("set CONVO_T03_REDIS_INTEGRATION_OK=yes only for an isolated Redis instance; FLUSHDB is destructive")
	}
	port, err := strconv.Atoi(os.Getenv("CONVO_T03_TEST_REDIS_PORT"))
	if err != nil || port <= 0 {
		t.Fatal("CONVO_T03_TEST_REDIS_PORT required")
	}
	dbIndex, err := strconv.Atoi(os.Getenv("CONVO_T03_TEST_REDIS_DB"))
	if err != nil || dbIndex <= 0 || dbIndex > 15 {
		t.Fatal("CONVO_T03_TEST_REDIS_DB must be 1..15 (isolated)")
	}
	if err := Init(&settings.RedisConfig{
		Host:     "127.0.0.1",
		Port:     port,
		DB:       dbIndex,
		PoolSize: poolSize,
	}); err != nil {
		t.Skipf("redis integration service is unavailable: %v", err)
	}
	if err := client.FlushDB().Err(); err != nil {
		t.Fatalf("flush integration db failed: %v", err)
	}
	if err := MarkVoteStateReady(); err != nil {
		t.Fatalf("initialize vote state marker failed: %v", err)
	}
	t.Cleanup(Close)
}
