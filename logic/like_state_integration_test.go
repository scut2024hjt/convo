//go:build integration

package logic

import (
	"errors"
	"fmt"
	goredis "github.com/go-redis/redis"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/scut2024hjt/convo/dao/mysql"
	redisdao "github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/settings"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOwnLikeVisibilityAndPersistedFallback(t *testing.T) {
	if os.Getenv("CONVO_T03_REDIS_INTEGRATION_OK") != "yes" {
		t.Fatal("isolated Redis opt-in required")
	}
	dbName := os.Getenv("CONVO_T03_TEST_MYSQL_DB")
	if !strings.HasSuffix(dbName, "_t03") {
		t.Fatal("isolated DB required")
	}
	mp, _ := strconv.Atoi(os.Getenv("CONVO_T03_TEST_MYSQL_PORT"))
	rp, _ := strconv.Atoi(os.Getenv("CONVO_T03_TEST_REDIS_PORT"))
	pwd := os.Getenv("CONVO_T03_TEST_MYSQL_PASSWORD")
	cfg := &settings.MySQLConfig{Host: "127.0.0.1", Port: mp, User: "root", Password: pwd, DB: dbName, MaxOpenConnection: 4, MaxIdleConnection: 2}
	if err := mysql.Init(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mysql.Close)
	conn, err := sqlx.Connect("mysql", fmt.Sprintf("root:%s@tcp(127.0.0.1:%d)/%s?parseTime=true", pwd, mp, dbName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	rcfg := &settings.RedisConfig{Host: "127.0.0.1", Port: rp, DB: 13, PoolSize: 4}
	if err = redisdao.Init(rcfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(redisdao.Close)
	r := goredis.NewClient(&goredis.Options{Addr: fmt.Sprintf("127.0.0.1:%d", rp), DB: 13})
	r.FlushDB()
	t.Cleanup(func() { r.FlushDB(); r.Close() })
	base := time.Now().UnixNano() / 1000
	visible, deleted := base, base+1
	uid := base + 2
	for _, id := range []int64{visible, deleted} {
		if _, err = conn.Exec("INSERT INTO post(post_id,title,content,author_id,community_id,status) VALUES(?,'state','state',?,1,?)", id, uid, map[bool]int{true: 1, false: 0}[id == visible]); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if _, err := conn.Exec("DELETE FROM post_vote WHERE user_id=?", uid); err != nil {
			t.Error(err)
		}
		if _, err := conn.Exec("DELETE FROM post WHERE post_id IN (?,?)", visible, deleted); err != nil {
			t.Error(err)
		}
	})
	if _, err = conn.Exec("INSERT INTO post_vote(user_id,post_id,direction,version,last_event_id) VALUES(?,?,1,7,'fallback-fixture')", uid, visible); err != nil {
		t.Fatal(err)
	}
	if err = redisdao.CreatePost(visible, 1); err != nil {
		t.Fatal(err)
	}
	redisdao.MarkVoteStateReady()
	r.ZAdd("convo:post:voted:"+strconv.FormatInt(visible, 10), goredis.Z{Score: 1, Member: uid})
	r.HSet("convo:post:vote:version:"+strconv.FormatInt(visible, 10), strconv.FormatInt(uid, 10), 7)
	r.Set("convo:post:voted:"+strconv.FormatInt(deleted, 10), "invalid invisible fixture", 0)
	states, err := ReadOwnLikes(uid, []int64{deleted, visible})
	if err != nil || len(states) != 2 || states[0].Code != 1009 || !states[1].Liked || states[1].Version != "7" || states[1].Source != "redis" {
		t.Fatalf("visible-only batch %#v %v", states, err)
	}
	redisdao.Close()
	states, err = ReadOwnLikes(uid, []int64{visible, deleted})
	if err != nil || !states[0].Liked || states[0].Version != "7" || states[0].Freshness != "persisted" || states[0].Source != "mysql" || states[1].Code != 1009 {
		t.Fatalf("persisted fallback %#v %v", states, err)
	}
	if err = redisdao.Init(rcfg); err != nil {
		t.Fatal(err)
	}
	r.Set("convo:state:post-vote-index", redisdao.VoteStateRebuilding, 0)
	if _, err = ReadOwnLikes(uid, []int64{visible}); !errors.Is(err, redisdao.ErrVoteStateUnavailable) {
		t.Fatalf("rebuilding returned fallback: %v", err)
	}
}
