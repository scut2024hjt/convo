//go:build integration
// +build integration

package mysql

import (
	"errors"
	"fmt"
	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/settings"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// T03 integration tests are OPT-IN and refuse the normal application DB.
func TestPersistLikeAndLegacyIsolation(t *testing.T) {
	dbName := os.Getenv("CONVO_T03_TEST_MYSQL_DB")
	if !strings.HasSuffix(dbName, "_t03") {
		t.Skip("set CONVO_T03_TEST_MYSQL_DB to a dedicated *_t03 database")
	}
	port, e := strconv.Atoi(os.Getenv("CONVO_T03_TEST_MYSQL_PORT"))
	if e != nil || port <= 0 {
		t.Fatal("CONVO_T03_TEST_MYSQL_PORT required")
	}
	cfg := &settings.MySQLConfig{Host: os.Getenv("CONVO_T03_TEST_MYSQL_HOST"), Port: port, User: os.Getenv("CONVO_T03_TEST_MYSQL_USER"), Password: os.Getenv("CONVO_T03_TEST_MYSQL_PASSWORD"), DB: dbName, MaxOpenConnection: 4, MaxIdleConnection: 2}
	if cfg.Host == "" || cfg.User == "" {
		t.Fatal("missing isolated MySQL test host/user")
	}
	if e = Init(cfg); e != nil {
		t.Fatalf("isolated mysql connect: %v", e)
	}
	defer Close()
	userID := time.Now().UnixNano() / 1000
	postID := userID + 1
	prefix := fmt.Sprintf("t03-%d", userID)
	defer func() {
		_, _ = db.Exec(`DELETE FROM mq_consumed_message WHERE event_id LIKE ?`, prefix+"%")
		_, _ = db.Exec(`DELETE FROM post_vote WHERE user_id=? AND post_id=?`, userID, postID)
	}()
	event := func(s string, kind string, direction int8, version int64) *models.VoteEvent {
		return &models.VoteEvent{EventID: prefix + s, EventType: kind, UserID: strconv.FormatInt(userID, 10), PostID: strconv.FormatInt(postID, 10), Direction: direction, Version: version, OccurredAt: time.Now().Unix()}
	}
	cases := []struct {
		evt  *models.VoteEvent
		want VotePersistStatus
		err  error
	}{
		{event("-like", models.LikeChangedEventType, 1, 1), VotePersisted, nil},
		{event("-like", models.LikeChangedEventType, 1, 1), VoteDuplicate, nil},
		{event("-unlike", models.LikeChangedEventType, 0, 2), VotePersisted, nil},
		{event("-old", models.VoteChangedEventType, -1, 1), VoteStale, nil},
		{event("-oldsame", models.VoteChangedEventType, -1, 2), VoteSameVersion, nil},
		{event("-oldfuture", models.VoteChangedEventType, 1, 3), "", ErrLegacyEventAdvance},
		{event("-newconflict", models.LikeChangedEventType, 1, 2), "", ErrVoteEventConflict},
		{event("-third", models.LikeChangedEventType, 1, 3), VotePersisted, nil},
	}
	for _, tc := range cases {
		got, err := PersistVoteEvent(tc.evt)
		if tc.err != nil && !errors.Is(err, tc.err) {
			t.Fatalf("%s error=%v want=%v", tc.evt.EventID, err, tc.err)
		}
		if tc.err == nil && err != nil {
			t.Fatalf("%s unexpected error %v", tc.evt.EventID, err)
		}
		if got != tc.want {
			t.Fatalf("%s status=%s want=%s", tc.evt.EventID, got, tc.want)
		}
	}
	var row struct {
		Direction int8  `db:"direction"`
		Version   int64 `db:"version"`
	}
	if err := db.Get(&row, `SELECT direction,version FROM post_vote WHERE user_id=? AND post_id=?`, userID, postID); err != nil {
		t.Fatal(err)
	}
	if row.Direction != 1 || row.Version != 3 {
		t.Fatalf("state %+v", row)
	}
}
