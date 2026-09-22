package redis

import (
	"fmt"

	"github.com/go-redis/redis"
	"github.com/scut2024hjt/convo/settings"
	"go.uber.org/zap"
)

var (
	client *redis.Client
	Nil    = redis.Nil
)

// Init 初始化连接
func Init(cfg *settings.RedisConfig) (err error) {
	client = redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
		PoolSize: cfg.PoolSize,
	})
	_, err = client.Ping().Result()
	return
}

func Close() {
	err := client.Close()
	if err != nil {
		zap.L().Error("Closed redis failed", zap.Error(err))
		return
	}
	return
}
