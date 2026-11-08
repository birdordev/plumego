package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	"with-tenant-admin/internal/auth"
	tenantadmin "with-tenant-admin/internal/tenant/admin"
)

func discardLogger() plumelog.StructuredLogger {
	return plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard})
}

func TestGetQuotaExistingTenantReturnsLimitAndRemaining(t *testing.T) {
	h, quotaStore, now := newTestHandler(t, "tenant-1", 5)
	reserveQuotaUsage(t, quotaStore, now, "tenant-1", 2)

	rec := serveQuotaAdmin(t, h.GetQuota, http.MethodGet, "/admin/quota/tenant-1", "", true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	got := decodeData[QuotaResponse](t, rec)
	if got.Limit != 5 || got.Used != 2 || got.Remaining != 3 {
		t.Fatalf("quota = %+v, want limit 5 used 2 remaining 3", got)
	}
}

func TestGetQuotaUnknownTenantReturnsNotFound(t *testing.T) {
	h, _, _ := newTestHandler(t, "", 0)

	rec := serveQuotaAdmin(t, h.GetQuota, http.MethodGet, "/admin/quota/missing", "", true)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSetQuotaUpdatesLimit(t *testing.T) {
	h, _, _ := newTestHandler(t, "tenant-1", 5)

	rec := serveQuotaAdmin(t, h.SetQuota, http.MethodPut, "/admin/quota/tenant-1", `{"limit":9}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("set status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	got := decodeData[QuotaResponse](t, rec)
	if got.Limit != 9 {
		t.Fatalf("set quota limit = %d, want 9", got.Limit)
	}

	rec = serveQuotaAdmin(t, h.GetQuota, http.MethodGet, "/admin/quota/tenant-1", "", true)
	got = decodeData[QuotaResponse](t, rec)
	if got.Limit != 9 {
		t.Fatalf("get quota limit = %d, want 9", got.Limit)
	}
}

func TestResetQuotaClearsUsedCounter(t *testing.T) {
	h, quotaStore, now := newTestHandler(t, "tenant-1", 5)
	reserveQuotaUsage(t, quotaStore, now, "tenant-1", 4)

	rec := serveQuotaAdmin(t, h.ResetQuota, http.MethodPost, "/admin/quota/tenant-1/reset", "", true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	got := decodeData[QuotaResponse](t, rec)
	if got.Used != 0 || got.Remaining != 5 {
		t.Fatalf("quota = %+v, want used 0 remaining 5", got)
	}
}

func TestUnauthenticatedQuotaAdminReturnsUnauthorized(t *testing.T) {
	h, _, _ := newTestHandler(t, "tenant-1", 5)

	rec := serveQuotaAdmin(t, h.GetQuota, http.MethodGet, "/admin/quota/tenant-1", "", false)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestSetQuotaNegativeLimitReturnsBadRequest(t *testing.T) {
	h, _, _ := newTestHandler(t, "tenant-1", 5)

	rec := serveQuotaAdmin(t, h.SetQuota, http.MethodPut, "/admin/quota/tenant-1", `{"limit":-1}`, true)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func newTestHandler(t *testing.T, tenantID string, limit int64) (*Handler, *tenantcore.InMemoryQuotaStore, time.Time) {
	t.Helper()
	tenantStore := tenantadmin.NewInMemoryStore()
	configs := tenantcore.NewInMemoryConfigManager()
	if tenantID != "" {
		_, _ = tenantStore.Create(t.Context(), tenantadmin.TenantRecord{ID: tenantID, Name: "Acme"})
		configs.SetTenantConfig(tenantcore.Config{
			TenantID: tenantID,
			Quota: tenantcore.QuotaConfig{
				Limits: []tenantcore.QuotaLimit{{
					Window:   tenantcore.QuotaWindowMinute,
					Requests: limit,
				}},
			},
		})
	}
	quotaStore := tenantcore.NewInMemoryQuotaStore()
	h := NewHandler(tenantStore, configs, quotaStore, discardLogger())
	now := time.Date(2026, 5, 18, 10, 11, 12, 0, time.UTC)
	h.now = func() time.Time { return now }
	return h, quotaStore, now
}

func reserveQuotaUsage(t *testing.T, quotaStore *tenantcore.InMemoryQuotaStore, now time.Time, tenantID string, count int64) {
	t.Helper()
	_, ok, err := quotaStore.Reserve(t.Context(), tenantcore.QuotaReserveRequest{
		TenantID:      tenantID,
		Window:        tenantcore.QuotaWindowMinute,
		WindowStart:   now.UTC().Truncate(time.Minute),
		DeltaRequests: count,
		LimitRequests: 100,
	})
	if err != nil {
		t.Fatalf("reserve quota usage: %v", err)
	}
	if !ok {
		t.Fatal("reserve quota usage was rejected")
	}
}

// serveQuotaAdmin calls handler directly — bypassing the router — with the
// tenantID path parameter injected via contract.WithRequestContext.
// The tenantID is extracted from the path by stripping the /admin/quota/ prefix
// and any trailing action segment, matching what the router would set at runtime.
func serveQuotaAdmin(t *testing.T, handler http.HandlerFunc, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tenantID := quotaPathTenantID(path); tenantID != "" {
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

// quotaPathTenantID extracts the :tenantID segment from paths of the form
// /admin/quota/:tenantID or /admin/quota/:tenantID/reset.
func quotaPathTenantID(path string) string {
	const prefix = "/admin/quota/"
	after, ok := strings.CutPrefix(path, prefix)
	if !ok {
		return ""
	}
	id, _, _ := strings.Cut(after, "/")
	return id
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
