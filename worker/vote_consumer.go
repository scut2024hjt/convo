package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	amqp "github.com/rabbitmq/amqp091-go"
	mysqldao "github.com/scut2024hjt/convo/dao/mysql"
	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/mq"
	"github.com/scut2024hjt/convo/settings"
	"go.uber.org/zap"
)

const retryHeader = "x-retry-count"

func RunVoteConsumer(ctx context.Context, client *mq.Client, consumerName string) {
	for ctx.Err() == nil {
		deliveries, closeChannel, err := client.ConsumeVote(ctx, consumerName)
		if err != nil {
			zap.L().Error("start vote consumer failed", zap.Error(err))
			if !waitForRetry(ctx) {
				return
			}
			continue
		}
		for delivery := range deliveries {
			handleVoteDelivery(ctx, client, delivery)
		}
		_ = closeChannel()
	}
}

func handleVoteDelivery(ctx context.Context, client *mq.Client, delivery amqp.Delivery) {
	var event models.VoteEvent
	if err := json.Unmarshal(delivery.Body, &event); err != nil {
		moveToDeadLetter(ctx, client, delivery, delivery.MessageId, fmt.Errorf("invalid json: %w", err))
		return
	}
	if err := validateVoteEvent(&event); err != nil {
		moveToDeadLetter(ctx, client, delivery, event.EventID, err)
		return
	}

	status, err := mysqldao.PersistVoteEvent(&event)
	if err == nil {
		if ackErr := delivery.Ack(false); ackErr != nil {
			zap.L().Error("ack consumed vote failed", zap.String("event_id", event.EventID), zap.Error(ackErr))
			return
		}
		zap.L().Debug("vote event consumed",
			zap.String("event_id", event.EventID),
			zap.String("status", string(status)),
		)
		return
	}
	if errors.Is(err, mysqldao.ErrVoteEventConflict) {
		moveToDeadLetter(ctx, client, delivery, event.EventID, err)
		return
	}
	retryVoteDelivery(ctx, client, delivery, event.EventID, err)
}

func retryVoteDelivery(ctx context.Context, client *mq.Client, delivery amqp.Delivery, eventID string, cause error) {
	retryCount := headerInt(delivery.Headers, retryHeader) + 1
	if retryCount > settings.Conf.RabbitMQConfig.MaxRetries {
		moveToDeadLetter(ctx, client, delivery, eventID, cause)
		return
	}
	headers := amqp.Table{retryHeader: int32(retryCount)}
	if err := client.Publish(ctx, mq.RetryExchange, delivery.Body, eventID, headers); err != nil {
		zap.L().Error("publish vote retry failed", zap.String("event_id", eventID), zap.Error(err))
		_ = delivery.Nack(false, true)
		return
	}
	if err := delivery.Ack(false); err != nil {
		zap.L().Error("ack vote after retry publish failed", zap.String("event_id", eventID), zap.Error(err))
	}
}

func moveToDeadLetter(ctx context.Context, client *mq.Client, delivery amqp.Delivery, eventID string, cause error) {
	headers := amqp.Table{
		retryHeader: headerInt(delivery.Headers, retryHeader),
		"x-error":   cause.Error(),
	}
	if err := client.Publish(ctx, mq.DeadExchange, delivery.Body, eventID, headers); err != nil {
		zap.L().Error("publish vote dead letter failed", zap.String("event_id", eventID), zap.Error(err))
		_ = delivery.Nack(false, true)
		return
	}
	if err := delivery.Ack(false); err != nil {
		zap.L().Error("ack vote after dead-letter publish failed", zap.String("event_id", eventID), zap.Error(err))
	}
}

func validateVoteEvent(event *models.VoteEvent) error {
	if event.EventID == "" || len(event.EventID) > 64 || event.UserID == "" || event.PostID == "" ||
		event.Version <= 0 || event.OccurredAt <= 0 {
		return errors.New("vote event is missing required fields")
	}
	if event.EventType != models.VoteChangedEventType {
		return fmt.Errorf("unsupported vote event type %q", event.EventType)
	}
	if event.Direction != -1 && event.Direction != 0 && event.Direction != 1 {
		return fmt.Errorf("invalid vote direction %d", event.Direction)
	}
	userID, err := strconv.ParseInt(event.UserID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	if userID <= 0 {
		return errors.New("user id must be positive")
	}
	postID, err := strconv.ParseInt(event.PostID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid post id: %w", err)
	}
	if postID <= 0 {
		return errors.New("post id must be positive")
	}
	return nil
}

func headerInt(headers amqp.Table, key string) int {
	value, ok := headers[key]
	if !ok {
		return 0
	}
	switch number := value.(type) {
	case int:
		return number
	case int8:
		return int(number)
	case int16:
		return int(number)
	case int32:
		return int(number)
	case int64:
		return int(number)
	case string:
		parsed, _ := strconv.Atoi(number)
		return parsed
	default:
		return 0
	}
}
