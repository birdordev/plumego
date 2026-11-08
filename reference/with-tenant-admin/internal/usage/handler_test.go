package usage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"with-tenant-admin/internal/auth"
	tenantadmin "with-tenant-admin/internal/tenant/admin"
)

func discardLogger() plumelog.StructuredLogger {
	return plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard})
}

func TestRecordUsageAndGetUsageReport(t *testing.T) {
	h := newTestHandler(t, "tenant-1")

	rec := serveUsageAdmin(t, h.RecordUsage, http.MethodPost, "/admin/usage/tenant-1", `{"resource":"api_requests","count":3}`, true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("record status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	rec = serveUsageAdmin(t, h.GetUsageReport, http.MethodGet, "/admin/usage/tenant-1", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("report status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	records := decodeData[[]UsageRecord](t, rec)
	if len(records) != 1 {
		t.Fatalf("records length = %d, want 1", len(records))
	}
	if records[0].TenantID != "tenant-1" || records[0].Resource != "api_requests" || records[0].Count != 3 {
		t.Fatalf("record = %+v, want tenant-1 api_requests count 3", records[0])
	}
}

func TestGetUsageReportUnknownTenantReturnsNotFound(t *testing.T) {
	h := newTestHandler(t, "")

	rec := serveUsageAdmin(t, h.GetUsageReport, http.MethodGet, "/admin/usage/missing", "", true)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRecordUsageZeroCountReturnsBadRequest(t *testing.T) {
	h := newTestHandler(t, "tenant-1")

	rec := serveUsageAdmin(t, h.RecordUsage, http.MethodPost, "/admin/usage/tenant-1", `{"resource":"api_requests","count":0}`, true)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestMultipleUsageRecordsAccumulate(t *testing.T) {
	h := newTestHandler(t, "tenant-1")

	serveUsageAdmin(t, h.RecordUsage, http.MethodPost, "/admin/usage/tenant-1", `{"resource":"api_requests","count":2}`, true)
	serveUsageAdmin(t, h.RecordUsage, http.MethodPost, "/admin/usage/tenant-1", `{"resource":"tokens","count":7}`, true)

	rec := serveUsageAdmin(t, h.GetUsageReport, http.MethodGet, "/admin/usage/tenant-1", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	records := decodeData[[]UsageRecord](t, rec)
	if len(records) != 2 {
		t.Fatalf("records length = %d, want 2", len(records))
	}
	if records[0].Count+records[1].Count != 9 {
		t.Fatalf("record counts = %+v, want total 9", records)
	}
}

func TestUnauthenticatedUsageAdminReturnsUnauthorized(t *testing.T) {
	h := newTestHandler(t, "tenant-1")

	rec := serveUsageAdmin(t, h.GetUsageReport, http.MethodGet, "/admin/usage/tenant-1", "", false)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func newTestHandler(t *testing.T, tenantID string) *Handler {
	t.Helper()
	tenantStore := tenantadmin.NewInMemoryStore()
	if tenantID != "" {
		_, _ = tenantStore.Create(t.Context(), tenantadmin.TenantRecord{ID: tenantID, Name: "Acme"})
	}
	return NewHandler(tenantStore, NewInMemoryUsageStore(), discardLogger())
}

// serveUsageAdmin calls handler directly — bypassing the router — with the
// tenantID path parameter injected via contract.WithRequestContext.
func serveUsageAdmin(t *testing.T, handler http.HandlerFunc, method string, path string, body string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tenantID := usagePathTenantID(path); tenantID != "" {
		ctx := contract.WithRequestContext(req.Context(), contract.RequestContext{
			Params: map[string]string{"tenantID": tenantID},
		})
		req = req.WithContext(ctx)
	}
	if authenticated {
		req.Header.Set(auth.HeaderAdminToken, "secret")
	}
	auth.RequireAdminToken("secret", discardLogger())(handler).ServeHTTP(rec, req)
	return rec
}

// usagePathTenantID extracts the :tenantID segment from /admin/usage/:tenantID paths.
func usagePathTenantID(path string) string {
	const prefix = "/admin/usage/"
	after, ok := strings.CutPrefix(path, prefix)
	if !ok {
		return ""
	}
	return after
}

func decodeData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var data T
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	return data
}
