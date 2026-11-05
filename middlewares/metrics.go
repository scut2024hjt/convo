package middlewares

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/scut2024hjt/convo/observability"
)

// Metrics runs before JWT middleware and observes both successful handlers
// and early rejections. No network calls occur in the request path.
func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		var code int64
		if value, ok := c.Get("convo_business_code"); ok {
			code, _ = value.(int64)
		}
		voteUnchanged := c.GetBool("convo_vote_unchanged")
		observability.RecordHTTP(c.Request.Method, c.FullPath(), c.Writer.Status(), code, time.Since(start), voteUnchanged)
	}
}
