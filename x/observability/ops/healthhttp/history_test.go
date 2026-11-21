package healthhttp

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestAcceptanceHistoryOffsetsPastEndHaveNoMore(t *testing.T) {
	for _, n := range []int{0, 3} {
		tracker, _ := newHistoryManager(t)
		for range n {
			tracker.CheckAllComponents(t.Context())
		}
		for _, offset := range []int{0, 1, 2, 3, math.MaxInt} {
			got := tracker.QueryHealthHistory(HealthHistoryQuery{Limit: 1, Offset: offset})
			if got.Total != n || got.Offset != offset || got.Limit != 1 || got.Entries == nil ||
				len(got.Entries) != min(1, max(0, n-offset)) || got.HasMore != (offset < n-1) {
				t.Fatalf("history size=%d offset=%d page=%+v", n, offset, got)
			}
		}
	}
}

func TestAcceptanceHistoryExportValidatesPaginationBeforeFormat(t *testing.T) {
	tracker, _ := newHistoryManager(t)
	tracker.CheckAllComponents(t.Context())
	handler := HealthHistoryExportHandler(tracker)
	for _, tc := range []struct {
		query  string
		status int
		code   string
		param  string
	}{
		{"", 200, "", ""},
		{"?limit=1&offset=1", 200, "", ""},
		{"?limit=-1&offset=-1", 200, "", ""},
		{"?limit=101&offset=0", 200, "", ""},
		{"?limit=marker", 400, contract.CodeInvalidQuery, "limit"},
		{"?offset=marker", 400, contract.CodeInvalidQuery, "offset"},
		{"?limit=9223372036854775808", 400, contract.CodeInvalidQuery, "limit"},
		{"?offset=9223372036854775808", 400, contract.CodeInvalidQuery, "offset"},
		{"?limit=marker&format=marker", 400, contract.CodeInvalidQuery, "limit"},
		{"?format=marker", 400, contract.CodeInvalidFormat, ""},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/history/export"+tc.query, nil))
		var response struct {
			Data  HealthHistoryQueryResult
			Error struct {
				Code    string
				Details map[string]string
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != tc.status ||
			response.Error.Code != tc.code || response.Error.Details["param"] != tc.param ||
			strings.Contains(w.Body.String(), "marker") {
			t.Fatalf("query=%q status=%d body=%q err=%v", tc.query, w.Code, w.Body.String(), err)
		}
		if tc.status == 200 && (response.Data.Total != 1 || response.Data.HasMore || response.Data.Entries == nil) {
			t.Fatalf("query=%q response=%+v", tc.query, response.Data)
		}
	}
}

func BenchmarkHistoryUnsupportedFormat(b *testing.B) {
	for _, size := range []int{0, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			tracker := NewTracker(nil)
			tracker.history = make([]HealthHistoryEntry, size)
			handler := HealthHistoryExportHandler(tracker)
			req := httptest.NewRequest("GET", "/history/export?format=unsupported", nil)
			b.ReportAllocs()
			for b.Loop() {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != 400 {
					b.Fatalf("status=%d", w.Code)
				}
			}
		})
	}
}

func TestHistoryStatsUsesTimestampExtremaAndPreservesInsertionOrder(t *testing.T) {
	tracker := NewTracker(nil)
	start := time.Now()
	if got := tracker.GetHealthHistoryStats(); got.TotalEntries != 0 || got.OldestEntry != nil || got.NewestEntry != nil {
		t.Fatalf("empty stats=%+v", got)
	}
	for _, fixture := range []struct {
		offset int
		name   string
	}{{2, "early insert latest"}, {1, "middle"}, {0, "first oldest"}, {0, "second oldest"}, {2, "last latest"}} {
		tracker.history = append(tracker.history, HealthHistoryEntry{
			Timestamp: start.Add(time.Duration(fixture.offset) * time.Second), State: health.StatusHealthy, Message: fixture.name, Components: []string{"sample"},
		})
	}
	stats := tracker.GetHealthHistoryStats()
	if stats.TimeSpan != 2*time.Second || stats.OldestEntry.Message != "first oldest" || stats.NewestEntry.Message != "last latest" ||
		stats.EntriesByState[string(health.StatusHealthy)] != 5 {
		t.Fatalf("stats=%+v", stats)
	}
	stats.OldestEntry.Components[0] = "caller"
	page := tracker.QueryHealthHistory(HealthHistoryQuery{Limit: 1})
	if page.Entries[0].Message != "early insert latest" || page.Entries[0].Components[0] != "sample" || tracker.history[2].Components[0] != "sample" {
		t.Fatalf("page or tracked entry changed: %+v", page)
	}
}

func TestHistorySmallPagePreservesCombinedFiltersAndOwnership(t *testing.T) {
	tracker := NewTracker(nil)
	now := time.Now()
	for i := range 20 {
		state := health.StatusHealthy
		if i%3 == 0 {
			state = health.StatusUnhealthy
		}
		component := "sample"
		if i%2 == 0 {
			component = "other"
		}
		tracker.history = append(tracker.history, HealthHistoryEntry{Timestamp: now.Add(time.Duration(i) * time.Second), State: state, Message: fmt.Sprint(i), Components: []string{component}})
	}
	start, end := now.Add(2*time.Second), now.Add(17*time.Second)
	state := health.StatusHealthy
	query := HealthHistoryQuery{StartTime: &start, EndTime: &end, State: &state, Component: "sample", Limit: 2, Offset: 1}
	got := tracker.QueryHealthHistory(query)
	if got.Total != 5 || !got.HasMore || len(got.Entries) != 2 || cap(got.Entries) != 2 || got.Entries[0].Message != "7" || got.Entries[1].Message != "11" {
		t.Fatalf("page=%+v", got)
	}
	got.Entries[0].Components[0] = "changed"
	if tracker.QueryHealthHistory(query).Entries[0].Components[0] != "sample" {
		t.Fatal("page mutated stored entry")
	}
	query.Offset = 5
	if last := tracker.QueryHealthHistory(query); last.Entries == nil || len(last.Entries) != 0 || last.HasMore || last.Total != 5 {
		t.Fatalf("last=%+v", last)
	}
}

func TestHistoryExportReportsDocumentedPaginationLimits(t *testing.T) {
	handler := HealthHistoryExportHandler(NewTracker(nil))
	for _, tc := range []struct {
		query         string
		limit, offset int
	}{
		{"", 50, 0}, {"?limit=0&offset=-2", 50, 0}, {"?limit=-1", 50, 0}, {"?limit=100&offset=7", 100, 7}, {"?limit=101", 50, 0},
	} {
		dst := httptest.NewRecorder()
		handler.ServeHTTP(dst, httptest.NewRequest("GET", "/export"+tc.query, nil))
		var output struct{ Data HealthHistoryQueryResult }
		if err := json.Unmarshal(dst.Body.Bytes(), &output); err != nil || dst.Code != 200 || output.Data.Limit != tc.limit || output.Data.Offset != tc.offset {
			t.Fatalf("query=%q status=%d data=%+v err=%v", tc.query, dst.Code, output.Data, err)
		}
	}
}

func BenchmarkHistoryFirstSmallPage(b *testing.B) {
	for _, size := range []int{100, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			tracker := NewTracker(nil)
			tracker.history = make([]HealthHistoryEntry, size)
			b.ReportAllocs()
			for b.Loop() {
				page := tracker.QueryHealthHistory(HealthHistoryQuery{Limit: 1})
				if page.Total != size || len(page.Entries) != 1 || !page.HasMore {
					b.Fatalf("page=%+v", page)
				}
			}
		})
	}
}

func historyFilterFixture(size, stride int) (*Tracker, int) {
	tracker := NewTracker(nil)
	tracker.history = make([]HealthHistoryEntry, size)
	total := 0
	for index := range tracker.history {
		entry := &tracker.history[index]
		entry.Components = []string{"other"}
		if index%stride == 0 {
			entry.Components = []string{"fixture"}
			total++
		}
		entry.State = health.StatusHealthy
		entry.Timestamp = time.Unix(int64(index), 0).UTC()
	}
	return tracker, total
}

func TestHistoryCSVFilterProducesOnePageAndJSONKeepsTotal(t *testing.T) {
	for _, stride := range []int{1, 100} {
		tracker, wantTotal := historyFilterFixture(1000, stride)
		handler := HealthHistoryExportHandler(tracker)
		const query = "/export?component=fixture&limit=1&offset=1"
		csvRecorder := httptest.NewRecorder()
		handler.ServeHTTP(csvRecorder, httptest.NewRequest("GET", query+"&format=csv", nil))
		rows, err := csv.NewReader(csvRecorder.Body).ReadAll()
		if err != nil || csvRecorder.Code != 200 || len(rows) != 2 || rows[1][3] != "fixture" || rows[1][0] != time.Unix(int64(stride), 0).UTC().Format(time.RFC3339) {
			t.Fatalf("stride=%d status=%d rows=%v error=%v", stride, csvRecorder.Code, rows, err)
		}
		jsonRecorder := httptest.NewRecorder()
		handler.ServeHTTP(jsonRecorder, httptest.NewRequest("GET", query, nil))
		got := decodeHealthHTTPData[HealthHistoryQueryResult](t, jsonRecorder)
		if jsonRecorder.Code != 200 || got.Total != wantTotal || len(got.Entries) != 1 || !got.HasMore {
			t.Fatalf("stride=%d status=%d page=%+v", stride, jsonRecorder.Code, got)
		}
	}
}

func BenchmarkHistoryCSVFilterDensity(b *testing.B) {
	for _, stride := range []int{1, 100} {
		b.Run(fmt.Sprintf("stride_%d", stride), func(b *testing.B) {
			tracker, _ := historyFilterFixture(10000, stride)
			handler := HealthHistoryExportHandler(tracker)
			req := httptest.NewRequest("GET", "/export?format=csv&component=fixture&limit=1&offset=1", nil)
			b.ReportAllocs()
			for b.Loop() {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != 200 {
					b.Fatalf("response=%d", rec.Code)
				}
			}
		})
	}
}

func TestClosedTrackerRecordsBatchResultWithoutAnotherAttempt(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(manager)
	calls := 0
	if err := tracker.RegisterComponent(functionChecker{name: "sample", check: func(context.Context) error { calls++; return nil }}); err != nil {
		t.Fatal(err)
	}
	tracker.CheckAllComponents(t.Context())
	before := tracker.GetMetrics()
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	status := tracker.CheckAllComponents(t.Context())
	after := tracker.GetMetrics()
	history := tracker.GetHealthHistory()
	if calls != 1 || before.CheckCount != 1 || after.CheckCount != 1 || len(history) != 2 || history[1].State != health.StatusUnhealthy ||
		status.Status != health.StatusUnhealthy || tracker.Readiness().Ready || after.LastCheckTime.Before(status.Timestamp) ||
		after.LastCheckTime.Before(before.LastCheckTime) {
		t.Fatalf("calls=%d status=%+v history=%+v metrics=%+v", calls, status, history, after)
	}
}

func TestHistoryExportPastEndKeepsTotalAndCSVHeader(t *testing.T) {
	tracker, _ := newHistoryManager(t)
	tracker.CheckAllComponents(t.Context())
	h := HealthHistoryExportHandler(tracker)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/export?format=json&offset=20", nil))
	result := decodeHealthHTTPData[HealthHistoryQueryResult](t, w)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || result.Total != 1 ||
		result.Entries == nil || len(result.Entries) != 0 || result.HasMore || result.Offset != 20 {
		t.Fatalf("status=%d headers=%v result=%+v", w.Code, w.Header(), result)
	}
	csvRecorder := httptest.NewRecorder()
	h.ServeHTTP(csvRecorder, httptest.NewRequest("GET", "/export?format=csv&offset=20", nil))
	rows, err := csv.NewReader(csvRecorder.Body).ReadAll()
	if err != nil || csvRecorder.Code != 200 || csvRecorder.Header().Get("Content-Type") != "text/csv" ||
		len(rows) != 1 || len(rows[0]) != 5 || rows[0][0] != "Timestamp" {
		t.Fatalf("status=%d headers=%v rows=%v err=%v", csvRecorder.Code, csvRecorder.Header(), rows, err)
	}
}
