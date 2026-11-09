//go:build integration

package redis

import (
	"errors"
	"fmt"
	goredis "github.com/go-redis/redis"
	"github.com/scut2024hjt/convo/models"
	"strconv"
	"testing"
	"time"
)

func TestLikeReadsRejectIncompletePairAndPreserveSafeVersions(t *testing.T) {
	initIntegrationRedis(t, 8)
	post := time.Now().UnixNano()
	pid := strconv.FormatInt(post, 10)
	uid := "2001"
	if err := CreatePost(post, 1); err != nil {
		t.Fatal(err)
	}
	voted, version := getRedisKey(KeyPostVotedZSetPrefix+pid), getRedisKey(KeyPostVoteVersionPrefix+pid)
	for _, tc := range []struct {
		name  string
		liked bool
		raw   string
	}{{"liked missing version", true, ""}, {"liked zero version", true, "0"}, {"cancelled zero version", false, "0"}, {"unsafe version", false, "9007199254740992"}} {
		t.Run(tc.name, func(t *testing.T) {
			client.Del(voted, version)
			if tc.liked {
				client.ZAdd(voted, goredis.Z{Score: 1, Member: uid})
			}
			if tc.raw != "" {
				client.HSet(version, uid, tc.raw)
			}
			score, _ := client.ZScore(getRedisKey(KeyPostScoreZSet), pid).Result()
			n, _ := client.XLen(getRedisKey(KeyVoteOutboxStream)).Result()
			if _, err := ReadOwnLikeState(uid, pid); !errors.Is(err, ErrLikeVersionLimit) {
				t.Fatalf("incomplete pair accepted for read: %v", err)
			}
			if _, err := VoteForPost(uid, pid, 1, "incomplete", time.Now().Unix()); !errors.Is(err, ErrLikeVersionLimit) {
				t.Fatalf("incomplete pair accepted for write: %v", err)
			}
			after, _ := client.ZScore(getRedisKey(KeyPostScoreZSet), pid).Result()
			events, _ := client.XLen(getRedisKey(KeyVoteOutboxStream)).Result()
			if after != score || events != n {
				t.Fatal("rejected state mutated heat/outbox")
			}
		})
	}
	client.Del(voted, version)
	client.HSet(version, uid, models.MaxLikeVersion-1)
	result, err := VoteForPost(uid, pid, 1, "max-safe", time.Now().Unix())
	if err != nil || result.Version != models.MaxLikeVersion {
		t.Fatalf("max safe transition: %#v %v", result, err)
	}
	states, err := ReadOwnLikeStates(uid, []string{pid})
	if err != nil || states[0].Version != "9007199254740991" || !states[0].Liked {
		t.Fatalf("large version state=%#v err=%v", states, err)
	}
	if _, err = VoteForPost(uid, pid, 0, "overflow", time.Now().Unix()); !errors.Is(err, ErrLikeVersionLimit) {
		t.Fatalf("overflow accepted: %v", err)
	}
	if same, err := VoteForPost(uid, pid, 1, "same-max", time.Now().Unix()); err != nil || same.Changed {
		t.Fatalf("same at safe maximum: %#v %v", same, err)
	}
	client.ScriptFlush()
	if states, err = ReadOwnLikeStates(uid, []string{pid}); err != nil || len(states) != 1 {
		t.Fatalf("cold pipeline %#v %v", states, err)
	}
	events, err := client.XRange(getRedisKey(KeyVoteOutboxStream), "-", "+").Result()
	if err != nil || len(events) != 1 || fmt.Sprint(events[0].Values["version"]) != "9007199254740991" {
		t.Fatalf("event version %#v %v", events, err)
	}
}
