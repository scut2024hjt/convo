package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/scut2024hjt/convo/dao/mysql"
	"github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/maintenance"
	"github.com/scut2024hjt/convo/settings"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "maintenance FAILED:", e)
		os.Exit(1)
	}
}
func run() error {
	confirm := flag.Bool("confirm-maintenance", false, "confirm old writers stopped and backups secured")
	database := flag.String("database", "", "explicit configured MySQL database to maintain")
	redisDB := flag.Int("redis-db", -1, "explicit configured Redis DB to maintain")
	batch := flag.Int("batch-size", 250, "number of vote rows per transaction (1-1000)")
	management := flag.String("rabbit-management-url", os.Getenv("CONVO_RABBITMQ_MANAGEMENT_URL"), "RabbitMQ management API base URL")
	flag.Parse()
	if !*confirm {
		return fmt.Errorf("--confirm-maintenance required")
	}
	if *batch < 1 || *batch > 1000 {
		return fmt.Errorf("batch-size must be 1..1000")
	}
	if e := settings.Init(); e != nil {
		return e
	}
	if err := maintenance.ValidateTarget(settings.Conf.MySQLConfig.DB, settings.Conf.RedisConfig.DB, *database, *redisDB); err != nil {
		return err
	}
	if e := mysql.Init(settings.Conf.MySQLConfig); e != nil {
		return e
	}
	defer mysql.Close()
	if e := redis.Init(settings.Conf.RedisConfig); e != nil {
		return e
	}
	defer redis.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, err := maintenance.CheckDrain(ctx, *management, settings.Conf.RabbitMQConfig)
	cancel()
	if err != nil {
		return fmt.Errorf("preflight drain: %w", err)
	}
	for {
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		_, err = maintenance.CheckDrain(ctx, *management, settings.Conf.RabbitMQConfig)
		if err != nil {
			cancel()
			return fmt.Errorf("continuation drain: %w", err)
		}
		progress, changed, done, e := mysql.ConvertLikeBatch(ctx, *batch)
		cancel()
		if e != nil {
			return e
		}
		fmt.Printf("migration phase=%s scanned=%d converted_total=%d changed_this_batch=%d\n", progress.Phase, progress.Scanned, progress.Converted, changed)
		if done {
			break
		}
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if e := mysql.TightenLikeDirectionCheck(ctx); e != nil {
		return fmt.Errorf("tighten direction constraint: %w", e)
	}
	fmt.Println("conversion complete; run redis rebuild before starting the new binary")
	return nil
}
