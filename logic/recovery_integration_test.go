//go:build integration
// +build integration

package logic

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	redigo "github.com/go-redis/redis"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/scut2024hjt/convo/dao/mysql"
	redisdao "github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/settings"
)

func TestRebuildRedisFromMySQL(t *testing.T) {
	dbName := os.Getenv("CONVO_T03_TEST_MYSQL_DB")
	if !strings.HasSuffix(dbName, "_t03") || os.Getenv("CONVO_T03_REDIS_INTEGRATION_OK") != "yes" {
		t.Skip("T03 isolated MySQL/Redis opt-in required")
	}
	mysqlPort, e := strconv.Atoi(os.Getenv("CONVO_T03_TEST_MYSQL_PORT"))
	if e != nil {
		t.Fatal("invalid MySQL test port")
	}
	redisPort, e := strconv.Atoi(os.Getenv("CONVO_T03_TEST_REDIS_PORT"))
	if e != nil {
		t.Fatal("invalid Redis test port")
	}
	redisDB, e := strconv.Atoi(os.Getenv("CONVO_T03_TEST_REDIS_DB"))
	if e != nil || redisDB <= 0 {
		t.Fatal("invalid isolated Redis DB")
	}
	mysqlConfig := &settings.MySQLConfig{Host: os.Getenv("CONVO_T03_TEST_MYSQL_HOST"), Port: mysqlPort, User: os.Getenv("CONVO_T03_TEST_MYSQL_USER"), Password: os.Getenv("CONVO_T03_TEST_MYSQL_PASSWORD"), DB: dbName, MaxOpenConnection: 4, MaxIdleConnection: 2}
	if err := mysql.Init(mysqlConfig); err != nil {
		t.Skipf("mysql integration service is unavailable: %v", err)
	}
	t.Cleanup(mysql.Close)
	cleanupDB, err := sqlx.Connect(
		"mysql",
		fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True",
			mysqlConfig.User, mysqlConfig.Password, mysqlConfig.Host, mysqlConfig.Port, mysqlConfig.DB,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanupDB.Close() })

	recoveryTestDB := redisDB
	redisConfig := &settings.RedisConfig{Host: os.Getenv("CONVO_T03_TEST_REDIS_HOST"), Port: redisPort, DB: recoveryTestDB, PoolSize: 8}
	if err := redisdao.Init(redisConfig); err != nil {
		t.Skipf("redis integration service is unavailable: %v", err)
	}
	t.Cleanup(redisdao.Close)
	assertClient := redigo.NewClient(&redigo.Options{
		Addr: fmt.Sprintf("%s:%d", os.Getenv("CONVO_T03_TEST_REDIS_HOST"), redisPort), DB: recoveryTestDB, PoolSize: 2,
	})
	if err := assertClient.FlushDB().Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = assertClient.FlushDB().Err()
		_ = assertClient.Close()
	})

	unique := time.Now().UnixNano()
	post := &models.Post{
		ID: unique, AuthorID: unique + 1, CommunityID: 1,
		Title: "redis recovery test", Content: "redis recovery test",
	}
	if err := mysql.CreatePost(post); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupEventPattern := fmt.Sprintf("%d-recovery-%%", unique)
		_, _ = cleanupDB.Exec(`DELETE FROM mq_consumed_message WHERE event_id LIKE ?`, cleanupEventPattern)
		_, _ = cleanupDB.Exec(`DELETE FROM post_vote WHERE post_id = ? AND user_id = ?`, post.ID, post.AuthorID)
		_, _ = cleanupDB.Exec(`DELETE FROM post WHERE post_id = ? AND author_id = ?`, post.ID, post.AuthorID)
	})

	events := []*models.VoteEvent{
		{
			EventID: fmt.Sprintf("%d-recovery-1", unique), EventType: models.LikeChangedEventType,
			UserID: strconv.FormatInt(post.AuthorID, 10), PostID: strconv.FormatInt(post.ID, 10),
			Direction: 1, Version: 1, OccurredAt: time.Now().Unix(),
		},
		{
			EventID: fmt.Sprintf("%d-recovery-2", unique), EventType: models.LikeChangedEventType,
			UserID: strconv.FormatInt(post.AuthorID, 10), PostID: strconv.FormatInt(post.ID, 10),
			Direction: 0, Version: 2, OccurredAt: time.Now().Unix(),
		},
	}
	for _, event := range events {
		if _, err := mysql.PersistVoteEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	storedPost, err := mysql.GetPostById(post.ID)
	if err != nil {
		t.Fatal(err)
	}

	report, err := RebuildRedisFromMySQL(100)
	if err != nil {
		t.Fatal(err)
	}
	if report.Posts < 1 || report.Votes < 1 {
		t.Fatalf("unexpected rebuild report: %#v", report)
	}
	if err = EnsureRedisStateReady(); err != nil {
		t.Fatalf("rebuilt state rejected by startup validation: %v", err)
	}

	postID := strconv.FormatInt(post.ID, 10)
	wantScore := float64(storedPost.CreateTime.Unix())
	gotScore, err := assertClient.ZScore("convo:post:score", postID).Result()
	if err != nil || gotScore != wantScore {
		t.Fatalf("score=%v want=%v err=%v", gotScore, wantScore, err)
	}
	if _, err := assertClient.ZScore("convo:post:voted:"+postID, strconv.FormatInt(post.AuthorID, 10)).Result(); err != redigo.Nil {
		t.Fatalf("cancelled like still present: %v", err)
	}
	version, err := assertClient.HGet("convo:post:vote:version:"+postID, strconv.FormatInt(post.AuthorID, 10)).Int64()
	if err != nil || version != 2 {
		t.Fatalf("version=%d err=%v", version, err)
	}
}
