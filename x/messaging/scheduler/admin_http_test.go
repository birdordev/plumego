package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
)

func TestAdminHandlerEndpoints(t *testing.T) {
	s := New(WithWorkers(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	_, err := s.Delay("admin-1", 10*time.Millisecond, func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	h := NewAdminHandler(s)

	req := httptest.NewRequest(http.MethodGet, "/scheduler/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status: %d", rec.Code)
	}
	assertJSONContentType(t, rec)

	req = httptest.NewRequest(http.MethodGet, "/scheduler/jobs/admin-1", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("job status: %d", rec.Code)
	}
	assertJSONContentType(t, rec)

	status := decodeAdminData[JobStatus](t, rec)
	if status.ID != "admin-1" {
		t.Fatalf("expected job id admin-1, got %s", status.ID)
	}

	req = httptest.NewRequest(http.MethodPost, "/scheduler/jobs/admin-1/pause", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pause status: %d", rec.Code)
	}
	assertAdminActionStatus(t, rec, "paused")

	block := make(chan struct{})
	_, err = s.Delay("admin-running", 0, func(ctx context.Context) error {
		<-block
		return nil
	})
	if err != nil {
		t.Fatalf("delay running: %v", err)
	}
	if !waitForState(t, s, "admin-running", JobStateRunning, 500*time.Millisecond) {
		t.Fatalf("expected admin-running to reach running state")
	}

	req = httptest.NewRequest(http.MethodGet, "/scheduler/jobs?state=running", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("jobs state status: %d", rec.Code)
	}
	assertJSONContentType(t, rec)
	jobs := decodeAdminData[[]JobStatus](t, rec)
	found := false
	for _, job := range jobs {
		if job.ID == "admin-running" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected admin-running to be in running jobs list")
	}
	close(block)
}

func TestAdminHandlerBulkDLQAndPrefix(t *testing.T) {
	s := New(WithWorkers(2), WithDeadLetterQueue(10))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	_, err := s.Delay("g1-t1", time.Hour, func(context.Context) error { return nil }, WithGroup("g1"), WithTags("t1"))
	if err != nil {
		t.Fatalf("delay g1-t1: %v", err)
	}
	_, err = s.Delay("g1-t2", time.Hour, func(context.Context) error { return nil }, WithGroup("g1"), WithTags("t2"))
	if err != nil {
		t.Fatalf("delay g1-t2: %v", err)
	}
	_, err = s.Delay("g2-t1", time.Hour, func(context.Context) error { return nil }, WithGroup("g2"), WithTags("t1"))
	if err != nil {
		t.Fatalf("delay g2-t1: %v", err)
	}

	_, err = s.Delay("dlq-fail", 0, func(context.Context) error { return context.Canceled }, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
	if err != nil {
		t.Fatalf("delay dlq-fail: %v", err)
	}
	if !waitForState(t, s, "dlq-fail", JobStateFailed, 500*time.Millisecond) {
		t.Fatal("expected dlq-fail to reach failed state")
	}

	h := NewAdminHandler(s).WithPrefix("/admin/sched")

	rec := doAdminReq(h, http.MethodGet, "/admin/sched/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("custom prefix health status: %d", rec.Code)
	}

	rec = doAdminReq(h, http.MethodPost, "/admin/sched/groups/g1/pause")
	if rec.Code != http.StatusOK {
		t.Fatalf("group pause status: %d", rec.Code)
	}
	assertAffectedCount(t, rec.Body.Bytes(), 2)

	rec = doAdminReq(h, http.MethodPost, "/admin/sched/tags/t2/resume")
	if rec.Code != http.StatusOK {
		t.Fatalf("tag resume status: %d", rec.Code)
	}
	assertAffectedCount(t, rec.Body.Bytes(), 1)

	rec = doAdminReq(h, http.MethodPost, "/admin/sched/tags/t1/cancel")
	if rec.Code != http.StatusOK {
		t.Fatalf("tag cancel status: %d", rec.Code)
	}
	assertAffectedCount(t, rec.Body.Bytes(), 2)

	rec = doAdminReq(h, http.MethodGet, "/admin/sched/jobs/g1-t1/pause")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected method not allowed for GET job action, got %d", rec.Code)
	}
	assertErrorCode(t, rec, "METHOD_NOT_ALLOWED")

	rec = doAdminReq(h, http.MethodGet, "/admin/sched/dlq/dlq-fail")
	if rec.Code != http.StatusOK {
		t.Fatalf("dlq entry get status: %d", rec.Code)
	}
	entry := decodeAdminData[struct {
		JobID JobID `json:"JobID"`
	}](t, rec)
	if entry.JobID != "dlq-fail" {
		t.Fatalf("expected dlq-fail entry, got %s", entry.JobID)
	}

	rec = doAdminReq(h, http.MethodPost, "/admin/sched/dlq/dlq-fail")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected method not allowed for dlq entry action, got %d", rec.Code)
	}
	assertErrorCode(t, rec, "METHOD_NOT_ALLOWED")

	rec = doAdminReq(h, http.MethodDelete, "/admin/sched/dlq/dlq-fail")
	if rec.Code != http.StatusOK {
		t.Fatalf("dlq entry delete status: %d", rec.Code)
	}
	assertAdminActionStatus(t, rec, "deleted")

	rec = doAdminReq(h, http.MethodGet, "/admin/sched/dlq/dlq-fail")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected dlq entry not found after delete, got %d", rec.Code)
	}
	assertErrorCode(t, rec, contract.CodeResourceNotFound)
}

func TestAdminHandlerInvalidBoolQueryReturnsStructuredError(t *testing.T) {
	s := New(WithWorkers(1))
	h := NewAdminHandler(s)

	for _, path := range []string{
		"/scheduler/jobs?running=maybe",
		"/scheduler/jobs?paused=maybe",
		"/scheduler/jobs?asc=maybe",
	} {
		t.Run(path, func(t *testing.T) {
			rec := doAdminReq(h, http.MethodGet, path)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			assertErrorCode(t, rec, contract.CodeInvalidQuery)
		})
	}
}

func TestAdminHandlerTypedResultResponses(t *testing.T) {
	s := New(WithWorkers(1), WithDeadLetterQueue(10))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	_, err := s.Delay("clear-dlq", 0, func(context.Context) error { return context.Canceled }, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
	if err != nil {
		t.Fatalf("delay clear-dlq: %v", err)
	}
	if !waitForState(t, s, "clear-dlq", JobStateFailed, 500*time.Millisecond) {
		t.Fatal("expected clear-dlq to reach failed state")
	}

	h := NewAdminHandler(s)
	rec := doAdminReq(h, http.MethodDelete, "/scheduler/dlq")
	if rec.Code != http.StatusOK {
		t.Fatalf("clear dlq status = %d, want %d", rec.Code, http.StatusOK)
	}
	cleared := decodeAdminData[adminClearDeadLettersResponse](t, rec)
	if cleared.Cleared != 1 {
		t.Fatalf("cleared = %d, want 1", cleared.Cleared)
	}
}

func doAdminReq(h *AdminHandler, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertAffectedCount(t *testing.T, payload []byte, want int) {
	t.Helper()
	var env struct {
		Data adminBulkActionResponse `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("decode affected count envelope: %v", err)
	}
	got := env.Data.Affected
	if got != want {
		t.Fatalf("expected affected=%d, got %d", want, got)
	}
}

func assertAdminActionStatus(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	action := decodeAdminData[adminActionResponse](t, rec)
	if action.Status != want {
		t.Fatalf("action status = %q, want %q", action.Status, want)
	}
}

func decodeAdminData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	assertJSONContentType(t, rec)

	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode success envelope: %v", err)
	}
	if len(env.Data) == 0 {
		t.Fatal("success envelope missing data")
	}

	var body T
	if err := json.Unmarshal(env.Data, &body); err != nil {
		t.Fatalf("decode success data: %v", err)
	}
	return body
}

func assertJSONContentType(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != contract.ContentTypeJSON {
		t.Fatalf("content type = %q, want %q", got, contract.ContentTypeJSON)
	}
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	assertJSONContentType(t, rec)

	var resp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp.Error.Code != want {
		t.Fatalf("error code = %q, want %q", resp.Error.Code, want)
	}
}

func TestParseJobQuery(t *testing.T) {
	q := parseJobQuery(nil)
	if q.Group != "" || len(q.Tags) != 0 || len(q.Kinds) != 0 || q.Running != nil ||
		q.Paused != nil || len(q.States) != 0 || q.OrderBy != "" || q.Ascending || q.Limit != 0 || q.Offset != 0 {
		t.Fatalf("expected zero JobQuery for nil request, got %+v", q)
	}

	req := httptest.NewRequest(
		http.MethodGet,
		"/scheduler/jobs?state=running&state=bogus&group=g1&tag=t1&tag=t2&kind=cron&kind=invalid&running=true&paused=bad&order_by=next_run&asc=false&limit=999999&offset=-1",
		nil,
	)
	parsed := parseJobQuery(req)
	if len(parsed.States) != 1 || parsed.States[0] != JobStateRunning {
		t.Fatalf("expected states [running], got %+v", parsed.States)
	}
	if parsed.Group != "g1" {
		t.Fatalf("expected group g1, got %s", parsed.Group)
	}
	if len(parsed.Tags) != 2 || parsed.Tags[0] != "t1" || parsed.Tags[1] != "t2" {
		t.Fatalf("unexpected tags: %+v", parsed.Tags)
	}
	if len(parsed.Kinds) != 1 || parsed.Kinds[0] != "cron" {
		t.Fatalf("expected kinds [cron], got %+v", parsed.Kinds)
	}
	if parsed.Running == nil || !*parsed.Running {
		t.Fatalf("expected running=true, got %+v", parsed.Running)
	}
	if parsed.Paused != nil {
		t.Fatalf("expected paused=nil for invalid bool, got %+v", parsed.Paused)
	}
	if parsed.OrderBy != "next_run" {
		t.Fatalf("expected order_by next_run, got %s", parsed.OrderBy)
	}
	if parsed.Ascending {
		t.Fatal("expected ascending=false")
	}
	if parsed.Limit != 10_000 {
		t.Fatalf("expected limit capped to 10000, got %d", parsed.Limit)
	}
	if parsed.Offset != 0 {
		t.Fatalf("expected invalid negative offset to be 0, got %d", parsed.Offset)
	}

	req2 := httptest.NewRequest(
		http.MethodGet,
		"/scheduler/jobs?offset=2000000&limit=12&running=false&paused=true",
		nil,
	)
	parsed2 := parseJobQuery(req2)
	if parsed2.Offset != 1_000_000 {
		t.Fatalf("expected offset capped to 1000000, got %d", parsed2.Offset)
	}
	if parsed2.Limit != 12 {
		t.Fatalf("expected limit 12, got %d", parsed2.Limit)
	}
	if parsed2.Running == nil || *parsed2.Running {
		t.Fatalf("expected running=false, got %+v", parsed2.Running)
	}
	if parsed2.Paused == nil || !*parsed2.Paused {
		t.Fatalf("expected paused=true, got %+v", parsed2.Paused)
	}

	if _, err := parseJobQueryE(httptest.NewRequest(http.MethodGet, "/scheduler/jobs?running=bad", nil)); err == nil || err.Error() != "running" {
		t.Fatalf("expected running bool parse error, got %v", err)
	}
}
