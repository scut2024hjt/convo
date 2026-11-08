//go:build integration

package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/pkg/feedcursor"
)

// Run ONLY against a disposable, isolated MySQL 8 schema already initialized
// with init.sql. No shared default port or live data is used by this test.
func TestT01TransactionalPublishFanoutAndFencing(t *testing.T) {
	dsn := os.Getenv("T01_TEST_MYSQL_DSN")
	if os.Getenv("T01_ALLOW_INTEGRATION") != "YES" || dsn == "" {
		t.Fatal("set T01_ALLOW_INTEGRATION=YES and T01_TEST_MYSQL_DSN for isolated t01 database")
	}
	connection, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	var name string
	if err = connection.Get(&name, "SELECT DATABASE()"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(name), "t01") {
		t.Fatalf("refusing to modify non-t01 DB %q", name)
	}
	previous := db
	db = connection
	defer func() { db = previous }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base := time.Now().UnixNano()
	author := base
	readerIDs := []int64{base + 1, base + 2, base + 3}
	for i, id := range append([]int64{author}, readerIDs...) {
		_, err = db.ExecContext(ctx, `INSERT INTO user (user_id,username,password) VALUES (?,?,?)`,
			id, fmt.Sprintf("t01_%d_%d", base, i), "not-a-login-user")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`DELETE FROM user WHERE user_id=?`, id)
	}
	for _, reader := range readerIDs {
		if _, err = db.ExecContext(ctx, `INSERT INTO user_follow(follower_id,followee_id) VALUES (?,?)`, reader, author); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`DELETE FROM user_follow WHERE follower_id=? AND followee_id=?`, reader, author)
	}
	p := &models.Post{ID: base + 10, AuthorID: author, CommunityID: 1, Title: "t01 test", Content: "snapshot"}
	eventID := base + 11
	if err = CreatePostWithDelivery(ctx, p, eventID); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM post WHERE post_id=?`, p.ID)
	defer db.Exec(`DELETE FROM content_outbox WHERE event_id=?`, eventID)
	defer db.Exec(`DELETE FROM content_projection_task WHERE event_id=?`, eventID)
	defer db.Exec(`DELETE FROM feed_inbox WHERE post_id=?`, p.ID)
	var count int
	if err = db.Get(&count, `SELECT COUNT(*) FROM content_projection_task WHERE event_id=?`, eventID); err != nil || count != 2 {
		t.Fatalf("tasks=%d err=%v", count, err)
	}
	if p.CreateTime.IsZero() {
		t.Fatal("missing original published timestamp")
	}
	// Index and fanout are independently claimable and completed.
	indexTask, err := ClaimProjection(ctx, ProjectionIndex, "index-owner", 10*time.Second, 12)
	if err != nil || indexTask == nil || indexTask.EventID != eventID {
		t.Fatalf("independent index task unavailable: %v, %v", indexTask, err)
	}
	var storedFailures int
	if err = db.Get(&storedFailures, `SELECT consecutive_failures FROM content_projection_task WHERE task_id=?`, indexTask.TaskID); err != nil {
		t.Fatal(err)
	}
	if indexTask.FailureCount != 0 || storedFailures != 0 {
		t.Fatalf("claiming a pending task must not count a failure: memory=%d stored=%d", indexTask.FailureCount, storedFailures)
	}
	if err = CompleteProjection(ctx, indexTask); err != nil {
		t.Fatalf("complete index while fanout pending: %v", err)
	}
	var indexStatus, fanoutStatus string
	if err = db.Get(&indexStatus, `SELECT status FROM content_projection_task WHERE event_id=? AND kind='index'`, eventID); err != nil {
		t.Fatal(err)
	}
	if err = db.Get(&fanoutStatus, `SELECT status FROM content_projection_task WHERE event_id=? AND kind='fanout'`, eventID); err != nil {
		t.Fatal(err)
	}
	if indexStatus != "done" || fanoutStatus != "pending" {
		t.Fatalf("projection outcomes must be independent: index=%s fanout=%s", indexStatus, fanoutStatus)
	}
	rolledBack := &models.Post{ID: base + 12, AuthorID: author, CommunityID: 1, Title: "should rollback", Content: "never visible"}
	if err = CreatePostWithDelivery(ctx, rolledBack, eventID); err == nil {
		t.Fatal("duplicate event should abort the whole transaction")
	}
	if err = db.Get(&count, `SELECT COUNT(*) FROM post WHERE post_id=?`, rolledBack.ID); err != nil || count != 0 {
		t.Fatalf("partial post commit: rows=%d err=%v", count, err)
	}
	if _, err = db.Exec(`UPDATE content_projection_task SET consecutive_failures=2 WHERE event_id=? AND kind='fanout'`, eventID); err != nil {
		t.Fatal(err)
	}
	task, err := ClaimProjection(ctx, ProjectionFanout, "owner-A", 10*time.Second, 12)
	if err != nil || task == nil {
		t.Fatalf("claim: task=%v err=%v", task, err)
	}
	got, done, err := FanoutBatch(ctx, task, 2, 10*time.Second)
	if err != nil || got != 2 || done {
		t.Fatalf("first batch: got=%d done=%v err=%v", got, done, err)
	}
	if err = db.Get(&storedFailures, `SELECT consecutive_failures FROM content_projection_task WHERE task_id=?`, task.TaskID); err != nil {
		t.Fatal(err)
	}
	if task.FailureCount != 0 || storedFailures != 0 {
		t.Fatalf("committed progress must reset consecutive failures: memory=%d stored=%d", task.FailureCount, storedFailures)
	}
	before := task.LastFollowerID
	// Force the lease to expire; owner-A must be fenced after owner-B claims.
	if _, err = db.Exec(`UPDATE content_projection_task SET lease_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND) WHERE task_id=?`, task.TaskID); err != nil {
		t.Fatal(err)
	}
	next, err := ClaimProjection(ctx, ProjectionFanout, "owner-B", 10*time.Second, 12)
	if err != nil || next == nil || next.Generation <= task.Generation || next.LastFollowerID != before {
		t.Fatalf("reclaim: task=%v err=%v", next, err)
	}
	if _, _, err = FanoutBatch(ctx, task, 2, 10*time.Second); !errors.Is(err, ErrProjectionLeaseLost) {
		t.Fatalf("old worker must fail fencing: %v", err)
	}
	got, done, err = FanoutBatch(ctx, next, 2, 10*time.Second)
	if err != nil || got != 1 || !done {
		t.Fatalf("resume: got=%d done=%v err=%v", got, done, err)
	}
	if err = db.Get(&count, `SELECT COUNT(*) FROM feed_inbox WHERE post_id=?`, p.ID); err != nil || count != 3 {
		t.Fatalf("inbox rows=%d err=%v", count, err)
	}
	var state struct {
		Status string `db:"status"`
		Last   int64  `db:"last_follower_id"`
	}
	if err = db.Get(&state, `SELECT status,last_follower_id FROM content_projection_task WHERE task_id=?`, task.TaskID); err != nil {
		t.Fatal(err)
	}
	if state.Status != "done" || state.Last != readerIDs[len(readerIDs)-1] {
		t.Fatalf("final state=%+v", state)
	}
	page, err := PushFeedPosts(readerIDs[0], 2, nil, 0)
	if err != nil || len(page) != 1 || page[0].ID != fmt.Sprint(p.ID) {
		t.Fatalf("push result=%v err=%v", page, err)
	}
	// Query-only fixture: posts with exactly the same original second and
	// large IDs must paginate without duplicates (not fake publish events).
	fixtureIDs := []int64{base + 20, base + 21}
	for _, id := range fixtureIDs {
		if _, err = db.Exec(`INSERT INTO post (post_id,title,content,author_id,community_id,create_time)
            VALUES (?,?,?,?,?,?)`, id, "same second", "fixture", author, 1, p.CreateTime); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`DELETE FROM post WHERE post_id=?`, id)
		if _, err = db.Exec(`INSERT INTO feed_inbox(recipient_id,post_id,create_time) VALUES (?,?,?)`, readerIDs[0], id, p.CreateTime); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`DELETE FROM feed_inbox WHERE recipient_id=? AND post_id=?`, readerIDs[0], id)
	}
	page, err = PushFeedPosts(readerIDs[0], 2, nil, 0)
	if err != nil || len(page) != 2 || page[0].ID != fmt.Sprint(fixtureIDs[1]) || page[1].ID != fmt.Sprint(fixtureIDs[0]) {
		t.Fatalf("first page order=%+v err=%v", page, err)
	}
	cursor := feedcursor.EncodePush(page[1].CreateTime, page[1].ID, readerIDs[0])
	beforeTime, lastID, err := feedcursor.DecodePush(cursor, readerIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	nextPage, err := PushFeedPosts(readerIDs[0], 2, beforeTime, lastID)
	if err != nil || len(nextPage) != 1 || nextPage[0].ID != fmt.Sprint(p.ID) {
		t.Fatalf("second page=%v err=%v", nextPage, err)
	}
	if _, err = db.Exec(`DELETE FROM user_follow WHERE follower_id=? AND followee_id=?`, readerIDs[0], author); err != nil {
		t.Fatal(err)
	}
	page, err = PushFeedPosts(readerIDs[0], 2, nil, 0)
	if err != nil || len(page) != 0 {
		t.Fatalf("unfollow must filter inbox: %v %v", page, err)
	}
	var owner sql.NullString
	if err = db.Get(&owner, `SELECT lease_owner FROM content_projection_task WHERE task_id=?`, task.TaskID); err != nil || owner.Valid {
		t.Fatalf("lease not released: %v %v", owner, err)
	}
}
