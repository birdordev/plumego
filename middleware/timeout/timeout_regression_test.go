package timeout

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spcent/plumego/contract"
)

type responseWriteCounter struct {
	http.ResponseWriter
	writes int
}

func (w *responseWriteCounter) WriteHeader(status int) {
	w.writes++
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriteCounter) Write(body []byte) (int, error) {
	w.writes++
	return w.ResponseWriter.Write(body)
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for signal")
	}
}

func TestTimeoutDefaultConfigDisablesDeadline(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Timeout != 0 || cfg.MaxBufferBytes != 10<<20 || cfg.MaxReplayBytes != 512<<10 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	called := false
	handler := Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if _, ok := r.Context().Deadline(); ok {
			t.Error("default configuration added a deadline")
		}
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Fatal("downstream was not called")
	}
}

func TestTimeoutSkipsAlreadyCanceledRequests(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "canceled"
		if deadline {
			name = "expired"
		}
		t.Run(name, func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			if deadline {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			} else {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			}
			defer cancel()
			var calls atomic.Int32
			handler := Middleware(Config{Timeout: time.Second})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
			}))
			rr := httptest.NewRecorder()
			writer := &responseWriteCounter{ResponseWriter: rr}
			handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
			if calls.Load() != 0 {
				t.Fatal("canceled request invoked downstream")
			}
			if deadline {
				if rr.Code != http.StatusGatewayTimeout {
					t.Fatalf("status = %d, want 504", rr.Code)
				}
			} else if writer.writes != 0 || rr.Body.Len() != 0 || len(rr.Header()) != 0 {
				t.Fatalf("explicit cancellation wrote a response: %+v", rr)
			}
		})
	}
}

func TestTimeoutCancellationDiscardsCompletedResult(t *testing.T) {
	for _, panics := range []bool{false, true} {
		name := "return"
		if panics {
			name = "panic"
		}
		t.Run(name, func(t *testing.T) {
			for i := 0; i < 100; i++ {
				ctx, cancel := context.WithCancel(context.Background())
				finished := make(chan struct{})
				reports := make(chan any, 2)
				var reportCount atomic.Int32
				handler := Middleware(Config{
					Timeout: time.Second,
					OnPanic: func(r *http.Request, value any) {
						reportCount.Add(1)
						reports <- value
					},
				})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(finished)
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte("partial"))
					cancel()
					if panics {
						panic("canceled panic")
					}
				}))
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
				awaitSignal(t, finished)
				if rr.Body.Len() != 0 || len(rr.Header()) != 0 || rr.Code != http.StatusOK {
					t.Fatalf("canceled result was committed: status=%d body=%q headers=%v", rr.Code, rr.Body.String(), rr.Header())
				}
				if panics {
					select {
					case value := <-reports:
						if value != "canceled panic" {
							t.Fatalf("panic = %v", value)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("canceled panic was not reported")
					}
					if reportCount.Load() != 1 {
						t.Fatal("panic was reported more than once")
					}
				}
				cancel()
			}
		})
	}
}

func TestTimeoutCancellationPropagatesWithoutWaitingForHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	exited := make(chan struct{})
	returned := make(chan struct{})
	writeErrors := make(chan error, 1)
	handler := Middleware(Config{Timeout: time.Second})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(exited)
		close(started)
		<-r.Context().Done()
		<-release
		_, err := w.Write([]byte("late"))
		writeErrors <- err
	}))
	rr := httptest.NewRecorder()
	go func() {
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
		close(returned)
	}()
	defer func() {
		cancel()
		close(release)
		awaitSignal(t, exited)
	}()
	awaitSignal(t, started)
	cancel()
	awaitSignal(t, returned)
	if rr.Body.Len() != 0 || len(rr.Header()) != 0 {
		t.Fatal("cancellation wrote a response")
	}
	select {
	case <-exited:
		t.Fatal("handler exited before release")
	default:
	}
	// The deferred release lets the handler verify writes fail with cancellation.
	t.Cleanup(func() {
		select {
		case err := <-writeErrors:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("late write error = %v, want context canceled", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("handler did not attempt a late write")
		}
	})
}

func TestTimeoutPreservesOuterHeadersAndIsolatesDownstreamEdits(t *testing.T) {
	cases := []struct {
		name   string
		cfg    Config
		status int
	}{
		{"success", Config{Timeout: time.Second}, http.StatusCreated},
		{"timeout", Config{Timeout: 100 * time.Millisecond}, http.StatusGatewayTimeout},
		{"buffer_limit", Config{Timeout: time.Second, MaxBufferBytes: 2, MaxReplayBytes: 10}, http.StatusInternalServerError},
		{"replay_limit", Config{Timeout: time.Second, MaxBufferBytes: 10, MaxReplayBytes: 2}, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			finished := make(chan struct{})
			handler := Middleware(tc.cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(finished)
				w.Header().Set("X-Override", "inner")
				w.Header().Del("X-Delete")
				w.Header().Add("X-Multi", "inner")
				w.Header().Set("X-Partial", "discard")
				w.WriteHeader(http.StatusCreated)
				if tc.name == "timeout" {
					<-r.Context().Done()
					return
				}
				_, _ = w.Write([]byte("body"))
			}))
			rr := httptest.NewRecorder()
			rr.Header().Set("Strict-Transport-Security", "max-age=31536000")
			rr.Header().Set(contract.RequestIDHeader, "outer-id")
			rr.Header().Set("X-Override", "outer")
			rr.Header().Set("X-Delete", "outer")
			rr.Header().Add("X-Multi", "outer")
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = req.WithContext(contract.WithRequestID(req.Context(), "outer-id"))
			handler.ServeHTTP(rr, req)
			awaitSignal(t, finished)
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d", rr.Code, tc.status)
			}
			if rr.Header().Get("Strict-Transport-Security") != "max-age=31536000" || rr.Header().Get(contract.RequestIDHeader) != "outer-id" {
				t.Fatalf("outer headers lost: %v", rr.Header())
			}
			if tc.name == "success" {
				if rr.Header().Get("X-Override") != "inner" || rr.Header().Get("X-Delete") != "" || len(rr.Header().Values("X-Multi")) != 2 {
					t.Fatalf("downstream edits were not replayed: %v", rr.Header())
				}
			} else {
				if rr.Header().Get("X-Override") != "outer" || rr.Header().Get("X-Delete") != "outer" || len(rr.Header().Values("X-Multi")) != 1 || rr.Header().Get("X-Partial") != "" {
					t.Fatalf("discarded header edits leaked: %v", rr.Header())
				}
				var body struct {
					RequestID string `json:"request_id"`
					Error     struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				wantCode := contract.CodeInternalError
				if tc.name == "timeout" {
					wantCode = contract.CodeTimeout
				}
				if body.RequestID != "outer-id" || body.Error.Code != wantCode {
					t.Fatalf("error payload = %+v", body)
				}
				if rr.Body.String() == "body" {
					t.Fatal("partial body leaked")
				}
			}
		})
	}
}

func TestTimeoutParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	finished := make(chan struct{})
	handler := Middleware(Config{Timeout: time.Hour})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		<-r.Context().Done()
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rr.Code)
	}
	awaitSignal(t, finished)
}

func TestTimeoutLatePanicRunsAfterResponse(t *testing.T) {
	release := make(chan struct{})
	finished := make(chan struct{})
	reported := make(chan int, 1)
	rr := httptest.NewRecorder()
	handler := Middleware(Config{
		Timeout: 100 * time.Millisecond,
		OnPanic: func(r *http.Request, value any) { reported <- rr.Code },
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		<-release
		panic("late panic")
	}))
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	close(release)
	awaitSignal(t, finished)
	select {
	case status := <-reported:
		if status != http.StatusGatewayTimeout {
			t.Fatalf("hook saw status = %d, want 504", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late panic was not reported")
	}
}

func TestTimeoutInformationalResponsesPreserveFinalStatus(t *testing.T) {
	server := httptest.NewServer(Middleware(Config{Timeout: time.Second})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusContinue)
		w.WriteHeader(http.StatusProcessing)
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusCreated)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("created"))
	})))
	defer server.Close()
	resp, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated || string(body) != "created" {
		t.Fatalf("status=%d body=%q, want 201 created", resp.StatusCode, body)
	}
}
