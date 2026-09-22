//go:build integration
// +build integration

package redis

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/go-redis/redis"
)

func TestVoteScriptTransitionsAndConcurrency(t *testing.T) {
	initIntegrationRedis(t, 32)

	base := time.Now().UnixNano()
	posts := []int64{base, base + 1, base + 2, base + 3}
	for _, postID := range posts[:3] {
		if err := CreatePost(postID, 1); err != nil {
			t.Fatal(err)
		}
	}
	// The fourth post is initialized as expired.
	client.ZAdd(getRedisKey(KeyPostTimeZSet), goredis.Z{
		Score: float64(time.Now().Unix() - oneWeekInSeconds - 1), Member: posts[3],
	})
	client.ZAdd(getRedisKey(KeyPostScoreZSet), goredis.Z{Score: 0, Member: posts[3]})
	defer cleanupVoteTestData(posts)

	post := strconv.FormatInt(posts[0], 10)
	user := strconv.FormatInt(base+1000, 10)
	directions := []int8{1, 1, -1, -1, 0, 0, -1, 1, 0}
	wantChanged := []bool{true, false, true, false, true, false, true, true, true}
	wantVersions := []int64{1, 1, 2, 2, 3, 3, 4, 5, 6}
	for index, direction := range directions {
		result, err := VoteForPost(
			user, post, direction, fmt.Sprintf("%d-transition-%d", base, index), time.Now().Unix(),
		)
		if err != nil {
			t.Fatalf("transition %d failed: %v", index, err)
		}
		if result.Changed != wantChanged[index] || result.Version != wantVersions[index] {
			t.Fatalf("transition %d: result=%#v", index, result)
		}
	}

	if _, err := VoteForPost(user, post, 2, "invalid", time.Now().Unix()); !errors.Is(err, ErrInvalidDirection) {
		t.Fatalf("expected invalid direction, got %v", err)
	}
	if _, err := VoteForPost(user, strconv.FormatInt(base+9999, 10), 1, "missing", time.Now().Unix()); !errors.Is(err, ErrPostNotInitialized) {
		t.Fatalf("expected missing post, got %v", err)
	}
	if _, err := VoteForPost(user, strconv.FormatInt(posts[3], 10), 1, "expired", time.Now().Unix()); !errors.Is(err, ErrVoteTimeExpired) {
		t.Fatalf("expected expired vote, got %v", err)
	}

	var sameUserChanges int64
	runConcurrentVotes(t, 100, func(index int) error {
		result, err := VoteForPost(
			strconv.FormatInt(base+2000, 10), strconv.FormatInt(posts[1], 10), 1,
			fmt.Sprintf("%d-same-%d", base, index), time.Now().Unix(),
		)
		if err == nil && result.Changed {
			atomic.AddInt64(&sameUserChanges, 1)
		}
		return err
	})
	if sameUserChanges != 1 {
		t.Fatalf("same user produced %d state changes, expected 1", sameUserChanges)
	}

	scoreKey := getRedisKey(KeyPostScoreZSet)
	initialScore, err := client.ZScore(scoreKey, strconv.FormatInt(posts[2], 10)).Result()
	if err != nil {
		t.Fatal(err)
	}
	runConcurrentVotes(t, 100, func(index int) error {
		_, err := VoteForPost(
			strconv.FormatInt(base+3000+int64(index), 10), strconv.FormatInt(posts[2], 10), 1,
			fmt.Sprintf("%d-multi-%d", base, index), time.Now().Unix(),
		)
		return err
	})
	upVotes, err := GetPostVoteCount(posts[2])
	if err != nil || upVotes != 100 {
		t.Fatalf("upvotes=%d err=%v", upVotes, err)
	}
	finalScore, err := client.ZScore(scoreKey, strconv.FormatInt(posts[2], 10)).Result()
	if err != nil || finalScore != initialScore+100*scorePerVote {
		t.Fatalf("score=%v want=%v err=%v", finalScore, initialScore+100*scorePerVote, err)
	}

	wantEvents := map[string]int{post: 6, strconv.FormatInt(posts[1], 10): 1, strconv.FormatInt(posts[2], 10): 100}
	gotEvents := countVoteTestEvents(t, wantEvents)
	for postID, want := range wantEvents {
		if gotEvents[postID] != want {
			t.Fatalf("post %s produced %d events, expected %d", postID, gotEvents[postID], want)
		}
	}
}

func runConcurrentVotes(t *testing.T, count int, vote func(index int) error) {
	t.Helper()
	errorsChannel := make(chan error, count)
	var group sync.WaitGroup
	for index := 0; index < count; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			if err := vote(index); err != nil {
				errorsChannel <- err
			}
		}(index)
	}
	group.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("concurrent vote failed: %v", err)
	}
}

func countVoteTestEvents(t *testing.T, posts map[string]int) map[string]int {
	t.Helper()
	messages, err := client.XRange(getRedisKey(KeyVoteOutboxStream), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int, len(posts))
	for _, message := range messages {
		postID := fmt.Sprint(message.Values["post_id"])
		if _, ok := posts[postID]; ok {
			counts[postID]++
		}
	}
	return counts
}

func cleanupVoteTestData(posts []int64) {
	postStrings := make(map[string]struct{}, len(posts))
	keys := make([]string, 0, len(posts)*2)
	for _, postID := range posts {
		post := strconv.FormatInt(postID, 10)
		postStrings[post] = struct{}{}
		keys = append(keys,
			getRedisKey(KeyPostVotedZSetPrefix+post),
			getRedisKey(KeyPostVoteVersionPrefix+post),
		)
		client.ZRem(getRedisKey(KeyPostTimeZSet), postID)
		client.ZRem(getRedisKey(KeyPostScoreZSet), postID)
		client.SRem(getRedisKey(KeyCommentZSetPrefix+"1"), postID)
	}
	if messages, err := client.XRange(getRedisKey(KeyVoteOutboxStream), "-", "+").Result(); err == nil {
		ids := make([]string, 0)
		for _, message := range messages {
			if _, ok := postStrings[fmt.Sprint(message.Values["post_id"])]; ok {
				ids = append(ids, message.ID)
			}
		}
		if len(ids) > 0 {
			client.XDel(getRedisKey(KeyVoteOutboxStream), ids...)
		}
	}
	client.Del(keys...)
}
