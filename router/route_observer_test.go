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
