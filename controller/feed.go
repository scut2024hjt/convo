package controller

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/scut2024hjt/convo/dao/mysql"
	"github.com/scut2024hjt/convo/logic"
	"go.uber.org/zap"
)

func parseFollowTarget(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != c.Param("id") {
		ResponseError(c, CodeInvalidParam)
		return 0, false
	}
	return id, true
}

func PutFollowHandler(c *gin.Context) {
	id, ok := parseFollowTarget(c)
	if !ok {
		return
	}
	userID, err := getCurrentUserID(c)
	if err != nil {
		ResponseError(c, CodeNeedLogin)
		return
	}
	err = logic.FollowAuthor(userID, id)
	switch {
	case errors.Is(err, logic.ErrSelfFollow):
		ResponseError(c, CodeSelfFollow)
	case errors.Is(err, mysql.ErrFollowTargetNotFound):
		ResponseError(c, CodeUserNotExist)
	case err != nil:
		zap.L().Error("follow author failed", zap.Error(err))
		ResponseError(c, CodeServerBusy)
	default:
		ResponseSuccess(c, gin.H{"following": true})
	}
}

func DeleteFollowHandler(c *gin.Context) {
	id, ok := parseFollowTarget(c)
	if !ok {
		return
	}
	userID, err := getCurrentUserID(c)
	if err != nil {
		ResponseError(c, CodeNeedLogin)
		return
	}
	if err = logic.UnfollowAuthor(userID, id); err != nil {
		zap.L().Error("unfollow author failed", zap.Error(err))
		ResponseError(c, CodeServerBusy)
		return
	}
	ResponseSuccess(c, gin.H{"following": false})
}

func GetFollowHandler(c *gin.Context) {
	id, ok := parseFollowTarget(c)
	if !ok {
		return
	}
	userID, err := getCurrentUserID(c)
	if err != nil {
		ResponseError(c, CodeNeedLogin)
		return
	}
	following, err := logic.IsFollowing(userID, id)
	if err != nil {
		zap.L().Error("get follow state failed", zap.Error(err))
		ResponseError(c, CodeServerBusy)
		return
	}
	ResponseSuccess(c, gin.H{"following": following})
}

func GetFeedHandler(c *gin.Context) {
	userID, err := getCurrentUserID(c)
	if err != nil {
		ResponseError(c, CodeNeedLogin)
		return
	}
	limit := 20
	if raw, ok := c.GetQuery("limit"); ok {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 50 {
			ResponseError(c, CodeInvalidParam)
			return
		}
	}
	mode := c.Query("mode")
	if mode != "" && mode != "pull" && mode != "push" {
		ResponseError(c, CodeInvalidParam)
		return
	}
	feed, err := logic.GetFeedMode(userID, limit, c.Query("cursor"), mode)
	if errors.Is(err, logic.ErrInvalidFeedCursor) {
		ResponseError(c, CodeInvalidParam)
		return
	}
	if err != nil {
		zap.L().Error("get feed failed", zap.Error(err))
		ResponseError(c, CodeServerBusy)
		return
	}
	ResponseSuccess(c, feed)
}
