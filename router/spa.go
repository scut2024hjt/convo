package router

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// registerSPA provides /app/ routes without affecting legacy /, /static,
// /swagger, /api/v1, pprof or the isolated metrics listener.
func registerSPA(r *gin.Engine) {
	r.GET("/app", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/app/") })
	r.GET("/app/*path", func(c *gin.Context) {
		rel := strings.TrimPrefix(c.Param("path"), "/")
		if rel != "" && !strings.Contains(rel, "\\") {
			clean := filepath.Clean(rel)
			if clean != ".." && !strings.HasPrefix(clean, "../") {
				file := filepath.Join("web", "dist", clean)
				if info, err := os.Stat(file); err == nil && !info.IsDir() {
					if strings.HasPrefix(rel, "assets/") {
						c.Header("Cache-Control", "public, max-age=31536000, immutable")
					}
					c.File(file)
					return
				}
			}
		}
		if strings.HasPrefix(rel, "assets/") || strings.Contains(rel, ".") {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "no-cache")
		if _, err := os.Stat("web/dist/index.html"); err != nil {
			c.String(http.StatusServiceUnavailable, "Web assets have not been built; run npm run build in web/")
			return
		}
		c.File("web/dist/index.html")
	})
}
