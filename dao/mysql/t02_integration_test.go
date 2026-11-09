//go:build integration

package mysql

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

// TestT02Lifecycle requires an EXCLUSIVE disposable DB whose name ends with
// _t02 and which was initialized using T02's init.sql. It never connects to
// the app's implicit localhost DB; only the explicit test DSN is accepted.
// Run ONLY this integration test with -run '^TestT02Lifecycle$'.
func TestT02Lifecycle(t *testing.T) {
	dsn := os.Getenv("CONVO_T02_TEST_DSN")
	if dsn == "" {
		t.Skip("set CONVO_T02_TEST_DSN for isolated MySQL; not a passed integration test")
	}
	parsed, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(parsed.DBName, "_t02") {
		t.Fatalf("unsafe database name %q: require _t02", parsed.DBName)
	}
	if parsed.ParseTime != true {
		t.Fatal("DSN requires parseTime=true")
	}
	conn, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	previous := db
	db = conn
	t.Cleanup(func() { db = previous; conn.Close() })
	ctx := context.Background()
	// Unique, positive, int64-safe IDs; no shared production users or posts.
	base := int64(9007199254740993) + (time.Now().UnixNano()%10000000)*10000
	actor, follower, foreign := base+1, base+2, base+3
	ids := []int64{base + 101, base + 102, base + 103, base + 104}
	username := func(id int64) string { return fmt.Sprintf("t02_%d", id) }
	for _, id := range []int64{actor, follower, foreign} {
		_, err = conn.ExecContext(ctx, `INSERT INTO user(user_id,username,password) VALUES(?,?,?)`, id, username(id), "test-not-a-login")
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		// Only this test's keys. No DROP/TRUNCATE, and no app workers may run.
		for _, q := range []string{
			`DELETE FROM follow_delivery_task WHERE follower_id=?`,
			`DELETE FROM feed_inbox WHERE recipient_id=?`,
			`DELETE FROM user_follow WHERE follower_id=?`,
			`DELETE FROM content_projection_task WHERE event_id IN (SELECT event_id FROM content_outbox WHERE post_id IN (?,?,?,?))`,
			`DELETE FROM content_outbox WHERE post_id IN (?,?,?,?)`,
			`DELETE FROM post WHERE post_id IN (?,?,?,?)`,
			`DELETE FROM user WHERE user_id IN (?,?,?)`,
		} {
			var args []interface{}
			switch {
			case strings.Contains(q, "follower_id=?"), strings.Contains(q, "recipient_id=?"):
				args = []interface{}{follower}
			case strings.Contains(q, "post_id IN"):
				args = []interface{}{ids[0], ids[1], ids[2], ids[3]}
			default:
				args = []interface{}{actor, follower, foreign}
			}
			if _, e := conn.ExecContext(ctx, q, args...); e != nil {
				t.Errorf("cleanup: %v", e)
			}
		}
	})
	at := time.Date(2025, 11, 1, 8, 0, 0, 0, time.UTC)
	for _, id := range ids {
		_, err = conn.ExecContext(ctx, `INSERT INTO post(post_id,title,content,author_id,community_id,create_time) VALUES(?,?,?,?,?,?)`, id, "T02 test", "test", actor, 1, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = FollowAuthor(follower, actor); err != nil {
		t.Fatal(err)
	}
	if err = FollowAuthor(follower, actor); err != nil {
		t.Fatal(err)
	}
	var gen uint64
	if err = conn.Get(&gen, `SELECT generation FROM user_follow WHERE follower_id=? AND followee_id=?`, follower, actor); err != nil {
		t.Fatal(err)
	}
	if gen != 1 {
		t.Fatalf("duplicate PUT advanced generation: %d", gen)
	}
	var tasks int
	if err = conn.Get(&tasks, `SELECT COUNT(*) FROM follow_delivery_task WHERE follower_id=? AND followee_id=? AND kind='backfill'`, follower, actor); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Fatalf("duplicate PUT created %d tasks", tasks)
	}
	claim, err := ClaimFollowTask(ctx, FollowBackfill, "t02-claimed", 15*time.Second, 12)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v %v", claim, err)
	}
	var failures int
	if err = conn.Get(&failures, `SELECT consecutive_failures FROM follow_delivery_task WHERE task_id=?`, claim.TaskID); err != nil {
		t.Fatal(err)
	}
	if failures != 0 || claim.FailureCount != 0 {
		t.Fatalf("normal claim counted as failure: stored=%d memory=%d", failures, claim.FailureCount)
	}
	// A later successful batch must clear a prior failure streak in memory
	// as well as SQL, before the worker decides how to retry another batch.
	if _, err = conn.ExecContext(ctx, `UPDATE follow_delivery_task SET consecutive_failures=2 WHERE task_id=?`, claim.TaskID); err != nil {
		t.Fatal(err)
	}
	claim.FailureCount = 2
	for tries := 0; tries < 10; tries++ {
		_, done, canceled, e := FollowTaskBatch(ctx, claim, 2, 15*time.Second)
		if e != nil {
			t.Fatal(e)
		}
		if canceled {
			t.Fatal("unexpected canceled backfill")
		}
		if claim.FailureCount != 0 {
			t.Fatalf("committed progress retained failure streak: %d", claim.FailureCount)
		}
		if done {
			break
		}
		if tries == 9 {
			t.Fatal("history did not complete")
		}
	}
	pull, err := FeedPosts(follower, 10, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	push, err := PushFeedPosts(follower, 10, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(push) != 4 || !reflect.DeepEqual(push, pull) {
		t.Fatalf("mismatch pull=%v push=%v", pull, push)
	}
	for i := 0; i < 4; i++ {
		if push[i].ID != fmt.Sprint(ids[3-i]) {
			t.Fatalf("same-second ordering at %d: %s", i, push[i].ID)
		}
	}
	// Explicit small-page keyset: all four same-second posts, no repeats.
	var scanned []string
	var before *time.Time
	var lastID int64
	for page := 0; page < 3; page++ {
		batch, e := PushFeedPosts(follower, 2, before, lastID)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range batch {
			scanned = append(scanned, item.ID)
		}
		if len(batch) == 0 {
			break
		}
		last := batch[len(batch)-1]
		v, e := strconv.ParseInt(last.ID, 10, 64)
		if e != nil {
			t.Fatal(e)
		}
		lastID = v
		copyTime := last.CreateTime
		before = &copyTime
	}
	if len(scanned) != 4 {
		t.Fatalf("keyset produced %v", scanned)
	}
	for i, id := range scanned {
		if id != fmt.Sprint(ids[3-i]) {
			t.Fatalf("small page ordering mismatch %v", scanned)
		}
	}
	if err = UnfollowAuthor(follower, actor); err != nil {
		t.Fatal(err)
	}
	if err = UnfollowAuthor(follower, actor); err != nil {
		t.Fatal(err)
	}
	visible, err := PushFeedPosts(follower, 10, nil, 0)
	if err != nil || len(visible) > 0 {
		t.Fatalf("inactive edge exposed posts: %v %v", visible, err)
	}
	if err = FollowAuthor(follower, actor); err != nil {
		t.Fatal(err)
	}
	if err = conn.Get(&gen, `SELECT generation FROM user_follow WHERE follower_id=? AND followee_id=?`, follower, actor); err != nil {
		t.Fatal(err)
	}
	if gen != 3 {
		t.Fatalf("refollow should be generation 3, got %d", gen)
	}
	// New-generation backfill must supersede old inbox ownership.
	claim, err = ClaimFollowTask(ctx, FollowBackfill, "t02-refollow", 15*time.Second, 12)
	if err != nil || claim == nil {
		t.Fatalf("refollow claim %v %v", claim, err)
	}
	// Force an expired lease on this isolated row, then check fencing.
	if _, err = conn.ExecContext(ctx, `UPDATE follow_delivery_task SET lease_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND) WHERE task_id=?`, claim.TaskID); err != nil {
		t.Fatal(err)
	}
	takenOver, e := ClaimFollowTask(ctx, FollowBackfill, "t02-takeover", 15*time.Second, 12)
	if e != nil || takenOver == nil || takenOver.ClaimGeneration <= claim.ClaimGeneration {
		t.Fatalf("invalid lease takeover %v %v", takenOver, e)
	}
	if _, _, _, e = FollowTaskBatch(ctx, claim, 2, 15*time.Second); !errors.Is(e, ErrFollowLeaseLost) {
		t.Fatalf("old generation advanced after takeover: %v", e)
	}
	claim = takenOver
	for tries := 0; tries < 10; tries++ {
		_, done, _, e := FollowTaskBatch(ctx, claim, 2, 15*time.Second)
		if e != nil {
			t.Fatal(e)
		}
		if done {
			break
		}
	}
	cleanup, err := ClaimFollowTask(ctx, FollowCleanup, "t02-clean", 15*time.Second, 12)
	if err != nil || cleanup == nil {
		t.Fatalf("cleanup claim %v %v", cleanup, err)
	}
	_, done, _, err := FollowTaskBatch(ctx, cleanup, 2, 15*time.Second)
	if err != nil || !done {
		t.Fatalf("old cleanup should skip new generation: %v done=%t", err, done)
	}
	push, err = PushFeedPosts(follower, 10, nil, 0)
	if err != nil || len(push) != 4 {
		t.Fatalf("old cleanup removed refollow rows: %v, err=%v", push, err)
	}
	var updated int
	if err = conn.Get(&updated, `SELECT COUNT(*) FROM feed_inbox WHERE recipient_id=? AND follow_generation=3`, follower); err != nil {
		t.Fatal(err)
	}
	if updated != 4 {
		t.Fatalf("new generation inbox rows=%d", updated)
	}
	// Deleting as a stranger is rejected; the author's repeated deletion is
	// idempotent and creates exactly one durable deletion event.
	if _, err = DeletePostWithDelivery(ctx, ids[0], foreign, base+501); !errors.Is(err, ErrDeleteForbidden) {
		t.Fatalf("foreign delete: %v", err)
	}
	changed, err := DeletePostWithDelivery(ctx, ids[0], actor, base+502)
	if err != nil || !changed {
		t.Fatalf("owner delete %t %v", changed, err)
	}
	changed, err = DeletePostWithDelivery(ctx, ids[0], actor, base+503)
	if err != nil || changed {
		t.Fatalf("repeat delete changed=%t err=%v", changed, err)
	}
	var deletedEvents int
	if err = conn.Get(&deletedEvents, `SELECT COUNT(*) FROM content_outbox WHERE post_id=? AND event_type='post.deleted'`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if deletedEvents != 1 {
		t.Fatalf("delete events=%d", deletedEvents)
	}
	pull, err = FeedPosts(follower, 10, nil, 0)
	if err != nil || len(pull) != 3 {
		t.Fatalf("pull after delete %v %v", pull, err)
	}
	push, err = PushFeedPosts(follower, 10, nil, 0)
	if err != nil || len(push) != 3 {
		t.Fatalf("push after delete %v %v", push, err)
	}
	deleteTask, err := ClaimProjection(ctx, ProjectionDeleteInbox, "t02-inbox-delete", 15*time.Second, 12)
	if err != nil || deleteTask == nil {
		t.Fatalf("claim delete inbox %v %v", deleteTask, err)
	}
	for tries := 0; tries < 5; tries++ {
		_, done, e := DeleteInboxBatch(ctx, deleteTask, 2, 15*time.Second)
		if e != nil {
			t.Fatal(e)
		}
		if done {
			break
		}
	}
	var residual int
	if err = conn.Get(&residual, `SELECT COUNT(*) FROM feed_inbox WHERE post_id=?`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if residual != 0 {
		t.Fatalf("delete inbox refs=%d", residual)
	}
}
