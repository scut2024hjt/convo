//go:build integration

package mysql

import (
	"context"
	"database/sql"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/scut2024hjt/convo/models"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLikeMigrationBatchesResumeAndCheckEnforcement(t *testing.T) {
	dsn := os.Getenv("T03_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Fatal("explicit T03_TEST_MYSQL_DSN required")
	}
	conn, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	var name string
	conn.Get(&name, "SELECT DATABASE()")
	if !strings.HasSuffix(name, "_t03") {
		t.Fatal("isolated *_t03 DB required")
	}
	old := db
	db = conn
	defer func() { db = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base := time.Now().UnixNano() / 1000
	exec := func(q string, args ...interface{}) {
		t.Helper()
		if _, e := conn.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec("ALTER TABLE post_vote DROP CHECK chk_post_vote_direction, ADD CONSTRAINT chk_post_vote_direction CHECK (direction IN (-1,0,1))")
	t.Cleanup(func() {
		if _, e := conn.Exec("DELETE FROM post_vote WHERE user_id BETWEEN ? AND ?", base, base+3); e != nil {
			t.Error(e)
		}
		if _, e := conn.Exec("ALTER TABLE post_vote DROP CHECK chk_post_vote_direction, ADD CONSTRAINT chk_post_vote_direction CHECK (direction IN (0,1))"); e != nil {
			t.Error(e)
		}
		if _, e := conn.Exec("UPDATE like_semantics_state SET phase='ready',last_user_id=0,last_post_id=0,scanned_count=0,converted_count=0 WHERE id=1"); e != nil {
			t.Error(e)
		}
	})
	exec("UPDATE like_semantics_state SET phase='pending',last_user_id=0,last_post_id=0,scanned_count=0,converted_count=0 WHERE id=1")
	for i, d := range []int{-1, 0, 1} {
		exec("INSERT INTO post_vote(user_id,post_id,direction,version,last_event_id) VALUES(?,?,?,?,?)", base+int64(i), base, d, []int{7, 12, 9}[i], "migration-source")
	}
	exec("INSERT INTO post_vote(user_id,post_id,direction,version,last_event_id) VALUES(?,?,?,?,?)", base+3, base, -1, models.MaxLikeVersion, "overflow-source")
	p, n, done, err := ConvertLikeBatch(ctx, 1)
	if err != nil || n != 1 || done || p.Converted != 1 {
		t.Fatalf("first batch %#v %d %v %v", p, n, done, err)
	}
	// A new call reloads the committed checkpoint; no per-process cursor is used.
	for i := 0; i < 2; i++ {
		if _, _, _, err = ConvertLikeBatch(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint, _ := GetLikeMigrationProgress(ctx)
	if _, _, _, err = ConvertLikeBatch(ctx, 1); err == nil {
		t.Fatal("unsafe version accepted")
	}
	unchanged, _ := GetLikeMigrationProgress(ctx)
	if unchanged.Scanned != checkpoint.Scanned || unchanged.LastUserID != checkpoint.LastUserID {
		t.Fatal("failed batch advanced checkpoint")
	}
	exec("UPDATE post_vote SET version=2 WHERE user_id=? AND post_id=?", base+3, base)
	for i := 0; i < 3; i++ {
		p, _, done, err = ConvertLikeBatch(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if !done || p.Converted != 2 || p.Scanned != 4 {
		t.Fatalf("final %#v done=%v", p, done)
	}
	var version int64
	var eventID string
	if err = conn.QueryRow("SELECT version,last_event_id FROM post_vote WHERE user_id=? AND post_id=?", base, base).Scan(&version, &eventID); err != nil || version != 8 || eventID != "migration-source" {
		t.Fatalf("negative conversion version=%d event=%s err=%v", version, eventID, err)
	}
	if _, n, done, err = ConvertLikeBatch(ctx, 1); err != nil || n != 0 || !done {
		t.Fatalf("repeat conversion %d %v %v", n, done, err)
	}
	if err = LikeMigrationReady(ctx); err == nil {
		t.Fatal("converted migration allowed serving")
	}
	if err = TightenLikeDirectionCheck(ctx); err != nil {
		t.Fatal(err)
	}
	if err = LikeMigrationRebuildable(ctx); err != nil {
		t.Fatal(err)
	}
	if err = CompleteLikeMigration(ctx); err != nil {
		t.Fatal(err)
	}
	if err = LikeMigrationReady(ctx); err != nil {
		t.Fatal(err)
	}
	exec("ALTER TABLE post_vote ALTER CHECK chk_post_vote_direction NOT ENFORCED")
	if err = LikeMigrationReady(ctx); err == nil {
		t.Fatal("disabled direction CHECK passed ready gate")
	}
	exec("ALTER TABLE post_vote ALTER CHECK chk_post_vote_direction ENFORCED")
	var ignored int
	if err = conn.Get(&ignored, "SELECT 1 FROM post_vote WHERE user_id=? AND post_id=? AND direction=-1", base, base); err != sql.ErrNoRows {
		t.Fatalf("negative remains: %v", err)
	}
}
