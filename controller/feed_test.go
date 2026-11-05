package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFollowTargetRejectsInvalidIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, bad := range []string{"0", "-1", "abc", "9223372036854775808"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Params = gin.Params{{Key: "id", Value: bad}}
		_, ok := parseFollowTarget(c)
		require.False(t, ok, bad)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/users/9007199254740993/follow", nil)
	c.Params = gin.Params{{Key: "id", Value: "9007199254740993"}}
	id, ok := parseFollowTarget(c)
	require.True(t, ok)
	require.Equal(t, int64(9007199254740993), id)
}
