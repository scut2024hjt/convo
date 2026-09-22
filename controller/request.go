package controller

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
)

const CtxtUserIDKey = "userID"
const CtxtSessionIDKey = "sessionID"

var ErrorUserNotLogin = errors.New("用户未登录")

// getCurrentUserID 获取当前登录的用户 ID
func getCurrentUserID(c *gin.Context) (userID int64, err error) {
	uid, ok := c.Get(CtxtUserIDKey)
	if !ok {
		err = ErrorUserNotLogin
		return
	}
	userID, ok = uid.(int64)
	if !ok {
		err = ErrorUserNotLogin
		return
	}
	return
}

// GetCurrentUserID 导出给中间件使用，避免中间件重复解析 JWT。
func GetCurrentUserID(c *gin.Context) (int64, error) {
	return getCurrentUserID(c)
}

func getCurrentSessionID(c *gin.Context) (string, error) {
	sid, ok := c.Get(CtxtSessionIDKey)
	if !ok {
		return "", ErrorUserNotLogin
	}
	value, ok := sid.(string)
	if !ok || value == "" {
		return "", ErrorUserNotLogin
	}
	return value, nil
}

// getPageInfo 获取分页参数
func getPageInfo(c *gin.Context) (int64, int64) {
	pageNumStr := c.Query("page")
	pageSizeStr := c.Query("size")
	var (
		page int64
		size int64
		err  error
	)
	page, err = strconv.ParseInt(pageNumStr, 10, 64)
	if err != nil {
		page = 1
	}
	size, err = strconv.ParseInt(pageSizeStr, 10, 64)
	if err != nil {
		size = 10
	}
	return page, size
}
