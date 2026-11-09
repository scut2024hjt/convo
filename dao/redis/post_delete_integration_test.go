//go:build integration

package redis

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	goredis "github.com/go-redis/redis"
	"github.com/scut2024hjt/convo/models"
)

func TestT02RedisDeletionReplayAndVoteState(t *testing.T) {
	if os.Getenv("T02_ALLOW_INTEGRATION") != "YES" || os.Getenv("T02_TEST_REDIS_ADDR") == "" || os.Getenv("T02_TEST_REDIS_DB") != "15" {
		t.Fatal("requires T02_ALLOW_INTEGRATION=YES and an isolated T02_TEST_REDIS_ADDR, DB 15")
	}
	old := client
	client = goredis.NewClient(&goredis.Options{Addr: os.Getenv("T02_TEST_REDIS_ADDR"), DB: 15, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second})
	t.Cleanup(func() { client.Close(); client = old })
	if err := client.Ping().Err(); err != nil {
		t.Fatal(err)
	}
	id := time.Now().UnixNano()
	community := id + 10
	sid := strconv.FormatInt(id, 10)
	timeKey, hotKey := getRedisKey(KeyPostTimeZSet), getRedisKey(KeyPostScoreZSet)
	communityKey := getRedisKey(KeyCommentZSetPrefix + strconv.FormatInt(community, 10))
	votedKey, versionKey := getRedisKey(KeyPostVotedZSetPrefix+sid), getRedisKey(KeyPostVoteVersionPrefix+sid)
	deletedKey, cacheKey := getRedisKey(KeyPostDeletedSet), postCacheKey(id)
	t.Cleanup(func() {
		client.ZRem(timeKey, sid)
		client.ZRem(hotKey, sid)
		client.SRem(deletedKey, sid)
		client.Del(communityKey, votedKey, versionKey, cacheKey)
	})
	ctx := context.Background()
	if err := InitializePublishedPost(ctx, id, community, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := client.ZAdd(votedKey, goredis.Z{Score: 1, Member: "reader"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(versionKey, "reader", 3).Err(); err != nil {
		t.Fatal(err)
	}
	if err := SetPostDetailCache(id, &models.CachedPostDetail{Post: &models.Post{ID: id, Status: 1}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := GetPostDetailCache(id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := RemoveDeletedPostIndexes(ctx, id, community); err != nil {
			t.Fatal(err)
		}
	}
	if err := InitializePublishedPost(ctx, id, community, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := GetPostDetailCache(id); err != goredis.Nil {
		t.Fatalf("real detail cache survived deletion: %v", err)
	}
	for _, key := range []string{timeKey, hotKey} {
		if err := client.ZScore(key, sid).Err(); err != goredis.Nil {
			t.Fatalf("deleted index resurrected: %s %v", key, err)
		}
	}
	if exists, err := client.Exists(cacheKey).Result(); err != nil || exists != 0 {
		t.Fatalf("cached deleted body survived: %d %v", exists, err)
	}
	if member, err := client.SIsMember(communityKey, sid).Result(); err != nil || member {
		t.Fatalf("community reference survived: %t %v", member, err)
	}
	if version, err := client.HGet(versionKey, "reader").Int64(); err != nil || version != 3 {
		t.Fatalf("delete reset version: %d %v", version, err)
	}
	if direction, err := client.ZScore(votedKey, "reader").Result(); err != nil || direction != 1 {
		t.Fatalf("delete reset vote direction: %v %v", direction, err)
	}
	// A malformed dependency must fail before changing the tombstone/indexes.
	badID, badCommunity := id+1, community+1
	badSID := strconv.FormatInt(badID, 10)
	badKey := getRedisKey(KeyCommentZSetPrefix + strconv.FormatInt(badCommunity, 10))
	t.Cleanup(func() {
		client.ZRem(timeKey, badSID)
		client.ZRem(hotKey, badSID)
		client.SRem(deletedKey, badSID)
		client.Del(badKey)
	})
	if err := InitializePublishedPost(ctx, badID, badCommunity, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(badKey, "wrong type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDeletedPostIndexes(ctx, badID, badCommunity); err == nil {
		t.Fatal("wrong-type dependency was accepted")
	}
	if member, err := client.SIsMember(deletedKey, badSID).Result(); err != nil || member {
		t.Fatalf("failed delete wrote tombstone: %t %v", member, err)
	}
	if err := client.ZScore(timeKey, badSID).Err(); err != nil {
		t.Fatalf("failed delete removed index: %v", err)
	}
}
