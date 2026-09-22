package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/scut2024hjt/convo/controller"
	"github.com/scut2024hjt/convo/dao/mysql"
	"github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/logger"
	"github.com/scut2024hjt/convo/mq"
	"github.com/scut2024hjt/convo/pkg/snowflake"
	"github.com/scut2024hjt/convo/router"
	"github.com/scut2024hjt/convo/settings"
	"github.com/scut2024hjt/convo/worker"
	"go.uber.org/zap"
)

// Go web 开发较通用的脚手架
// @title convo 项目接口文档
// @version 1.0
// @description Go web 开发进阶项目实战 --> 源自李文周

// @contact.name scut2024hjt
// @contact.email 330459539@qq.com

// @license.name Apache 2.0
// @license.url https://www.apache.org/licenses/LICENSE-2.0.html

// @host 222.201.144.196:9090/api/v1
func main() {
	// 1. 加载配置
	if err := settings.Init(); err != nil {
		fmt.Printf("init settings failed, err: %v\n", err)
		return
	}
	// 2. 初始化日志
	if err := logger.Init(settings.Conf.LogConfig); err != nil {
		fmt.Printf("init settings failed, err: %v\n", err)
		return
	}
	zap.L().Debug("logger init success")
	defer func(l *zap.Logger) {
		err := l.Sync()
		if err != nil {
			return
		}
	}(zap.L())
	// 3. 初始化 MySQL
	if err := mysql.Init(settings.Conf.MySQLConfig); err != nil {
		fmt.Printf("init settings failed, err: %v\n", err)
		return
	}
	defer mysql.Close()
	// 4. 初始化 Redis
	if err := redis.Init(settings.Conf.RedisConfig); err != nil {
		fmt.Printf("init settings failed, err: %v\n", err)
		return
	}
	defer redis.Close()
	// 雪花算法：分布式 ID 生成器
	if err := snowflake.Init(settings.Conf.SnowFlakeConfig.StartTime, settings.Conf.SnowFlakeConfig.MachineId); err != nil {
		fmt.Printf("init snowflake failed, err: %v\n", err)
		return
	}
	// RabbitMQ 同时承载 Stream relay 的可靠发布和 MySQL 落库消费者。
	mqClient, err := mq.New(settings.Conf.RabbitMQConfig)
	if err != nil {
		fmt.Printf("init rabbitmq failed, err: %v\n", err)
		return
	}
	defer mqClient.Close()
	// 初始化 gin 框架内置的翻译器
	if err := controller.InitTrans("zh"); err != nil {
		fmt.Printf("Init validator trans failed, err: %v\n", err)
		return
	}
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	var workerGroup sync.WaitGroup
	workerGroup.Add(2)
	instanceID := fmt.Sprintf("%d", settings.Conf.SnowFlakeConfig.MachineId)
	go func() {
		defer workerGroup.Done()
		worker.RunVoteRelay(workerCtx, mqClient, "vote-relay-"+instanceID)
	}()
	go func() {
		defer workerGroup.Done()
		worker.RunVoteConsumer(workerCtx, mqClient, "vote-db-"+instanceID)
	}()
	// 5. 注册路由
	r := router.Setup()
	// 6. 启动服务（优雅关机）
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", settings.Conf.AppConfig.Port),
		Handler: r,
	}
	serverErrors := make(chan error, 1)
	go func() {
		// 开启一个 goroutine 启动服务
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErrors <- err
		}
	}()
	fmt.Println("GOMAXPROCS: ", runtime.GOMAXPROCS(0))
	// 等待中断信号来优雅地关闭服务器，未关闭服务器操作设置一个 5 秒的超时
	quit := make(chan os.Signal, 1) // 创建一个接收信号的通道
	// kill 默认会发送 syscall.SIGTERM 信号
	// kill -2 发送 syscall.SIGINT 信号，我们常用的 Ctrl + C 就是触发系统 SIGINT 信号
	// kill -9 发送 syscall.SIGKILL 信号，但是不能被捕获，所以不需要添加它
	// signal.Notify 把收到的 syscall.SIGINT 或者 syscall.SIGTERM 信号转发给 quit
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM) // 此处不会阻塞
	select {
	case <-quit:
	case err := <-serverErrors:
		zap.L().Error("http server stopped unexpectedly", zap.Error(err))
	}
	zap.L().Info("Shutdown Server ...")
	// 创建一个 5 秒超时 context
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	// 五秒内优雅关闭服务（将未处理完的请求处理完再关闭服务），超出 5 秒就超时退出
	if err := srv.Shutdown(shutdownCtx); err != nil {
		zap.L().Error("Server Shutdown", zap.Error(err))
	}
	cancelShutdown()
	stopWorkers()
	workersDone := make(chan struct{})
	go func() {
		workerGroup.Wait()
		close(workersDone)
	}()
	workerTimer := time.NewTimer(5 * time.Second)
	defer workerTimer.Stop()
	select {
	case <-workersDone:
	case <-workerTimer.C:
		zap.L().Warn("worker shutdown timed out")
	}
	zap.L().Info("Server exiting ...")
}
