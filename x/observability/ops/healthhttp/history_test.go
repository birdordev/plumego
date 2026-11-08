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
