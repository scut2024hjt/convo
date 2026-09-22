//go:build integration
// +build integration

package mysql

import (
	"testing"
	"time"

	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/settings"
)

func TestCreatePost(t *testing.T) {
	dbConfig := settings.MySQLConfig{ // 测试的数据库
		Host:              "127.0.0.1",
		User:              "root",
		Password:          "123456",
		DB:                "convo",
		Port:              33306,
		MaxOpenConnection: 10,
		MaxIdleConnection: 10,
	}
	err := Init(&dbConfig)
	if err != nil {
		t.Skipf("mysql integration service is unavailable: %v", err)
	}
	defer Close()
	p := &models.Post{
		ID:          time.Now().UnixNano(),
		AuthorID:    123,
		CommunityID: 1,
		Title:       "test",
		Content:     "just a test",
	}
	defer func() { _, _ = db.Exec(`DELETE FROM post WHERE post_id = ?`, p.ID) }()
	err = CreatePost(p)
	if err != nil {
		t.Fatalf("CreatePost insert record into mysql failed, err: %v\n", err)
	}
	t.Logf("CreatePost insert record into mysql success")
}
