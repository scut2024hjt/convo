package logic

import (
	"errors"
	"fmt"

	"github.com/scut2024hjt/convo/dao/mysql"
	"github.com/scut2024hjt/convo/dao/redis"
)

var ErrRedisStateNeedsRebuild = errors.New("redis post/vote state needs an offline rebuild")

type RedisRebuildReport struct {
	Posts int64
	Votes int64
}

// EnsureRedisStateReady prevents the API from serving an empty or partially
// rebuilt post index. It does not attempt an online repair because MySQL may be
// temporarily behind Redis while RabbitMQ still has vote events in flight.
func EnsureRedisStateReady() error {
	databasePosts, err := mysql.CountActivePosts()
	if err != nil {
		return fmt.Errorf("count mysql posts: %w", err)
	}
	indexedPosts, err := redis.CountIndexedPosts()
	if err != nil {
		return fmt.Errorf("count redis posts: %w", err)
	}
	status, err := redis.GetVoteStateStatus()
	if err != nil {
		return fmt.Errorf("read redis state marker: %w", err)
	}

	if status == "" && databasePosts == indexedPosts {
		// Upgrade path for a healthy v2 deployment and initialization for an
		// empty fresh database.
		if err = redis.MarkVoteStateReady(); err != nil {
			return fmt.Errorf("initialize redis state marker: %w", err)
		}
		return nil
	}
	if status != redis.VoteStateReady || databasePosts != indexedPosts {
		return fmt.Errorf(
			"%w: marker=%q mysql_posts=%d redis_posts=%d; stop writers, drain RabbitMQ and run convo_rebuild_redis --confirm-maintenance",
			ErrRedisStateNeedsRebuild, status, databasePosts, indexedPosts,
		)
	}
	return nil
}

// RebuildRedisFromMySQL is an explicit maintenance operation. The caller must
// stop all application instances and drain RabbitMQ first; otherwise MySQL can
// be behind newer Redis state and must not be used to overwrite it.
func RebuildRedisFromMySQL(batchSize int) (*RedisRebuildReport, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	if err := redis.PrepareVoteStateRebuild(); err != nil {
		return nil, fmt.Errorf("prepare redis rebuild: %w", err)
	}

	report := new(RedisRebuildReport)
	var afterPostID int64
	for {
		posts, err := mysql.GetPostRecoveryBatch(afterPostID, batchSize)
		if err != nil {
			return nil, fmt.Errorf("read post recovery batch: %w", err)
		}
		if len(posts) == 0 {
			break
		}
		postIDs := make([]int64, 0, len(posts))
		for _, post := range posts {
			postIDs = append(postIDs, post.PostID)
		}
		votes, err := mysql.GetVoteRecoverySnapshots(postIDs)
		if err != nil {
			return nil, fmt.Errorf("read vote recovery batch: %w", err)
		}
		if err = redis.RestoreVoteStateBatch(posts, votes); err != nil {
			return nil, fmt.Errorf("write redis recovery batch: %w", err)
		}
		report.Posts += int64(len(posts))
		report.Votes += int64(len(votes))
		afterPostID = posts[len(posts)-1].PostID
	}
	indexedPosts, err := redis.CountIndexedPosts()
	if err != nil {
		return nil, fmt.Errorf("verify rebuilt redis indexes: %w", err)
	}
	if indexedPosts != report.Posts {
		return nil, fmt.Errorf("verify rebuilt redis indexes: restored=%d indexed=%d", report.Posts, indexedPosts)
	}
	if err := redis.MarkVoteStateReady(); err != nil {
		return nil, fmt.Errorf("complete redis rebuild: %w", err)
	}
	return report, nil
}
