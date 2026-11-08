package healthhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestComponentHealthAfterCloseFailsClosed(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	checker := functionChecker{name: "db", check: func(context.Context) error { return nil }}
	if err := manager.RegisterComponent(checker); err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckComponent(t.Context(), "db"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	ComponentHealthHandler(manager, "db").ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), `"status":"healthy"`) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}
func TestComponentHealthFailureDoesNotExposeBackendError(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterComponent(functionChecker{name: "db", check: func(context.Context) error { return errors.New("private-backend-canary") }}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	ComponentHealthHandler(manager, "db").ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "private-backend-canary") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	missing := httptest.NewRecorder()
	ComponentHealthHandler(manager, "missing").ServeHTTP(missing, httptest.NewRequest("GET", "/", nil))
	if missing.Code != 404 {
		t.Fatalf("missing status=%d", missing.Code)
	}
}

func TestComponentCheckSupersededDoesNotReplayCachedSuccess(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	checker := functionChecker{name: "db", check: func(context.Context) error {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil
	}}
	if err := manager.RegisterComponent(checker); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ComponentHealthHandler(manager, "db").ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	}()
	<-started
	if err := manager.CheckComponent(t.Context(), "db"); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	if rec.Code != 503 {
		t.Fatalf("superseded check status=%d body=%s", rec.Code, rec.Body.String())
	}
	component, ok := manager.GetComponentHealth("db")
	if !ok || component.Status != "healthy" {
		t.Fatal("handler changed the newer shared health result")
	}
}

func TestComponentHealthCanceledProbeFailsClosed(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterComponent(functionChecker{name: "db", check: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckComponent(t.Context(), "db"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rec := httptest.NewRecorder()
	ComponentHealthHandler(manager, "db").ServeHTTP(rec, httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	if rec.Code != 503 {
		t.Fatalf("canceled check status=%d", rec.Code)
	}
}
