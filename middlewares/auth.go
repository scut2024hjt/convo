package middlewares

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/scut2024hjt/convo/controller"
	redisdao "github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/pkg/jwt"
)

// JWTAuthMiddleware 基于 JWT 的认证中间件
func JWTAuthMiddleware() func(c *gin.Context) {
	return func(c *gin.Context) {
		// 客户端携带 Token 的三种方式：1. 放在请求头 2. 放在请求体 3. 放在 URI
		// 这里假设 Token 放在 Header 的 Authorization 中，并使用 Bearer 开头
		// Authorization: Bearer xxx.xxx.xxx
		authHeader := c.Request.Header.Get("Authorization")
		if authHeader == "" {
			controller.ResponseError(c, controller.CodeNeedLogin)
			c.Abort()
			return
		}
		// 按空格切割
		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			controller.ResponseError(c, controller.CodeValidToken)
			c.Abort()
			return
		}
		// parts[1] 是获取的 tokenString, 我们使用之前定义好的解析 JWT 的函数来解析它
		mc, err := jwt.ParseToken(parts[1])
		if err != nil {
			controller.ResponseError(c, controller.CodeValidToken)
			c.Abort()
			return
		}
		userID, err := strconv.ParseInt(mc.Subject, 10, 64)
		if err != nil || mc.SessionID == "" {
			controller.ResponseError(c, controller.CodeValidToken)
			c.Abort()
			return
		}
		valid, err := redisdao.ValidateSession(userID, mc.SessionID)
		if err != nil {
			controller.ResponseError(c, controller.CodeAuthUnavailable)
			c.Abort()
			return
		}
		if !valid {
			controller.ResponseError(c, controller.CodeValidToken)
			c.Abort()
			return
		}
		// 将当前请求的 userID 信息保存到请求的上下文 c 上
		c.Set(controller.CtxtUserIDKey, userID)
		c.Set(controller.CtxtSessionIDKey, mc.SessionID)
		c.Next() // 后续处理请求函数可以用 c.Get("username") 来获取当前请求的用户信息
	}
}
