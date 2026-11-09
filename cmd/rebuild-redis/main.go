package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/scut2024hjt/convo/dao/mysql"
	"github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/logic"
	"github.com/scut2024hjt/convo/maintenance"
	"github.com/scut2024hjt/convo/settings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	confirmed := flag.Bool(
		"confirm-maintenance",
		false,
		"confirm that all application writers are stopped and RabbitMQ is drained",
	)
	batchSize := flag.Int("batch-size", 500, "number of posts restored per batch")
	database := flag.String("database", "", "explicit configured MySQL database to maintain")
	redisDB := flag.Int("redis-db", -1, "explicit configured Redis DB to maintain")
	managementURL := flag.String("rabbit-management-url", os.Getenv("CONVO_RABBITMQ_MANAGEMENT_URL"), "RabbitMQ management base URL")
	flag.Parse()
	if !*confirmed {
		return fmt.Errorf("refusing to rebuild: stop all application instances, drain RabbitMQ, then pass --confirm-maintenance")
	}
	if err := settings.Init(); err != nil {
		return fmt.Errorf("init settings: %w", err)
	}
	if err := maintenance.ValidateTarget(settings.Conf.MySQLConfig.DB, settings.Conf.RedisConfig.DB, *database, *redisDB); err != nil {
		return err
	}
	if *batchSize < 1 || *batchSize > 1000 {
		return fmt.Errorf("batch-size must be 1..1000")
	}
	if err := mysql.Init(settings.Conf.MySQLConfig); err != nil {
		return fmt.Errorf("init mysql: %w", err)
	}
	defer mysql.Close()
	if err := redis.Init(settings.Conf.RedisConfig); err != nil {
		return fmt.Errorf("init redis: %w", err)
	}
	defer redis.Close()

	// Check all queues and both stream states immediately before destructive IO.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := maintenance.CheckDrain(ctx, *managementURL, settings.Conf.RabbitMQConfig); err != nil {
		return fmt.Errorf("rebuild drain preflight: %w", err)
	}
	report, err := logic.RebuildRedisFromMySQL(*batchSize)
	if err != nil {
		return err
	}
	fmt.Printf("redis rebuild completed: posts=%d vote_states=%d\n", report.Posts, report.Votes)
	return nil
}
