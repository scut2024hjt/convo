package redis

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/go-redis/redis"
)

//go:embed content_projection.lua
var contentProjectionLua string

var contentProjectionScript = goredis.NewScript(contentProjectionLua)

var ErrPostIndexCorrupt = errors.New("post redis index is incomplete or has invalid key types")

// InitializePublishedPost is atomic and strictly non-destructive. Existing
// score, timestamp, direction and version are never overwritten on replay.
func InitializePublishedPost(ctx context.Context, postID, communityID int64, publishedAt time.Time) error {
	operation, err := newProjectionClient(ctx)
	if err != nil {
		return err
	}
	defer operation.Close()
	id := strconv.FormatInt(postID, 10)
	keyCommunity := getRedisKey(KeyCommentZSetPrefix + strconv.FormatInt(communityID, 10))
	result, err := contentProjectionScript.Run(operation, []string{
		getRedisKey(KeyPostTimeZSet), getRedisKey(KeyPostScoreZSet), keyCommunity,
		getRedisKey(KeyPostVotedZSetPrefix + id), getRedisKey(KeyPostVoteVersionPrefix + id),
	}, id, publishedAt.UTC().Unix()).Result()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	code, err := redisInt64(result)
	if err != nil {
		return err
	}
	if code >= 2 {
		return fmt.Errorf("%w: code=%d post_id=%s", ErrPostIndexCorrupt, code, id)
	}
	if code != 0 && code != 1 {
		return fmt.Errorf("unexpected content projection result %d", code)
	}
	return ctx.Err()
}

// MissingPublishedPostIndexes checks both global indexes without mutating vote
// state. A mismatch or unexpected Redis type is not silently repaired.
func MissingPublishedPostIndexes(ctx context.Context, posts []int64) (map[int64]bool, error) {
	result := make(map[int64]bool)
	if len(posts) == 0 {
		return result, nil
	}
	operation, err := newProjectionClient(ctx)
	if err != nil {
		return nil, err
	}
	defer operation.Close()
	pipe := operation.Pipeline()
	timeCmds := make([]*goredis.FloatCmd, 0, len(posts))
	scoreCmds := make([]*goredis.FloatCmd, 0, len(posts))
	for _, postID := range posts {
		id := strconv.FormatInt(postID, 10)
		timeCmds = append(timeCmds, pipe.ZScore(getRedisKey(KeyPostTimeZSet), id))
		scoreCmds = append(scoreCmds, pipe.ZScore(getRedisKey(KeyPostScoreZSet), id))
	}
	_, err = pipe.Exec()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && err != goredis.Nil {
		return nil, err
	}
	for i, id := range posts {
		a, b := timeCmds[i].Err(), scoreCmds[i].Err()
		if a != nil && a != goredis.Nil {
			return nil, a
		}
		if b != nil && b != goredis.Nil {
			return nil, b
		}
		if (a == goredis.Nil) != (b == goredis.Nil) {
			return nil, fmt.Errorf("%w: index membership differs for post %d", ErrPostIndexCorrupt, id)
		}
		if a == goredis.Nil {
			result[id] = true
		}
	}
	return result, ctx.Err()
}
