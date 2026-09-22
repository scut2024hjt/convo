package logic

import (
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/scut2024hjt/convo/dao/mysql"
	"github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/pkg/snowflake"
	"github.com/scut2024hjt/convo/settings"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

var sgPostList singleflight.Group

var (
	ErrPostNotFound = errors.New("post not found")
	ErrForbidden    = errors.New("post can only be edited by its author")
)

func CreatePost(p *models.Post) (err error) {
	// 1. 生成 post_id
	p.ID = snowflake.GenID()
	// 2. 保存到数据库
	err = mysql.CreatePost(p)
	if err != nil {
		return
	}
	err = redis.CreatePost(p.ID, p.CommunityID)
	return
}

// GetPostById 根据帖子 id 查询帖子详情数据
func GetPostById(pid int64) (data *models.ApiPostDetail, err error) {
	cached, err := redis.GetPostDetailCache(pid)
	if err == redis.Nil {
		data, err = mysql.GetPostDetailByID(pid)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPostNotFound
		}
		if err != nil {
			return nil, err
		}
		ttl := time.Duration(settings.Conf.CacheConfig.PostDetailTTLSeconds) * time.Second
		cached = &models.CachedPostDetail{
			AuthorName: data.AuthorName, Post: data.Post, CommunityDetail: data.CommunityDetail,
		}
		if cacheErr := redis.SetPostDetailCache(pid, cached, ttl); cacheErr != nil {
			zap.L().Warn("set post detail cache failed", zap.Int64("post_id", pid), zap.Error(cacheErr))
		}
	} else if err != nil {
		zap.L().Warn("get post detail cache failed; falling back to mysql", zap.Int64("post_id", pid), zap.Error(err))
		data, err = mysql.GetPostDetailByID(pid)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPostNotFound
		}
		if err != nil {
			return nil, err
		}
	} else {
		data = &models.ApiPostDetail{
			AuthorName: cached.AuthorName, Post: cached.Post, CommunityDetail: cached.CommunityDetail,
		}
	}

	// VoteNum is dynamic and therefore is not trusted from the detail cache.
	voteCount, voteErr := redis.GetPostVoteCount(pid)
	if voteErr != nil {
		zap.L().Warn("get vote count from redis failed; falling back to mysql", zap.Int64("post_id", pid), zap.Error(voteErr))
		voteCount, voteErr = mysql.CountPostUpVotes(pid)
		if voteErr != nil {
			return nil, voteErr
		}
	}
	data.VoteNum = voteCount
	return data, nil
}

func UpdatePost(postID, authorID int64, params *models.ParamsUpdatePost) error {
	updated, err := mysql.UpdatePost(postID, authorID, params.Title, params.Content)
	if err != nil {
		return err
	}
	if !updated {
		ownerID, ownerErr := mysql.GetPostAuthorID(postID)
		if errors.Is(ownerErr, sql.ErrNoRows) {
			return ErrPostNotFound
		}
		if ownerErr != nil {
			return ownerErr
		}
		if ownerID != authorID {
			return ErrForbidden
		}
		// The submitted content is identical to the stored content.
		return nil
	}

	if err = redis.DeletePostDetailCache(postID); err != nil {
		zap.L().Warn("delete post cache after update failed", zap.Int64("post_id", postID), zap.Error(err))
	}
	delay := time.Duration(settings.Conf.CacheConfig.DelayedDeleteMilliseconds) * time.Millisecond
	time.AfterFunc(delay, func() {
		if deleteErr := redis.DeletePostDetailCache(postID); deleteErr != nil {
			zap.L().Warn("delayed post cache delete failed", zap.Int64("post_id", postID), zap.Error(deleteErr))
		}
	})
	return nil
}

// GetPostList 获取帖子列表
func GetPostList(page, size int64) (data []*models.ApiPostDetail, err error) {
	// 查询帖子数据
	posts, err := mysql.GetPostList(page, size)
	if err != nil {
		return nil, err
	}
	data = make([]*models.ApiPostDetail, 0, len(posts))
	for _, post := range posts {
		// 根据作者 id 查询作者信息
		user, err := mysql.GetUserById(post.AuthorID)
		if err != nil {
			zap.L().Error("mysql.GetUserById(post.AuthorID) failed", zap.Error(err))
			continue
		}
		// 根据社区 id 查询社区详细信息
		community, err := mysql.GetCommunityDetailByID(post.CommunityID)
		if err != nil {
			zap.L().Error("mysql.GetCommunityDetailByID(post.CommunityID) failed", zap.Error(err))
			continue
		}
		postDetail := &models.ApiPostDetail{
			AuthorName:      user.Username,
			Post:            post,
			CommunityDetail: community,
		}
		data = append(data, postDetail)
	}
	return
}

func buildPostDetails(posts []*models.Post, ids []string) (data []*models.ApiPostDetail, err error) {
	data = make([]*models.ApiPostDetail, 0, len(posts))
	if len(posts) == 0 {
		return
	}

	// 提前查询好每篇帖子的投票数
	voteData, err := redis.GetPostVoteData(ids)
	if err != nil {
		return nil, err
	}

	authorIDSet := make(map[int64]struct{}, len(posts))
	communityIDSet := make(map[int64]struct{}, len(posts))
	authorIDs := make([]int64, 0, len(posts))
	communityIDs := make([]int64, 0, len(posts))
	for _, post := range posts {
		if _, ok := authorIDSet[post.AuthorID]; !ok {
			authorIDSet[post.AuthorID] = struct{}{}
			authorIDs = append(authorIDs, post.AuthorID)
		}
		if _, ok := communityIDSet[post.CommunityID]; !ok {
			communityIDSet[post.CommunityID] = struct{}{}
			communityIDs = append(communityIDs, post.CommunityID)
		}
	}

	users, err := mysql.GetUsersByIDs(authorIDs)
	if err != nil {
		zap.L().Error("mysql.GetUsersByIDs(authorIDs) failed", zap.Error(err))
		return nil, err
	}
	userNameMap := make(map[int64]string, len(users))
	for _, user := range users {
		userNameMap[user.UserID] = user.Username
	}

	communities, err := mysql.GetCommunityDetailsByIDs(communityIDs)
	if err != nil {
		zap.L().Error("mysql.GetCommunityDetailsByIDs(communityIDs) failed", zap.Error(err))
		return nil, err
	}
	communityMap := make(map[int64]*models.CommunityDetail, len(communities))
	for _, community := range communities {
		communityMap[community.ID] = community
	}

	// 将帖子的作者以及分区信息查询出来填充到帖子中
	for idx, post := range posts {
		userName, ok := userNameMap[post.AuthorID]
		if !ok {
			zap.L().Warn("author info not found", zap.Int64("author_id", post.AuthorID))
			continue
		}
		community, ok := communityMap[post.CommunityID]
		if !ok {
			zap.L().Warn("community info not found", zap.Int64("community_id", post.CommunityID))
			continue
		}
		var voteNum int64
		if idx < len(voteData) {
			voteNum = voteData[idx]
		}
		postDetail := &models.ApiPostDetail{
			AuthorName:      userName,
			VoteNum:         voteNum,
			Post:            post,
			CommunityDetail: community,
		}
		data = append(data, postDetail)
	}
	return
}

// GetPostListTwo 获取帖子列表(升级版)
func GetPostListTwo(p *models.ParamsPostList) (data []*models.ApiPostDetail, err error) {
	// 去 redis 查询 id 列表
	ids, err := redis.GetPostIDsInOrder(p)
	if err != nil {
		return
	}
	if len(ids) == 0 {
		zap.L().Warn("redis.GetPostIDsInOrder(p) return 0 data")
		return
	}
	// 根据 id 去数据库查询帖子详细信息
	// 返回的数据还要按照我给定的 id 的顺序返回
	posts, err := mysql.GetPostListByIds(ids)
	if err != nil {
		zap.L().Error("mysql.GetPostListByIds(ids) error", zap.Error(err))
		return
	}
	// 原始实现（保留，便于对照）：
	data = make([]*models.ApiPostDetail, 0, len(posts))
	// 提前查询好每篇帖子的投票数
	// voteData, err := redis.GetPostVoteData(ids)
	// if err != nil {
	// 	return nil, err
	// }
	// // 将帖子的作者以及分区信息查询出来填充到帖子中
	// for idx, post := range posts {
	// 	// 根据作者 id 查询作者信息
	// 	user, err := mysql.GetUserById(post.AuthorID)
	// 	if err != nil {
	// 		zap.L().Error("mysql.GetUserById(post.AuthorID) failed", zap.Error(err))
	// 		continue
	// 	}
	// 	// 根据社区 id 查询社区详细信息
	// 	community, err := mysql.GetCommunityDetailByID(post.CommunityID)
	// 	if err != nil {
	// 		zap.L().Error("mysql.GetCommunityDetailByID(post.CommunityID) failed", zap.Error(err))
	// 		continue
	// 	}
	// 	postDetail := &models.ApiPostDetail{
	// 		AuthorName:      user.Username,
	// 		VoteNum:         voteData[idx],
	// 		Post:            post,
	// 		CommunityDetail: community,
	// 	}
	// 	data = append(data, postDetail)
	// }
	// return
	return buildPostDetails(posts, ids)
}

func GetCommunityPostListHandler(p *models.ParamsPostList) (data []*models.ApiPostDetail, err error) {
	// 去 redis 查询 id 列表
	ids, err := redis.GetCommunityPostIDsInOrder(p)
	if err != nil {
		return
	}
	if len(ids) == 0 {
		zap.L().Warn("redis.GetPostIDsInOrder(p) return 0 data")
		return
	}
	// 根据 id 去数据库查询帖子详细信息
	// 返回的数据还要按照我给定的 id 的顺序返回
	posts, err := mysql.GetPostListByIds(ids)
	if err != nil {
		zap.L().Error("mysql.GetPostListByIds(ids) error", zap.Error(err))
		return
	}
	// 原始实现（保留，便于对照）：
	// data = make([]*models.ApiPostDetail, 0, len(posts))
	// // 提前查询好每篇帖子的投票数
	// voteData, err := redis.GetPostVoteData(ids)
	// if err != nil {
	// 	return
	// }
	// if err != nil {
	// 	return nil, err
	// }
	// // 将帖子的作者以及分区信息查询出来填充到帖子中
	// for idx, post := range posts {
	// 	// 根据作者 id 查询作者信息
	// 	user, err := mysql.GetUserById(post.AuthorID)
	// 	if err != nil {
	// 		zap.L().Error("mysql.GetUserById(post.AuthorID) failed", zap.Error(err))
	// 		continue
	// 	}
	// 	// 根据社区 id 查询社区详细信息
	// 	community, err := mysql.GetCommunityDetailByID(post.CommunityID)
	// 	if err != nil {
	// 		zap.L().Error("mysql.GetCommunityDetailByID(post.CommunityID) failed", zap.Error(err))
	// 		continue
	// 	}
	// 	postDetail := &models.ApiPostDetail{
	// 		AuthorName:      user.Username,
	// 		VoteNum:         voteData[idx],
	// 		Post:            post,
	// 		CommunityDetail: community,
	// 	}
	// 	data = append(data, postDetail)
	// }
	// return
	return buildPostDetails(posts, ids)
}

// GetPostListNew 将两个查询帖子列表逻辑合二为一的函数
func GetPostListNew(p *models.ParamsPostList) (data []*models.ApiPostDetail, err error) {
	// 根据请求参数的不同，执行不同的逻辑
	if p.CommunityID == 0 {
		// 查所有
		// data, err = GetPostListTwo(p)
		data, err = GetPostListWithSingleFlight(p)
	} else {
		// 根据社区 id 查询
		data, err = GetCommunityPostListHandler(p)
	}
	if err != nil {
		zap.L().Error("GetPostListNew failed", zap.Error(err))
		return nil, err
	}
	return
}

// 封装一层，加入 singleflight
func GetPostListWithSingleFlight(p *models.ParamsPostList) ([]*models.ApiPostDetail, error) {
	key := "post:list:" + p.Order + ":" + strconv.FormatInt(p.Page, 10) + ":" + strconv.FormatInt(p.Size, 10)

	// 所有 goroutine 都会执行到这里
	// zap.L().Debug("[singleflight] 开始请求", zap.String("key", key))

	// 核心调用：只有第一个会进 fn，其他直接阻塞
	v, err, _ := sgPostList.Do(key, func() (interface{}, error) {
		// 只有【第一个】请求会进这里！！
		// zap.L().Info("[singleflight] 🔥 真正执行查询", zap.String("key", key))
		return GetPostListTwo(p)
	})

	// 所有 goroutine 都会执行到这里（包括等待的）
	// zap.L().Info("[singleflight] 请求结束",
	// 	zap.String("key", key),
	// 	zap.Bool("is_shared", shared), // ✅ 关键：true = 共享别人结果
	// 	zap.Error(err),
	// )

	if err != nil {
		return nil, err
	}

	return v.([]*models.ApiPostDetail), nil
}
