//go:build integration

package redis

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	goredis "github.com/go-redis/redis"
)

func TestT01RedisIndexReplayDoesNotResetVotes(t *testing.T) {
	if os.Getenv("T01_ALLOW_INTEGRATION") != "YES" || os.Getenv("T01_TEST_REDIS_ADDR") == "" || os.Getenv("T01_TEST_REDIS_DB") != "15" {
		t.Fatal("requires isolated Redis DB 15, T01_TEST_REDIS_ADDR and T01_ALLOW_INTEGRATION=YES")
	}
	addr := os.Getenv("T01_TEST_REDIS_ADDR")
	if !strings.Contains(addr, ":") {
		t.Fatal("invalid Redis host:port")
	}
	original := client
	client = goredis.NewClient(&goredis.Options{Addr: addr, DB: 15, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second})
	defer func() { client.Close(); client = original }()
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	postID := time.Now().UnixNano()
	id := strconv.FormatInt(postID, 10)
	community := int64(998)
	timeKey := getRedisKey(KeyPostTimeZSet)
	scoreKey := getRedisKey(KeyPostScoreZSet)
	communityKey := getRedisKey(KeyCommentZSetPrefix + strconv.FormatInt(community, 10))
	votedKey := getRedisKey(KeyPostVotedZSetPrefix + id)
	versionKey := getRedisKey(KeyPostVoteVersionPrefix + id)
	defer client.ZRem(timeKey, id)
	defer client.ZRem(scoreKey, id)
	defer client.SRem(communityKey, id)
	defer client.Del(votedKey, versionKey)
	publishedAt := time.Unix(1780000000, 0).UTC()
	if err := InitializePublishedPost(context.Background(), postID, community, publishedAt); err != nil {
		t.Fatal(err)
	}
	client.ZIncrBy(scoreKey, 432, id)
	client.ZAdd(votedKey, goredis.Z{Score: 1, Member: "9007199254740993"})
	client.HSet(versionKey, "9007199254740993", 3)
	if err := InitializePublishedPost(context.Background(), postID, community, publishedAt); err != nil {
		t.Fatal(err)
	}
	score, err := client.ZScore(scoreKey, id).Result()
	if err != nil || score != float64(publishedAt.Unix()+432) {
		t.Fatalf("replay reset score: %v %v", score, err)
	}
	timestamp, err := client.ZScore(timeKey, id).Result()
	if err != nil || timestamp != float64(publishedAt.Unix()) {
		t.Fatalf("replay reset original time: %v %v", timestamp, err)
	}
	direction, err := client.ZScore(votedKey, "9007199254740993").Result()
	if err != nil || direction != 1 {
		t.Fatalf("replay altered vote direction: %v %v", direction, err)
	}
	version, err := client.HGet(versionKey, "9007199254740993").Int64()
	if err != nil || version != 3 {
		t.Fatalf("replay altered vote version: %v %v", version, err)
	}
	badID := postID + 1
	badKey := getRedisKey(KeyPostVoteVersionPrefix + strconv.FormatInt(badID, 10))
	if err = client.HSet(badKey, "u", 1).Err(); err != nil {
		t.Fatal(err)
	}
	defer client.Del(badKey)
	if err = InitializePublishedPost(context.Background(), badID, community, publishedAt); !errors.Is(err, ErrPostIndexCorrupt) {
		t.Fatalf("must not repair orphaned vote state: %v", err)
	}
	strayID := postID + 2
	stray := strconv.FormatInt(strayID, 10)
	if err = client.SAdd(communityKey, stray).Err(); err != nil {
		t.Fatal(err)
	}
	defer client.SRem(communityKey, stray)
	if err = InitializePublishedPost(context.Background(), strayID, community, publishedAt); !errors.Is(err, ErrPostIndexCorrupt) {
		t.Fatalf("dangling community membership must not be reset: %v", err)
	}
	canceledID := postID + 3
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err = InitializePublishedPost(canceled, canceledID, community, publishedAt); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation before IO: %v", err)
	}
	if err = client.ZScore(timeKey, strconv.FormatInt(canceledID, 10)).Err(); err != goredis.Nil {
		t.Fatalf("canceled projection created an index: %v", err)
	}
	if err = client.Do("CLIENT", "PAUSE", 500, "ALL").Err(); err != nil {
		t.Fatal(err)
	}
	deadline, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	started := time.Now()
	err = InitializePublishedPost(deadline, canceledID, community, publishedAt)
	elapsed := time.Since(started)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) || elapsed > 250*time.Millisecond {
		t.Fatalf("projection ignored its IO deadline: elapsed=%v error=%v", elapsed, err)
	}
	if err = client.ZScore(timeKey, strconv.FormatInt(canceledID, 10)).Err(); err != goredis.Nil {
		t.Fatalf("timed-out connection initialization wrote an index: %v", err)
	}

}
