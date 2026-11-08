package httpmetrics_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spcent/plumego/middleware/httpmetrics"
)

type statusObserver struct{ statuses chan int }

func (o statusObserver) ObserveHTTP(_ context.Context, _ string, _ string, status, _ int, _ time.Duration) {
	o.statuses <- status
}

func TestObservedTransport(t *testing.T) {
	for _, status := range []int{200, 404, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			statuses := make(chan int, 2)
			h := httpmetrics.Middleware(statusObserver{statuses})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Interim", "yes")
				w.WriteHeader(100)
				w.WriteHeader(103)
				w.Header().Del("X-Interim")
				w.Header().Set("X-Final", "yes")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "body")
			}))
			h = httpmetrics.Middleware(statusObserver{statuses})(h)
			server := httptest.NewServer(h)
			defer server.Close()
			resp, err := http.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != status || <-statuses != status || <-statuses != status || string(body) != "body" || resp.Header.Get("X-Final") != "yes" || resp.Header.Get("X-Interim") != "" {
				t.Fatalf("unexpected response: %+v body=%q", resp, body)
			}
		})
	}
	for _, flush := range []bool{false, true} {
		t.Run(map[bool]string{false: "header-only", true: "flush"}[flush], func(t *testing.T) {
			statuses := make(chan int, 2)
			h := httpmetrics.Middleware(statusObserver{statuses})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Final", "yes")
				if flush {
					w.(http.Flusher).Flush()
					w.WriteHeader(418)
				}
			}))
			h = httpmetrics.Middleware(statusObserver{statuses})(h)
			server := httptest.NewServer(h)
			defer server.Close()
			resp, err := http.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			if resp.StatusCode != 200 || <-statuses != 200 || <-statuses != 200 || resp.Header.Get("X-Final") != "yes" {
				t.Fatalf("unexpected response: %+v", resp)
			}
		})
	}
}

func TestObservedTrailers(t *testing.T) {
	h := httpmetrics.Middleware(statusObserver{make(chan int, 1)})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "X-Sum")
		w.Header().Set("X-Frozen", "before")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "body")
		w.Header().Set("X-Sum", "done")
		w.Header().Set(http.TrailerPrefix+"X-Extra", "extra")
		w.Header().Set("X-Frozen", "after")
	}))
	server := httptest.NewServer(h)
	defer server.Close()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.Trailer.Get("X-Sum") != "done" || resp.Trailer.Get("X-Extra") != "extra" || resp.Header.Get("X-Frozen") != "before" {
		t.Fatalf("headers=%v trailers=%v", resp.Header, resp.Trailer)
	}
}
