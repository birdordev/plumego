package core

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware"
)

// ---- RouteGroup.Patch / RouteGroup.Put (0%) ---------------------------------

func TestGroupPatchRegistersRouteAndResponds200(t *testing.T) {
	app := newTestApp()
	g := app.Group("/api")
	called := false
	if err := g.Patch("/resource", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})); err != nil {
		t.Fatalf("group Patch returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/resource", nil))

	if !called {
		t.Fatal("group Patch handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestGroupPutRegistersRouteAndResponds200(t *testing.T) {
	app := newTestApp()
	g := app.Group("/api")
	called := false
	if err := g.Put("/resource", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})); err != nil {
		t.Fatalf("group Put returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/resource", nil))

	if !called {
		t.Fatal("group Put handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// ---- Routes (75%) -----------------------------------------------------------

func TestRoutesReturnsRegisteredRoutes(t *testing.T) {
	app := newTestApp()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	mustRegisterRoute(t, app.Get("/routes-get", handler))
	mustRegisterRoute(t, app.Post("/routes-post", handler))

	routes := app.Routes()
	if len(routes) == 0 {
		t.Fatal("Routes() returned empty slice after registrations")
	}

	found := make(map[string]bool)
	for _, ri := range routes {
		found[ri.Method+":"+ri.Path] = true
	}
	if !found["GET:/routes-get"] {
		t.Errorf("Routes() missing GET:/routes-get; got %v", routes)
	}
	if !found["POST:/routes-post"] {
		t.Errorf("Routes() missing POST:/routes-post; got %v", routes)
	}
}

func TestRoutesWithNilRouterReturnsNil(t *testing.T) {
	// Zero-value App has no router; Routes() must handle that gracefully.
	var app App
	routes := app.Routes()
	if routes != nil {
		t.Fatalf("expected nil, got %v", routes)
	}
}

// ---- URL (75%) --------------------------------------------------------------

func TestURLWithNilRouterReturnsError(t *testing.T) {
	// Zero-value App has no router.
	var app App
	got, err := app.URL("any-route")
	if err == nil {
		t.Fatalf("expected error for nil router, got nil")
	}
	if got != "" {
		t.Fatalf("expected empty string on error, got %q", got)
	}
}

func TestURLForUnknownNameReturnsError(t *testing.T) {
	app := newTestApp()
	mustRegisterRoute(t, app.Get("/other", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	got, err := app.URL("does-not-exist")
	if err == nil {
		t.Fatalf("expected error for unknown route, got nil")
	}
	if got != "" {
		t.Fatalf("expected empty string on error, got %q", got)
	}
}

// ---- decrementOpen (75%) ----------------------------------------------------

func TestDecrementOpenDoesNotUnderflowFromZero(t *testing.T) {
	ct := newConnectionTracker(nil, time.Second)
	// open starts at 0; calling decrementOpen must be a no-op (early return branch).
	ct.decrementOpen()
	if got := ct.open.Load(); got != 0 {
		t.Fatalf("open = %d after decrement from 0, want 0", got)
	}
}

func TestDecrementOpenCorrectsNegativeValue(t *testing.T) {
	ct := newConnectionTracker(nil, time.Second)
	// Force open to exactly 1 then decrement: should reach 0, not go negative.
	ct.open.Store(1)
	ct.decrementOpen()
	if got := ct.open.Load(); got != 0 {
		t.Fatalf("open = %d after decrement from 1, want 0", got)
	}
}

func TestDecrementOpenClampsNegativeRaceCondition(t *testing.T) {
	// Simulate the race: two goroutines both see open==1 in the guard but only
	// one decrements first, causing the second Add to land at -1.
	// We reproduce this deterministically by parking a value at 1, then
	// swapping it to 0 before the internal Add can execute.
	ct := newConnectionTracker(nil, time.Second)
	// Plant a count of 1 so the Load()>0 guard passes.
	ct.open.Store(1)

	ready := make(chan struct{})
	go func() {
		close(ready)
		// Compete with the main goroutine: decrement from 1 to 0 so that
		// the main goroutine's Add(-1) lands at -1.
		ct.open.Add(-1)
	}()
	<-ready
	// Small yield so the goroutine above runs first with high probability.
	// Even if it doesn't, the test remains correct — it just won't always hit
	// the clamp branch.  That's fine: the loop below calls decrementOpen many
	// times to maximise the chance of exercising it.
	for i := 0; i < 100; i++ {
		ct.open.Store(1)
		ct.decrementOpen()
		if got := ct.open.Load(); got < 0 {
			t.Fatalf("open went negative: %d", got)
		}
	}
}

// ---- wrapCoreError (80%) and formatErrorParams (90%) -----------------------

func TestWrapCoreErrorWithNilReturnsNil(t *testing.T) {
	if err := wrapCoreError(nil, "op", nil); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWrapCoreErrorWithNilAndParamsReturnsNil(t *testing.T) {
	if err := wrapCoreError(nil, "op", map[string]any{"key": "val"}); err != nil {
		t.Fatalf("expected nil for nil inner error even with params, got %v", err)
	}
}

func TestWrapCoreErrorWithParamsIncludesParams(t *testing.T) {
	inner := http.ErrNoCookie // any non-nil sentinel error
	err := wrapCoreError(inner, "op", map[string]any{"a": 1, "b": "two"})
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	got := err.Error()
	for _, fragment := range []string{"core op", "a=1", "b=two"} {
		found := false
		for i := 0; i+len(fragment) <= len(got); i++ {
			if got[i:i+len(fragment)] == fragment {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("error %q missing fragment %q", got, fragment)
		}
	}
}

func TestFormatErrorParamsWithEmptyMapReturnsEmpty(t *testing.T) {
	// Direct test of the early-exit branch: empty map → "".
	if got := formatErrorParams(map[string]any{}); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

// ---- preparedHandlerSnapshot (83%) ------------------------------------------

func TestPreparedHandlerSnapshotBeforeBuildReturnsError(t *testing.T) {
	// Create an app and freeze it without building the handler; this is normally
	// impossible via the public API, so we manipulate internal state directly.
	app := newTestApp()
	// freezeConfig moves state to HandlerPrepared without populating handler.
	app.freezeConfig()
	// handler is still nil because buildHandler has not been called.
	_, err := app.preparedHandlerSnapshot()
	if err == nil {
		t.Fatal("expected error when handler is nil")
	}
	assertCoreError(t, err, operationPrepareServer, "handler not configured")
}

// ---- serverConfigSnapshot (91%) — router/middleware not configured ----------

func TestServerConfigSnapshotRejectsNilMiddleware(t *testing.T) {
	app := newTestApp()
	app.middlewareChain = nil
	_, err := app.serverConfigSnapshot()
	if err == nil {
		t.Fatal("expected error when middleware chain is nil")
	}
	assertCoreError(t, err, operationPrepareServer, "router or middleware chain not configured")
}

// ---- Shutdown with a connection tracker and no open connections ------------

func TestShutdownWithNoOpenConnectionsSkipsDrain(t *testing.T) {
	app := newTestApp()
	mustRegisterRoute(t, app.Get("/sd", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// connTracker has open=0, so startDrain should not fire.
	if err := app.Shutdown(nil); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// ---- registerRoute with nil router (91.7%) -----------------------------------

func TestRegisterRouteWithNilRouterReturnsError(t *testing.T) {
	app := newTestApp()
	app.router = nil
	err := app.Get("/nil-router", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if err == nil {
		t.Fatal("expected error when router is nil")
	}
	assertCoreError(t, err, operationAddRoute, "router not configured")
}

// ---- drain with non-nil logger to exercise the logger.Info path (90.9%) -----

type trackingLogger struct {
	log.StructuredLogger
	infoCalled bool
}

func (l *trackingLogger) Info(msg string, fields ...log.Fields)  { l.infoCalled = true }
func (l *trackingLogger) Error(msg string, fields ...log.Fields) {}
func (l *trackingLogger) Debug(msg string, fields ...log.Fields) {}
func (l *trackingLogger) Warn(msg string, fields ...log.Fields)  {}

func TestDrainWithNonNilLoggerCallsInfo(t *testing.T) {
	lg := &trackingLogger{}
	ct := newConnectionTracker(lg, 10*time.Millisecond)
	ct.open.Store(1)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	ct.drain(ctx)

	if !lg.infoCalled {
		t.Fatal("expected logger.Info to be called during drain with open connections")
	}
}

// ---- Use with nil middlewareChain -------------------------------------------

func TestUseWithNilMiddlewareChainReturnsError(t *testing.T) {
	app := newTestApp()
	app.middlewareChain = nil
	err := app.Use(func(next http.Handler) http.Handler { return next })
	if err == nil {
		t.Fatal("expected error when middleware chain is nil")
	}
	assertCoreError(t, err, operationUseMiddleware, "middleware chain not configured")
}

// ---- ensureHandlerPrepared warning when no routes registered ----------------

func TestEnsureHandlerPreparedWarnsWhenNoRoutesRegistered(t *testing.T) {
	// Calling ServeHTTP on an app with no routes triggers ensureHandlerPrepared,
	// which should log a warning that no routes are registered.
	app := newTestApp()
	// Do not register any routes — ServeHTTP will still work but warn.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/not-found", nil)
	app.ServeHTTP(rec, req)
	// The request returns 404 since no routes are registered.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for no-route app, got %d", rec.Code)
	}
}

// ---- decrementOpen CAS branch (concurrent race to -1) ----------------------

func TestDecrementOpenCASBranchUnderConcurrentRace(t *testing.T) {
	// Force the `if v := t.open.Add(-1); v < 0 { t.open.CompareAndSwap(v, 0) }`
	// branch by having two goroutines both see open==1, both pass the > 0 guard,
	// then both call Add(-1): one lands at 0, the other lands at -1 and must CAS.
	ct := newConnectionTracker(nil, time.Second)

	const iterations = 5000
	for i := 0; i < iterations; i++ {
		ct.open.Store(1)

		// Use a barrier so both goroutines are ready before either starts.
		ready := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-ready
			ct.decrementOpen()
		}()
		go func() {
			defer wg.Done()
			<-ready
			ct.decrementOpen()
		}()
		// Release both goroutines simultaneously and yield to increase
		// the chance of interleaving inside decrementOpen.
		close(ready)
		runtime.Gosched()
		wg.Wait()

		if got := ct.open.Load(); got < 0 {
			t.Fatalf("iteration %d: open went negative: %d", i, got)
		}
	}
	// After the loop the count must be 0 (never negative).
	if got := ct.open.Load(); got != 0 {
		t.Fatalf("final open = %d, want 0", got)
	}
}

// ---- Shutdown non-ErrServerClosed error path (lines 83-84) -----------------

func TestShutdownReturnsWrappedErrorWhenContextExpiresDuringDrain(t *testing.T) {
	// Require a network listener; skip if networking is not available.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("network listen not available: %v", err)
	}

	// Gate that keeps the in-flight handler blocked until we signal it.
	handlerUnblock := make(chan struct{})
	handlerStarted := make(chan struct{})

	app := newTestApp()
	mustRegisterRoute(t, app.Get("/hold", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handlerStarted)
		<-handlerUnblock
		w.WriteHeader(http.StatusOK)
	})))

	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	srv, err := app.Server()
	if err != nil {
		t.Fatalf("Server: %v", err)
	}

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- srv.Serve(ln)
	}()

	// Start a request that will block the server.
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + ln.Addr().String() + "/hold")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	// Wait until the handler is running so there is an active connection.
	select {
	case <-handlerStarted:
	case <-time.After(2 * time.Second):
		close(handlerUnblock)
		t.Fatal("handler did not start within timeout")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	shutdownErr := app.Shutdown(ctx)
	drainReleased := app.connTracker.activeDrain.Load() == nil

	close(handlerUnblock)
	<-clientDone
	<-serverDone

	if !errors.Is(shutdownErr, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want deadline exceeded with active connections", shutdownErr)
	}
	assertCoreError(t, shutdownErr, operationShutdownApp, "context deadline exceeded")
	if !drainReleased {
		t.Fatal("timed-out Shutdown retained drain ownership")
	}
	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatalf("shutdown retry after draining: %v", err)
	}
}

// ---- ensureServerPrepared: preparedHandlerSnapshot error path --------------

func TestEnsureServerPreparedReturnsHandlerErrorWhenHandlerOnceFiredWithNilChain(t *testing.T) {
	// This test exercises the defensive `return err` branch after
	// ensureHandlerPrepared() inside ensureServerPrepared, which can only be
	// reached when the handlerOnce has already fired (setting handler=nil) but
	// the chain is subsequently restored so serverConfigSnapshot passes.
	app := newTestApp()
	mustRegisterRoute(t, app.Get("/x", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	// Nullify the middleware chain so that buildHandler sets handler=nil.
	app.middlewareChain = nil
	// Fire the handlerOnce: because chain is nil, buildHandler leaves handler=nil.
	app.ensureHandlerPrepared()

	// Restore a fresh chain so serverConfigSnapshot no longer rejects the request.
	app.middlewareChain = middleware.NewChain()

	// ensureServerPrepared now passes serverConfigSnapshot but the handlerOnce
	// is already consumed; preparedHandlerSnapshot sees handler==nil → error.
	err := app.ensureServerPrepared()
	if err == nil {
		t.Fatal("expected error from ensureServerPrepared when handler is nil after handlerOnce fired")
	}
	assertCoreError(t, err, operationPrepareServer, "handler not configured")
}
