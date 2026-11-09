package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/scut2024hjt/convo/dao/mysql"
	"github.com/scut2024hjt/convo/observability"
	"github.com/scut2024hjt/convo/settings"
	"go.uber.org/zap"
)

// RunFollowDelivery is bounded; every row persists in MySQL and an expired
// lease can be claimed by another process with a new fencing generation.
func RunFollowDelivery(ctx context.Context, kind, instance string, cfg settings.ContentDeliveryConfig) {
	slots := cfg.BackfillWorkers
	if kind == mysql.FollowCleanup {
		slots = cfg.CleanupWorkers
	}
	var wg sync.WaitGroup
	for i := 0; i < slots; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			owner := fmt.Sprintf("%s-follow-%s-%d-%d", instance, kind, slot, time.Now().UnixNano())
			ticker := time.NewTicker(time.Duration(cfg.PollMilliseconds) * time.Millisecond)
			defer ticker.Stop()
			for ctx.Err() == nil {
				op, cancel := context.WithTimeout(ctx, time.Duration(cfg.IOTimeoutSeconds)*time.Second)
				task, err := mysql.ClaimFollowTask(op, kind, owner, time.Duration(cfg.LeaseSeconds)*time.Second, cfg.MaxAttempts)
				cancel()
				if err != nil {
					zap.L().Warn("follow claim failed", zap.String("kind", kind), zap.Error(err))
				}
				if task != nil {
					runFollowTask(ctx, task, cfg)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}(i)
	}
	wg.Wait()
}

func runFollowTask(parent context.Context, task *mysql.FollowTask, cfg settings.ContentDeliveryConfig) {
	start := time.Now()
	result := "done"
	defer func() { observability.RecordFollowTask(task.Kind, result, time.Since(start)) }()
	lease := time.Duration(cfg.LeaseSeconds) * time.Second
	ioBudget := time.Duration(cfg.IOTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(parent, time.Duration(cfg.TaskBudgetSeconds)*time.Second)
	defer cancel()
	zap.L().Info("follow task claimed", zap.Int64("task_id", task.TaskID), zap.String("kind", task.Kind),
		zap.Int64("follower_id", task.FollowerID), zap.Int64("followee_id", task.FolloweeID),
		zap.Uint64("generation", task.Generation), zap.Uint64("claim_generation", task.ClaimGeneration))
	var err error
	finished := false
	for ctx.Err() == nil {
		op, c := context.WithTimeout(ctx, ioBudget)
		var n int64
		var done, canceled bool
		n, done, canceled, err = mysql.FollowTaskBatch(op, task, cfg.BatchSize, lease)
		c()
		if err != nil {
			break
		}
		observability.RecordFollowRows(task.Kind, n)
		zap.L().Info("follow task batch", zap.Int64("task_id", task.TaskID), zap.Uint64("claim_generation", task.ClaimGeneration),
			zap.Int64("last_post_id", task.LastPostID), zap.Int64("batch_rows", n), zap.Bool("done", done))
		if done {
			finished = true
			if canceled {
				result = "canceled"
			}
			break
		}
	}
	if finished {
		return
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && parent.Err() == nil {
		op, c := context.WithTimeout(parent, ioBudget)
		err = mysql.ReleaseFollowTask(op, task)
		c()
		if err == nil {
			result = "yielded"
			return
		}
	}
	if errors.Is(err, mysql.ErrFollowLeaseLost) {
		result = "lost_lease"
		return
	}
	if parent.Err() != nil {
		result = "interrupted"
		return
	}
	result = "retry"
	zap.L().Warn("follow task processing failed", zap.Int64("task_id", task.TaskID),
		zap.String("kind", task.Kind), zap.Uint64("claim_generation", task.ClaimGeneration), zap.Error(err))
	delay := taskBackoff(task.FailureCount+1, time.Duration(cfg.MaxBackoffSeconds)*time.Second)
	op, c := context.WithTimeout(parent, ioBudget)
	state, e := mysql.RetryFollowTask(op, task, cfg.MaxAttempts, delay)
	c()
	if e != nil {
		if errors.Is(e, mysql.ErrFollowLeaseLost) {
			result = "lost_lease"
		} else {
			result = "retry_error"
		}
		zap.L().Warn("follow task requeue failed", zap.Int64("task_id", task.TaskID), zap.Error(e))
	} else if state == "failed" {
		result = "failed"
	}
}
