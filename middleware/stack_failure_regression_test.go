package middleware_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/middleware/auth"
	"github.com/birdordev/plumego/middleware/bodylimit"
	"github.com/birdordev/plumego/middleware/cors"
	"github.com/birdordev/plumego/middleware/requestid"
	"github.com/birdordev/plumego/middleware/timeout"
	"github.com/birdordev/plumego/security/authn"
)

type blockedResponseWriter struct {
	*httptest.ResponseRecorder
	started chan struct{}
	release chan struct{}
	writes  int
}

func (w *blockedResponseWriter) Write(body []byte) (int, error) {
	w.writes++
	if w.writes == 1 {
		close(w.started)
		<-w.release
	}
	return w.ResponseRecorder.Write(body)
}

func TestBodyLimitTimeoutResponseOwnership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		writer := &blockedResponseWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{})}
		contexts := make(chan context.Context, 1)
		handlerRelease := make(chan struct{})
		exited := make(chan struct{})
		returned := make(chan struct{})
		handler := middleware.NewChain(
			bodylimit.Middleware(bodylimit.Config{MaxBytes: 2}),
			timeout.Middleware(timeout.Config{Timeout: time.Second}),
		).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(exited)
			contexts <- r.Context()
			_, _ = io.ReadAll(r.Body)
			<-handlerRelease
		}))
		go func() {
			handler.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("oversized")))
			close(returned)
		}()
		ctx := <-contexts
		<-ctx.Done()
		<-writer.started
		synctest.Wait()
		select {
		case <-returned:
			t.Error("request returned while its response write was still blocked")
		default:
		}
		close(writer.release)
		<-returned
		before := writer.Body.String()
		close(handlerRelease)
		<-exited
		synctest.Wait()
		if writer.writes != 1 || writer.Body.String() != before {
			t.Fatalf("response changed after return: writes=%d body=%q", writer.writes, writer.Body.String())
		}
		if writer.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d, want 413", writer.Code)
		}
		var payload struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(writer.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Error.Code != contract.CodeRequestBodyTooLarge {
			t.Fatalf("payload=%+v", payload)
		}
	})
}

func TestBodyLimitLateReadCannotWriteAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		exited := make(chan struct{})
		handler := middleware.NewChain(
			bodylimit.Middleware(bodylimit.Config{MaxBytes: 2}),
			timeout.Middleware(timeout.Config{Timeout: time.Second}),
		).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(exited)
			<-release
			_, _ = io.ReadAll(r.Body)
		}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("oversized")))
		if rec.Code != http.StatusGatewayTimeout {
			t.Fatalf("status=%d, want 504", rec.Code)
		}
		before := rec.Body.String()
		close(release)
		<-exited
		synctest.Wait()
		if rec.Body.String() != before {
			t.Fatal("late body read changed the response")
		}
	})
}

func TestRecommendedStackTerminalResponses(t *testing.T) {
	authMW, err := auth.Authenticate(authn.StaticToken("secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"success", "body_limit", "timeout", "panic", "auth", "preflight"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				finished := make(chan struct{})
				handler := middleware.NewChain(
					requestid.Middleware(requestid.WithGenerator(func() string { return "stack-id" })), mustRecoveryMw(t), mustSecurityHeadersMw(t),
					timeout.Middleware(timeout.Config{Timeout: time.Second}),
					bodylimit.Middleware(bodylimit.Config{MaxBytes: 2}),
					cors.Middleware(cors.CORSOptions{}), authMW,
				).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(finished)
					calls++
					switch name {
					case "body_limit":
						_, _ = io.ReadAll(r.Body)
					case "timeout":
						<-r.Context().Done()
					case "panic":
						panic("stack panic")
					default:
						w.WriteHeader(http.StatusAccepted)
					}
				}))
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("large"))
				req.Header.Set("Authorization", "Bearer secret")
				want := http.StatusAccepted
				switch name {
				case "body_limit":
					want = http.StatusRequestEntityTooLarge
				case "timeout":
					want = http.StatusGatewayTimeout
				case "panic":
					want = http.StatusInternalServerError
				case "auth":
					req.Header.Del("Authorization")
					want = http.StatusUnauthorized
				case "preflight":
					req.Method = http.MethodOptions
					req.Header.Set("Origin", "https://app.example")
					req.Header.Set("Access-Control-Request-Method", "POST")
					want = http.StatusNoContent
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				synctest.Wait()
				if rec.Code != want {
					t.Fatalf("status=%d, want %d", rec.Code, want)
				}
				expectedCalls := 1
				if name == "auth" || name == "preflight" {
					expectedCalls = 0
				}
				if calls != expectedCalls {
					t.Fatalf("calls=%d, want %d", calls, expectedCalls)
				}
				if calls == 1 {
					<-finished
				}
				if rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" || rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get(contract.RequestIDHeader) != "stack-id" {
					t.Fatalf("terminal response lost outer headers: %v", rec.Header())
				}
				if want >= 400 {
					var envelope map[string]any
					if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}
					if envelope["error"] == nil || envelope["request_id"] != "stack-id" {
						t.Fatalf("error envelope=%v", envelope)
					}
				}
			})
		})
	}
}

func TestCanceledBodyOverrunDoesNotEmitResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := bodylimit.Middleware(bodylimit.Config{MaxBytes: 2})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		cancel()
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("oversized")).WithContext(ctx))
	if len(rec.Header()) != 0 || rec.Body.Len() != 0 {
		t.Fatalf("canceled overrun wrote a response: %v %q", rec.Header(), rec.Body.String())
	}
}

type readOnErrorHeaderWriter struct {
	*httptest.ResponseRecorder
	release  chan struct{}
	readDone chan struct{}
}

func (w *readOnErrorHeaderWriter) WriteHeader(status int) {
	w.ResponseRecorder.WriteHeader(status)
	close(w.release)
	<-w.readDone
}

func TestBodyLimitPreservesCommittedTimeoutError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		readDone := make(chan struct{})
		writer := &readOnErrorHeaderWriter{ResponseRecorder: httptest.NewRecorder(), release: release, readDone: readDone}
		handler := middleware.NewChain(
			bodylimit.Middleware(bodylimit.Config{MaxBytes: 2}),
			timeout.Middleware(timeout.Config{Timeout: time.Second}),
		).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
			_, _ = io.ReadAll(r.Body)
			close(readDone)
		}))
		handler.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("oversized")))
		synctest.Wait()
		var payload struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(writer.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if writer.Code != http.StatusGatewayTimeout || payload.Error.Code != contract.CodeTimeout {
			t.Fatalf("committed timeout changed: status=%d body=%q", writer.Code, writer.Body.String())
		}
	})
}
