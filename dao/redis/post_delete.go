package redis

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"

	goredis "github.com/go-redis/redis"
)

//go:embed post_delete.lua
var deletePostLua string
var deletePostScript = goredis.NewScript(deletePostLua)

// Tombstone and removal share one Redis atomic operation. A replayed old
// publish Lua script observes the tombstone and cannot resurrect the indexes.
func RemoveDeletedPostIndexes(ctx context.Context, postID, communityID int64) error {
	op, err := newProjectionClient(ctx)
	if err != nil {
		return err
	}
	defer op.Close()
	id := strconv.FormatInt(postID, 10)
	value, err := deletePostScript.Run(op, []string{
		getRedisKey(KeyPostTimeZSet), getRedisKey(KeyPostScoreZSet),
		getRedisKey(KeyCommentZSetPrefix + strconv.FormatInt(communityID, 10)),
		getRedisKey(KeyPostDeletedSet), postCacheKey(postID),
	}, id).Result()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	code, err := redisInt64(value)
	if err != nil {
		return err
	}
	if code != 1 {
		return fmt.Errorf("delete index unexpected redis key type or result: %d", code)
	}
	return nil
}
