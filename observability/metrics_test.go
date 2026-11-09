package observability

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsKeepRouteTemplatesAndBusinessFailure(t *testing.T) {
	RecordHTTP("POST", "/api/v1/vote", 200, 1012, 13*time.Millisecond, false)
	RecordHTTP("GET", "", 404, 0, time.Millisecond, false)
	RecordHTTP("POST", "/api/v1/vote", 200, 1000, time.Millisecond, true)
	r := httptest.NewRecorder()
	MetricsHandler(r, httptest.NewRequest("GET", "/metrics", nil))
	body := r.Body.String()
	for _, part := range []string{`route="/api/v1/vote"`, `result="rate_limited"`, `result="unchanged"`, `route="unmatched"`, `le="+Inf"`, "convo_vote_outbox_sample_ok"} {
		if !strings.Contains(body, part) {
			t.Errorf("missing %s in metrics", part)
		}
	}
}

// Multiple arbitrary extension methods must collapse into one method label.
func TestCustomMethodsUseOtherLabel(t *testing.T) {
	for _, method := range []string{"TEST_ONE", "TEST_TWO", "CUSTOM_83"} {
		RecordHTTP(method, "/api/v1/feed", 200, 1000, time.Millisecond, false)
	}
	r := httptest.NewRecorder()
	MetricsHandler(r, httptest.NewRequest("GET", "/metrics", nil))
	body := r.Body.String()
	if strings.Count(body, `convo_http_requests_total{method="other",route="/api/v1/feed",status="200",code="1000"} 3`) != 1 {
		t.Errorf("expected a single aggregated method=other series: %s", body)
	}
	for _, forbidden := range []string{`method="TEST_ONE"`, `method="TEST_TWO"`, `method="CUSTOM_83"`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("unbounded method series %s", forbidden)
		}
	}
}

type blockingMetricsWriter struct {
	entered chan struct{}
	release chan struct{}
	header  http.Header
}

func (w *blockingMetricsWriter) Header() http.Header { return w.header }
func (w *blockingMetricsWriter) WriteHeader(int)     {}
func (w *blockingMetricsWriter) Write(data []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(data), nil
}

func TestSlowMetricsWriterDoesNotHoldRegistryLock(t *testing.T) {
	w := &blockingMetricsWriter{
		entered: make(chan struct{}), release: make(chan struct{}), header: make(http.Header),
	}
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		MetricsHandler(w, httptest.NewRequest("GET", "/metrics", nil))
	}()
	defer func() {
		close(w.release)
		<-completed
	}()
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("metrics write did not start")
	}
	recorded := make(chan struct{})
	go func() {
		RecordHTTP("GET", "/api/v1/feed", 200, 1000, time.Millisecond, false)
		close(recorded)
	}()
	select {
	case <-recorded:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("business metrics blocked by an in-progress HTTP metrics write")
	}
}

func TestT02TaskMetricsBoundedAndSamples(t *testing.T) {
	for _, kind := range []string{"delete_index", "delete_inbox"} {
		RecordContentTask(kind, "done", 2*time.Millisecond)
	}
	RecordFollowTask("backfill", "canceled", time.Millisecond)
	RecordFollowTask("cleanup", "done", time.Millisecond)
	RecordFollowRows("backfill", 5)
	RecordFollowRows("cleanup", 2)
	RecordFollowTask("user-9007199254740993", "done", time.Second) // unbounded label rejected
	RecordFollowQueueSample([]FollowTaskSample{{Kind: "backfill", Status: "pending", Count: 4, OldestAge: 12}, {Kind: "cleanup", Status: "failed", Count: 1, OldestAge: 5}}, nil)
	r := httptest.NewRecorder()
	MetricsHandler(r, httptest.NewRequest("GET", "/metrics", nil))
	body := r.Body.String()
	for _, expected := range []string{
		`convo_content_task_state{kind="delete_index",status="pending"}`,
		`convo_content_task_state{kind="delete_inbox",status="pending"}`,
		`convo_follow_task_total{kind="backfill",result="canceled"} 1`,
		`convo_follow_rows_total{kind="backfill"} 5`,
		`convo_follow_task_state{kind="backfill",status="pending"} 4`,
		`convo_follow_task_state{kind="cleanup",status="failed"} 1`,
		`convo_follow_oldest_unfinished_seconds{kind="backfill"} 12`,
		`convo_follow_queue_sample_ok 1`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("missing metric %q", expected)
		}
	}
	if strings.Contains(body, "user-9007199254740993") {
		t.Fatal("high cardinality label leaked")
	}
	RecordFollowQueueSample(nil, fmt.Errorf("test collector disconnected"))
	r = httptest.NewRecorder()
	MetricsHandler(r, httptest.NewRequest("GET", "/metrics", nil))
	body = r.Body.String()
	if !strings.Contains(body, "convo_follow_queue_sample_ok 0") || !strings.Contains(body, `convo_follow_task_state{kind="backfill",status="pending"} 4`) {
		t.Fatal("failed sampling should report disconnect but keep last values")
	}
}
