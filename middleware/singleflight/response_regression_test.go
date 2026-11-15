package singleflight

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type waiterContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *waiterContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

type waiterSelectContext struct {
	context.Context
	beforeSelect func()
}

func (c waiterSelectContext) Done() <-chan struct{} {
	c.beforeSelect()
	return c.Context.Done()
}

func TestWaiterCancellationWinsOverOtherReadyOutcomes(t *testing.T) {
	for _, outcome := range []string{"complete", "leader failed", "timeout"} {
		t.Run(outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const timeout = time.Second
				failHook := func(string, error) { t.Error("reported an error for canceled waiter") }
				replayHook := func(string, int) { t.Error("reported a replay for canceled waiter") }
				c := New(Config{Timeout: timeout, OnError: failHook, OnCoalesced: replayHook})
				// Repeated selects exercise both ready choices in the pre-fix code.
				for range 32 {
					inflight := &inFlightRequest{done: make(chan struct{})}
					inflight.waiters.Store(1)
					ctx, cancel := context.WithCancel(t.Context())
					req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(waiterSelectContext{
						Context: ctx,
						beforeSelect: func() {
							cancel()
							switch outcome {
							case "complete":
								inflight.response = &capturedResponse{statusCode: http.StatusOK, body: []byte("leader")}
								close(inflight.done)
							case "leader failed":
								inflight.err = errLeaderWrite
								close(inflight.done)
							case "timeout":
								time.Sleep(timeout) // Advance the synthetic clock before selection.
							}
						},
					})
					w := &responseWriteRecorder{ResponseRecorder: httptest.NewRecorder()}
					c.waitForInFlight(w, req, "same", inflight)
					if w.headers != 0 || w.Body.Len() != 0 || inflight.waiters.Load() != 0 {
						t.Fatalf("headers=%d body=%q waiters=%d", w.headers, w.Body, inflight.waiters.Load())
					}
				}
			})
		})
	}
}

func TestCoalescedResponsesPreserveTrailers(t *testing.T) {
	leaderEntered := make(chan struct{})
	waiterJoined := make(chan struct{})
	release := make(chan struct{})
	releaseLeader := sync.OnceFunc(func() { close(release) })
	coalesce := New(Config{KeyFunc: func(*http.Request) string { return "same" }}).Middleware()
	handler := coalesce(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/leader" {
			t.Error("the waiter executed the downstream handler")
			return
		}
		close(leaderEntered)
		<-release
		w.Header().Set("Trailer", "X-Checksum")
		w.Header().Set("X-Phase", "committed")
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, "payload"); err != nil {
			t.Error(err)
		}
		w.Header().Add("X-Checksum", "one")
		w.Header().Add("X-Checksum", "two")
		w.Header().Set(http.TrailerPrefix+"X-Dynamic", "done")
		w.Header().Set("X-Phase", "late")
	}))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/waiter" {
			r = r.WithContext(&waiterContext{Context: r.Context(), entered: waiterJoined})
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	defer releaseLeader()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type result struct {
		response *http.Response
		err      error
	}
	start := func(path string) <-chan result {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan result, 1)
		go func() {
			response, err := server.Client().Do(req)
			done <- result{response, err}
		}()
		return done
	}
	leader := start("/leader")
	select {
	case <-leaderEntered:
	case <-ctx.Done():
		t.Fatal("leader did not enter its handler")
	}
	waiter := start("/waiter")
	select {
	case <-waiterJoined:
	case <-ctx.Done():
		t.Fatal("waiter did not join the leader")
	}
	releaseLeader()

	for name, done := range map[string]<-chan result{"leader": leader, "waiter": waiter} {
		got := <-done
		if got.err != nil {
			t.Fatalf("%s: %v", name, got.err)
		}
		response := got.response
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || string(body) != "payload" {
			t.Fatalf("%s: status=%d body=%q err=%v", name, response.StatusCode, body, err)
		}
		if response.Header.Get("X-Phase") != "committed" ||
			strings.Join(response.Trailer.Values("X-Checksum"), ",") != "one,two" ||
			response.Trailer.Get("X-Dynamic") != "done" {
			t.Fatalf("%s: header=%v trailer=%v", name, response.Header, response.Trailer)
		}
	}
}

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

type responseWriteRecorder struct {
	*httptest.ResponseRecorder
	writeBytes int
	writeErr   error
	headers    int
}

func (w *responseWriteRecorder) WriteHeader(status int) {
	w.headers++
	w.ResponseRecorder.WriteHeader(status)
}

func (w *responseWriteRecorder) Write(p []byte) (int, error) {
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
				leader := &responseWriteRecorder{ResponseRecorder: httptest.NewRecorder(), writeBytes: tc.bytes, writeErr: tc.writerErr}
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

func TestWaiterWriteOutcomeControlsReplayHooks(t *testing.T) {
	const body = "sample"
	for _, tc := range []struct {
		name, method string
		bytes        int
		writeErr     error
		wantSuccess  bool
	}{
		{"complete", http.MethodGet, len(body), nil, true},
		{"HEAD", http.MethodHead, 0, errors.New("unexpected body write"), true},
		{"short", http.MethodGet, 3, nil, false},
		{"explicit error", http.MethodGet, 3, errors.New("writer diagnostic"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var successes int
				var failures []error
				c := New(Config{
					KeyFunc:     func(*http.Request) string { return "same" },
					OnCoalesced: func(_ string, count int) { successes += count },
					OnError:     func(_ string, err error) { failures = append(failures, err) },
				})
				release := make(chan struct{})
				handler := c.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					<-release
					_, _ = io.WriteString(w, body)
				}))
				go handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
				synctest.Wait()
				waiter := &responseWriteRecorder{
					ResponseRecorder: httptest.NewRecorder(),
					writeBytes:       tc.bytes,
					writeErr:         tc.writeErr,
				}
				go handler.ServeHTTP(waiter, httptest.NewRequest(tc.method, "/", nil))
				synctest.Wait()
				close(release)
				synctest.Wait()

				if waiter.headers != 1 || waiter.Code != http.StatusOK || waiter.Body.String() != body[:tc.bytes] {
					t.Fatalf("commits=%d status=%d body=%q", waiter.headers, waiter.Code, waiter.Body)
				}
				if tc.wantSuccess {
					if successes != 1 || len(failures) != 0 {
						t.Fatalf("successes=%d failures=%v", successes, failures)
					}
				} else if successes != 0 || len(failures) != 1 || failures[0] != errWaiterWrite {
					t.Fatalf("successes=%d failures=%v", successes, failures)
				}
			})
		})
	}
}
