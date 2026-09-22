package middlewares

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/scut2024hjt/convo/controller"
	"github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/settings"
	"go.uber.org/zap"
)

// VoteRateLimitMiddleware 用户级滑动窗口限流，只挂在投票接口上。
//
// 它解决的是「短时高频刷票」，和幂等解决的不是同一个问题：
//   - 幂等管的是「同一次投票别重复生效」——重复提交同一方向只会被忽略；
//   - 限流管的是「别发得太快」——用户不断切换方向时每次都会真的改变状态，
//     幂等拦不住，必须在入口按用户维度限速。
//
// 所以两者要同时存在，缺一不可。
func VoteRateLimitMiddleware() func(c *gin.Context) {
	window := time.Duration(settings.Conf.VoteRateLimitWindowMilliseconds) * time.Millisecond
	limit := settings.Conf.VoteRateLimitMaxRequests
	return func(c *gin.Context) {
		userID, err := controller.GetCurrentUserID(c)
		if err != nil {
			controller.ResponseError(c, controller.CodeNeedLogin)
			c.Abort()
			return
		}
		result, err := redis.AllowVoteRequest(strconv.FormatInt(userID, 10), window, limit)
		if err != nil {
			// 限流器依赖 Redis，而投票本身也依赖 Redis：这一刻投票必然失败，
			// 因此直接失败关闭——宁可拒绝请求，也不能在限流器故障时被绕过。
			zap.L().Error("vote rate limit check failed", zap.Int64("user_id", userID), zap.Error(err))
			controller.ResponseError(c, controller.CodeServerBusy)
			c.Abort()
			return
		}
		if !result.Allowed {
			zap.L().Warn("vote rate limited",
				zap.Int64("user_id", userID),
				zap.Int64("count", result.Count),
				zap.Duration("retry_after", result.RetryAfter),
			)
			retryAfterSeconds := int64(result.RetryAfter.Seconds()) + 1
			c.Header("Retry-After", strconv.FormatInt(retryAfterSeconds, 10))
			controller.ResponseErrorWithMsg(c, controller.CodeTooManyRequests, gin.H{
				"reason":      "投票过于频繁，请稍后再试",
				"retry_after": retryAfterSeconds,
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
