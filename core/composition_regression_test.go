package core

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if err := child.Get("/items/:id", wrap("route")(h)); err != nil {
		t.Fatal(err)
	}
	if err := app.Group("/public").Get("/items/:id", h); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, want string }{{"/api/v1/items/one", "global,parent,child,route,handler"}, {"/public/items/one", "global,handler"}} {
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

func TestRunContextClosesActiveConnectionAfterDrainTimeout(t *testing.T) {
	for _, cause := range []string{"cancel", "listener exit"} {
		t.Run(cause, func(t *testing.T) {
			ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			cfg := DefaultConfig()
			cfg.Addr = "127.0.0.1:0"
			app := New(cfg, AppDependencies{})
			entered := make(chan struct{})
			if err := app.Get("/hold", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				close(entered)
				<-r.Context().Done()
			})); err != nil {
				t.Fatal(err)
			}
			if err := app.Prepare(); err != nil {
				t.Fatal(err)
			}
			server, err := app.Server()
			if err != nil {
				t.Fatal(err)
			}
			bound := make(chan net.Listener, 1)
			server.BaseContext = func(listener net.Listener) context.Context {
				bound <- listener
				return ctx
			}
			runErr := make(chan error, 1)
			go func() { runErr <- app.RunContext(runCtx, 25*time.Millisecond) }()
			var listener net.Listener
			select {
			case listener = <-bound:
			case err := <-runErr:
				t.Fatalf("server exited before listening: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/hold", nil)
			if err != nil {
				t.Fatal(err)
			}
			requestErr := make(chan error, 1)
			go func() {
				resp, err := http.DefaultClient.Do(req)
				if resp != nil {
					_ = resp.Body.Close()
				}
				requestErr <- err
			}()
			select {
			case <-entered:
			case err := <-requestErr:
				t.Fatalf("request completed before entering handler: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if cause == "cancel" {
				cancel()
			} else if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-runErr:
				if !errors.Is(err, context.DeadlineExceeded) || (cause == "listener exit" && !errors.Is(err, net.ErrClosed)) {
					t.Fatalf("run error = %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case err := <-requestErr:
				if err == nil || errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("request error = %v, want connection closed", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}

func TestMountOwnsPrefixAcrossMethodsAndFallbacks(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Router.MethodNotAllowed = true
	app := New(cfg, AppDependencies{})
	child := router.NewRouter(router.WithMethodNotAllowed(true))
	if err := child.AddRoute(http.MethodPost, "/api/items", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})); err != nil {
		t.Fatal(err)
	}
	child.Freeze()
	if err := app.Get("/*page", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("frontend"))
	})); err != nil {
		t.Fatal(err)
	}
	if err := app.Mount("/api", child); err != nil {
		t.Fatal(err)
	}
	if err := app.Mount("/api/v2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/items" {
			t.Errorf("child path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, allow string
		status              int
	}{
		{"GET", "/api/missing", "", 404},
		{"GET", "/api/items", "POST", 405},
		{"HEAD", "/api/items", "POST", 405},
		{"POST", "/api/items", "", 201},
		{"GET", "/api/v2/items", "", 204},
		{"GET", "/api-legacy", "", 200},
	} {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.status || rec.Header().Get("Allow") != tc.allow || tc.method == "HEAD" && rec.Body.Len() != 0 {
			t.Errorf("%s %s: status=%d, Allow=%q, body=%q", tc.method, tc.path, rec.Code, rec.Header().Get("Allow"), rec.Body.String())
		}
	}
}

func TestMountFailureDoesNotPublishHalfAMount(t *testing.T) {
	app := New(DefaultConfig(), AppDependencies{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	if err := app.Any("/api/*existing", handler); err != nil {
		t.Fatal(err)
	}
	if err := app.Mount("/api", handler); err == nil {
		t.Fatal("conflicting mount was accepted")
	}
	routes := app.Routes()
	if len(routes) != 1 || routes[0].Path != "/api/*existing" {
		t.Fatalf("routes after failed mount = %+v", routes)
	}
}
