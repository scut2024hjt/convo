package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	redisdao "github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/logic"
	"github.com/scut2024hjt/convo/models"
	"go.uber.org/zap"
)

func likeID(c *gin.Context) (int64, bool) {
	v := c.Param("id")
	if v == "" || len(v) > 19 || strings.Trim(v, "0123456789") != "" || v[0] == '0' {
		ResponseError(c, CodeInvalidParam)
		return 0, false
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n <= 0 {
		ResponseError(c, CodeInvalidParam)
		return 0, false
	}
	return n, true
}
func respondLikeErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, logic.ErrPostNotFound):
		ResponseError(c, CodePostNotFound)
	case errors.Is(err, logic.ErrPostIndexPending):
		ResponseError(c, CodePostIndexPending)
	case errors.Is(err, redisdao.ErrPostNotInitialized):
		ResponseError(c, CodePostIndexPending)
	case errors.Is(err, redisdao.ErrInvalidDirection):
		ResponseError(c, CodeInvalidParam)
	default:
		zap.L().Warn("like request unavailable", zap.Error(err))
		ResponseError(c, CodeServerBusy)
	}
}
func likeWrite(c *gin.Context, direction int8) {
	postID, ok := likeID(c)
	if !ok {
		return
	}
	userID, e := getCurrentUserID(c)
	if e != nil {
		ResponseError(c, CodeNeedLogin)
		return
	}
	p := &models.ParamsVoteData{PostID: postID, Direction: direction}
	result, e := logic.VoteForPost(userID, p)
	if e != nil {
		respondLikeErr(c, e)
		return
	}
	if !result.Changed {
		c.Set("convo_vote_unchanged", true)
	}
	ResponseSuccess(c, gin.H{
		"post_id": strconv.FormatInt(postID, 10), "liked": direction == 1,
		"version": strconv.FormatInt(result.Version, 10), "changed": result.Changed,
	})
}

// PutLikeHandler sets the current user's target state.
// @Summary 点赞
// @Description 幂等设为已点赞；成功表示 Redis 状态及事件已接受，MySQL 异步持久化。
// @Tags 点赞
// @Produce json
// @Security ApiKeyAuth
// @Param id path string true "帖子 ID"
// @Success 200 {object} ResponseData{data=DocLikeChange}
// @Router /post/{id}/like [put]
func PutLikeHandler(c *gin.Context) { likeWrite(c, 1) }

// DeleteLikeHandler sets the current user's target state.
// @Summary 取消点赞
// @Description 幂等设为未点赞；保留历史版本。
// @Tags 点赞
// @Produce json
// @Security ApiKeyAuth
// @Param id path string true "帖子 ID"
// @Success 200 {object} ResponseData{data=DocLikeChange}
// @Router /post/{id}/like [delete]
func DeleteLikeHandler(c *gin.Context) { likeWrite(c, 0) }

// GetLikeHandler reads only the authenticated user's state.
// @Summary 查询本人点赞状态
// @Description source=mysql/freshness=persisted 表示可能滞后的已持久化状态。
// @Tags 点赞
// @Produce json
// @Security ApiKeyAuth
// @Param id path string true "帖子 ID"
// @Success 200 {object} ResponseData{data=models.LikeState}
// @Router /post/{id}/like [get]
func GetLikeHandler(c *gin.Context) {
	postID, ok := likeID(c)
	if !ok {
		return
	}
	userID, e := getCurrentUserID(c)
	if e != nil {
		ResponseError(c, CodeNeedLogin)
		return
	}
	states, e := logic.ReadOwnLikes(userID, []int64{postID})
	if e != nil {
		respondLikeErr(c, e)
		return
	}
	if len(states) != 1 {
		ResponseError(c, CodeServerBusy)
		return
	}
	if states[0].Code != 0 {
		if states[0].Code == int64(CodePostIndexPending) {
			ResponseError(c, CodePostIndexPending)
		} else {
			ResponseError(c, CodePostNotFound)
		}
		return
	}
	ResponseSuccess(c, states[0])
}

type batchLikeRequest struct {
	PostIDs []string `json:"post_ids"`
}

// PostLikeStatesHandler reads a bounded batch, deduplicated by post ID.
// @Summary 批量查询本人点赞状态
// @Description 去重后最多 50 个 ID，原始数组最多 500 项、请求体最多 16KiB。不可见条目带业务 code。
// @Tags 点赞
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param body body batchLikeRequest true "帖子 ID 列表"
// @Success 200 {object} ResponseData{data=DocLikeBatch}
// @Router /posts/like-states [post]
func PostLikeStatesHandler(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var req batchLikeRequest
	if e := c.ShouldBindJSON(&req); e != nil || len(req.PostIDs) == 0 || len(req.PostIDs) > 500 {
		ResponseError(c, CodeInvalidParam)
		return
	}
	userID, e := getCurrentUserID(c)
	if e != nil {
		ResponseError(c, CodeNeedLogin)
		return
	}
	ids := make([]int64, 0, len(req.PostIDs))
	seen := map[int64]bool{}
	for _, s := range req.PostIDs {
		if s == "" || len(s) > 19 || s[0] == '0' || strings.Trim(s, "0123456789") != "" {
			ResponseError(c, CodeInvalidParam)
			return
		}
		id, parseErr := strconv.ParseInt(s, 10, 64)
		if parseErr != nil || id <= 0 {
			ResponseError(c, CodeInvalidParam)
			return
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
			if len(ids) > 50 {
				ResponseError(c, CodeInvalidParam)
				return
			}
		}
	}
	states, e := logic.ReadOwnLikes(userID, ids)
	if e != nil {
		respondLikeErr(c, e)
		return
	}
	ResponseSuccess(c, gin.H{"items": states})
}
