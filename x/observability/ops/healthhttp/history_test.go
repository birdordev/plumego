package healthhttp

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
)

func TestHealthHistoryExportHandler(t *testing.T) {
	manager, checker := newHistoryManager(t)

	manager.CheckAllComponents(t.Context())

	req := httptest.NewRequest("GET", "/health/history/export?format=json", nil)
	rr := httptest.NewRecorder()
	HealthHistoryExportHandler(manager).ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if contentType := rr.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("expected content type application/json, got %s", contentType)
	}

	result := decodeHealthHTTPData[HealthHistoryQueryResult](t, rr)
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}

	checker.healthy = false
	manager.CheckAllComponents(t.Context())

	req = httptest.NewRequest("GET", "/health/history/export?format=csv", nil)
	rr = httptest.NewRecorder()
	HealthHistoryExportHandler(manager).ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if contentType := rr.Header().Get("Content-Type"); contentType != "text/csv" {
		t.Fatalf("expected content type text/csv, got %s", contentType)
	}

	reader := csv.NewReader(strings.NewReader(rr.Body.String()))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("failed to parse CSV: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 CSV records (header + 2 data rows), got %d", len(records))
	}
	if len(records[0]) != 5 {
		t.Fatalf("expected 5 CSV columns, got %d", len(records[0]))
	}

	req = httptest.NewRequest("GET", "/health/history/export?format=invalid", nil)
	rr = httptest.NewRecorder()
	HealthHistoryExportHandler(manager).ServeHTTP(rr, req)

	if rr.Code != 400 {
		t.Fatalf("expected status 400 for invalid format, got %d", rr.Code)
	}
	if contentType := rr.Header().Get("Content-Type"); contentType != contract.ContentTypeJSON {
		t.Fatalf("expected content type %s for invalid format, got %s", contract.ContentTypeJSON, contentType)
	}

	var errResp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal error response: %v", err)
	}
	if errResp.Error.Code != "INVALID_FORMAT" {
		t.Fatalf("expected INVALID_FORMAT, got %s", errResp.Error.Code)
	}
}

func TestHealthHistoryExportHandlerInvalidQueryUsesSafeError(t *testing.T) {
	manager, _ := newHistoryManager(t)

	req := httptest.NewRequest("GET", "/health/history/export?state=broken", nil)
	rr := httptest.NewRecorder()
	HealthHistoryExportHandler(manager).ServeHTTP(rr, req)

	if rr.Code != 400 {
		t.Fatalf("expected status 400 for invalid query, got %d", rr.Code)
	}

	var errResp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal error response: %v", err)
	}
	if errResp.Error.Code != contract.CodeInvalidQuery {
		t.Fatalf("expected %s, got %s", contract.CodeInvalidQuery, errResp.Error.Code)
	}
	if errResp.Error.Message != "invalid health history query" {
		t.Fatalf("expected safe invalid query message, got %q", errResp.Error.Message)
	}
	if strings.Contains(errResp.Error.Message, "valid states") {
		t.Fatalf("message exposes validation detail: %q", errResp.Error.Message)
	}
	if errResp.Error.Details["param"] != "state" {
		t.Fatalf("expected state param detail, got %v", errResp.Error.Details)
	}
	if errResp.Error.Details["validation_message"] != "valid states: healthy, degraded, unhealthy" {
		t.Fatalf("expected validation detail, got %v", errResp.Error.Details)
	}
}

func TestHealthHistoryStatsHandler(t *testing.T) {
	manager, checker := newHistoryManager(t)

	manager.CheckAllComponents(t.Context())
	manager.CheckAllComponents(t.Context())
	checker.healthy = false
	manager.CheckAllComponents(t.Context())

	req := httptest.NewRequest("GET", "/health/history/stats", nil)
	rr := httptest.NewRecorder()
	HealthHistoryStatsHandler(manager).ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if contentType := rr.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("expected content type application/json, got %s", contentType)
	}

	stats := decodeHealthHTTPData[HistoryStats](t, rr)

	if stats.TotalEntries != 3 {
		t.Fatalf("expected total_entries to be 3, got %d", stats.TotalEntries)
	}
}

type mutableChecker struct {
	name    string
	healthy bool
}

func (c *mutableChecker) Name() string { return c.name }

func (c *mutableChecker) Check(context.Context) error {
	if c.healthy {
		return nil
	}
	return errUnhealthy
}

var errUnhealthy = errors.New("unhealthy")

func newHistoryManager(t *testing.T) (*Tracker, *mutableChecker) {
	t.Helper()

	coreManager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	manager := NewTracker(coreManager)

	checker := &mutableChecker{name: "history", healthy: true}
	if err := manager.RegisterComponent(checker); err != nil {
		t.Fatalf("register component: %v", err)
	}

	return manager, checker
}

func TestHealthHistoryComponentsAreDetached(t *testing.T) {
	tracker, checker := newHistoryManager(t)
	tracker.CheckAllComponents(t.Context())

	snapshot := tracker.GetHealthHistory()
	page := tracker.QueryHealthHistory(HealthHistoryQuery{Component: checker.name, Limit: 1})
	stats := tracker.GetHealthHistoryStats()
	if len(snapshot) != 1 || page.Total != 1 || len(page.Entries) != 1 || stats.OldestEntry == nil || stats.NewestEntry == nil {
		t.Fatalf("expected one history record, got history %v, page %+v, stats %+v", snapshot, page, stats)
	}

	for name, components := range map[string][]string{
		"history": snapshot[0].Components,
		"query":   page.Entries[0].Components,
		"oldest":  stats.OldestEntry.Components,
		"newest":  stats.NewestEntry.Components,
	} {
		if len(components) != 1 || components[0] != checker.name {
			t.Fatalf("%s components = %v, want [%s]", name, components, checker.name)
		}
		components[0] = "changed"
		if got := tracker.GetHealthHistory()[0].Components[0]; got != checker.name {
			t.Fatalf("changing %s altered the stored component to %q", name, got)
		}
	}
}

func TestHealthHistoryPreservesEmptyComponentsAndPagination(t *testing.T) {
	tracker := NewTracker(nil)
	tracker.history = []HealthHistoryEntry{
		{Message: "first"},
		{Message: "second", Components: []string{}},
	}

	snapshot := tracker.GetHealthHistory()
	if snapshot[0].Components != nil || snapshot[1].Components == nil || len(snapshot[1].Components) != 0 {
		t.Fatalf("snapshot changed nil or empty components: %+v", snapshot)
	}
	firstPage := tracker.QueryHealthHistory(HealthHistoryQuery{Limit: 1})
	if len(firstPage.Entries) != 1 || cap(firstPage.Entries) != 1 || !firstPage.HasMore {
		t.Fatalf("first page = %+v, capacity = %d", firstPage, cap(firstPage.Entries))
	}
	page := tracker.QueryHealthHistory(HealthHistoryQuery{Offset: 1, Limit: 1})
	if page.Total != 2 || page.Offset != 1 || page.HasMore || len(page.Entries) != 1 ||
		page.Entries[0].Message != "second" || page.Entries[0].Components == nil || len(page.Entries[0].Components) != 0 {
		t.Fatalf("unexpected second page: %+v", page)
	}
}

func TestHistoryCSVExportStopsAfterFailedWrite(t *testing.T) {
	tracker, _ := newHistoryManager(t)
	tracker.CheckAllComponents(t.Context())
	w := &componentWriteFailure{ResponseRecorder: httptest.NewRecorder()}
	HealthHistoryExportHandler(tracker).ServeHTTP(w, httptest.NewRequest("GET", "/?format=csv", nil))
	if w.Code != 200 || w.headers != 1 || w.writes != 1 || w.Header().Get("Content-Type") != "text/csv" {
		t.Fatalf("status=%d commits=%d writes=%d headers=%v", w.Code, w.headers, w.writes, w.Header())
	}
}

func TestAcceptanceHistoryTimestampFiltersRejectInvalidWindows(t *testing.T) {
	tracker, _ := newHistoryManager(t)
	handler := HealthHistoryExportHandler(tracker)
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"", 200},
		{"?start_time=2026-01-01T00:00:00Z&end_time=2026-01-01T00:00:00Z", 200},
		{"?start_time=fixture-date", 400},
		{"?end_time=fixture-date", 400},
		{"?start_time=2026-02-01T00:00:00Z&end_time=2026-01-01T00:00:00Z", 400},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/"+tc.query, nil))
		if w.Code != tc.status || strings.Contains(w.Body.String(), "fixture-date") ||
			tc.status == 400 && !strings.Contains(w.Body.String(), contract.CodeInvalidQuery) {
			t.Fatalf("status=%d want=%d body=%q", w.Code, tc.status, w.Body)
		}
	}
}

func TestTrackerRecordsCompletionOrderWithoutReplacingNewerReadiness(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	checker := &aggregateChecker{entered: make(chan struct{}), release: make(chan struct{})}
	if err := manager.RegisterComponent(checker); err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(manager)
	first := make(chan health.HealthStatus, 1)
	go func() { first <- tracker.CheckAllComponents(t.Context()) }()
	<-checker.entered
	newer := tracker.CheckAllComponents(t.Context())
	before := manager.Readiness()
	close(checker.release)
	older := <-first
	after := manager.Readiness()
	if newer.Status != health.StatusHealthy || older.Status != health.StatusUnhealthy ||
		!before.Ready || !after.Ready || !before.Timestamp.Equal(after.Timestamp) {
		t.Fatalf("new=%+v old=%+v readiness before=%+v after=%+v", newer, older, before, after)
	}
	history := tracker.GetHealthHistory()
	if len(history) != 2 || history[0].State != newer.Status || history[1].State != older.Status ||
		!history[0].Timestamp.Equal(newer.Timestamp) || !history[1].Timestamp.Equal(older.Timestamp) {
		t.Fatalf("history=%+v", history)
	}
}
