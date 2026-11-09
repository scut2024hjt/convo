// Package maintenance implements fail-closed maintenance-window checks; it does
// not pause writers or alter RabbitMQ queues. Operators must stop old binaries.
package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	redisdao "github.com/scut2024hjt/convo/dao/redis"
	"github.com/scut2024hjt/convo/mq"
	"github.com/scut2024hjt/convo/settings"
)

type QueueSample struct {
	Name    string `json:"-"`
	Ready   *int64 `json:"messages_ready"`
	Unacked *int64 `json:"messages_unacknowledged"`
}
type DrainSample struct {
	StreamLength int64         `json:"stream_length"`
	Pending      int64         `json:"pending"`
	Queues       []QueueSample `json:"queues"`
}

func CheckDrain(ctx context.Context, managementURL string, conf *settings.RabbitMQConfig) (*DrainSample, error) {
	if managementURL == "" {
		return nil, fmt.Errorf("rabbitmq management URL is required")
	}
	length, pending, err := redisdao.DrainVoteOutbox()
	if err != nil {
		return nil, fmt.Errorf("redis stream/PEL query: %w", err)
	}
	report := &DrainSample{StreamLength: length, Pending: pending}
	if length != 0 || pending != 0 {
		return report, fmt.Errorf("vote outbox not drained: length=%d PEL=%d", length, pending)
	}
	amqpURI, err := url.Parse(conf.URL)
	if err != nil {
		return report, err
	}
	vhost := strings.TrimPrefix(amqpURI.EscapedPath(), "/")
	if vhost == "" {
		vhost = "%2F"
	} else {
		vhost = decodeAndEscape(vhost)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	base := strings.TrimRight(managementURL, "/")
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return report, fmt.Errorf("invalid rabbit management URL")
	}
	for _, name := range []string{conf.VoteQueue, mq.RetryQueue, mq.DeadQueue} {
		target := base + "/api/queues/" + vhost + "/" + url.PathEscape(name)
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if e != nil {
			return report, e
		}
		user := os.Getenv("CONVO_RABBITMQ_MANAGEMENT_USER")
		password := os.Getenv("CONVO_RABBITMQ_MANAGEMENT_PASSWORD")
		if user == "" && amqpURI.User != nil {
			user = amqpURI.User.Username()
			password, _ = amqpURI.User.Password()
		}
		req.SetBasicAuth(user, password)
		resp, e := client.Do(req)
		if e != nil {
			return report, fmt.Errorf("rabbit management query failed: %w", e)
		}
		var sample QueueSample
		sample.Name = name
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return report, fmt.Errorf("rabbit management queue %q HTTP %d", name, resp.StatusCode)
		}
		e = json.NewDecoder(resp.Body).Decode(&sample)
		resp.Body.Close()
		if e != nil {
			return report, fmt.Errorf("rabbit management JSON for %q: %w", name, e)
		}
		if sample.Ready == nil || sample.Unacked == nil {
			return report, fmt.Errorf("rabbit management queue %q missing counts", name)
		}
		report.Queues = append(report.Queues, sample)
		if *sample.Ready != 0 || *sample.Unacked != 0 {
			return report, fmt.Errorf("queue %q not drained: ready=%d unacked=%d", name, *sample.Ready, *sample.Unacked)
		}
	}
	return report, nil
}
func decodeAndEscape(s string) string {
	v, e := url.PathUnescape(s)
	if e != nil {
		return s
	}
	return url.PathEscape(v)
}
