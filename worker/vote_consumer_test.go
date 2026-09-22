package worker

import (
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/scut2024hjt/convo/models"
)

func TestValidateVoteEvent(t *testing.T) {
	event := &models.VoteEvent{
		EventID: "1", EventType: models.VoteChangedEventType,
		UserID: "2", PostID: "3", Direction: 1, Version: 1, OccurredAt: 1,
	}
	if err := validateVoteEvent(event); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	event.Direction = 2
	if err := validateVoteEvent(event); err == nil {
		t.Fatal("invalid direction accepted")
	}
}

func TestHeaderInt(t *testing.T) {
	headers := amqp.Table{retryHeader: int32(4)}
	if value := headerInt(headers, retryHeader); value != 4 {
		t.Fatalf("expected 4, got %d", value)
	}
}
