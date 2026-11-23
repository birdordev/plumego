package httpmetrics_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/abuseguard"
	"github.com/birdordev/plumego/middleware/accesslog"
	"github.com/birdordev/plumego/middleware/httpmetrics"
	"github.com/birdordev/plumego/router"
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
	paths := make(chan string, 1)
	r := router.NewRouter(router.WithMethodNotAllowed(true))
	if err := r.AddRoute(http.MethodGet, "/items/:id", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })); err != nil {
		t.Fatal(err)
	}
	guard := abuseguard.NewAbuseGuard(abuseguard.AbuseGuardConfig{Rate: 0.001, Capacity: 1})
	t.Cleanup(guard.Stop)
	h := httpmetrics.MiddlewareWithRouteLabels(routeObserver{paths: paths})(guard.Middleware()(r))
	for _, tc := range []struct {
		method, path, peer, label string
		status                    int
	}{
		{http.MethodGet, "/wrong/one", "192.0.2.1:80", "unmatched", 404},
		{http.MethodGet, "/wrong/two", "192.0.2.2:80", "unmatched", 404},
		{http.MethodPost, "/items/one", "192.0.2.3:80", "unmatched", 405},
		{http.MethodGet, "/items/one", "192.0.2.1:80", "unclassified", 429},
		{http.MethodGet, "/items/two", "192.0.2.1:80", "unclassified", 429},
		{http.MethodGet, "/items/one", "192.0.2.4:80", "/items/:id", 204},
		{http.MethodGet, "/items/two", "192.0.2.5:80", "/items/:id", 204},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.RemoteAddr = tc.peer
		h.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s %s: status=%d, want %d", tc.method, tc.path, w.Code, tc.status)
		}
		if got := <-paths; got != tc.label {
			t.Fatalf("route label=%q, want %q", got, tc.label)
		}
	}
}
