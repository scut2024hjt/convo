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

	driver "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/scut2024hjt/convo/models"
)

func t02RegressionDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("CONVO_T02_TEST_DSN")
	if dsn == "" {
		t.Skip("CONVO_T02_TEST_DSN must name an exclusive disposable _t02 database")
	}
	parsed, err := driver.ParseDSN(dsn)
	if err != nil || !parsed.ParseTime || !strings.HasSuffix(parsed.DBName, "_t02") {
		t.Fatalf("unsafe T02 DSN: %v", err)
	}
	conn, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	previous := db
	db = conn
	t.Cleanup(func() { db = previous; conn.Close() })
	return conn
}

type t02Fixture struct{ author, reader, old1, old2, history, published, event int64 }

func newT02Fixture(t *testing.T, c *sqlx.DB) t02Fixture {
	t.Helper()
	base := time.Now().UnixNano()
	f := t02Fixture{base, base + 1, base + 20, base + 30, base + 100, base + 300, base + 500}
	for _, id := range []int64{f.author, f.reader, f.old1, f.old2} {
		if _, err := c.Exec(`INSERT INTO user(user_id,username,password) VALUES(?,?,?)`, id, fmt.Sprintf("t02r_%d", id), "not-a-login"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		statements := []string{
			`DELETE FROM content_projection_task WHERE event_id IN (SELECT event_id FROM content_outbox WHERE author_id=?)`,
			`DELETE FROM content_outbox WHERE author_id=?`,
			`DELETE FROM follow_delivery_task WHERE followee_id=?`,
			`DELETE FROM feed_inbox WHERE author_id=?`,
			`DELETE FROM user_follow WHERE followee_id=?`,
			`DELETE FROM post WHERE author_id=?`,
		}
		for _, q := range statements {
			if _, err := c.Exec(q, f.author); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
		if _, err := c.Exec(`DELETE FROM user WHERE user_id IN (?,?,?,?)`, f.author, f.reader, f.old1, f.old2); err != nil {
			t.Errorf("user cleanup: %v", err)
		}
	})
	if _, err := c.Exec(`INSERT INTO post(post_id,title,content,author_id,community_id,create_time) VALUES(?,?,?,?,1,?)`, f.history, "history", "fixture", f.author, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	return f
}

// Wait on an observed database lock, not a guessed sleep, before advancing the
// other transaction. These tests require an exclusive root-owned test schema.
func waitT02Lock(t *testing.T, c *sqlx.DB, table string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		err := c.Get(&n, `SELECT COUNT(*) FROM performance_schema.data_lock_waits w
    JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID
    WHERE l.OBJECT_SCHEMA=DATABASE() AND l.OBJECT_NAME=?`, table)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no observed lock wait on %s", table)
}
func takeT02Result(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("transaction did not finish")
	}
}
func finishT02Backfill(t *testing.T, task *FollowTask) {
	t.Helper()
	for i := 0; i < 20; i++ {
		_, done, _, err := FollowTaskBatch(context.Background(), task, 2, 15*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			return
		}
	}
	t.Fatal("backfill did not finish")
}
func drainT02Deliveries(t *testing.T) {
	t.Helper()
	for i := 0; i < 20; i++ {
		task, err := ClaimFollowTask(context.Background(), FollowBackfill, "t02-drain", 15*time.Second, 12)
		if err != nil {
			t.Fatal(err)
		}
		if task == nil {
			break
		}
		finishT02Backfill(t, task)
	}
	for i := 0; i < 20; i++ {
		task, err := ClaimProjection(context.Background(), ProjectionFanout, "t02-drain", 15*time.Second, 12)
		if err != nil {
			t.Fatal(err)
		}
		if task == nil {
			return
		}
		for batch := 0; batch < 20; batch++ {
			_, done, e := FanoutBatch(context.Background(), task, 2, 15*time.Second)
			if e != nil {
				t.Fatal(e)
			}
			if done {
				break
			}
		}
	}
	t.Fatal("fanout did not drain")
}

func TestT02PublishFollowCommitBoundary(t *testing.T) {
	c := t02RegressionDB(t)
	for _, publishFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("publish_first_%t", publishFirst), func(t *testing.T) {
			f := newT02Fixture(t, c)
			blocker, err := c.Beginx()
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			pub := make(chan error, 1)
			follow := make(chan error, 1)
			post := &models.Post{ID: f.published, AuthorID: f.author, CommunityID: 1, Title: "concurrent publish", Content: "fixture"}
			if publishFirst {
				// The absent outbox-key gap blocks publication after it has obtained
				// the production author lock, but before publication can commit.
				var ignored int64
				err = blocker.Get(&ignored, `SELECT event_id FROM content_outbox WHERE event_id=? FOR UPDATE`, f.event)
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("expected absent event: %v", err)
				}
				go func() { pub <- CreatePostWithDelivery(context.Background(), post, f.event) }()
				waitT02Lock(t, c, "content_outbox")
				go func() { follow <- FollowAuthor(f.reader, f.author) }()
				waitT02Lock(t, c, "user")
			} else {
				// The existing latest-post lock blocks the production follow upper
				// bound read after it has acquired the shared publication author lock.
				var ignored int64
				if err = blocker.Get(&ignored, `SELECT post_id FROM post FORCE INDEX (idx_feed_author)
                    WHERE author_id=? AND status=1 ORDER BY create_time DESC,post_id DESC LIMIT 1 FOR UPDATE`, f.author); err != nil {
					t.Fatal(err)
				}
				go func() { follow <- FollowAuthor(f.reader, f.author) }()
				waitT02Lock(t, c, "post")
				go func() { pub <- CreatePostWithDelivery(context.Background(), post, f.event) }()
				waitT02Lock(t, c, "user")
			}
			if err = blocker.Commit(); err != nil {
				t.Fatal(err)
			}
			takeT02Result(t, pub)
			takeT02Result(t, follow)
			var upper int64
			if err = c.Get(&upper, `SELECT upper_post_id FROM follow_delivery_task WHERE follower_id=? AND followee_id=? AND kind='backfill'`, f.reader, f.author); err != nil {
				t.Fatal(err)
			}
			want := f.history
			if publishFirst {
				want = f.published
			}
			if upper != want {
				t.Fatalf("incorrect commit coverage: upper=%d want=%d", upper, want)
			}
			drainT02Deliveries(t)
			push, err := PushFeedPosts(f.reader, 10, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(push) != 2 || push[0].ID != fmt.Sprint(f.published) || push[1].ID != fmt.Sprint(f.history) {
				t.Fatalf("commit boundary lost content: %+v", push)
			}
		})
	}
}

func TestT02OldFollowTaskCannotOverwriteRefollow(t *testing.T) {
	c := t02RegressionDB(t)
	f := newT02Fixture(t, c)
	for i := int64(1); i < 4; i++ {
		if _, err := c.Exec(`INSERT INTO post(post_id,title,content,author_id,community_id) VALUES(?,?,?,?,1)`, f.history+i, "more history", "fixture", f.author); err != nil {
			t.Fatal(err)
		}
	}
	if err := FollowAuthor(f.reader, f.author); err != nil {
		t.Fatal(err)
	}
	old, err := ClaimFollowTask(context.Background(), FollowBackfill, "old-follow", 15*time.Second, 12)
	if err != nil || old == nil {
		t.Fatalf("old task: %v %v", old, err)
	}
	if _, done, _, err := FollowTaskBatch(context.Background(), old, 2, 15*time.Second); err != nil || done {
		t.Fatalf("expected an unfinished old batch: %v %t", err, done)
	}
	if err := UnfollowAuthor(f.reader, f.author); err != nil {
		t.Fatal(err)
	}
	if err := FollowAuthor(f.reader, f.author); err != nil {
		t.Fatal(err)
	}
	fresh, err := ClaimFollowTask(context.Background(), FollowBackfill, "new-follow", 15*time.Second, 12)
	if err != nil || fresh == nil || fresh.Generation != 3 {
		t.Fatalf("new task: %v %v", fresh, err)
	}
	finishT02Backfill(t, fresh)
	if _, done, canceled, err := FollowTaskBatch(context.Background(), old, 2, 15*time.Second); err != nil || !done || !canceled {
		t.Fatalf("old task was not canceled: done=%t canceled=%t err=%v", done, canceled, err)
	}
	cleanup, err := ClaimFollowTask(context.Background(), FollowCleanup, "late-cleanup", 15*time.Second, 12)
	if err != nil || cleanup == nil {
		t.Fatalf("cleanup: %v %v", cleanup, err)
	}
	if _, done, _, err := FollowTaskBatch(context.Background(), cleanup, 2, 15*time.Second); err != nil || !done {
		t.Fatalf("late cleanup: %v %t", err, done)
	}
	var n int
	if err := c.Get(&n, `SELECT COUNT(*) FROM feed_inbox WHERE recipient_id=? AND follow_generation=3`, f.reader); err != nil || n != 4 {
		t.Fatalf("new generation damaged: %d %v", n, err)
	}
}

func TestT02LateLowIDFollowerGetsBackfill(t *testing.T) {
	c := t02RegressionDB(t)
	f := newT02Fixture(t, c)
	for _, id := range []int64{f.old1, f.old2} {
		if _, err := c.Exec(`INSERT INTO user_follow(follower_id,followee_id) VALUES(?,?)`, id, f.author); err != nil {
			t.Fatal(err)
		}
	}
	post := &models.Post{ID: f.published, AuthorID: f.author, CommunityID: 1, Title: "fanout in progress", Content: "fixture"}
	if err := CreatePostWithDelivery(context.Background(), post, f.event); err != nil {
		t.Fatal(err)
	}
	task, err := ClaimProjection(context.Background(), ProjectionFanout, "low-id-fanout", 15*time.Second, 12)
	if err != nil || task == nil {
		t.Fatalf("fanout: %v %v", task, err)
	}
	if _, done, err := FanoutBatch(context.Background(), task, 1, 15*time.Second); err != nil || done {
		t.Fatalf("expected unfinished fanout: %v %t", err, done)
	}
	if task.LastFollowerID <= f.reader {
		t.Fatal("fixture did not put the new reader behind the fanout cursor")
	}
	if err := FollowAuthor(f.reader, f.author); err != nil {
		t.Fatal(err)
	}
	if _, done, err := FanoutBatch(context.Background(), task, 1, 15*time.Second); err != nil || !done {
		t.Fatalf("fanout completion: %v %t", err, done)
	}
	drainT02Deliveries(t)
	push, err := PushFeedPosts(f.reader, 10, nil, 0)
	if err != nil || len(push) != 2 || push[0].ID != fmt.Sprint(f.published) {
		t.Fatalf("late low ID reader lost publication: %v %v", push, err)
	}
}
