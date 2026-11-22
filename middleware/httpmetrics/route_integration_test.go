package httpmetrics_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/accesslog"
	"github.com/birdordev/plumego/middleware/httpmetrics"
	"github.com/birdordev/plumego/router"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type routeObserver struct{ paths chan string }

func (o routeObserver) ObserveHTTP(_ context.Context, _, path string, _, _ int, _ time.Duration) {
	o.paths <- path
}
func TestOuterObservations(t *testing.T) {
	var output bytes.Buffer
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatJSON, Output: &output})
	logs, err := accesslog.Middleware(accesslog.Config{Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	o := routeObserver{paths: make(chan string, 2)}
	app := core.New(core.DefaultConfig(), core.AppDependencies{})
	if err = app.Use(httpmetrics.Middleware(o), logs); err != nil {
		t.Fatal(err)
	}
	if err = app.AddRoute("GET", "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), router.WithRouteName("users.show")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/users/1", "/users/2"} {
		app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
		if got := <-o.paths; got != "/users/:id" {
			t.Errorf("metric path=%q", got)
		}
	}
	decoder := json.NewDecoder(&output)
	for i := 0; i < 2; i++ {
		var entry map[string]any
		if err = decoder.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		if entry["route"] != "/users/:id" || entry["route_name"] != "users.show" {
			t.Errorf("log route=%v name=%v", entry["route"], entry["route_name"])
		}
	}
}

func TestBoundedFallbackRouteLabels(t *testing.T) {
	paths := make(chan string, 3)
	r := router.NewRouter()
	if err := r.AddRoute(http.MethodGet, "/items/:id", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })); err != nil {
		t.Fatal(err)
	}
	h := httpmetrics.MiddlewareWithRouteLabels(routeObserver{paths: paths})(r)
	for _, tc := range []struct{ path, label string }{{"/wrong/one", "unmatched"}, {"/wrong/two", "unmatched"}, {"/items/one", "/items/:id"}} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tc.path, nil))
		if got := <-paths; got != tc.label {
			t.Fatalf("route label=%q, want %q", got, tc.label)
		}
	}
}
