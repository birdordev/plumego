package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/health"
	plumelog "github.com/birdordev/plumego/log"
)

func discardLogger() plumelog.StructuredLogger {
	return plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard})
}

func TestHealthHandlerLiveReturns200(t *testing.T) {
	h := HealthHandler{ServiceName: "gw-test", Logger: discardLogger()}
	rec := httptest.NewRecorder()
	h.Live(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHealthHandlerReadyNoCheckersReturns200(t *testing.T) {
	h := HealthHandler{ServiceName: "gw-test", Logger: discardLogger()}
	rec := httptest.NewRecorder()
	h.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// alwaysFailChecker is a health.ComponentChecker that always fails.
type alwaysFailChecker struct{ name string }

func (c alwaysFailChecker) Name() string                { return c.name }
func (c alwaysFailChecker) Check(context.Context) error { return errors.New("component down") }

var _ health.ComponentChecker = alwaysFailChecker{}

func TestHealthHandlerReadyWithFailingCheckerReturns503(t *testing.T) {
	h := HealthHandler{
		ServiceName: "gw-test",
		Logger:      discardLogger(),
		Checkers:    []health.ComponentChecker{alwaysFailChecker{"db"}},
	}
	rec := httptest.NewRecorder()
	h.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
