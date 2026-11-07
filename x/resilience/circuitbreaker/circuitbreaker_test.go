package circuitbreaker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockClock is a controllable clock for testing time-dependent behavior.
type mockClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMockClock() *mockClock {
	return &mockClock{now: time.Now()}
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mockClock) Since(t time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now.Sub(t)
}

func (c *mockClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestCB creates a CircuitBreaker with a mock clock for deterministic tests.
func newTestCB(config Config) (*CircuitBreaker, *mockClock) {
	cb := New(config)
	mc := newMockClock()
	cb.clock = mc
	cb.stateMu.Lock()
	cb.stateChanged = mc.Now()
	cb.stateMu.Unlock()
	return cb, mc
}

// errTest is a reusable sentinel error for tests.
var errTest = errors.New("test error")

type circuitBreakerErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Details struct {
			Circuit string `json:"circuit"`
		} `json:"details"`
	} `json:"error"`
}

// ---------------------------------------------------------------------------
// State.String
// ---------------------------------------------------------------------------

func TestStateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.state.String(); got != tt.want {
				t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Counts helpers
// ---------------------------------------------------------------------------

func TestCountsFailureRate(t *testing.T) {
	tests := []struct {
		name     string
		counts   Counts
		wantRate float64
	}{
		{"zero requests", Counts{}, 0.0},
		{"no failures", Counts{Requests: 10, Successes: 10}, 0.0},
		{"half failures", Counts{Requests: 10, Failures: 5, Successes: 5}, 0.5},
		{"all failures", Counts{Requests: 4, Failures: 4}, 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.counts.FailureRate(); got != tt.wantRate {
				t.Errorf("FailureRate() = %v, want %v", got, tt.wantRate)
			}
		})
	}
}

func TestCountsSuccessRate(t *testing.T) {
	tests := []struct {
		name     string
		counts   Counts
		wantRate float64
	}{
		{"zero requests", Counts{}, 0.0},
		{"all successes", Counts{Requests: 5, Successes: 5}, 1.0},
		{"half successes", Counts{Requests: 10, Successes: 5, Failures: 5}, 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.counts.SuccessRate(); got != tt.wantRate {
				t.Errorf("SuccessRate() = %v, want %v", got, tt.wantRate)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// New — default values
// ---------------------------------------------------------------------------

func TestNewDefaults(t *testing.T) {
	cb := New(Config{})

	if cb.Name() != "circuit-breaker" {
		t.Errorf("default name = %q, want %q", cb.Name(), "circuit-breaker")
	}
	if cb.config.FailureThreshold != 0.5 {
		t.Errorf("default FailureThreshold = %v, want 0.5", cb.config.FailureThreshold)
	}
	if cb.config.SuccessThreshold != 3 {
		t.Errorf("default SuccessThreshold = %v, want 3", cb.config.SuccessThreshold)
	}
	if cb.config.Timeout != 30*time.Second {
		t.Errorf("default Timeout = %v, want 30s", cb.config.Timeout)
	}
	if cb.config.MinRequests != 10 {
		t.Errorf("default MinRequests = %v, want 10", cb.config.MinRequests)
	}
	if cb.config.MaxRequests != 1 {
		t.Errorf("default MaxRequests = %v, want 1", cb.config.MaxRequests)
	}
	if cb.State() != StateClosed {
		t.Errorf("initial state = %v, want Closed", cb.State())
	}
}

func TestNewCustomConfig(t *testing.T) {
	cb := New(Config{
		Name:             "my-cb",
		FailureThreshold: 0.7,
		SuccessThreshold: 5,
		Timeout:          10 * time.Second,
		MinRequests:      20,
		MaxRequests:      3,
	})

	if cb.Name() != "my-cb" {
		t.Errorf("name = %q, want %q", cb.Name(), "my-cb")
	}
	if cb.config.FailureThreshold != 0.7 {
		t.Errorf("FailureThreshold = %v, want 0.7", cb.config.FailureThreshold)
	}
	if cb.config.SuccessThreshold != 5 {
		t.Errorf("SuccessThreshold = %v, want 5", cb.config.SuccessThreshold)
	}
	if cb.config.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", cb.config.Timeout)
	}
	if cb.config.MinRequests != 20 {
		t.Errorf("MinRequests = %v, want 20", cb.config.MinRequests)
	}
	if cb.config.MaxRequests != 3 {
		t.Errorf("MaxRequests = %v, want 3", cb.config.MaxRequests)
	}
}

// ---------------------------------------------------------------------------
// Call — closed state
// ---------------------------------------------------------------------------

func TestCallSuccessInClosedState(t *testing.T) {
	cb, _ := newTestCB(Config{MinRequests: 5})

	err := cb.Call(func() error { return nil })
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	counts := cb.Counts()
	if counts.Requests != 1 || counts.Successes != 1 || counts.Failures != 0 {
		t.Errorf("unexpected counts: %+v", counts)
	}
}

func TestCallFailureInClosedState(t *testing.T) {
	cb, _ := newTestCB(Config{MinRequests: 100}) // high min so it stays closed

	err := cb.Call(func() error { return errTest })
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}

	counts := cb.Counts()
	if counts.Requests != 1 || counts.Failures != 1 {
		t.Errorf("unexpected counts: %+v", counts)
	}
	if cb.State() != StateClosed {
		t.Errorf("state = %v, want Closed (min requests not reached)", cb.State())
	}
}

// ---------------------------------------------------------------------------
// Closed → Open transition
// ---------------------------------------------------------------------------

func TestTransitionClosedToOpen(t *testing.T) {
	cb, _ := newTestCB(Config{
		FailureThreshold: 0.5,
		MinRequests:      4,
	})

	// 4 failures → 100 % failure rate, exceeds 50 % threshold
	for i := 0; i < 4; i++ {
		_ = cb.Call(func() error { return errTest })
	}

	if cb.State() != StateOpen {
		t.Fatalf("state = %v, want Open after exceeding failure threshold", cb.State())
	}
}

func TestNoOpenBeforeMinRequests(t *testing.T) {
	cb, _ := newTestCB(Config{
		FailureThreshold: 0.5,
		MinRequests:      10,
	})

	// 3 failures out of 3 requests — 100 % but below MinRequests
	for i := 0; i < 3; i++ {
		_ = cb.Call(func() error { return errTest })
	}

	if cb.State() != StateClosed {
		t.Fatalf("state = %v, want Closed (below MinRequests)", cb.State())
	}
}

// ---------------------------------------------------------------------------
// Open → HalfOpen transition (timeout elapsed)
// ---------------------------------------------------------------------------

func TestTransitionOpenToHalfOpen(t *testing.T) {
	cb, mc := newTestCB(Config{
		FailureThreshold: 0.5,
		MinRequests:      2,
		Timeout:          5 * time.Second,
	})

	// Trip to open
	for i := 0; i < 2; i++ {
		_ = cb.Call(func() error { return errTest })
	}
	if cb.State() != StateOpen {
		t.Fatalf("state = %v, want Open", cb.State())
	}

	// Before timeout: requests should fail fast
	mc.Advance(4 * time.Second)
	err := cb.Call(func() error { return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen before timeout, got %v", err)
	}

	// After timeout: should transition to half-open and allow the request
	mc.Advance(2 * time.Second) // total 6s > 5s timeout
	err = cb.Call(func() error { return nil })
	if err != nil {
		t.Fatalf("expected nil after timeout, got %v", err)
	}
	if cb.State() != StateHalfOpen {
		t.Fatalf("state = %v, want HalfOpen after timeout", cb.State())
	}
}

// ---------------------------------------------------------------------------
// HalfOpen → Closed transition (success threshold met)
// ---------------------------------------------------------------------------

func TestTransitionHalfOpenToClosed(t *testing.T) {
	cb, mc := newTestCB(Config{
		FailureThreshold: 0.5,
		MinRequests:      2,
		Timeout:          1 * time.Second,
		SuccessThreshold: 3,
		MaxRequests:      10, // allow enough requests in half-open
	})

	// Trip to open
	for i := 0; i < 2; i++ {
		_ = cb.Call(func() error { return errTest })
	}

	// Advance past timeout → half-open
	mc.Advance(2 * time.Second)

	// 3 successes in half-open should close the circuit
	for i := 0; i < 3; i++ {
		err := cb.Call(func() error { return nil })
		if err != nil {
			t.Fatalf("call %d: unexpected error %v", i, err)
		}
	}

	if cb.State() != StateClosed {
		t.Fatalf("state = %v, want Closed after success threshold", cb.State())
	}

	// Metrics should be reset
	counts := cb.Counts()
	if counts.Requests != 0 || counts.Failures != 0 {
		t.Errorf("expected reset counts after closing, got %+v", counts)
	}
}

// ---------------------------------------------------------------------------
// HalfOpen → Open transition (failure in half-open)
// ---------------------------------------------------------------------------

func TestTransitionHalfOpenToOpen(t *testing.T) {
	cb, mc := newTestCB(Config{
		FailureThreshold: 0.5,
		MinRequests:      2,
		Timeout:          1 * time.Second,
		MaxRequests:      10,
	})

	// Trip to open
	for i := 0; i < 2; i++ {
		_ = cb.Call(func() error { return errTest })
	}

	// Advance past timeout → half-open
	mc.Advance(2 * time.Second)
	// Trigger half-open transition
	_ = cb.Call(func() error { return nil })
	if cb.State() != StateHalfOpen {
		t.Fatalf("state = %v, want HalfOpen", cb.State())
	}

	// A failure in half-open should reopen
	_ = cb.Call(func() error { return errTest })
	if cb.State() != StateOpen {
		t.Fatalf("state = %v, want Open after half-open failure", cb.State())
	}
}

// ---------------------------------------------------------------------------
// MaxRequests in half-open state
// ---------------------------------------------------------------------------

func TestHalfOpenMaxRequests(t *testing.T) {
	cb, mc := newTestCB(Config{
		FailureThreshold: 0.5,
		MinRequests:      2,
		Timeout:          1 * time.Second,
		MaxRequests:      1,
		SuccessThreshold: 5, // high so we stay in half-open
	})

	// Trip to open
	for i := 0; i < 2; i++ {
		_ = cb.Call(func() error { return errTest })
	}

	// Advance past timeout → first call moves to half-open
	mc.Advance(2 * time.Second)
	_ = cb.Call(func() error { return nil }) // consumes the 1 allowed request

	if cb.State() != StateHalfOpen {
		t.Fatalf("state = %v, want HalfOpen", cb.State())
	}

	// Second call should be rejected (max 1 request)
	err := cb.Call(func() error { return nil })
	if !errors.Is(err, ErrTooManyRequests) {
		t.Fatalf("expected ErrTooManyRequests, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// CallWithContext
// ---------------------------------------------------------------------------

func TestCallWithContextCancelled(t *testing.T) {
	cb, _ := newTestCB(Config{})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := cb.CallWithContext(ctx, func() error { return nil })
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestCallWithContextSuccess(t *testing.T) {
	cb, _ := newTestCB(Config{})

	err := cb.CallWithContext(t.Context(), func() error { return nil })
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Custom ShouldTrip
// ---------------------------------------------------------------------------

func TestCustomShouldTrip(t *testing.T) {
	cb, _ := newTestCB(Config{
		MinRequests: 2,
		ShouldTrip: func(counts Counts) bool {
			// Trip if there are any failures at all
			return counts.Failures > 0
		},
	})

	// One success, one failure
	_ = cb.Call(func() error { return nil })
	_ = cb.Call(func() error { return errTest })

	if cb.State() != StateOpen {
		t.Fatalf("state = %v, want Open (custom ShouldTrip)", cb.State())
	}
}

func TestCustomShouldTripNotTripped(t *testing.T) {
	cb, _ := newTestCB(Config{
		MinRequests: 2,
		ShouldTrip: func(counts Counts) bool {
			return counts.Failures >= 10 // very lenient
		},
	})

	// 2 failures but threshold is 10
	for i := 0; i < 3; i++ {
		_ = cb.Call(func() error { return errTest })
	}

	if cb.State() != StateClosed {
		t.Fatalf("state = %v, want Closed (custom ShouldTrip not met)", cb.State())
	}
}

// ---------------------------------------------------------------------------
// OnStateChange hook
// ---------------------------------------------------------------------------

func TestOnStateChangeHook(t *testing.T) {
	type transition struct {
		from, to State
	}
	// Use a buffered channel to collect transitions reliably from async goroutines
	ch := make(chan transition, 10)

	cb, mc := newTestCB(Config{
		MinRequests:      2,
		FailureThreshold: 0.5,
		Timeout:          1 * time.Second,
		SuccessThreshold: 1,
		MaxRequests:      10,
		OnStateChange: func(from, to State) {
			ch <- transition{from, to}
		},
	})

	// Closed → Open
	for i := 0; i < 2; i++ {
		_ = cb.Call(func() error { return errTest })
	}

	// Open → HalfOpen → Closed (SuccessThreshold=1 so one success closes it)
	mc.Advance(2 * time.Second)
	_ = cb.Call(func() error { return nil })

	// Collect all 3 expected transitions with a timeout
	var transitions []transition
	timeout := time.After(2 * time.Second)
	for len(transitions) < 3 {
		select {
		case tr := <-ch:
			transitions = append(transitions, tr)
		case <-timeout:
			t.Fatalf("timed out waiting for transitions, got %d: %v", len(transitions), transitions)
		}
	}

	expected := []transition{
		{StateClosed, StateOpen},
		{StateOpen, StateHalfOpen},
		{StateHalfOpen, StateClosed},
	}

	// Hooks fire asynchronously, so check that all expected transitions are present
	for _, exp := range expected {
		found := false
		for _, got := range transitions {
			if got.from == exp.from && got.to == exp.to {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing transition %v→%v in %v", exp.from, exp.to, transitions)
		}
	}
}

// ---------------------------------------------------------------------------
// Reset and Trip
// ---------------------------------------------------------------------------

func TestReset(t *testing.T) {
	cb, _ := newTestCB(Config{MinRequests: 2, FailureThreshold: 0.5})

	// Trip to open
	for i := 0; i < 2; i++ {
		_ = cb.Call(func() error { return errTest })
	}
	if cb.State() != StateOpen {
		t.Fatalf("state = %v, want Open", cb.State())
	}

	cb.Reset()
	if cb.State() != StateClosed {
		t.Fatalf("state = %v, want Closed after Reset()", cb.State())
	}

	counts := cb.Counts()
	if counts.Requests != 0 || counts.Failures != 0 {
		t.Errorf("expected zeroed counts after Reset, got %+v", counts)
	}
}

func TestTrip(t *testing.T) {
	cb, _ := newTestCB(Config{})

	if cb.State() != StateClosed {
		t.Fatalf("state = %v, want Closed initially", cb.State())
	}

	cb.Trip()
	if cb.State() != StateOpen {
		t.Fatalf("state = %v, want Open after Trip()", cb.State())
	}
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

func TestStats(t *testing.T) {
	cb, _ := newTestCB(Config{Name: "test-cb", MinRequests: 100})

	_ = cb.Call(func() error { return nil })
	_ = cb.Call(func() error { return errTest })

	stats := cb.Stats()
	if stats.Name != "test-cb" {
		t.Errorf("stats.Name = %q, want %q", stats.Name, "test-cb")
	}
	if stats.State != StateClosed {
		t.Errorf("stats.State = %v, want Closed", stats.State)
	}
	if stats.Counts.Requests != 2 {
		t.Errorf("stats.Counts.Requests = %d, want 2", stats.Counts.Requests)
	}
	if stats.Counts.Successes != 1 {
		t.Errorf("stats.Counts.Successes = %d, want 1", stats.Counts.Successes)
	}
	if stats.Counts.Failures != 1 {
		t.Errorf("stats.Counts.Failures = %d, want 1", stats.Counts.Failures)
	}
}

func TestStatsString(t *testing.T) {
	s := Stats{
		Name:  "svc",
		State: StateClosed,
		Counts: Counts{
			Requests:  10,
			Successes: 8,
			Failures:  2,
		},
	}
	got := s.String()
	want := "svc [closed] requests=10 successes=8 failures=2 rate=20.00%"
	if got != want {
		t.Errorf("Stats.String() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Middleware — normal passthrough
// ---------------------------------------------------------------------------

func TestMiddlewarePassthrough(t *testing.T) {
	handler := Middleware(Config{MinRequests: 100})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		}),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
	}
}

// ---------------------------------------------------------------------------
// Middleware — 5xx triggers failure tracking
// ---------------------------------------------------------------------------

func TestMiddleware5xxCountsAsFailure(t *testing.T) {
	var callCount int
	handler := Middleware(Config{
		Name:             "5xx-test",
		MinRequests:      2,
		FailureThreshold: 0.5,
	})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)

	// Send enough requests to trip the breaker
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		handler.ServeHTTP(rec, req)
	}

	// Next request should get 503 (circuit open)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when circuit is open", rec.Code)
	}

	// Verify response body
	var body circuitBreakerErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if body.Error.Code != CodeCircuitOpen {
		t.Errorf("error code = %v, want %q", body.Error.Code, CodeCircuitOpen)
	}
	if body.Error.Details.Circuit != "5xx-test" {
		t.Errorf("details circuit = %v, want %q", body.Error.Details.Circuit, "5xx-test")
	}

	// Verify X-Circuit-Breaker-State header
	if got := rec.Header().Get("X-Circuit-Breaker-State"); got != "open" {
		t.Errorf("X-Circuit-Breaker-State = %q, want %q", got, "open")
	}
}

// ---------------------------------------------------------------------------
// Middleware — 4xx does NOT count as failure
// ---------------------------------------------------------------------------

func TestMiddleware4xxDoesNotCountAsFailure(t *testing.T) {
	handler := Middleware(Config{
		MinRequests:      2,
		FailureThreshold: 0.5,
	})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}),
	)

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("request %d: status = %d, want 400", i, rec.Code)
		}
	}
	// Circuit should still be closed — 4xx is not a server failure
}

// ---------------------------------------------------------------------------
// MiddlewareWithErrorHandler
// ---------------------------------------------------------------------------

func TestMiddlewareWithErrorHandler(t *testing.T) {
	var handlerCalled atomic.Bool
	customHandler := func(w http.ResponseWriter, r *http.Request, cb *CircuitBreaker, err error) {
		handlerCalled.Store(true)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("custom error: " + err.Error()))
	}

	handler := MiddlewareWithErrorHandler(
		Config{
			Name:             "custom-eh",
			MinRequests:      2,
			FailureThreshold: 0.5,
		},
		customHandler,
	)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)

	// Trip the breaker
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		handler.ServeHTTP(rec, req)
	}

	// Next request triggers custom error handler
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if !handlerCalled.Load() {
		t.Fatal("custom error handler was not called")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if got := rec.Body.String(); got != "custom error: circuit breaker is open" {
		t.Errorf("body = %q, want custom error message", got)
	}
}

// ---------------------------------------------------------------------------
// statusWriter
// ---------------------------------------------------------------------------

func TestStatusWriterDefaultStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	sw.Write([]byte("hello"))

	if sw.statusCode != http.StatusOK {
		t.Errorf("statusCode = %d, want 200", sw.statusCode)
	}
	if !sw.written {
		t.Error("expected written = true after Write()")
	}
}

func TestStatusWriterExplicitHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	sw.WriteHeader(http.StatusNotFound)
	sw.WriteHeader(http.StatusOK) // second call should be ignored

	if sw.statusCode != http.StatusNotFound {
		t.Errorf("statusCode = %d, want 404 (first call wins)", sw.statusCode)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("recorded status = %d, want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Concurrent access
// ---------------------------------------------------------------------------

func TestConcurrentCalls(t *testing.T) {
	cb, _ := newTestCB(Config{
		MinRequests:      100,
		FailureThreshold: 0.9, // very lenient
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if n%3 == 0 {
				_ = cb.Call(func() error { return errTest })
			} else {
				_ = cb.Call(func() error { return nil })
			}
		}(i)
	}
	wg.Wait()

	counts := cb.Counts()
	if counts.Requests != 50 {
		t.Errorf("Requests = %d, want 50", counts.Requests)
	}
	if total := counts.Successes + counts.Failures; total != 50 {
		t.Errorf("Successes(%d) + Failures(%d) = %d, want 50", counts.Successes, counts.Failures, total)
	}
}

// ---------------------------------------------------------------------------
// Full lifecycle: Closed → Open → HalfOpen → Closed
// ---------------------------------------------------------------------------

func TestFullLifecycle(t *testing.T) {
	cb, mc := newTestCB(Config{
		Name:             "lifecycle",
		FailureThreshold: 0.5,
		MinRequests:      4,
		Timeout:          5 * time.Second,
		SuccessThreshold: 2,
		MaxRequests:      10,
	})

	// Phase 1: Closed — all failures
	for i := 0; i < 4; i++ {
		_ = cb.Call(func() error { return errTest })
	}
	if cb.State() != StateOpen {
		t.Fatalf("phase 1: state = %v, want Open", cb.State())
	}

	// Phase 2: Open — requests fail fast
	err := cb.Call(func() error { return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("phase 2: expected ErrCircuitOpen, got %v", err)
	}

	// Phase 3: After timeout → HalfOpen
	mc.Advance(6 * time.Second)
	err = cb.Call(func() error { return nil })
	if err != nil {
		t.Fatalf("phase 3: unexpected error %v", err)
	}
	// The first call transitions Open→HalfOpen; check state
	if s := cb.State(); s != StateHalfOpen {
		t.Fatalf("phase 3: state = %v, want HalfOpen", s)
	}

	// Phase 4: Enough successes in half-open → Closed
	_ = cb.Call(func() error { return nil }) // second success
	if cb.State() != StateClosed {
		t.Fatalf("phase 4: state = %v, want Closed", cb.State())
	}
}

// ---------------------------------------------------------------------------
// setState is idempotent (same state → no-op)
// ---------------------------------------------------------------------------

func TestSetStateSameStateNoop(t *testing.T) {
	var called atomic.Bool
	cb, _ := newTestCB(Config{
		OnStateChange: func(from, to State) {
			called.Store(true)
		},
	})

	// Calling setState with the current state should not fire the hook
	cb.setState(StateClosed)
	time.Sleep(20 * time.Millisecond)

	if called.Load() {
		t.Error("OnStateChange should not be called for same-state transition")
	}
}
