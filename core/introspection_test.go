package core

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/router"
)

func TestNewAppStartsMutable(t *testing.T) {
	app := New(DefaultConfig(), AppDependencies{})

	if err := app.Use(func(next http.Handler) http.Handler { return next }); err != nil {
		t.Fatalf("expected new app to accept middleware registration, got %v", err)
	}
	if err := app.Get("/mutable", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatalf("expected new app to accept route registration, got %v", err)
	}
}

func TestAppRouteIntrospectionReturnsIndependentTags(t *testing.T) {
	app := New(DefaultConfig(), AppDependencies{})
	if err := app.AddRoute(http.MethodGet, "/items/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Route", contract.RequestContextFromContext(r.Context()).RouteName)
	}), router.WithRouteName("item"), router.WithTags("original")); err != nil {
		t.Fatal(err)
	}
	snapshot := app.Routes()
	snapshot[0].Meta.Tags[0] = "changed"
	snapshot[0].Meta.Name = "changed"
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/items/1", nil))
	if now := app.Routes()[0].Meta; now.Name != "item" || now.Tags[0] != "original" || w.Header().Get("X-Route") != "item" {
		t.Fatalf("metadata=%+v response=%v", now, w.Header())
	}
}
