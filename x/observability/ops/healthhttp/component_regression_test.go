package healthhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
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
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("canceled check status=%d", rec.Code)
	}
	if got, ok := manager.GetComponentHealth("db"); !ok || got.Status != "unhealthy" || got.Details["check_attempts"] != 0 {
		t.Fatalf("canceled probe status: %+v", got)
	}
	rec = httptest.NewRecorder()
	ComponentHealthHandler(manager, "db").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("successful follow-up status=%d", rec.Code)
	}
	if got, ok := manager.GetComponentHealth("db"); !ok || got.Status != "healthy" {
		t.Fatalf("fresh successful status: %+v", got)
	}
}

func TestAcceptanceRetryDelayReturnsContextCauseAndCountsAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager, err := NewManager(Config{RetryCount: 1, RetryDelay: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		if err := manager.RegisterComponent(functionChecker{name: "fixture", check: func(context.Context) error {
			if calls.Add(1) == 1 {
				return errors.New("backend fixture")
			}
			return nil
		}}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() { result <- manager.CheckComponent(ctx, "fixture") }()
		synctest.Wait()
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) || calls.Load() != 1 {
			t.Fatalf("checks=%d err=%v", calls.Load(), err)
		}
		component, _ := manager.GetComponentHealth("fixture")
		if attempts := component.Details["check_attempts"]; attempts != 1 {
			t.Fatalf("attempts=%v", attempts)
		}
		if err := manager.CheckComponent(t.Context(), "fixture"); err != nil {
			t.Fatalf("subsequent check: %v", err)
		}
	})
}
