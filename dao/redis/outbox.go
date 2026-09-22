package redis

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis"
	"github.com/scut2024hjt/convo/models"
)

const VoteOutboxGroup = "vote-relay"

type VoteOutboxMessage struct {
	StreamID string
	Event    *models.VoteEvent
}

func EnsureVoteOutboxGroup() error {
	err := client.XGroupCreateMkStream(getRedisKey(KeyVoteOutboxStream), VoteOutboxGroup, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

func ReadVoteOutbox(consumer, start string, count int64, block time.Duration) ([]*VoteOutboxMessage, error) {
	streams, err := client.XReadGroup(&redis.XReadGroupArgs{
		Group:    VoteOutboxGroup,
		Consumer: consumer,
		Streams:  []string{getRedisKey(KeyVoteOutboxStream), start},
		Count:    count,
		Block:    block,
	}).Result()
	if err == Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]*VoteOutboxMessage, 0)
	for _, stream := range streams {
		for _, message := range stream.Messages {
			event, parseErr := voteEventFromStream(message.Values)
			if parseErr != nil {
				return nil, fmt.Errorf("parse outbox message %s: %w", message.ID, parseErr)
			}
			result = append(result, &VoteOutboxMessage{StreamID: message.ID, Event: event})
		}
	}
	return result, nil
}

// ClaimStaleVoteOutbox lets a healthy instance recover messages left pending
// by an instance that will not return with the same consumer name.
func ClaimStaleVoteOutbox(consumer string, minIdle time.Duration, count int64) ([]*VoteOutboxMessage, error) {
	stream := getRedisKey(KeyVoteOutboxStream)
	pending, err := client.XPendingExt(&redis.XPendingExtArgs{
		Stream: stream,
		Group:  VoteOutboxGroup,
		Start:  "-",
		End:    "+",
		Count:  count,
	}).Result()
	if err == Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(pending))
	for _, item := range pending {
		if item.Idle >= minIdle {
			ids = append(ids, item.Id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	messages, err := client.XClaim(&redis.XClaimArgs{
		Stream:   stream,
		Group:    VoteOutboxGroup,
		Consumer: consumer,
		MinIdle:  minIdle,
		Messages: ids,
	}).Result()
	if err != nil {
		return nil, err
	}
	result := make([]*VoteOutboxMessage, 0, len(messages))
	for _, message := range messages {
		event, parseErr := voteEventFromStream(message.Values)
		if parseErr != nil {
			return nil, fmt.Errorf("parse claimed outbox message %s: %w", message.ID, parseErr)
		}
		result = append(result, &VoteOutboxMessage{StreamID: message.ID, Event: event})
	}
	return result, nil
}

func AckVoteOutbox(streamID string) error {
	stream := getRedisKey(KeyVoteOutboxStream)
	const script = `
redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
return redis.call('XDEL', KEYS[1], ARGV[2])
`
	return client.Eval(script, []string{stream}, VoteOutboxGroup, streamID).Err()
}

func voteEventFromStream(values map[string]interface{}) (*models.VoteEvent, error) {
	get := func(key string) (string, error) {
		value, ok := values[key]
		if !ok {
			return "", fmt.Errorf("missing field %s", key)
		}
		switch v := value.(type) {
		case string:
			return v, nil
		case []byte:
			return string(v), nil
		default:
			return fmt.Sprint(v), nil
		}
	}
	eventID, err := get("event_id")
	if err != nil {
		return nil, err
	}
	eventType, err := get("event_type")
	if err != nil {
		return nil, err
	}
	userID, err := get("user_id")
	if err != nil {
		return nil, err
	}
	postID, err := get("post_id")
	if err != nil {
		return nil, err
	}
	directionValue, err := get("direction")
	if err != nil {
		return nil, err
	}
	versionValue, err := get("version")
	if err != nil {
		return nil, err
	}
	occurredValue, err := get("occurred_at")
	if err != nil {
		return nil, err
	}
	direction, err := strconv.ParseInt(directionValue, 10, 8)
	if err != nil {
		return nil, err
	}
	version, err := strconv.ParseInt(versionValue, 10, 64)
	if err != nil {
		return nil, err
	}
	occurredAt, err := strconv.ParseInt(occurredValue, 10, 64)
	if err != nil {
		return nil, err
	}
	return &models.VoteEvent{
		EventID: eventID, EventType: eventType, UserID: userID, PostID: postID,
		Direction: int8(direction), Version: version, OccurredAt: occurredAt,
	}, nil
}
