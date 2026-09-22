//go:build integration
// +build integration

package mysql

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/settings"
)

func TestPersistVoteEventVersions(t *testing.T) {
	if err := Init(&settings.MySQLConfig{
		Host: "127.0.0.1", Port: 33306, User: "root", Password: "123456", DB: "convo",
		MaxOpenConnection: 4, MaxIdleConnection: 2,
	}); err != nil {
		t.Skipf("mysql integration service is unavailable: %v", err)
	}
	unique := time.Now().UnixNano()
	userID := fmt.Sprintf("%d", unique)
	postID := fmt.Sprintf("%d", unique+1)
	event := func(id string, direction int8, version int64) *models.VoteEvent {
		return &models.VoteEvent{
			EventID: id, EventType: models.VoteChangedEventType,
			UserID: userID, PostID: postID, Direction: direction,
			Version: version, OccurredAt: time.Now().Unix(),
		}
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM mq_consumed_message WHERE event_id LIKE ?`, fmt.Sprintf("%d-%%", unique))
		_, _ = db.Exec(`DELETE FROM post_vote WHERE user_id = ? AND post_id = ?`, userID, postID)
		Close()
	})

	first := event(fmt.Sprintf("%d-1", unique), 1, 1)
	status, err := PersistVoteEvent(first)
	if err != nil || status != VotePersisted {
		t.Fatalf("persist first event: status=%s err=%v", status, err)
	}
	status, err = PersistVoteEvent(first)
	if err != nil || status != VoteDuplicate {
		t.Fatalf("deduplicate event: status=%s err=%v", status, err)
	}

	newer := event(fmt.Sprintf("%d-3", unique), -1, 3)
	status, err = PersistVoteEvent(newer)
	if err != nil || status != VotePersisted {
		t.Fatalf("persist newer event: status=%s err=%v", status, err)
	}
	older := event(fmt.Sprintf("%d-2", unique), 0, 2)
	status, err = PersistVoteEvent(older)
	if err != nil || status != VoteStale {
		t.Fatalf("reject stale event: status=%s err=%v", status, err)
	}
	same := event(fmt.Sprintf("%d-4", unique), -1, 3)
	status, err = PersistVoteEvent(same)
	if err != nil || status != VoteSameVersion {
		t.Fatalf("accept same-version event: status=%s err=%v", status, err)
	}

	conflict := event(fmt.Sprintf("%d-5", unique), 1, 3)
	if _, err = PersistVoteEvent(conflict); !errors.Is(err, ErrVoteEventConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
	var stored struct {
		Direction int8  `db:"direction"`
		Version   int64 `db:"version"`
	}
	if err = db.Get(&stored,
		`SELECT direction, version FROM post_vote WHERE user_id = ? AND post_id = ?`,
		userID, postID,
	); err != nil {
		t.Fatal(err)
	}
	if stored.Direction != -1 || stored.Version != 3 {
		t.Fatalf("unexpected final state: %#v", stored)
	}
}
