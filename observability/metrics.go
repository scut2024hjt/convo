// Package observability exposes bounded-cardinality Prometheus metrics without
// adding an instrumentation dependency to the existing application's go.mod.
package observability

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type histogram struct {
	Buckets []uint64
	Count   uint64
	Sum     float64
}

type registry struct {
	sync.Mutex
	httpCount          map[string]uint64
	httpDuration       map[string]*histogram
	vote               map[string]uint64
	likeReadFallback   map[string]uint64
	relay              map[string]uint64
	consumer           map[string]uint64
	endToEnd           *histogram
	outboxLength       float64
	outboxAge          float64
	outboxPending      float64
	outboxOK           float64
	outboxLastSuccess  float64
	contentTasks       map[string]uint64
	contentLatency     map[string]*histogram
	contentDelivered   uint64
	contentSnapshot    map[string]float64
	contentOldest      map[string]float64
	contentSampleOK    float64
	contentLastSuccess float64
	followTasks        map[string]uint64
	followRows         map[string]uint64
	followLatency      map[string]*histogram
	followSnapshot     map[string]float64
	followOldest       map[string]float64
	followSampleOK     float64
	followLastSuccess  float64
}

var reg = registry{
	httpCount:        make(map[string]uint64),
	httpDuration:     make(map[string]*histogram),
	vote:             make(map[string]uint64),
	likeReadFallback: make(map[string]uint64),
	relay:            make(map[string]uint64),
	consumer:         make(map[string]uint64),
	endToEnd:         &histogram{Buckets: make([]uint64, 10)},
	contentTasks:     make(map[string]uint64),
	contentLatency:   make(map[string]*histogram),
	contentSnapshot:  make(map[string]float64),
	contentOldest:    make(map[string]float64),
	followTasks:      make(map[string]uint64),
	followRows:       make(map[string]uint64),
	followLatency:    make(map[string]*histogram),
	followSnapshot:   make(map[string]float64),
	followOldest:     make(map[string]float64),
}

var httpBounds = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}
var persistBounds = []float64{.1, .5, 1, 2.5, 5, 10, 30, 60, 120, 300}

func observe(h *histogram, value float64, bounds []float64) {
	if value < 0 {
		value = 0
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	h.Count++
	h.Sum += value
	for i, bound := range bounds {
		if value <= bound {
			h.Buckets[i]++
		}
	}
}

// normalizeHTTPMethod bounds cardinality for arbitrary extension/custom methods.
// Method names supplied by clients must not create unbounded time series.
func normalizeHTTPMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
		http.MethodTrace, http.MethodConnect:
		return method
	default:
		return "other"
	}
}

func RecordHTTP(method, route string, status int, code int64, duration time.Duration, voteUnchanged bool) {
	method = normalizeHTTPMethod(method)
	if route == "" {
		route = "unmatched"
	}
	// FullPath is a static Gin route template; never use raw URLs or IDs.
	key := method + "\x00" + route + "\x00" + strconv.Itoa(status) + "\x00" + strconv.FormatInt(code, 10)
	reg.Lock()
	reg.httpCount[key]++
	h := reg.httpDuration[key]
	if h == nil {
		h = &histogram{Buckets: make([]uint64, len(httpBounds))}
		reg.httpDuration[key] = h
	}
	observe(h, duration.Seconds(), httpBounds)
	if (route == "/api/v1/vote" && method == "POST") ||
		(route == "/api/v1/post/:id/like" && (method == "PUT" || method == "DELETE")) {
		// Business codes are set by the central response helpers and also cover
		// JWT and rate-limit middleware aborts (which may return HTTP 200).
		result := "server_error"
		switch code {
		case 1000:
			result = "changed"
			if voteUnchanged {
				result = "unchanged"
			}
		case 1001, 1008, 1009:
			result = "business_rejected"
		case 1014:
			result = "index_pending"
		case 1006, 1007, 1011:
			result = "auth_failed"
		case 1012:
			result = "rate_limited"
		}
		reg.vote[result]++
	}
	reg.Unlock()
}

func RecordRelay(result string) {
	if result != "confirmed" && result != "failed" {
		return
	}
	reg.Lock()
	reg.relay[result]++
	reg.Unlock()
}

func RecordConsumer(result string) {
	switch result {
	case "persisted", "duplicate", "same_version", "stale", "retry", "dlq", "legacy_rejected", "like_conflict":
	default:
		return
	}
	reg.Lock()
	reg.consumer[result]++
	reg.Unlock()
}

// RecordLikeReadFallback counts only a fixed, bounded result label.
func RecordLikeReadFallback(result string) {
	if result != "mysql" && result != "error" {
		return
	}
	reg.Lock()
	reg.likeReadFallback[result]++
	reg.Unlock()
}

func RecordPersistLatency(occurredAt int64) {
	if occurredAt <= 0 {
		return
	}
	seconds := time.Since(time.Unix(occurredAt, 0)).Seconds()
	reg.Lock()
	observe(reg.endToEnd, seconds, persistBounds)
	reg.Unlock()
}

// Failure retains previous queue values, sets sample_ok=0, and exposes a stale timestamp.
// This avoids silently interpreting an outage as an empty stream.
func RecordOutboxSample(length, age, pending float64, err error) {
	reg.Lock()
	defer reg.Unlock()
	if err != nil {
		reg.outboxOK = 0
		return
	}
	reg.outboxLength = length
	reg.outboxAge = age
	reg.outboxPending = pending
	reg.outboxOK = 1
	reg.outboxLastSuccess = float64(time.Now().Unix())
}

// ContentTaskSample is intentionally made of bounded status/kind labels.
type ContentTaskSample struct {
	Kind      string
	Status    string
	Count     int64
	OldestAge float64
}

func validContentKind(kind string) bool {
	return kind == "index" || kind == "fanout" || kind == "delete_index" || kind == "delete_inbox"
}
func validContentStatus(status string) bool {
	switch status {
	case "pending", "running", "retry", "done", "failed":
		return true
	}
	return false
}
func RecordContentTask(kind, result string, elapsed time.Duration) {
	if !validContentKind(kind) {
		return
	}
	switch result {
	case "done", "yielded", "retry", "failed", "lost_lease", "retry_error", "interrupted":
	default:
		return
	}
	key := kind + "\x00" + result
	reg.Lock()
	reg.contentTasks[key]++
	h := reg.contentLatency[key]
	if h == nil {
		h = &histogram{Buckets: make([]uint64, len(persistBounds))}
		reg.contentLatency[key] = h
	}
	observe(h, elapsed.Seconds(), persistBounds)
	reg.Unlock()
}
func RecordContentDelivered(n int64) {
	if n <= 0 {
		return
	}
	reg.Lock()
	reg.contentDelivered += uint64(n)
	reg.Unlock()
}
func RecordContentQueueSample(samples []ContentTaskSample, err error) {
	reg.Lock()
	defer reg.Unlock()
	if err != nil {
		reg.contentSampleOK = 0
		return
	}
	counts := make(map[string]float64)
	oldest := make(map[string]float64)
	for _, s := range samples {
		if !validContentKind(s.Kind) || !validContentStatus(s.Status) {
			continue
		}
		key := s.Kind + "\x00" + s.Status
		counts[key] = float64(s.Count)
		oldest[s.Kind] = math.Max(oldest[s.Kind], s.OldestAge)
	}
	reg.contentSnapshot = counts
	reg.contentOldest = oldest
	reg.contentSampleOK = 1
	reg.contentLastSuccess = float64(time.Now().Unix())
}

// FollowTaskSample is a sampled global state and is collected from a single
// designated app instance. No user, edge, task, or post IDs enter labels.
type FollowTaskSample struct {
	Kind      string
	Status    string
	Count     int64
	OldestAge float64
}

func validFollowKind(kind string) bool     { return kind == "backfill" || kind == "cleanup" }
func validFollowStatus(status string) bool { return validContentStatus(status) || status == "canceled" }
func RecordFollowTask(kind, result string, elapsed time.Duration) {
	if !validFollowKind(kind) {
		return
	}
	switch result {
	case "done", "canceled", "yielded", "retry", "failed", "lost_lease", "retry_error", "interrupted":
	default:
		return
	}
	key := kind + "\x00" + result
	reg.Lock()
	reg.followTasks[key]++
	h := reg.followLatency[key]
	if h == nil {
		h = &histogram{Buckets: make([]uint64, len(persistBounds))}
		reg.followLatency[key] = h
	}
	observe(h, elapsed.Seconds(), persistBounds)
	reg.Unlock()
}
func RecordFollowRows(kind string, n int64) {
	if !validFollowKind(kind) || n <= 0 {
		return
	}
	reg.Lock()
	reg.followRows[kind] += uint64(n)
	reg.Unlock()
}
func RecordFollowQueueSample(samples []FollowTaskSample, err error) {
	reg.Lock()
	defer reg.Unlock()
	if err != nil {
		reg.followSampleOK = 0
		return
	}
	counts := make(map[string]float64)
	oldest := make(map[string]float64)
	for _, sample := range samples {
		if !validFollowKind(sample.Kind) || !validFollowStatus(sample.Status) {
			continue
		}
		counts[sample.Kind+"\x00"+sample.Status] = float64(sample.Count)
		oldest[sample.Kind] = math.Max(oldest[sample.Kind], sample.OldestAge)
	}
	reg.followSnapshot, reg.followOldest = counts, oldest
	reg.followSampleOK = 1
	reg.followLastSuccess = float64(time.Now().Unix())
}

func metricLine(b *strings.Builder, name, labels string, value interface{}) {
	if labels != "" {
		fmt.Fprintf(b, "%s{%s} %v\n", name, labels, value)
	} else {
		fmt.Fprintf(b, "%s %v\n", name, value)
	}
}

func outputHistogram(b *strings.Builder, prefix, labels string, h *histogram, bounds []float64) {
	for i, bound := range bounds {
		lb := "le=\"" + strconv.FormatFloat(bound, 'f', -1, 64) + "\""
		if labels != "" {
			lb = labels + "," + lb
		}
		metricLine(b, prefix+"_bucket", lb, h.Buckets[i])
	}
	lb := `le="+Inf"`
	if labels != "" {
		lb = labels + "," + lb
	}
	metricLine(b, prefix+"_bucket", lb, h.Count)
	metricLine(b, prefix+"_sum", labels, strconv.FormatFloat(h.Sum, 'f', -1, 64))
	metricLine(b, prefix+"_count", labels, h.Count)
}

// renderMetrics snapshots the registry as text under the lock, but does not
// interact with the network. HTTP writes must happen after the lock is released.
func renderMetrics() []byte {
	reg.Lock()
	defer reg.Unlock()
	var b strings.Builder
	b.WriteString("# HELP convo_http_requests_total HTTP requests including business failures.\n# TYPE convo_http_requests_total counter\n")
	keys := make([]string, 0, len(reg.httpCount))
	for k := range reg.httpCount {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fields := strings.Split(k, "\x00")
		// Gin route patterns are fixed, but escape labels defensively.
		labels := fmt.Sprintf("method=%q,route=%q,status=%q,code=%q", fields[0], fields[1], fields[2], fields[3])
		metricLine(&b, "convo_http_requests_total", labels, reg.httpCount[k])
	}
	b.WriteString("# HELP convo_http_request_duration_seconds HTTP response latency.\n# TYPE convo_http_request_duration_seconds histogram\n")
	for _, k := range keys {
		fields := strings.Split(k, "\x00")
		labels := fmt.Sprintf("method=%q,route=%q,status=%q,code=%q", fields[0], fields[1], fields[2], fields[3])
		outputHistogram(&b, "convo_http_request_duration_seconds", labels, reg.httpDuration[k], httpBounds)
	}
	for _, group := range []struct {
		name, help string
		counts     map[string]uint64
	}{
		{"convo_vote_requests_total", "Vote business outcomes.", reg.vote},
		{"convo_vote_relay_publish_total", "Outbox events published to RabbitMQ.", reg.relay},
		{"convo_vote_consumer_total", "Vote persistence and delivery outcomes.", reg.consumer},
		{"convo_like_read_fallback_total", "Authenticated like reads served from persisted MySQL or failed.", reg.likeReadFallback},
	} {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n", group.name, group.help, group.name)
		results := make([]string, 0, len(group.counts))
		for r := range group.counts {
			results = append(results, r)
		}
		sort.Strings(results)
		for _, r := range results {
			metricLine(&b, group.name, fmt.Sprintf("result=%q", r), group.counts[r])
		}
	}
	b.WriteString("# HELP convo_vote_persist_latency_seconds Seconds from event timestamp to committed MySQL state advancement (second resolution).\n# TYPE convo_vote_persist_latency_seconds histogram\n")
	outputHistogram(&b, "convo_vote_persist_latency_seconds", "", reg.endToEnd, persistBounds)
	for _, sample := range []struct {
		name, help string
		value      float64
	}{
		{"convo_vote_outbox_length", "Undelivered Redis Stream entries (includes pending, do not add pending).", reg.outboxLength},
		{"convo_vote_outbox_oldest_age_seconds", "Age of oldest undelivered event; zero when empty and sample healthy.", reg.outboxAge},
		{"convo_vote_outbox_pending", "Pending subset of Stream entries.", reg.outboxPending},
		{"convo_vote_outbox_sample_ok", "1 if latest Redis sample succeeded, otherwise 0.", reg.outboxOK},
		{"convo_vote_outbox_last_success_timestamp_seconds", "Unix timestamp of last successful Redis sample.", reg.outboxLastSuccess},
	} {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", sample.name, sample.help, sample.name)
		metricLine(&b, sample.name, "", strconv.FormatFloat(sample.value, 'f', -1, 64))
	}
	b.WriteString("# HELP convo_content_projection_total Durable content projection outcomes.\n# TYPE convo_content_projection_total counter\n")
	ckeys := make([]string, 0, len(reg.contentTasks))
	for key := range reg.contentTasks {
		ckeys = append(ckeys, key)
	}
	sort.Strings(ckeys)
	for _, key := range ckeys {
		a := strings.Split(key, "\x00")
		labels := fmt.Sprintf("kind=%q,result=%q", a[0], a[1])
		metricLine(&b, "convo_content_projection_total", labels, reg.contentTasks[key])
	}
	b.WriteString("# HELP convo_content_projection_duration_seconds Time spent processing each claimed task.\n# TYPE convo_content_projection_duration_seconds histogram\n")
	for _, key := range ckeys {
		a := strings.Split(key, "\x00")
		labels := fmt.Sprintf("kind=%q,result=%q", a[0], a[1])
		outputHistogram(&b, "convo_content_projection_duration_seconds", labels, reg.contentLatency[key], persistBounds)
	}
	b.WriteString("# HELP convo_content_delivered_total New inbox references committed.\n# TYPE convo_content_delivered_total counter\n")
	metricLine(&b, "convo_content_delivered_total", "", reg.contentDelivered)
	b.WriteString("# HELP convo_content_task_state Durable task counts (sample once per database).\n# TYPE convo_content_task_state gauge\n")
	for _, kind := range []string{"index", "fanout", "delete_index", "delete_inbox"} {
		for _, status := range []string{"pending", "running", "retry", "done", "failed"} {
			labels := fmt.Sprintf("kind=%q,status=%q", kind, status)
			metricLine(&b, "convo_content_task_state", labels, reg.contentSnapshot[kind+"\x00"+status])
		}
	}
	b.WriteString("# HELP convo_content_oldest_unfinished_seconds Oldest unfinished task age.\n# TYPE convo_content_oldest_unfinished_seconds gauge\n")
	for _, kind := range []string{"index", "fanout", "delete_index", "delete_inbox"} {
		metricLine(&b, "convo_content_oldest_unfinished_seconds", fmt.Sprintf("kind=%q", kind), reg.contentOldest[kind])
	}
	b.WriteString("# HELP convo_content_queue_sample_ok Latest database gauge sample succeeded.\n# TYPE convo_content_queue_sample_ok gauge\n")
	metricLine(&b, "convo_content_queue_sample_ok", "", reg.contentSampleOK)
	b.WriteString("# HELP convo_content_queue_last_success_timestamp_seconds Last successful DB sample unix timestamp.\n# TYPE convo_content_queue_last_success_timestamp_seconds gauge\n")
	metricLine(&b, "convo_content_queue_last_success_timestamp_seconds", "", reg.contentLastSuccess)
	b.WriteString("# HELP convo_follow_task_total Durable follow task outcomes.\n# TYPE convo_follow_task_total counter\n")
	fkeys := make([]string, 0, len(reg.followTasks))
	for key := range reg.followTasks {
		fkeys = append(fkeys, key)
	}
	sort.Strings(fkeys)
	for _, key := range fkeys {
		a := strings.Split(key, "\x00")
		metricLine(&b, "convo_follow_task_total", fmt.Sprintf("kind=%q,result=%q", a[0], a[1]), reg.followTasks[key])
	}
	b.WriteString("# HELP convo_follow_task_duration_seconds Duration per claimed follow task.\n# TYPE convo_follow_task_duration_seconds histogram\n")
	for _, key := range fkeys {
		a := strings.Split(key, "\x00")
		outputHistogram(&b, "convo_follow_task_duration_seconds", fmt.Sprintf("kind=%q,result=%q", a[0], a[1]), reg.followLatency[key], persistBounds)
	}
	b.WriteString("# HELP convo_follow_rows_total Inbox rows inserted or removed by follow tasks.\n# TYPE convo_follow_rows_total counter\n")
	for _, kind := range []string{"backfill", "cleanup"} {
		metricLine(&b, "convo_follow_rows_total", fmt.Sprintf("kind=%q", kind), reg.followRows[kind])
	}
	b.WriteString("# HELP convo_follow_task_state Durable relation task states; sample on one instance.\n# TYPE convo_follow_task_state gauge\n")
	for _, kind := range []string{"backfill", "cleanup"} {
		for _, status := range []string{"pending", "running", "retry", "done", "failed", "canceled"} {
			metricLine(&b, "convo_follow_task_state", fmt.Sprintf("kind=%q,status=%q", kind, status), reg.followSnapshot[kind+"\x00"+status])
		}
	}
	b.WriteString("# HELP convo_follow_oldest_unfinished_seconds Age of oldest unfinished follow task.\n# TYPE convo_follow_oldest_unfinished_seconds gauge\n")
	for _, kind := range []string{"backfill", "cleanup"} {
		metricLine(&b, "convo_follow_oldest_unfinished_seconds", fmt.Sprintf("kind=%q", kind), reg.followOldest[kind])
	}
	b.WriteString("# HELP convo_follow_queue_sample_ok Whether last MySQL follow-task sampler succeeded.\n# TYPE convo_follow_queue_sample_ok gauge\n")
	metricLine(&b, "convo_follow_queue_sample_ok", "", reg.followSampleOK)
	b.WriteString("# HELP convo_follow_queue_last_success_timestamp_seconds Last successful follow sampling time.\n# TYPE convo_follow_queue_last_success_timestamp_seconds gauge\n")
	metricLine(&b, "convo_follow_queue_last_success_timestamp_seconds", "", reg.followLastSuccess)
	return []byte(b.String())
}

// MetricsHandler is deliberately hosted on a distinct, non-public listener.
func MetricsHandler(w http.ResponseWriter, _ *http.Request) {
	payload := renderMetrics()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write(payload)
}
