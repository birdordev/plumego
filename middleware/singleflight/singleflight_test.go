package singleflight

import (
	"bufio"
	"bytes"
	"context"
	"hash/fnv"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testMiddleware(config Config) func(http.Handler) http.Handler {
	return New(config).Middleware()
}

func TestCoalesce_SingleRequest(t *testing.T) {
	callCount := int32(0)

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response"))
	})

	middleware := testMiddleware(Config{})
	handler := middleware(backend)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("Expected backend to be called once, got %d", callCount)
	}

	if w.Body.String() != "response" {
		t.Errorf("Expected response 'response', got '%s'", w.Body.String())
	}
}

func TestCoalesce_ConcurrentIdenticalRequests(t *testing.T) {
	callCount := int32(0)

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		time.Sleep(100 * time.Millisecond) // Simulate slow backend
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response"))
	})

	middleware := testMiddleware(Config{})
	handler := middleware(backend)

	// Launch 10 concurrent identical requests
	concurrency := 10
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()

			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			// All should receive the same response
			if w.Body.String() != "response" {
				t.Errorf("Expected response 'response', got '%s'", w.Body.String())
			}
		}()
	}

	wg.Wait()

	// Backend should be called only once
	count := atomic.LoadInt32(&callCount)
	if count != 1 {
		t.Errorf("Expected backend to be called once, got %d", count)
	}
}

func TestCoalesce_DifferentRequests(t *testing.T) {
	callCount := int32(0)

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(r.URL.Path))
	})

	middleware := testMiddleware(Config{})
	handler := middleware(backend)

	var wg sync.WaitGroup
	wg.Add(2)

	// Request 1
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("GET", "/test1", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}()

	// Request 2 (different URL)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("GET", "/test2", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}()

	wg.Wait()

	// Backend should be called twice (different URLs)
	count := atomic.LoadInt32(&callCount)
	if count != 2 {
		t.Errorf("Expected backend to be called twice, got %d", count)
	}
}

func TestCoalesce_DifferentHosts(t *testing.T) {
	callCount := int32(0)

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(r.Host))
	})

	middleware := testMiddleware(Config{})
	handler := middleware(backend)

	var wg sync.WaitGroup
	wg.Add(2)

	for _, host := range []string{"a.example.test", "b.example.test"} {
		go func(host string) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "http://"+host+"/same", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Body.String() != host {
				t.Errorf("expected host-specific response %q, got %q", host, w.Body.String())
			}
		}(host)
	}

	wg.Wait()

	if count := atomic.LoadInt32(&callCount); count != 2 {
		t.Errorf("expected backend to be called twice for different hosts, got %d", count)
	}
}

func TestCoalesce_NonCacheableMethods(t *testing.T) {
	callCount := int32(0)

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	middleware := testMiddleware(Config{})
	handler := middleware(backend)

	var wg sync.WaitGroup
	wg.Add(2)

	// POST requests should not be coalesced
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/test", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
		}()
	}

	wg.Wait()

	// Backend should be called twice (POST not coalesced)
	count := atomic.LoadInt32(&callCount)
	if count != 2 {
		t.Errorf("Expected backend to be called twice for POST, got %d", count)
	}
}

func TestCoalesce_CoalescedHeader(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response"))
	})

	middleware := testMiddleware(Config{AddCoalescedHeader: true})
	handler := middleware(backend)

	var wg sync.WaitGroup
	var firstResponse *httptest.ResponseRecorder
	var secondResponse *httptest.ResponseRecorder

	wg.Add(2)

	// First request
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("GET", "/test", nil)
		firstResponse = httptest.NewRecorder()
		handler.ServeHTTP(firstResponse, req)
	}()

	// Wait a bit to ensure first request starts
	time.Sleep(10 * time.Millisecond)

	// Second request (should be coalesced)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("GET", "/test", nil)
		secondResponse = httptest.NewRecorder()
		handler.ServeHTTP(secondResponse, req)
	}()

	wg.Wait()

	// Second response should have X-Coalesced header
	if secondResponse.Header().Get("X-Coalesced") != "true" {
		t.Error("Expected X-Coalesced header on second response")
	}
}

func TestCoalesce_CoalescedHEADDoesNotReplayBody(t *testing.T) {
	coalescer := New(Config{Timeout: time.Second, AddCoalescedHeader: true})
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		w.Header().Set("X-Head-Test", "present")
		w.WriteHeader(http.StatusNoContent)
		_, _ = w.Write([]byte("should-not-replay"))
	})

	handler := coalescer.Middleware()(backend)
	leaderDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodHead, "/head", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		leaderDone <- rec
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader request did not start")
	}

	waiterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodHead, "/head", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		waiterDone <- rec
	}()

	waitForCoalesceWaiterForMethod(t, coalescer, http.MethodHead, "/head")
	close(release)

	select {
	case rec := <-leaderDone:
		if rec.Code != http.StatusNoContent {
			t.Fatalf("leader status = %d, want %d", rec.Code, http.StatusNoContent)
		}
	case <-time.After(time.Second):
		t.Fatal("leader request did not finish")
	}

	select {
	case rec := <-waiterDone:
		if rec.Code != http.StatusNoContent {
			t.Fatalf("waiter status = %d, want %d", rec.Code, http.StatusNoContent)
		}
		if got := rec.Header().Get("X-Head-Test"); got != "present" {
			t.Fatalf("waiter X-Head-Test = %q, want present", got)
		}
		if got := rec.Header().Get("X-Coalesced"); got != "true" {
			t.Fatalf("waiter X-Coalesced = %q, want true", got)
		}
		if got := rec.Body.String(); got != "" {
			t.Fatalf("HEAD waiter body = %q, want empty", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not released")
	}
}

func TestCoalesce_HeaderAwareKeyFunc(t *testing.T) {
	callCount := int32(0)

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	// Use header-aware key function
	middleware := testMiddleware(Config{
		KeyFunc: HeaderAwareKeyFunc([]string{"Accept"}),
	})
	handler := middleware(backend)

	var wg sync.WaitGroup
	wg.Add(2)

	// Request 1 with Accept: application/json
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}()

	// Request 2 with Accept: application/xml (different header)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept", "application/xml")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}()

	wg.Wait()

	// Backend should be called twice (different Accept headers)
	count := atomic.LoadInt32(&callCount)
	if count != 2 {
		t.Errorf("Expected backend to be called twice with different headers, got %d", count)
	}
}

func TestCoalesce_DefaultKeyPartitionsCredentialedRequests(t *testing.T) {
	callCount := int32(0)

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(r.Header.Get("Authorization")))
	})

	handler := testMiddleware(Config{})(backend)

	var wg sync.WaitGroup
	wg.Add(2)
	for _, token := range []string{"Bearer one", "Bearer two"} {
		go func(token string) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/private", nil)
			req.Header.Set("Authorization", token)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if got := w.Body.String(); got != token {
				t.Errorf("expected credential-specific response %q, got %q", token, got)
			}
		}(token)
	}
	wg.Wait()

	if count := atomic.LoadInt32(&callCount); count != 2 {
		t.Fatalf("expected backend to be called twice for different credentials, got %d", count)
	}
}

func TestCoalesce_BlankKeyFuncFailsOpen(t *testing.T) {
	callCount := int32(0)
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(r.URL.Query().Get("id")))
	})
	handler := testMiddleware(Config{
		KeyFunc: func(r *http.Request) string { return " \t " },
	})(backend)

	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 2)
	wg.Add(2)
	for _, id := range []string{"one", "two"} {
		go func(id string) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/blank?id="+id, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			responses <- rec
		}(id)
	}
	wg.Wait()
	close(responses)

	if count := atomic.LoadInt32(&callCount); count != 2 {
		t.Fatalf("backend calls = %d, want 2", count)
	}
	for rec := range responses {
		if got := rec.Header().Get("X-Coalesced"); got != "" {
			t.Fatalf("blank-key pass-through response had X-Coalesced = %q", got)
		}
	}
}

func TestCoalesce_Stats(t *testing.T) {
	coalescer := New(Config{})

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	handler := coalescer.Middleware()(backend)

	// Start a request in background
	go func() {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}()

	// Wait a bit for request to be in-flight
	time.Sleep(20 * time.Millisecond)

	// Check stats
	stats := coalescer.Stats()
	if stats.InFlight != 1 {
		t.Errorf("Expected 1 in-flight request, got %d", stats.InFlight)
	}

	// Wait for completion
	time.Sleep(100 * time.Millisecond)

	// Check stats again
	stats = coalescer.Stats()
	if stats.InFlight != 0 {
		t.Errorf("Expected 0 in-flight requests after completion, got %d", stats.InFlight)
	}
}

func TestCoalesce_CleansUpAndReleasesWaitersAfterPanic(t *testing.T) {
	coalescer := New(Config{Timeout: time.Second})
	started := make(chan struct{})
	release := make(chan struct{})
	panicSeen := make(chan any, 1)
	var startedOnce sync.Once

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		panic("boom")
	})

	handler := coalescer.Middleware()(backend)
	primaryDone := make(chan struct{})
	go func() {
		defer close(primaryDone)
		defer func() {
			panicSeen <- recover()
		}()
		req := httptest.NewRequest(http.MethodGet, "/panic", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("primary request did not start")
	}

	waiterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/panic", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		waiterDone <- rec
	}()

	waitForCoalesceWaiter(t, coalescer, "/panic")
	close(release)

	select {
	case rec := <-waiterDone:
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("waiter status = %d, want %d", rec.Code, http.StatusBadGateway)
		}
		if !strings.Contains(rec.Body.String(), "upstream_failed") {
			t.Fatalf("expected upstream failure error, got %q", rec.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not released after primary panic")
	}

	select {
	case rec := <-panicSeen:
		if rec == nil {
			t.Fatal("expected primary panic to propagate")
		}
	case <-time.After(time.Second):
		t.Fatal("primary request did not finish")
	}
	<-primaryDone

	if stats := coalescer.Stats(); stats.InFlight != 0 {
		t.Fatalf("in-flight entries = %d, want 0", stats.InFlight)
	}
}

func TestCoalesce_OversizedResponseDoesNotReplayToWaiters(t *testing.T) {
	coalescer := New(Config{
		MaxResponseBytes: 4,
		Timeout:          time.Second,
	})
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		w.Header().Set("X-Leader", "true")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("toolarge"))
	})

	handler := coalescer.Middleware()(backend)
	leaderDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/large", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		leaderDone <- rec
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader request did not start")
	}

	waiterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/large", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		waiterDone <- rec
	}()

	waitForCoalesceWaiter(t, coalescer, "/large")
	close(release)

	select {
	case rec := <-leaderDone:
		if rec.Code != http.StatusAccepted {
			t.Fatalf("leader status = %d, want %d", rec.Code, http.StatusAccepted)
		}
		if got := rec.Body.String(); got != "toolarge" {
			t.Fatalf("leader body = %q, want toolarge", got)
		}
		if got := rec.Header().Get("X-Leader"); got != "true" {
			t.Fatalf("leader header X-Leader = %q, want true", got)
		}
	case <-time.After(time.Second):
		t.Fatal("leader request did not finish")
	}

	select {
	case rec := <-waiterDone:
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("waiter status = %d, want %d", rec.Code, http.StatusBadGateway)
		}
		if !strings.Contains(rec.Body.String(), "upstream_failed") {
			t.Fatalf("expected upstream failure for oversized replay, got %q", rec.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not released")
	}
}

func TestCoalesce_FlushedLeaderDoesNotReplayToWaiters(t *testing.T) {
	coalescer := New(Config{Timeout: time.Second})
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("partial"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("done"))
	})

	handler := coalescer.Middleware()(backend)
	leaderDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/flush", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		leaderDone <- rec
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader request did not start")
	}

	waiterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/flush", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		waiterDone <- rec
	}()

	waitForCoalesceWaiter(t, coalescer, "/flush")
	close(release)

	select {
	case rec := <-leaderDone:
		if rec.Code != http.StatusOK {
			t.Fatalf("leader status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Body.String(); got != "partialdone" {
			t.Fatalf("leader body = %q, want partialdone", got)
		}
	case <-time.After(time.Second):
		t.Fatal("leader request did not finish")
	}

	select {
	case rec := <-waiterDone:
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("waiter status = %d, want %d", rec.Code, http.StatusBadGateway)
		}
		if !strings.Contains(rec.Body.String(), "upstream_failed") {
			t.Fatalf("expected upstream failure for flushed replay, got %q", rec.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not released")
	}
}

func TestCoalesce_HijackedLeaderDoesNotReplayToWaiters(t *testing.T) {
	coalescer := New(Config{Timeout: time.Second})
	started := make(chan struct{})
	release := make(chan struct{})
	hijackErr := make(chan string, 1)
	var startedOnce sync.Once

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			hijackErr <- "coalesce writer did not expose hijacker"
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			hijackErr <- err.Error()
			return
		}
		if conn != nil {
			_ = conn.Close()
		}
		hijackErr <- ""
	})

	handler := coalescer.Middleware()(backend)
	leaderDone := make(chan *coalesceHijackWriter, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/hijack", nil)
		writer := &coalesceHijackWriter{}
		handler.ServeHTTP(writer, req)
		leaderDone <- writer
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader request did not start")
	}

	waiterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/hijack", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		waiterDone <- rec
	}()

	waitForCoalesceWaiter(t, coalescer, "/hijack")
	close(release)

	select {
	case writer := <-leaderDone:
		if !writer.hijacked {
			t.Fatal("leader writer was not hijacked")
		}
		if errText := <-hijackErr; errText != "" {
			t.Fatalf("leader hijack error: %s", errText)
		}
	case <-time.After(time.Second):
		t.Fatal("leader request did not finish")
	}

	select {
	case rec := <-waiterDone:
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("waiter status = %d, want %d", rec.Code, http.StatusBadGateway)
		}
		if !strings.Contains(rec.Body.String(), "upstream_failed") {
			t.Fatalf("expected upstream failure for hijacked replay, got %q", rec.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not released")
	}
}

func TestCoalesce_OnErrorPanicDoesNotBlockErrorResponse(t *testing.T) {
	coalescer := New(Config{
		MaxResponseBytes: 4,
		Timeout:          time.Second,
		OnError: func(key string, err error) {
			panic("hook panic")
		},
	})
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("toolarge"))
	})

	handler := coalescer.Middleware()(backend)
	leaderDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/hook-error", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		leaderDone <- rec
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader request did not start")
	}

	waiterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/hook-error", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		waiterDone <- rec
	}()

	waitForCoalesceWaiter(t, coalescer, "/hook-error")
	close(release)

	select {
	case rec := <-waiterDone:
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("waiter status = %d, want %d", rec.Code, http.StatusBadGateway)
		}
		if !strings.Contains(rec.Body.String(), "upstream_failed") {
			t.Fatalf("expected upstream failure, got %q", rec.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not receive upstream error")
	}

	select {
	case rec := <-leaderDone:
		if rec.Code != http.StatusOK {
			t.Fatalf("leader status = %d, want %d", rec.Code, http.StatusOK)
		}
	case <-time.After(time.Second):
		t.Fatal("leader did not finish")
	}
}

type coalesceHijackWriter struct {
	header   http.Header
	hijacked bool
}

func (w *coalesceHijackWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *coalesceHijackWriter) WriteHeader(int) {}

func (w *coalesceHijackWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *coalesceHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	server, client := net.Pipe()
	_ = client.Close()
	var rw bytes.Buffer
	return server, bufio.NewReadWriter(bufio.NewReader(&rw), bufio.NewWriter(&rw)), nil
}

func TestCoalesce_WaiterTimeoutDoesNotLeakInFlightRequest(t *testing.T) {
	coalescer := New(Config{Timeout: 20 * time.Millisecond})
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("late"))
	})

	handler := coalescer.Middleware()(backend)
	leaderDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/slow", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		leaderDone <- rec
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader request did not start")
	}

	waiterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/slow", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		waiterDone <- rec
	}()

	waitForCoalesceWaiter(t, coalescer, "/slow")

	select {
	case rec := <-waiterDone:
		if rec.Code != http.StatusGatewayTimeout {
			t.Fatalf("waiter status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
		}
		if !strings.Contains(rec.Body.String(), "timeout") {
			t.Fatalf("expected timeout response, got %q", rec.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not time out")
	}

	if stats := coalescer.Stats(); stats.InFlight != 1 {
		t.Fatalf("in-flight entries before leader release = %d, want 1", stats.InFlight)
	}

	close(release)

	select {
	case rec := <-leaderDone:
		if rec.Code != http.StatusOK {
			t.Fatalf("leader status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Body.String(); got != "late" {
			t.Fatalf("leader body = %q, want late", got)
		}
	case <-time.After(time.Second):
		t.Fatal("leader did not finish")
	}

	if stats := coalescer.Stats(); stats.InFlight != 0 {
		t.Fatalf("in-flight entries after leader release = %d, want 0", stats.InFlight)
	}
}

func TestCoalesce_WaiterContextCancelReturnsWithoutReplay(t *testing.T) {
	coalescer := New(Config{Timeout: time.Second})
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("leader response"))
	})

	handler := coalescer.Middleware()(backend)
	leaderDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/cancel", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		leaderDone <- rec
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader request did not start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	waiterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/cancel", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		waiterDone <- rec
	}()

	waitForCoalesceWaiter(t, coalescer, "/cancel")
	cancel()

	select {
	case rec := <-waiterDone:
		if rec.Body.Len() != 0 {
			t.Fatalf("canceled waiter wrote body %q", rec.Body.String())
		}
		if got := rec.Header().Get("X-Coalesced"); got != "" {
			t.Fatalf("canceled waiter had X-Coalesced = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter did not return")
	}

	key := DefaultKeyFunc(httptest.NewRequest(http.MethodGet, "/cancel", nil))
	coalescer.mu.RLock()
	inflight := coalescer.inFlight[key]
	if inflight == nil {
		coalescer.mu.RUnlock()
		t.Fatal("leader in-flight entry was removed before release")
	}
	waiters := inflight.waiters.Load()
	coalescer.mu.RUnlock()
	if waiters != 0 {
		t.Fatalf("waiters = %d, want 0 after cancellation", waiters)
	}

	close(release)
	select {
	case rec := <-leaderDone:
		if rec.Code != http.StatusOK {
			t.Fatalf("leader status = %d, want %d", rec.Code, http.StatusOK)
		}
	case <-time.After(time.Second):
		t.Fatal("leader did not complete")
	}
}

func waitForCoalesceWaiter(t *testing.T, coalescer *Coalescer, path string) {
	t.Helper()

	waitForCoalesceWaiterForMethod(t, coalescer, http.MethodGet, path)
}

func waitForCoalesceWaiterForMethod(t *testing.T, coalescer *Coalescer, method, path string) {
	t.Helper()

	key := DefaultKeyFunc(httptest.NewRequest(method, path, nil))
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		coalescer.mu.RLock()
		inflight := coalescer.inFlight[key]
		waiters := int32(0)
		if inflight != nil {
			waiters = inflight.waiters.Load()
		}
		coalescer.mu.RUnlock()

		if waiters > 0 {
			return
		}

		select {
		case <-deadline:
			t.Fatal("coalesced waiter was not registered")
		case <-ticker.C:
		}
	}
}

func TestCoalesce_OnCoalescedCallback(t *testing.T) {
	coalescedCount := int32(0)
	coalescedSum := int32(0)
	var coalescedKey atomic.Value

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	middleware := testMiddleware(Config{
		OnCoalesced: func(key string, count int) {
			if count != 1 {
				t.Errorf("OnCoalesced count = %d, want 1", count)
			}
			atomic.AddInt32(&coalescedCount, 1)
			atomic.AddInt32(&coalescedSum, int32(count))
			coalescedKey.Store(key)
		},
	})
	handler := middleware(backend)

	var wg sync.WaitGroup
	wg.Add(3)

	// Launch 3 concurrent identical requests
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
		}()
	}

	wg.Wait()

	// Callback should be called for waiters (2 requests waited)
	count := atomic.LoadInt32(&coalescedCount)
	if count != 2 {
		t.Errorf("Expected OnCoalesced to be called twice, got %d", count)
	}
	if sum := atomic.LoadInt32(&coalescedSum); sum != 2 {
		t.Errorf("Expected OnCoalesced count sum to be 2, got %d", sum)
	}

	key, _ := coalescedKey.Load().(string)
	if key == "" {
		t.Error("Expected coalescedKey to be set")
	}
}

func TestCoalesce_OnCoalescedPanicDoesNotEscape(t *testing.T) {
	coalescer := New(Config{
		Timeout: time.Second,
		OnCoalesced: func(key string, count int) {
			panic("hook panic")
		},
	})
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("response"))
	})

	handler := coalescer.Middleware()(backend)
	leaderDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/hook-success", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		leaderDone <- rec
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader request did not start")
	}

	waiterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/hook-success", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		waiterDone <- rec
	}()

	waitForCoalesceWaiter(t, coalescer, "/hook-success")
	close(release)

	select {
	case rec := <-waiterDone:
		if rec.Code != http.StatusOK {
			t.Fatalf("waiter status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Body.String(); got != "response" {
			t.Fatalf("waiter body = %q, want response", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not receive replay")
	}

	select {
	case rec := <-leaderDone:
		if rec.Code != http.StatusOK {
			t.Fatalf("leader status = %d, want %d", rec.Code, http.StatusOK)
		}
	case <-time.After(time.Second):
		t.Fatal("leader did not finish")
	}
}

func TestCoalesce_OnCoalescedIgnoresTimedOutWaiters(t *testing.T) {
	onCoalescedCalls := int32(0)
	onCoalescedSum := int32(0)

	coalescer := New(Config{
		Timeout: 40 * time.Millisecond,
		OnCoalesced: func(key string, count int) {
			if count != 1 {
				t.Errorf("OnCoalesced count = %d, want 1", count)
			}
			atomic.AddInt32(&onCoalescedCalls, 1)
			atomic.AddInt32(&onCoalescedSum, int32(count))
		},
	})
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("late"))
	})

	handler := coalescer.Middleware()(backend)
	leaderDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/hook-timeout", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		leaderDone <- rec
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("leader request did not start")
	}

	timedOutWaiterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/hook-timeout", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		timedOutWaiterDone <- rec
	}()

	waitForCoalesceWaiter(t, coalescer, "/hook-timeout")

	select {
	case rec := <-timedOutWaiterDone:
		if rec.Code != http.StatusGatewayTimeout {
			t.Fatalf("timed-out waiter status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
		}
	case <-time.After(time.Second):
		t.Fatal("first waiter did not time out")
	}

	successfulWaiterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/hook-timeout", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		successfulWaiterDone <- rec
	}()

	waitForCoalesceWaiter(t, coalescer, "/hook-timeout")
	close(release)

	select {
	case rec := <-leaderDone:
		if rec.Code != http.StatusOK {
			t.Fatalf("leader status = %d, want %d", rec.Code, http.StatusOK)
		}
	case <-time.After(time.Second):
		t.Fatal("leader did not finish")
	}

	select {
	case rec := <-successfulWaiterDone:
		if rec.Code != http.StatusOK {
			t.Fatalf("successful waiter status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Body.String(); got != "late" {
			t.Fatalf("successful waiter body = %q, want late", got)
		}
	case <-time.After(time.Second):
		t.Fatal("successful waiter was not released")
	}

	if calls := atomic.LoadInt32(&onCoalescedCalls); calls != 1 {
		t.Fatalf("OnCoalesced calls = %d, want 1", calls)
	}
	if sum := atomic.LoadInt32(&onCoalescedSum); sum != 1 {
		t.Fatalf("OnCoalesced count sum = %d, want 1", sum)
	}
}

func TestDefaultKeyFunc(t *testing.T) {
	req1 := httptest.NewRequest("GET", "/test?a=1", nil)
	req2 := httptest.NewRequest("GET", "/test?a=1", nil)
	req3 := httptest.NewRequest("GET", "/test?a=2", nil)
	req4 := httptest.NewRequest("GET", "http://other.example/test?a=1", nil)

	key1 := DefaultKeyFunc(req1)
	key2 := DefaultKeyFunc(req2)
	key3 := DefaultKeyFunc(req3)
	key4 := DefaultKeyFunc(req4)

	if key1 != key2 {
		t.Error("Expected identical requests to generate same key")
	}

	if key1 == key3 {
		t.Error("Expected different requests to generate different keys")
	}

	if key1 == key4 {
		t.Error("Expected requests with different hosts to generate different keys")
	}
}

func TestConfigWithDefaultsAppliesResponseCaptureLimit(t *testing.T) {
	cfg := (&Config{}).WithDefaults()
	if cfg.MaxResponseBytes != defaultMaxResponseBytes {
		t.Fatalf("MaxResponseBytes = %d, want %d", cfg.MaxResponseBytes, defaultMaxResponseBytes)
	}
}

func TestDefaultKeyFuncPartitionsCommonVariantHeaders(t *testing.T) {
	base := httptest.NewRequest(http.MethodGet, "/variant", nil)
	baseKey := DefaultKeyFunc(base)

	tests := []struct {
		name   string
		header string
		value  string
	}{
		{name: "accept", header: "Accept", value: "application/json"},
		{name: "accept encoding", header: "Accept-Encoding", value: "gzip"},
		{name: "accept language", header: "Accept-Language", value: "zh-CN"},
		{name: "authorization", header: "Authorization", value: "Bearer secret"},
		{name: "cookie", header: "Cookie", value: "session=abc"},
		{name: "range", header: "Range", value: "bytes=0-10"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/variant", nil)
			req.Header.Set(tt.header, tt.value)
			if got := DefaultKeyFunc(req); got == baseKey {
				t.Fatalf("expected %s to partition default coalesce key", tt.header)
			}
		})
	}
}

func TestHeaderAwareKeyFuncCanonicalizesConfiguredHeaders(t *testing.T) {
	keyFunc := HeaderAwareKeyFunc([]string{" accept "})
	req1 := httptest.NewRequest(http.MethodGet, "/variant", nil)
	req1.Header.Set("Accept", "application/json")
	req2 := httptest.NewRequest(http.MethodGet, "/variant", nil)
	req2.Header.Set("accept", "application/json")

	if keyFunc(req1) != keyFunc(req2) {
		t.Fatal("expected canonicalized header names to produce the same key")
	}
}

func TestCoalesce_ResponseStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"200 OK", http.StatusOK},
		{"404 Not Found", http.StatusNotFound},
		{"500 Internal Server Error", http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(50 * time.Millisecond)
				w.WriteHeader(tt.statusCode)
			})

			middleware := testMiddleware(Config{})
			handler := middleware(backend)

			var wg sync.WaitGroup
			responses := make(chan *httptest.ResponseRecorder, 2)

			wg.Add(2)

			// Launch 2 concurrent requests
			for i := 0; i < 2; i++ {
				go func() {
					defer wg.Done()
					req := httptest.NewRequest("GET", "/test", nil)
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, req)
					responses <- w
				}()
			}

			wg.Wait()
			close(responses)

			// Both should receive the same status code
			for resp := range responses {
				if resp.Code != tt.statusCode {
					t.Errorf("Expected status %d, got %d", tt.statusCode, resp.Code)
				}
			}
		})
	}
}

func TestWriteResponse_ReplacesStaleHeadersAndPreservesMultiValue(t *testing.T) {
	resp := &capturedResponse{
		statusCode: http.StatusAccepted,
		header: http.Header{
			"X-Test":     {"fresh"},
			"Set-Cookie": {"a=1", "b=2"},
		},
		body: []byte("payload"),
	}
	rec := httptest.NewRecorder()
	rec.Header().Set("X-Test", "stale")

	writeResponse(rec, httptest.NewRequest(http.MethodGet, "/replay", nil), resp, true)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if got := rec.Header().Values("X-Test"); len(got) != 1 || got[0] != "fresh" {
		t.Fatalf("X-Test values = %v, want [fresh]", got)
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 2 || got[0] != "a=1" || got[1] != "b=2" {
		t.Fatalf("Set-Cookie values = %v, want [a=1 b=2]", got)
	}
	if got := rec.Header().Get("X-Coalesced"); got != "true" {
		t.Fatalf("X-Coalesced = %q, want true", got)
	}
	if got := rec.Body.String(); got != "payload" {
		t.Fatalf("body = %q, want payload", got)
	}
}

func TestWriteResponse_DoesNotWriteBodyForHEAD(t *testing.T) {
	resp := &capturedResponse{
		statusCode: http.StatusOK,
		header:     http.Header{"X-Test": {"fresh"}},
		body:       []byte("payload"),
	}
	rec := httptest.NewRecorder()

	writeResponse(rec, httptest.NewRequest(http.MethodHead, "/replay", nil), resp, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("X-Test"); got != "fresh" {
		t.Fatalf("X-Test = %q, want fresh", got)
	}
	if got := rec.Body.String(); got != "" {
		t.Fatalf("body = %q, want empty", got)
	}
}

func TestCoalesce_WaiterReplayUsesCommittedHeaders(t *testing.T) {
	leaderCommitted := make(chan struct{})
	release := make(chan struct{})
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Before", "before")
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("X-Late", "late")
		close(leaderCommitted)
		<-release
		_, _ = w.Write([]byte("response"))
	})

	coalescer := New(Config{Timeout: time.Second, AddCoalescedHeader: true})
	handler := coalescer.Middleware()(backend)
	leaderDone := make(chan *httptest.ResponseRecorder, 1)
	waiterDone := make(chan *httptest.ResponseRecorder, 1)

	go func() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/headers", nil))
		leaderDone <- rec
	}()

	select {
	case <-leaderCommitted:
	case <-time.After(time.Second):
		t.Fatal("leader did not commit headers")
	}

	go func() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/headers", nil))
		waiterDone <- rec
	}()
	waitForCoalesceWaiter(t, coalescer, "/headers")
	close(release)

	select {
	case rec := <-waiterDone:
		if rec.Code != http.StatusAccepted {
			t.Fatalf("waiter status = %d, want %d", rec.Code, http.StatusAccepted)
		}
		if got := rec.Header().Get("X-Before"); got != "before" {
			t.Fatalf("waiter X-Before = %q, want before", got)
		}
		if got := rec.Header().Get("X-Late"); got != "" {
			t.Fatalf("waiter X-Late = %q, want empty", got)
		}
		if got := rec.Header().Get("X-Coalesced"); got != "true" {
			t.Fatalf("waiter X-Coalesced = %q, want true", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not complete")
	}

	select {
	case rec := <-leaderDone:
		if rec.Code != http.StatusAccepted {
			t.Fatalf("leader status = %d, want %d", rec.Code, http.StatusAccepted)
		}
	case <-time.After(time.Second):
		t.Fatal("leader did not complete")
	}
}

func TestConfigWithDefaultsNormalizesMethods(t *testing.T) {
	cfg := (&Config{Methods: []string{" get ", "", "\tHead"}}).WithDefaults()

	if len(cfg.Methods) != 2 || cfg.Methods[0] != http.MethodGet || cfg.Methods[1] != http.MethodHead {
		t.Fatalf("Methods = %v, want [GET HEAD]", cfg.Methods)
	}
}

// ---------------------------------------------------------------------------
// limitedResponseRecorder unit tests
// ---------------------------------------------------------------------------

// TestLimitedResponseRecorderUnwrap verifies that Unwrap returns the underlying
// http.ResponseWriter, exercising the 0%-covered Unwrap method.
func TestLimitedResponseRecorderUnwrap(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := newLimitedResponseRecorder(inner, defaultMaxResponseBytes)
	if got := rec.Unwrap(); got != inner {
		t.Fatalf("Unwrap() = %v, want inner recorder", got)
	}
}

// TestLimitedResponseRecorderWriteHeaderIdempotent verifies that calling
// WriteHeader a second time is silently ignored (covers the wroteHeader guard).
func TestLimitedResponseRecorderWriteHeaderIdempotent(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := newLimitedResponseRecorder(inner, defaultMaxResponseBytes)
	rec.WriteHeader(http.StatusAccepted)
	rec.WriteHeader(http.StatusTeapot) // must be ignored
	if rec.statusCode != http.StatusAccepted {
		t.Fatalf("statusCode = %d after second WriteHeader, want %d", rec.statusCode, http.StatusAccepted)
	}
}

// TestLimitedResponseRecorderFlushSetsUnreplayable verifies that Flush marks
// the response as unreplayable (covers the Flush body).
func TestLimitedResponseRecorderFlushSetsUnreplayable(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := newLimitedResponseRecorder(inner, defaultMaxResponseBytes)
	// Flush before any WriteHeader — must auto-commit the header first.
	rec.Flush()
	if !rec.Unreplayable() {
		t.Fatal("expected Flush to mark response unreplayable")
	}
	if !rec.wroteHeader {
		t.Fatal("expected Flush to commit header")
	}
}

// TestLimitedResponseRecorderHijackOnNonHijackableWriter exercises the error
// path in Hijack when the underlying writer does not implement http.Hijacker.
func TestLimitedResponseRecorderHijackOnNonHijackableWriter(t *testing.T) {
	// httptest.ResponseRecorder does NOT implement http.Hijacker.
	inner := httptest.NewRecorder()
	rec := newLimitedResponseRecorder(inner, defaultMaxResponseBytes)
	conn, rw, err := rec.Hijack()
	if err == nil {
		t.Fatal("expected error hijacking non-hijackable writer")
	}
	if conn != nil || rw != nil {
		t.Fatal("expected nil conn and rw on hijack error")
	}
}

// TestWriteHeaderKeyNilRequest covers the nil-request guard in writeHeaderKey.
func TestWriteHeaderKeyNilRequest(t *testing.T) {
	h := fnv.New64a()
	before := h.Sum64()
	// Must not panic.
	writeHeaderKey(h, nil, "Accept")
	if h.Sum64() != before {
		t.Fatal("expected hash unchanged for nil request")
	}
}

// TestWriteHeaderKeyEmptyHeaderName covers the empty-name guard after
// CanonicalHeaderKey in writeHeaderKey.
func TestWriteHeaderKeyEmptyHeaderName(t *testing.T) {
	h := fnv.New64a()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	before := h.Sum64()
	// Passing only whitespace should produce an empty canonical name.
	writeHeaderKey(h, req, "   ")
	if h.Sum64() != before {
		t.Fatal("expected hash unchanged for whitespace-only header name")
	}
}

// TestWriteHeaderKeyHeaderWithEmptyValues covers the early return when the
// request has no values for the given header.
func TestWriteHeaderKeyHeaderWithEmptyValues(t *testing.T) {
	h := fnv.New64a()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Request has no Accept header — the len(values)==0 guard should fire.
	before := h.Sum64()
	writeHeaderKey(h, req, "Accept")
	if h.Sum64() != before {
		t.Fatal("expected hash unchanged when header is absent")
	}
}

// TestWriteHeaderKeySkipsEmptyValue exercises the inner value-is-empty skip
// path by setting a header that contains only whitespace.
func TestWriteHeaderKeySkipsEmptyValue(t *testing.T) {
	h1 := fnv.New64a()
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Header.Set("Accept", "application/json")
	writeHeaderKey(h1, req1, "Accept")

	h2 := fnv.New64a()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	// A value of "   " is non-empty at the Values level but all whitespace —
	// the inner trimmed-value guard skips it.
	req2.Header["Accept"] = []string{"   "}
	writeHeaderKey(h2, req2, "Accept")

	if h1.Sum64() == h2.Sum64() {
		t.Error("expected different hashes: application/json contributes value bytes " +
			"while the whitespace-only value contributes only the outer key bytes")
	}
}
