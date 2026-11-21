package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
)

func TestRegisterHandlerReturnsOK(t *testing.T) {
	transcoder := New("rpc://backend")
	err := transcoder.Register(t.Context(), func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "hello"})
	}, "GET /v1/hello")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/hello", nil)
	transcoder.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["message"] != "hello" {
		t.Fatalf("message = %q, want hello", body["message"])
	}
}

func TestUnknownPathReturnsNotFound(t *testing.T) {
	transcoder := New("rpc://backend")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)

	transcoder.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestMethodMismatchReturnsMethodNotAllowed(t *testing.T) {
	transcoder := New("rpc://backend")
	if err := transcoder.Register(context.Background(), func(http.ResponseWriter, *http.Request, map[string]string) {}, "POST /v1/hello"); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/hello", nil)
	transcoder.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if rec.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("Allow = %q, want POST", rec.Header().Get("Allow"))
	}
}

func TestRegisterNilHandlerReturnsError(t *testing.T) {
	if err := New("").Register(t.Context(), nil, "GET /"); !errors.Is(err, contract.ErrHandlerNil) {
		t.Fatalf("Register() error = %v, want %v", err, contract.ErrHandlerNil)
	}
}

func TestRegisterMethodsOnSamePath(t *testing.T) {
	transcoder := New("")
	for _, registration := range []struct {
		pattern string
		status  int
	}{
		{"/demo", http.StatusOK},
		{"POST /demo", http.StatusCreated},
	} {
		err := transcoder.Register(t.Context(), func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
			w.WriteHeader(registration.status)
		}, registration.pattern)
		if err != nil {
			t.Fatal(err)
		}
	}
	handler := func(http.ResponseWriter, *http.Request, map[string]string) {}
	for _, pattern := range []string{"GET /demo", "GET /{", "G@T /demo"} {
		if err := transcoder.Register(t.Context(), handler, pattern); err == nil {
			t.Errorf("Register(%q) should fail", pattern)
		}
	}
	for _, tc := range []struct {
		method string
		status int
		allow  string
	}{
		{http.MethodGet, http.StatusOK, ""},
		{http.MethodPost, http.StatusCreated, ""},
		{http.MethodHead, http.StatusMethodNotAllowed, "GET, POST"},
	} {
		req := httptest.NewRequest(tc.method, "/demo", nil)
		rec := httptest.NewRecorder()
		transcoder.Handler().ServeHTTP(rec, req)
		if rec.Code != tc.status || rec.Header().Get("Allow") != tc.allow {
			t.Errorf("%s: status=%d Allow=%q", tc.method, rec.Code, rec.Header().Get("Allow"))
		}
	}
	err := transcoder.Register(t.Context(), func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
		w.WriteHeader(http.StatusNoContent)
	}, "HEAD /demo")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	transcoder.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/demo", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("registered HEAD: status=%d", rec.Code)
	}
}

func TestRegisterUsesRequestContextOnlyAtDispatch(t *testing.T) {
	transcoder := New("")
	registrationCtx, cancel := context.WithCancel(t.Context())
	cancel()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/context", nil)

	handler := func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		if r.Context() != request.Context() {
			t.Error("dispatch did not preserve the HTTP request context")
		}
		w.WriteHeader(http.StatusNoContent)
	}
	if err := transcoder.Register(registrationCtx, handler, "GET /context"); err != nil {
		t.Fatalf("Register with canceled context: %v", err)
	}
	for _, pattern := range []string{"GET /context", "GET /{"} {
		if err := transcoder.Register(t.Context(), handler, pattern); err == nil {
			t.Errorf("Register(%q) unexpectedly succeeded", pattern)
		}
	}

	rec := httptest.NewRecorder()
	transcoder.Handler().ServeHTTP(rec, request)
	if rec.Code != http.StatusNoContent {
		t.Errorf("dispatch returned %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestHandlerCanRegisterAnotherRPCPath(t *testing.T) {
	transcoder := New("")
	calls := 0
	first := func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		calls++
		err := transcoder.Register(r.Context(), func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
			w.WriteHeader(http.StatusCreated)
		}, "GET /second")
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
	if err := transcoder.Register(t.Context(), first, "GET /first"); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		transcoder.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/first", nil))
		done <- rec.Code
	}()
	select {
	case status := <-done:
		if status != http.StatusNoContent || calls != 1 {
			t.Fatalf("status=%d calls=%d", status, calls)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler could not reenter registration")
	}
	rec := httptest.NewRecorder()
	transcoder.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/second", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("new route status = %d", rec.Code)
	}
}

func TestTranscoderOptionsObserveInitializedTarget(t *testing.T) {
	var instances []*HTTPTranscoder
	var order []int
	option := func(i int) Option {
		return func(tcd *HTTPTranscoder) {
			if tcd.Target() != "service" || tcd.Handler() == nil {
				t.Error("option received an uninitialized transcoder")
			}
			instances = append(instances, tcd)
			order = append(order, i)
		}
	}
	tcd := New(" service ", option(1), nil, option(2))
	if len(order) != 2 || order[0] != 1 || order[1] != 2 || instances[0] != tcd || instances[1] != tcd || tcd.Target() != "service" {
		t.Errorf("constructed target = %q, option order = %v", tcd.Target(), order)
	}
}

func TestTranscoderBlankPatternUsesGETRoot(t *testing.T) {
	tc := New("service")
	calls := 0
	if err := tc.Register(t.Context(), func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		calls++
		if r.URL.Path != "/" {
			t.Errorf("path=%q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}, "  "); err != nil {
		t.Fatal(err)
	}
	get, post := httptest.NewRecorder(), httptest.NewRecorder()
	tc.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/", nil))
	tc.Handler().ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/", nil))
	if get.Code != http.StatusNoContent || post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET" || calls != 1 {
		t.Errorf("GET=%d POST=%d Allow=%q calls=%d", get.Code, post.Code, post.Header().Get("Allow"), calls)
	}
}
