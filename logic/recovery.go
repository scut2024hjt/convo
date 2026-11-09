package logic

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	status, err := redis.GetVoteStateStatus()
	if err != nil {
		return fmt.Errorf("read redis state marker: %w", err)
	}
	if status != "" && status != redis.VoteStateReady {
		return fmt.Errorf("%w: redis marker=%q; offline rebuild required", ErrRedisStateNeedsRebuild, status)
	}
	databasePosts, err := mysql.CountActivePosts()
	if err != nil {
		return fmt.Errorf("count mysql posts: %w", err)
	}
	indexedPosts, err := redis.CountIndexedPosts()
	if err != nil {
		return fmt.Errorf("count redis posts: %w", err)
	}
	// A count difference is legitimate only when *every* missing post has an
	// unfinished persisted index task. Scan also catches mismatched IDs with
	// equal counts; never infer health from counts alone.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pending, err := mysql.UnfinishedIndexPostIDs(ctx)
	if err != nil {
		return fmt.Errorf("read unfinished index tasks: %w", err)
	}
	var present, missing int64
	var after int64
	for {
		ids, readErr := mysql.ReadActivePostIDs(ctx, after, 500)
		if readErr != nil {
			return fmt.Errorf("scan active posts: %w", readErr)
		}
		if len(ids) == 0 {
			break
		}
		absent, checkErr := redis.MissingPublishedPostIndexes(ctx, ids)
		if checkErr != nil {
			return fmt.Errorf("check redis post index membership: %w", checkErr)
		}
		for _, postID := range ids {
			if absent[postID] {
				if !pending[postID] {
					return fmt.Errorf("%w: active post %d lacks indexes and has no runnable index projection", ErrRedisStateNeedsRebuild, postID)
				}
				missing++
			} else {
				present++
			}
		}
		after = ids[len(ids)-1]
	}
	// A deleted post can legitimately remain indexed until its delete_index
	// projection completes. Only that specific pending event can justify an
	// extra Redis member; failed/done deletes cannot mask unrelated corruption.
	deletedPending, err := mysql.UnfinishedDeletedPostIDs(ctx)
	if err != nil {
		return fmt.Errorf("read unfinished delete indexes: %w", err)
	}
	var pendingDeletedIndexed int64
	for i := 0; i < len(deletedPending); i += 500 {
		end := i + 500
		if end > len(deletedPending) {
			end = len(deletedPending)
		}
		absent, e := redis.MissingPublishedPostIndexes(ctx, deletedPending[i:end])
		if e != nil {
			return fmt.Errorf("check deleted Redis index: %w", e)
		}
		for _, id := range deletedPending[i:end] {
			if !absent[id] {
				pendingDeletedIndexed++
			}
		}
	}
	if present+missing != databasePosts || present+pendingDeletedIndexed != indexedPosts {
		return fmt.Errorf("%w: mysql=%d redis=%d verified=%d pending_missing=%d pending_deleted_indexed=%d; offline rebuild required",
			ErrRedisStateNeedsRebuild, databasePosts, indexedPosts, present, missing, pendingDeletedIndexed)
	}
	if status == "" {
		if err = redis.MarkVoteStateReady(); err != nil {
			return fmt.Errorf("mark redis state ready: %w", err)
		}
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
