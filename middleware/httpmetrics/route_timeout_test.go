package httpmetrics_test

import (
	"context"
	"github.com/birdordev/plumego/middleware/httpmetrics"
	"github.com/birdordev/plumego/middleware/timeout"
	"github.com/birdordev/plumego/router"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOuterObservationsWithTimeout(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "after-cancel"}[late], func(t *testing.T) {
			rt := router.NewRouter()
			if err := rt.AddRoute("GET", "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })); err != nil {
				t.Fatal(err)
			}
			entered, release, workerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(workerDone)
				close(entered)
				if late {
					<-release
				}
				rt.ServeHTTP(w, r)
			})
			o := routeObserver{paths: make(chan string, 1)}
			h := httpmetrics.Middleware(o)(timeout.Middleware(timeout.Config{Timeout: time.Hour})(inner))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/users/123", nil).WithContext(ctx))
				close(done)
			}()
			<-entered
			if late {
				cancel()
			}
			<-done
			got := <-o.paths
			if late {
				close(release)
				if got != "/users/123" {
					t.Fatalf("canceled path=%q", got)
				}
			} else if got != "/users/:id" {
				t.Fatalf("path=%q", got)
			}
			<-workerDone
		})
	}
}
