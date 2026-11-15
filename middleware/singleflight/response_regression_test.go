package singleflight

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
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

type leaderWriteRecorder struct {
	*httptest.ResponseRecorder
	writeBytes int
	writeErr   error
	headers    int
}

func (w *leaderWriteRecorder) WriteHeader(status int) {
	w.headers++
	w.ResponseRecorder.WriteHeader(status)
}

func (w *leaderWriteRecorder) Write(p []byte) (int, error) {
	n, _ := w.ResponseRecorder.Write(p[:w.writeBytes])
	return n, w.writeErr
}

func TestLeaderWriteOutcomeControlsWaiterReplay(t *testing.T) {
	writeErr := errors.New("transport failure")
	for _, tc := range []struct {
		name                  string
		bytes                 int
		writerErr, handlerErr error
		status                int
	}{
		{"complete", 6, nil, nil, http.StatusOK},
		{"short", 3, nil, io.ErrShortWrite, http.StatusBadGateway},
		{"explicit error", 3, writeErr, writeErr, http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				leader := &leaderWriteRecorder{ResponseRecorder: httptest.NewRecorder(), writeBytes: tc.bytes, writeErr: tc.writerErr}
				waiter := httptest.NewRecorder()
				handler := New(Config{}).Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					close(entered)
					<-release
					if n, err := io.WriteString(w, "sample"); n != tc.bytes || !errors.Is(err, tc.handlerErr) {
						t.Errorf("leader write=%d, %v", n, err)
					}
				}))
				go handler.ServeHTTP(leader, httptest.NewRequest(http.MethodGet, "/", nil))
				<-entered
				go handler.ServeHTTP(waiter, httptest.NewRequest(http.MethodGet, "/", nil))
				synctest.Wait()
				close(release)
				synctest.Wait()
				if leader.headers != 1 || leader.Body.String() != "sample"[:tc.bytes] {
					t.Fatalf("leader headers=%d, body=%q", leader.headers, leader.Body)
				}
				if waiter.Code != tc.status || (tc.status == http.StatusOK && waiter.Body.String() != "sample") {
					t.Fatalf("waiter status=%d, body=%q", waiter.Code, waiter.Body)
				}
			})
		})
	}
}
