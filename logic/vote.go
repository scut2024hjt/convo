package logic

import (
	"context"
	"errors"
	"github.com/scut2024hjt/convo/dao/mysql"
	"strconv"
	"time"

	"github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/observability"
	"github.com/scut2024hjt/convo/pkg/snowflake"
	"go.uber.org/zap"
)

// 投票功能
// 本项目使用简化版的投票分数
// 投一票就加 432 分 86400/200 —> 需要 200 张赞成票可以给你的帖子续一天

// 1. 判断投票的限制
// 2. 更新帖子的分数
// 3. 记录用户为该帖子投票的分数

var ErrPostIndexPending = errors.New("post index pending projection")

// VoteForPost 为帖子投票的函数
func VoteForPost(userID int64, p *models.ParamsVoteData) (*models.VoteResult, error) {
	if p.Direction != 0 && p.Direction != 1 {
		return nil, redis.ErrInvalidDirection
	}
	// Requests started after a committed delete must not create new votes.
	checkCtx, cancelCheck := context.WithTimeout(context.Background(), 2*time.Second)
	visible, checkErr := mysql.PostVisible(checkCtx, p.PostID)
	cancelCheck()
	if checkErr != nil {
		return nil, checkErr
	}
	if !visible {
		return nil, ErrPostNotFound
	}
	if err := redis.EnsureVoteStateReadable(); err != nil {
		return nil, err
	}
	zap.L().Debug("VoteForPost",
		zap.Int64("userID", userID),
		zap.Int64("postID", p.PostID),
		zap.Int8("direction", p.Direction))
	eventID := strconv.FormatInt(snowflake.GenID(), 10)
	result, err := redis.VoteForPost(
		strconv.FormatInt(userID, 10),
		strconv.FormatInt(p.PostID, 10),
		p.Direction,
		eventID,
		time.Now().Unix(),
	)
	if errors.Is(err, redis.ErrPostNotInitialized) {
		lookupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		pending, lookupErr := mysql.IsPostIndexPending(lookupCtx, p.PostID)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if pending {
			return nil, ErrPostIndexPending
		}
	}
	if err == nil && result.Changed {
		zap.L().Info("like event created", zap.String("event_id", eventID), zap.Int64("version", result.Version))
	}
	return result, err
}

// ReadOwnLikes assembles only visible content, with bounded SQL reads and a
// batch lookup that distinguishes pending publication from a lost index.
func ReadOwnLikes(userID int64, ids []int64) ([]*models.LikeState, error) {
	if len(ids) == 0 || len(ids) > 50 {
		return nil, errors.New("batch requires 1..50 posts")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	visible, err := mysql.VisiblePosts(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*models.LikeState, len(ids))
	visibleIDs := make([]int64, 0, len(ids))
	postIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		sid := strconv.FormatInt(id, 10)
		if !visible[id] {
			byID[sid] = &models.LikeState{PostID: sid, Code: 1009}
			continue
		}
		visibleIDs = append(visibleIDs, id)
		postIDs = append(postIDs, sid)
	}
	if len(visibleIDs) > 0 {
		err = redis.EnsureVoteStateReadable()
		if errors.Is(err, redis.ErrVoteStateUnavailable) {
			return nil, err
		}
		var states []*models.LikeState
		if err == nil {
			states, err = redis.ReadOwnLikeStates(strconv.FormatInt(userID, 10), postIDs)
		}
		if err != nil {
			if errors.Is(err, redis.ErrVoteStateUnavailable) || errors.Is(err, redis.ErrPostNotInitialized) || errors.Is(err, redis.ErrVoteKeyType) || errors.Is(err, redis.ErrLikeVersionLimit) {
				return nil, err
			}
			states, err = mysql.PersistedLikeStates(ctx, userID, visibleIDs)
			if err != nil {
				observability.RecordLikeReadFallback("error")
				return nil, err
			}
			observability.RecordLikeReadFallback("mysql")
		}
		missing := make([]int64, 0)
		for i, state := range states {
			if state.Code == 1014 {
				missing = append(missing, visibleIDs[i])
			}
		}
		if len(missing) > 0 {
			pending, e := mysql.PendingLikeIndexes(ctx, missing)
			if e != nil {
				return nil, e
			}
			for _, id := range missing {
				if !pending[id] {
					return nil, ErrRedisStateNeedsRebuild
				}
			}
		}
		for _, state := range states {
			byID[state.PostID] = state
		}
	}
	out := make([]*models.LikeState, 0, len(ids))
	for _, id := range ids {
		state := byID[strconv.FormatInt(id, 10)]
		if state == nil {
			return nil, errors.New("incomplete like state batch")
		}
		out = append(out, state)
	}
	return out, nil
}
