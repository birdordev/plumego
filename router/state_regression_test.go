package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spcent/plumego/contract"
	"github.com/spcent/plumego/middleware/timeout"
	"github.com/spcent/plumego/router"
)

func TestRouteMetadataSurvivesInnerTimeout(t *testing.T) {
	rt := router.NewRouter(router.WithCacheCapacity(1))
	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan contract.RequestContext, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		result <- contract.RequestContextFromContext(r.Context())
	})
	if err := rt.AddRoute("GET", "/users/:id", timeout.Middleware(timeout.Config{Timeout: time.Hour})(handler), router.WithRouteName("user")); err != nil {
		t.Fatal(err)
	}
	if err := rt.AddRoute("GET", "/evict", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		rt.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/users/123", nil).WithContext(ctx))
	}()
	<-started
	cancel()
	<-done
	// Evict the timed-out match while the worker still owns its context.
	rt.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/evict", nil))
	close(release)
	got := <-result
	if got.Params["id"] != "123" || got.RoutePattern != "/users/:id" || got.RouteName != "user" {
		t.Fatalf("metadata lost: %+v", got)
	}
}

func TestRouteMetadataConcurrentCancellation(t *testing.T) {
	rt := router.NewRouter()
	started := make(chan struct{})
	stop := make(chan struct{})
	workerDone := make(chan struct{})
	outerDone := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(workerDone)
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
				if got := router.Param(r, "id"); got != "123" {
					t.Errorf("parameter changed: %q", got)
					return
				}
			}
		}
	})
	if err := rt.AddRoute("GET", "/users/:id", timeout.Middleware(timeout.Config{Timeout: time.Hour})(handler)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		defer close(outerDone)
		rt.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/users/123", nil).WithContext(ctx))
	}()
	<-started
	cancel()
	<-outerDone
	close(stop)
	<-workerDone
}

func TestRouteParametersBeyondInlineCapacity(t *testing.T) {
	rt := router.NewRouter()
	if err := rt.AddRoute("GET", "/:a/:b/:c/:d/:e/:f/:g/:h/:i/*rest", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := router.Param(r, "i"); got != "9" {
			t.Errorf("ninth parameter=%q", got)
		}
		if got := router.Param(r, "rest"); got != "tail/end" {
			t.Errorf("wildcard=%q", got)
		}
		snapshot := contract.RequestContextFromContext(r.Context())
		if len(snapshot.Params) != 10 {
			t.Errorf("parameter count=%d", len(snapshot.Params))
		}
	})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		rt.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/1/2/3/4/5/6/7/8/9/tail/end", nil))
	}
}
