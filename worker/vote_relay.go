package worker

import (
	"context"
	"encoding/json"
	"time"

	redisdao "github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/mq"
	"github.com/scut2024hjt/convo/settings"
	"go.uber.org/zap"
)

func RunVoteRelay(ctx context.Context, client *mq.Client, consumerName string) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := redisdao.EnsureVoteOutboxGroup(); err != nil {
			zap.L().Error("ensure vote outbox group failed", zap.Error(err))
			if !waitForRetry(ctx) {
				return
			}
			continue
		}

		// A stable consumer name lets a restarted instance finish its own pending
		// messages before reading new entries.
		claimed, err := redisdao.ClaimStaleVoteOutbox(consumerName, 30*time.Second, 32)
		if err != nil {
			zap.L().Error("claim stale vote outbox failed", zap.Error(err))
			if !waitForRetry(ctx) {
				return
			}
			continue
		}
		if !publishVoteMessages(ctx, client, claimed) {
			return
		}
		if !relayVoteMessages(ctx, client, consumerName, "0", 0) {
			return
		}
		if !relayVoteMessages(ctx, client, consumerName, ">", 5*time.Second) {
			return
		}
	}
}

func relayVoteMessages(ctx context.Context, client *mq.Client, consumerName, start string, block time.Duration) bool {
	for {
		messages, err := redisdao.ReadVoteOutbox(consumerName, start, 32, block)
		if err != nil {
			zap.L().Error("read vote outbox failed", zap.Error(err))
			return waitForRetry(ctx)
		}
		if len(messages) == 0 {
			return true
		}
		if !publishVoteMessages(ctx, client, messages) {
			return false
		}
		if start == ">" {
			return true
		}
	}
}

func publishVoteMessages(ctx context.Context, client *mq.Client, messages []*redisdao.VoteOutboxMessage) bool {
	for _, message := range messages {
		body, err := json.Marshal(message.Event)
		if err != nil {
			zap.L().Error("marshal vote event failed", zap.Error(err))
			return waitForRetry(ctx)
		}
		if err = client.Publish(ctx, settings.Conf.RabbitMQConfig.Exchange, body, message.Event.EventID, nil); err != nil {
			zap.L().Error("publish vote event failed",
				zap.String("event_id", message.Event.EventID),
				zap.Error(err),
			)
			return waitForRetry(ctx)
		}
		if err = redisdao.AckVoteOutbox(message.StreamID); err != nil {
			// Publishing twice is safe: MySQL deduplicates by event_id.
			zap.L().Error("ack vote outbox failed",
				zap.String("event_id", message.Event.EventID),
				zap.Error(err),
			)
			return waitForRetry(ctx)
		}
	}
	return true
}

func waitForRetry(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
