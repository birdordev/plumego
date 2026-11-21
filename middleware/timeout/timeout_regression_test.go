package timeout

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/birdordev/plumego/contract"
)

func TestTimeoutDiscardsResponseWhenHandlerExits(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for _, tc := range []struct {
		name   string
		cancel bool
		status int
	}{
		{"active", false, http.StatusInternalServerError},
		{"canceled", true, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := Middleware(Config{Timeout: time.Second})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Partial", "discard")
				_, _ = io.WriteString(w, "partial body")
				if tc.cancel {
					cancel()
				}
				runtime.Goexit()
			}))
			dst := &responseWriteCounter{ResponseWriter: httptest.NewRecorder()}
			handler.ServeHTTP(dst, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil))
			rec := dst.ResponseWriter.(*httptest.ResponseRecorder)
			if rec.Code != tc.status || rec.Header().Get("X-Partial") != "" {
				t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
			}
			if tc.cancel {
				if dst.writes != 0 || rec.Body.Len() != 0 {
					t.Fatalf("canceled request wrote %d headers and %q", dst.writes, rec.Body.String())
				}
			} else {
				var response struct {
					Error struct{ Code string }
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.Error.Code != contract.CodeInternalError || dst.writes != 2 {
					t.Fatalf("writes=%d body=%q parse error=%v", dst.writes, rec.Body.String(), err)
				}
			}
		})
	}
}

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
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		finished := make(chan struct{})
		var reported []int
		rr := httptest.NewRecorder()
		dst := &responseWriteCounter{ResponseWriter: rr}
		handler := Middleware(Config{
			Timeout: time.Millisecond,
			OnPanic: func(_ *http.Request, _ any) {
				reported = append(reported, rr.Code)
			},
		})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			defer close(finished)
			<-release
			panic("late panic")
		}))
		handler.ServeHTTP(dst, httptest.NewRequest(http.MethodGet, "/", nil))
		if rr.Code != http.StatusGatewayTimeout || dst.writes != 2 {
			t.Errorf("status=%d writes=%d", rr.Code, dst.writes)
		}
		close(release)
		<-finished
		synctest.Wait()
		if len(reported) != 1 || reported[0] != http.StatusGatewayTimeout || dst.writes != 2 {
			t.Fatalf("hook statuses=%v response writes=%d", reported, dst.writes)
		}
	})
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

type failedReplayWriter struct {
	responseWriteCounter
}

func (w *failedReplayWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, io.ErrClosedPipe
}

func TestTimeoutReplayWriteFailureDoesNotCommitAgain(t *testing.T) {
	rec := httptest.NewRecorder()
	dst := &failedReplayWriter{responseWriteCounter{ResponseWriter: rec}}
	handler := Middleware(Config{Timeout: time.Second})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "body")
	}))
	handler.ServeHTTP(dst, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusCreated || dst.writes != 2 || rec.Body.Len() != 0 {
		t.Fatalf("status=%d writes=%d body=%q", rec.Code, dst.writes, rec.Body.String())
	}
}

func TestTimeoutErrorPreservesMetadataAndDropsStaleRepresentation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Length", "1")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("ETag", `"original"`)
		w.Header().Set("X-Trace", "request-fixture")
		w.Header().Set("Cache-Control", "no-store")
		done := make(chan struct{})
		h := Middleware(Config{Timeout: time.Millisecond})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			defer close(done)
			<-r.Context().Done()
		}))
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		<-done
		hdr := w.Result().Header
		if w.Code != http.StatusGatewayTimeout || hdr.Get("Content-Type") != contract.ContentTypeJSON ||
			hdr.Get("Content-Length") != "" || hdr.Get("Content-Encoding") != "" || hdr.Get("ETag") != "" ||
			hdr.Get("X-Trace") != "request-fixture" || hdr.Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d header=%v", w.Code, hdr)
		}
	})
}

func TestAcceptanceTimeoutReplayReleasesCapturedBuffer(t *testing.T) {
	const maxReplay = 512 << 10
	var writer *timeoutResponseWriter
	dst := &responseWriteCounter{ResponseWriter: httptest.NewRecorder()}
	handler := Middleware(Config{Timeout: time.Second, MaxBufferBytes: 2 * maxReplay, MaxReplayBytes: maxReplay})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writer = w.(*timeoutResponseWriter)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(make([]byte, maxReplay))
		_, _ = w.Write([]byte("over"))
		_, _ = w.Write([]byte("discarded"))
	}))
	handler.ServeHTTP(dst, httptest.NewRequest(http.MethodGet, "/", nil))
	rec := dst.ResponseWriter.(*httptest.ResponseRecorder)
	if writer == nil || cap(writer.buffer.Body()) != 0 || writer.buffer.StatusCode() != http.StatusCreated {
		t.Fatalf("unexpected recorder state after abandoning replay: %+v", writer)
	}
	if dst.writes != 2 || rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "discarded") {
		t.Fatalf("status=%d count=%d body=%q", rec.Code, dst.writes, rec.Body)
	}
}

func TestTimeoutCloseParentCancellationAndLocalDeadline(t *testing.T) {
	for _, cancelFirst := range []bool{true, false} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			const limit = time.Second
			entered, finished := make(chan struct{}), make(chan struct{})
			w := &responseWriteCounter{ResponseWriter: httptest.NewRecorder()}
			h := Middleware(Config{Timeout: limit})(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				defer close(finished)
				_, _ = w.Write([]byte("discarded"))
				close(entered)
				<-req.Context().Done()
			}))
			served := make(chan struct{})
			go func() {
				defer close(served)
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
			}()
			<-entered
			if cancelFirst {
				time.Sleep(limit - time.Nanosecond)
				cancel()
			}
			<-served
			<-finished
			rec := w.ResponseWriter.(*httptest.ResponseRecorder)
			if cancelFirst {
				if w.writes != 0 || rec.Body.Len() != 0 {
					t.Fatalf("parent canceled: commits=%d body=%q", w.writes, rec.Body)
				}
			} else if rec.Code != http.StatusGatewayTimeout || w.writes != 2 || strings.Contains(rec.Body.String(), "discarded") {
				t.Fatalf("deadline: status=%d commits=%d body=%q", rec.Code, w.writes, rec.Body)
			}
		})
	}
}

func TestCanceledParentOwnsResponseBeforeLatePanicHook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		h := Middleware(Config{Timeout: time.Hour, OnPanic: func(r *http.Request, recovered any) {
			if r.Context().Err() != context.Canceled || recovered != "fixture" {
				t.Errorf("context=%v panic=%v", r.Context().Err(), recovered)
			}
			calls.Add(1)
		}})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "private to buffer")
			close(entered)
			<-release
			panic("fixture")
		}))
		dst := &responseWriteCounter{ResponseWriter: httptest.NewRecorder()}
		go func() { defer close(returned); h.ServeHTTP(dst, httptest.NewRequestWithContext(ctx, "GET", "/", nil)) }()
		<-entered
		cancel()
		<-returned
		close(release)
		synctest.Wait()
		if calls.Load() != 1 || dst.writes != 0 {
			t.Fatalf("hooks=%d writes=%d", calls.Load(), dst.writes)
		}
	})
}

func TestReplayLimitDiscardsBufferedTrailerAndBody(t *testing.T) {
	h := Middleware(Config{Timeout: time.Second, MaxBufferBytes: 64, MaxReplayBytes: 4})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Old-Trailer")
		w.WriteHeader(202)
		_, _ = io.WriteString(w, "fixture payload")
		w.Header().Set("X-Old-Trailer", "fixture trailer")
	}))
	rec := httptest.NewRecorder()
	rec.Header().Set("X-Existing", "outer")
	dst := &responseWriteCounter{ResponseWriter: rec}
	h.ServeHTTP(dst, httptest.NewRequest("GET", "/", nil))
	result := rec.Result()
	defer result.Body.Close()
	if result.StatusCode != 500 || dst.writes != 2 || result.Header.Get("Trailer") != "" || result.Trailer.Get("X-Old-Trailer") != "" ||
		rec.Header().Get("X-Old-Trailer") != "" || result.Header.Get("X-Existing") != "outer" || strings.Contains(rec.Body.String(), "fixture payload") {
		t.Fatalf("status=%d headers=%v trailer=%v writes=%d body=%q", result.StatusCode, result.Header, result.Trailer, dst.writes, rec.Body.String())
	}
}

func TestTimeoutBufferAndReplayLimitsAtExactBoundary(t *testing.T) {
	for _, tc := range []struct {
		name           string
		buffer, replay int
		final, message string
	}{
		{"both exact", 4, 4, "cd", ""},
		{"buffer first", 4, 8, "cde", "response exceeded buffer limit"},
		{"replay first", 8, 4, "cde", "response too large for timeout buffering"},
		{"both crossed", 4, 4, "cde", "response too large for timeout buffering"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := Middleware(Config{Timeout: time.Second, MaxBufferBytes: tc.buffer, MaxReplayBytes: tc.replay})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Etag", "original")
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, "ab")
				_, _ = io.WriteString(w, tc.final)
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if tc.message == "" {
				if rec.Code != http.StatusAccepted || rec.Header().Get("Etag") != "original" || rec.Body.String() != "abcd" {
					t.Fatalf("success=%d headers=%v body=%q", rec.Code, rec.Header(), rec.Body)
				}
				return
			}
			if rec.Code != http.StatusInternalServerError || rec.Header().Get("Etag") != "" ||
				!strings.Contains(rec.Body.String(), tc.message) || strings.Contains(rec.Body.String(), "abc") {
				t.Fatalf("failure=%d headers=%v body=%q", rec.Code, rec.Header(), rec.Body)
			}
		})
	}
}

func TestTimedOutHandlerCannotFlushOrWriteToOuterResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		type outcome struct {
			flush, write error
			n            int
		}
		done := make(chan outcome, 1)
		h := Middleware(Config{Timeout: time.Millisecond})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			<-release
			flush := http.NewResponseController(w).Flush()
			n, err := io.WriteString(w, "late fixture")
			done <- outcome{flush, err, n}
		}))
		rec := httptest.NewRecorder()
		dst := &responseWriteCounter{ResponseWriter: rec}
		h.ServeHTTP(dst, httptest.NewRequest(http.MethodGet, "/", nil))
		close(release)
		late := <-done
		if rec.Code != http.StatusGatewayTimeout || dst.writes != 2 || rec.Flushed || strings.Contains(rec.Body.String(), "late fixture") ||
			!errors.Is(late.flush, http.ErrNotSupported) || !errors.Is(late.write, context.DeadlineExceeded) || late.n != 0 {
			t.Fatalf("status=%d writes=%d flushed=%t body=%q late=%+v", rec.Code, dst.writes, rec.Flushed, rec.Body, late)
		}
	})
}
