package core

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/router"
)

func TestGroupMiddlewareUsesMatchedRequest(t *testing.T) {
	app := New(DefaultConfig(), AppDependencies{})
	var order []string
	wrap := func(label string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, label)
				if label == "child" && router.Param(r, "id") != "one" {
					t.Error("child middleware did not receive path parameter")
				}
				next.ServeHTTP(w, r)
			})
		}
	}
	parent := app.Group("/api")
	child := parent.Group("/v1")
	for _, err := range []error{app.Use(wrap("global")), parent.Use(wrap("parent")), child.Use(wrap("child"))} {
		if err != nil {
			t.Fatal(err)
		}
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "handler")
		w.WriteHeader(http.StatusNoContent)
	})
	if err := child.Get("/items/:id", h); err != nil {
		t.Fatal(err)
	}
	if err := app.Group("/public").Get("/items/:id", h); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, want string }{{"/api/v1/items/one", "global,parent,child,handler"}, {"/public/items/one", "global,handler"}} {
		order = nil
		app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tc.path, nil))
		if got := strings.Join(order, ","); got != tc.want {
			t.Fatalf("%s order=%s", tc.path, got)
		}
	}
	if err := child.Use(wrap("late")); err == nil {
		t.Fatal("group mutable after preparation")
	}
}

func TestRunContextReportsBindErrorAndHonorsCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	cfg := DefaultConfig()
	cfg.Addr = listener.Addr().String()
	app := New(cfg, AppDependencies{})
	if err := app.RunContext(t.Context(), 1); err == nil {
		t.Fatal("expected bind error")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := app.RunContext(ctx, 1); err != context.Canceled {
		t.Fatalf("error=%v", err)
	}
}
