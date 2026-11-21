package router

import (
	"context"
	"github.com/birdordev/plumego/contract"
	"net/http"
	"net/http/httptest"
	"testing"
)

type routeReceiver struct {
	http.ResponseWriter
	route contract.RequestContext
}

func (w *routeReceiver) SetRouteContext(ctx context.Context) {
	w.route = contract.RequestContextFromContext(ctx)
}
func TestRoutePublication(t *testing.T) {
	r := NewRouter()
	if err := r.AddRoute("GET", "/items/:id", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), WithRouteName("item")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/items/1", "/items/1", "/missing"} {
		w := &routeReceiver{ResponseWriter: httptest.NewRecorder()}
		req := httptest.NewRequest("GET", path, nil)
		r.ServeHTTP(w, req)
		if path != "/missing" && (w.route.RoutePattern != "/items/:id" || w.route.RouteName != "item") {
			t.Fatalf("route=%+v", w.route)
		}
		if contract.RequestContextFromContext(req.Context()).RoutePattern != "" {
			t.Fatal("incoming request mutated")
		}
	}
}

func TestRouteSnapshotSurvivesCacheEvictionDuringDispatch(t *testing.T) {
	r := NewRouter(WithCacheCapacity(1))
	entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() { <-done }()
	defer close(resume)
	if err := r.AddRoute(http.MethodGet, "/a/:id", http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		close(entered)
		<-resume
		if got := Param(req, "id"); got != "first" {
			t.Errorf("in-flight parameter changed to %q", got)
		}
	}), WithRouteName("a")); err != nil {
		close(done)
		t.Fatal(err)
	}
	if err := r.AddRoute(http.MethodGet, "/b/:id", http.NotFoundHandler(), WithRouteName("b")); err != nil {
		close(done)
		t.Fatal(err)
	}
	first := &routeReceiver{ResponseWriter: httptest.NewRecorder()}
	go func() {
		defer close(done)
		r.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/a/first", nil))
	}()
	<-entered
	next := &routeReceiver{ResponseWriter: httptest.NewRecorder()}
	r.ServeHTTP(next, httptest.NewRequest(http.MethodGet, "/b/second", nil))
	if first.route.RouteName != "a" || first.route.Params["id"] != "first" ||
		next.route.RouteName != "b" || next.route.Params["id"] != "second" {
		t.Fatalf("first=%+v next=%+v", first.route, next.route)
	}
}
