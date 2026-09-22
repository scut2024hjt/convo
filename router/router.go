package router

import (
	"net/http"
	"runtime"

	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	"github.com/scut2024hjt/convo/controller"
	_ "github.com/scut2024hjt/convo/docs" // 千万不要忘了导入上一步生成的docs
	"github.com/scut2024hjt/convo/logger"
	"github.com/scut2024hjt/convo/middlewares"
	"github.com/scut2024hjt/convo/settings"
	gs "github.com/swaggo/gin-swagger"
	"github.com/swaggo/gin-swagger/swaggerFiles"
)

func Setup() *gin.Engine {
	// 设置 gin 框架日志输出模式
	gin.SetMode(settings.Conf.GinConfig.Mode)
	// 创建一个路由引擎
	r := gin.New()
	r.Use(logger.GinLogger(), logger.GinRecovery(true))
	// 加载静态文件
	r.LoadHTMLFiles("./templates/index.html")
	r.Static("/static", "./static")
	// 根路由直接访问 index.html
	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})
	r.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})
	// 注册 swagger api 相关路由
	r.GET("/swagger/*any", gs.WrapHandler(swaggerFiles.Handler))
	// 注册业务路由
	v1 := r.Group("/api/v1")
	v1.POST("/signup", controller.SignUpHandler)
	v1.POST("/login", controller.LoginHandler)
	{
		v1.GET("/community", controller.CommunityHandler)
		v1.GET("/community/:id", controller.CommunityDetailHandler)
		v1.GET("/post/:id", controller.GetPostDetailHandler)
		v1.GET("/posts", controller.GetPostListHandler)
		// 根据时间或分数获取帖子列表
		v1.GET("/posts2", controller.GetPostListTwoHandler)
	}

	v1.Use(middlewares.JWTAuthMiddleware())
	{
		v1.POST("/post", controller.CreatePostHandler)
		v1.PUT("/post/:id", controller.UpdatePostHandler)
		v1.POST("/vote", controller.PostVoteHandler)
		v1.POST("/logout", controller.LogoutHandler)
	}
	runtime.SetBlockProfileRate(1)
	pprof.Register(r) // 注册 pprof 相关路由
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"msg": "404",
		})
	})
	return r
}
