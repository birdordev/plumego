package singleflight

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type waiterContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *waiterContext) Done() <-chan struct{} { c.once.Do(func() { close(c.entered) }); return nil }

func TestFinalResponseSemantics(t *testing.T) {
	for _, status := range []int{0, 200, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			handler := New(Config{KeyFunc: func(*http.Request) string { return "same" }}).Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status != 0 {
					w.Header().Set("X-Interim", "temporary")
					w.WriteHeader(103)
					w.Header().Del("X-Interim")
				}
				w.Header().Set("X-Final", "kept")
				close(entered)
				<-release
				if status != 0 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte("body"))
				}
			}))
			// The leader uses a real server so informational responses have wire semantics.
			server := httptest.NewServer(handler)
			defer server.Close()
			leader := make(chan *http.Response, 1)
			fail := make(chan error, 1)
			go func() {
				res, err := http.Get(server.URL)
				if err != nil {
					fail <- err
					return
				}
				leader <- res
			}()
			<-entered
			waiter := httptest.NewRecorder()
			ctx := &waiterContext{Context: context.Background(), entered: make(chan struct{})}
			done := make(chan struct{})
			go func() {
				handler.ServeHTTP(waiter, httptest.NewRequest("GET", server.URL, nil).WithContext(ctx))
				close(done)
			}()
			<-ctx.entered
			close(release)
			<-done
			var response *http.Response
			select {
			case response = <-leader:
			case err := <-fail:
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := status
			if want == 0 {
				want = 200
			}
			for name, res := range map[string]*http.Response{"leader": response, "waiter": waiter.Result()} {
				if res.StatusCode != want || res.Header.Get("X-Final") != "kept" || res.Header.Get("X-Interim") != "" {
					t.Errorf("%s status=%d headers=%v", name, res.StatusCode, res.Header)
				}
			}
			if status != 0 && (string(body) != "body" || waiter.Body.String() != "body") {
				t.Fatalf("bodies: %q / %q", body, waiter.Body.String())
			}
		})
	}
}
