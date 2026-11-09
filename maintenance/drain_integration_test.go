//go:build integration

package maintenance

import (
	"context"
	"fmt"
	goredis "github.com/go-redis/redis"
	redisdao "github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/mq"
	"github.com/scut2024hjt/convo/settings"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDrainRejectsUnknownCountsAndPendingWithoutEntries(t *testing.T) {
	if os.Getenv("CONVO_T03_REDIS_INTEGRATION_OK") != "yes" {
		t.Fatal("isolated Redis opt-in required")
	}
	port, err := strconv.Atoi(os.Getenv("CONVO_T03_TEST_REDIS_PORT"))
	if err != nil || port == 0 {
		t.Fatal("explicit Redis test port required")
	}
	cfg := &settings.RedisConfig{Host: "127.0.0.1", Port: port, DB: 14, PoolSize: 4}
	if err = redisdao.Init(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(redisdao.Close)
	r := goredis.NewClient(&goredis.Options{Addr: fmt.Sprintf("127.0.0.1:%d", port), DB: 14})
	t.Cleanup(func() { r.FlushDB(); r.Close() })
	r.FlushDB()
	conf := &settings.RabbitMQConfig{URL: "amqp://review:secret@127.0.0.1:5672/", VoteQueue: "convo.vote.persist.v1"}
	for _, tc := range []struct {
		name, target, body string
		status             int
		valid              bool
	}{
		{"all empty", "", `{"messages_ready":0,"messages_unacknowledged":0}`, 200, true},
		{"missing counts", "", `{}`, 200, false},
		{"query failure", "", `{}`, 503, false},
		{"invalid JSON", "", `not-json`, 200, false},
		{"main unacked", conf.VoteQueue, `{"messages_ready":0,"messages_unacknowledged":1}`, 200, false},
		{"retry pending", mq.RetryQueue, `{"messages_ready":1,"messages_unacknowledged":0}`, 200, false},
		{"DLQ pending", mq.DeadQueue, `{"messages_ready":1,"messages_unacknowledged":0}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				body := `{"messages_ready":0,"messages_unacknowledged":0}`
				status := 200
				if tc.target == "" || strings.Contains(req.URL.Path, tc.target) {
					body = tc.body
					status = tc.status
				}
				w.WriteHeader(status)
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := CheckDrain(ctx, server.URL, conf)
			if (err == nil) != tc.valid {
				t.Fatalf("drain err=%v want valid=%v", err, tc.valid)
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var stream string
	if err = redisdao.EnsureVoteOutboxGroup(); err != nil {
		t.Fatal(err)
	}
	// Locate the unique group-created stream without coupling to a hand-written key.
	keys, err := r.Keys("*").Result()
	if err != nil || len(keys) != 1 {
		t.Fatalf("stream keys %v %v", keys, err)
	}
	stream = keys[0]
	id, err := r.XAdd(&goredis.XAddArgs{Stream: stream, Values: map[string]interface{}{"event_id": "pending"}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CheckDrain(ctx, "http://127.0.0.1:1", conf); err == nil {
		t.Fatal("non-empty stream allowed")
	}
	if _, err = r.XReadGroup(&goredis.XReadGroupArgs{Group: redisdao.VoteOutboxGroup, Consumer: "drain-fixture", Streams: []string{stream, ">"}, Count: 1}).Result(); err != nil {
		t.Fatal(err)
	}
	if err = r.XDel(stream, id).Err(); err != nil {
		t.Fatal(err)
	}
	sample, err := CheckDrain(ctx, "http://127.0.0.1:1", conf)
	if err == nil || sample == nil || sample.StreamLength != 0 || sample.Pending != 1 {
		t.Fatalf("empty stream with PEL accepted: %#v %v", sample, err)
	}
}
