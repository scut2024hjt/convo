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
	httpCount         map[string]uint64
	httpDuration      map[string]*histogram
	vote              map[string]uint64
	relay             map[string]uint64
	consumer          map[string]uint64
	endToEnd          *histogram
	outboxLength      float64
	outboxAge         float64
	outboxPending     float64
	outboxOK          float64
	outboxLastSuccess float64
}

var reg = registry{
	httpCount:    make(map[string]uint64),
	httpDuration: make(map[string]*histogram),
	vote:         make(map[string]uint64),
	relay:        make(map[string]uint64),
	consumer:     make(map[string]uint64),
	endToEnd:     &histogram{Buckets: make([]uint64, 10)},
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
	if route == "/api/v1/vote" {
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
	case "persisted", "duplicate", "same_version", "stale", "retry", "dlq":
	default:
		return
	}
	reg.Lock()
	reg.consumer[result]++
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
	return []byte(b.String())
}

// MetricsHandler is deliberately hosted on a distinct, non-public listener.
func MetricsHandler(w http.ResponseWriter, _ *http.Request) {
	payload := renderMetrics()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write(payload)
}
