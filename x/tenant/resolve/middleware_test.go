package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/security/authn"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	tenanttransport "github.com/birdordev/plumego/x/tenant/transport"
)

func TestMiddlewareFromHeader(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenantcore.TenantIDFromContext(r.Context()) != "t-1" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(authn.WithPrincipal(req.Context(), &authn.Principal{TenantID: "t-1"}))
	rec := httptest.NewRecorder()

	mw := Middleware(Options{})
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}

func TestMiddlewareExtractorTakesPrecedenceOverHeader(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenantcore.TenantIDFromContext(r.Context()) != "query-tenant" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/?tenant=query-tenant", nil)
	req.Header.Set("X-Tenant-ID", "header-tenant")
	rec := httptest.NewRecorder()

	mw := Middleware(Options{
		Extractor:        tenantcore.FromQuery("tenant"),
		DisablePrincipal: true,
	})
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}

func TestMiddlewareInvalidTenantID(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "bad tenant id")
	rec := httptest.NewRecorder()

	mw := Middleware(Options{})
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != tenanttransport.CodeInvalidID {
		t.Fatalf("unexpected error code %q", body.Error.Code)
	}
}

func TestMiddlewareMissing(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	mw := Middleware(Options{})
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != tenanttransport.CodeRequired {
		t.Fatalf("unexpected error code %q", body.Error.Code)
	}
}

// TestMiddlewareExtractorErrorTreatedAsMissing verifies that when a custom extractor
// returns an error, the middleware fails closed: the error is treated as "no tenant"
// and the request is rejected with 401 (default AllowMissing: false).
func TestMiddlewareExtractorErrorTreatedAsMissing(t *testing.T) {
	sentinel := errors.New("resolver backend unavailable")
	failExtractor := func(*http.Request) (string, error) {
		return "", sentinel
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	mw := Middleware(Options{
		Extractor:        failExtractor,
		DisablePrincipal: true,
	})
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when extractor fails, got %d", rec.Code)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != tenanttransport.CodeRequired {
		t.Fatalf("error code = %q, want %q", body.Error.Code, tenanttransport.CodeRequired)
	}
}

// TestMiddlewareAllowMissingPassthrough verifies that when AllowMissing is true,
// a request with no tenant identity passes through to the handler with no tenant
// in context.
func TestMiddlewareAllowMissingPassthrough(t *testing.T) {
	handlerCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		if id := tenantcore.TenantIDFromContext(r.Context()); id != "" {
			t.Errorf("expected empty tenant ID in context, got %q", id)
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	mw := Middleware(Options{AllowMissing: true, DisablePrincipal: true})
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with AllowMissing, got %d", rec.Code)
	}
	if !handlerCalled {
		t.Fatal("handler should be called when AllowMissing is true and tenant is absent")
	}
}

// TestMiddlewareOnMissingCallbackInvoked verifies that a custom OnMissing callback
// is called instead of the default 401 response when the tenant is absent.
func TestMiddlewareOnMissingCallbackInvoked(t *testing.T) {
	callbackCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	mw := Middleware(Options{
		DisablePrincipal: true,
		OnMissing: func(w http.ResponseWriter, r *http.Request) {
			callbackCalled = true
			w.WriteHeader(http.StatusServiceUnavailable)
		},
	})
	mw(handler).ServeHTTP(rec, req)

	if !callbackCalled {
		t.Fatal("OnMissing callback should have been called")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected callback status 503, got %d", rec.Code)
	}
}

// TestMiddlewareHookInvokedOnSuccessfulResolve verifies that the Resolve hook is
// called with the correct TenantID and source when a tenant is resolved from a header.
func TestMiddlewareHookInvokedOnSuccessfulResolve(t *testing.T) {
	var resolveInfo tenantcore.ResolveInfo
	hookCalled := false

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "hook-tenant")
	rec := httptest.NewRecorder()

	mw := Middleware(Options{
		DisablePrincipal: true,
		Hooks: tenantcore.Hooks{
			OnResolve: func(_ context.Context, info tenantcore.ResolveInfo) {
				hookCalled = true
				resolveInfo = info
			},
		},
	})
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !hookCalled {
		t.Fatal("Resolve hook should have been called")
	}
	if resolveInfo.TenantID != "hook-tenant" {
		t.Fatalf("hook TenantID = %q, want hook-tenant", resolveInfo.TenantID)
	}
	if resolveInfo.Source != "header" {
		t.Fatalf("hook Source = %q, want header", resolveInfo.Source)
	}
}
