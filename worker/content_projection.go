package worker

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/scut2024hjt/convo/dao/mysql"
	redisdao "github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/observability"
	"github.com/scut2024hjt/convo/settings"
	"go.uber.org/zap"
)

func taskBackoff(attempt int, max time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Second
	for i := 1; i < attempt && d < max; i++ {
		if d > max/2 {
			return max
		}
		d *= 2
	}
	if d > max {
		return max
	}
	return d
}

// RunContentProjection starts fixed, bounded worker slots for exactly one kind.
// Tasks remain in MySQL on shutdown. A new instance can claim expired leases.
func RunContentProjection(ctx context.Context, kind, instance string, cfg settings.ContentDeliveryConfig) {
	if err := mysql.ValidateProjectionKind(kind); err != nil {
		zap.L().Error("invalid content worker", zap.Error(err))
		return
	}
	slots := cfg.IndexWorkers
	if kind == mysql.ProjectionFanout || kind == mysql.ProjectionDeleteInbox {
		slots = cfg.FanoutWorkers
	}
	var group sync.WaitGroup
	for i := 0; i < slots; i++ {
		group.Add(1)
		go func(slot int) {
			defer group.Done()
			owner := fmt.Sprintf("%s-%s-%d-%d", instance, kind, slot, time.Now().UnixNano())
			poll := time.NewTicker(time.Duration(cfg.PollMilliseconds) * time.Millisecond)
			defer poll.Stop()
			for ctx.Err() == nil {
				// Each DB operation has a finite deadline, even when there are
				// no claims; busy loops are prevented by the poll interval.
				opCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.IOTimeoutSeconds)*time.Second)
				task, err := mysql.ClaimProjection(opCtx, kind, owner, time.Duration(cfg.LeaseSeconds)*time.Second, cfg.MaxAttempts)
				cancel()
				if err != nil {
					zap.L().Warn("content projection claim failed", zap.String("kind", kind), zap.Error(err))
				} else if task != nil {
					runProjection(ctx, task, cfg)
				}
				select {
				case <-ctx.Done():
					return
				case <-poll.C:
				}
			}
		}(i)
	}
	group.Wait()
}

func runProjection(parent context.Context, task *mysql.ProjectionTask, cfg settings.ContentDeliveryConfig) {
	started := time.Now()
	logFields := []zap.Field{
		zap.String("kind", task.Kind), zap.Int64("task_id", task.TaskID), zap.Int64("event_id", task.EventID),
		zap.Int64("post_id", task.PostID), zap.Uint64("generation", task.Generation),
		zap.Int("attempt", task.Attempts), zap.Int("consecutive_failures", task.FailureCount),
		zap.String("owner", task.LeaseOwner),
	}
	zap.L().Info("content task claimed", logFields...)
	result := "done"
	defer func() { observability.RecordContentTask(task.Kind, result, time.Since(started)) }()
	ioBudget := time.Duration(cfg.IOTimeoutSeconds) * time.Second
	lease := time.Duration(cfg.LeaseSeconds) * time.Second
	budget := time.Duration(cfg.TaskBudgetSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	var err error
	if task.Kind == mysql.ProjectionIndex || task.Kind == mysql.ProjectionDeleteIndex {
		opCtx, c := context.WithTimeout(runCtx, ioBudget)
		if task.Kind == mysql.ProjectionIndex {
			var visible bool
			visible, err = mysql.PostVisible(opCtx, task.PostID)
			if err == nil && visible {
				err = redisdao.InitializePublishedPost(opCtx, task.PostID, task.CommunityID, task.OccurredAt)
			}
		} else {
			err = redisdao.RemoveDeletedPostIndexes(opCtx, task.PostID, task.CommunityID)
		}
		c()
		if err == nil {
			opCtx, c = context.WithTimeout(runCtx, ioBudget)
			err = mysql.CompleteProjection(opCtx, task)
			c()
		}
	} else {
		finished := false
		for runCtx.Err() == nil {
			opCtx, c := context.WithTimeout(runCtx, ioBudget)
			var count int64
			var done bool
			if task.Kind == mysql.ProjectionDeleteInbox {
				count, done, err = mysql.DeleteInboxBatch(opCtx, task, cfg.BatchSize, lease)
			} else {
				count, done, err = mysql.FanoutBatch(opCtx, task, cfg.BatchSize, lease)
			}
			c()
			if err != nil {
				break
			}
			if task.Kind == mysql.ProjectionFanout {
				observability.RecordContentDelivered(count)
			}
			zap.L().Info("content delivery batch committed", append(logFields, zap.Int64("batch_rows", count), zap.Bool("done", done))...)
			if done {
				finished = true
				break
			}
		}
		if !finished && errors.Is(runCtx.Err(), context.DeadlineExceeded) && parent.Err() == nil {
			opCtx, c := context.WithTimeout(parent, ioBudget)
			err = mysql.ReleaseProjection(opCtx, task)
			c()
			if err == nil {
				result = "yielded"
				return
			}
		}
	}

	if err == nil {
		if parent.Err() != nil {
			result = "interrupted"
		} else {
			result = "done"
		}
		zap.L().Info("content projection finished", append(logFields, zap.String("result", result))...)
		return
	}
	if errors.Is(err, mysql.ErrProjectionLeaseLost) {
		result = "lost_lease"
		zap.L().Warn("content projection lost lease", append(logFields, zap.Error(err))...)
		return
	}
	// Shutdown must not keep the process alive to requeue. The lease expires
	// and a new instance takes over; no progress is lost.
	if parent.Err() != nil {
		result = "interrupted"
		return
	}
	result = "retry"
	delay := taskBackoff(task.FailureCount+1, time.Duration(cfg.MaxBackoffSeconds)*time.Second)
	opCtx, c := context.WithTimeout(parent, ioBudget)
	state, failErr := mysql.RetryProjection(opCtx, task, mysql.ProjectionErrorSummary(err), cfg.MaxAttempts, delay)
	c()
	if failErr != nil {
		if errors.Is(failErr, mysql.ErrProjectionLeaseLost) {
			result = "lost_lease"
		} else {
			result = "retry_error"
		}
		zap.L().Error("content task requeue failed", append(logFields, zap.Error(failErr))...)
	} else {
		if state == "failed" {
			result = "failed"
		}
		zap.L().Warn("content projection retry scheduled", append(logFields,
			zap.String("new_status", state), zap.String("error_type", fmt.Sprintf("%T", err)),
			zap.String("delay", delay.String()))...)
	}
}

// StableOwnerPrefix is derived from the service identity but each process and
// worker slot adds a distinct nonce; generation still fences duplicate names.
func StableOwnerPrefix(machineID int64) string { return "convo-" + strconv.FormatInt(machineID, 10) }
