//go:build integration
// +build integration

package redis

import (
	"errors"
	"strconv"
	"testing"
	"time"

	redigo "github.com/go-redis/redis"
	"github.com/scut2024hjt/convo/models"
)

func TestVoteStateRebuild(t *testing.T) {
	initIntegrationRedis(t, 16)

	client.ZAdd(getRedisKey(KeyPostTimeZSet), redigo.Z{Score: 1, Member: "999"})
	client.ZAdd(getRedisKey(KeyPostVotedZSetPrefix+"999"), redigo.Z{Score: 1, Member: "888"})
	client.HSet(getRedisKey(KeyPostVoteVersionPrefix+"999"), "888", 7)
	client.SAdd(getRedisKey(KeyCommentZSetPrefix+"9"), "999")

	if err := PrepareVoteStateRebuild(); err != nil {
		t.Fatal(err)
	}
	status, err := GetVoteStateStatus()
	if err != nil || status != VoteStateRebuilding {
		t.Fatalf("status=%q err=%v", status, err)
	}
	if _, err = GetPostVoteCount(999); !errors.Is(err, ErrVoteStateUnavailable) {
		t.Fatalf("rebuilding state must not be served as zero votes: %v", err)
	}

	createdAt := time.Now().Unix()
	posts := []*models.PostRecoverySnapshot{
		{PostID: 101, CommunityID: 1, CreateUnix: createdAt, NetVote: 0},
	}
	votes := []*models.VoteRecoverySnapshot{
		{PostID: 101, UserID: 201, Direction: 1, Version: 1},
		{PostID: 101, UserID: 202, Direction: -1, Version: 3},
		{PostID: 101, UserID: 203, Direction: 0, Version: 4},
	}
	if err = RestoreVoteStateBatch(posts, votes); err != nil {
		t.Fatal(err)
	}
	if err = MarkVoteStateReady(); err != nil {
		t.Fatal(err)
	}

	postID := strconv.FormatInt(posts[0].PostID, 10)
	score, err := client.ZScore(getRedisKey(KeyPostScoreZSet), postID).Result()
	if err != nil || score != float64(createdAt) {
		t.Fatalf("score=%v want=%v err=%v", score, createdAt, err)
	}
	upVotes, err := GetPostVoteCount(posts[0].PostID)
	if err != nil || upVotes != 1 {
		t.Fatalf("upvotes=%d err=%v", upVotes, err)
	}
	if direction, err := client.ZScore(getRedisKey(KeyPostVotedZSetPrefix+postID), "202").Result(); err != nil || direction != -1 {
		t.Fatalf("direction=%v err=%v", direction, err)
	}
	if exists, err := client.ZScore(getRedisKey(KeyPostVotedZSetPrefix+postID), "203").Result(); err != Nil || exists != 0 {
		t.Fatalf("direction=0 must remain absent from vote zset: score=%v err=%v", exists, err)
	}
	if version, err := client.HGet(getRedisKey(KeyPostVoteVersionPrefix+postID), "203").Int64(); err != nil || version != 4 {
		t.Fatalf("cancel tombstone version=%d err=%v", version, err)
	}
	status, err = GetVoteStateStatus()
	if err != nil || status != VoteStateReady {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestVoteStateRebuildRejectsPendingOutbox(t *testing.T) {
	initIntegrationRedis(t, 4)
	if err := client.XAdd(&redigo.XAddArgs{
		Stream: getRedisKey(KeyVoteOutboxStream),
		Values: map[string]interface{}{"event_id": "pending"},
	}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := PrepareVoteStateRebuild(); !errors.Is(err, ErrVoteOutboxNotEmpty) {
		t.Fatalf("expected ErrVoteOutboxNotEmpty, got %v", err)
	}
}
