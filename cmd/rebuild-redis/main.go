package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/scut2024hjt/convo/dao/mysql"
	"github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/logic"
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
	flag.Parse()
	if !*confirmed {
		return fmt.Errorf("refusing to rebuild: stop all application instances, drain RabbitMQ, then pass --confirm-maintenance")
	}
	if err := settings.Init(); err != nil {
		return fmt.Errorf("init settings: %w", err)
	}
	if err := mysql.Init(settings.Conf.MySQLConfig); err != nil {
		return fmt.Errorf("init mysql: %w", err)
	}
	defer mysql.Close()
	if err := redis.Init(settings.Conf.RedisConfig); err != nil {
		return fmt.Errorf("init redis: %w", err)
	}
	defer redis.Close()

	report, err := logic.RebuildRedisFromMySQL(*batchSize)
	if err != nil {
		return err
	}
	fmt.Printf("redis rebuild completed: posts=%d vote_states=%d\n", report.Posts, report.Votes)
	return nil
}
