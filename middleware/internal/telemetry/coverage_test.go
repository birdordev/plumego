package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"
)

// ---------------------------------------------------------------------------
// helpers.go — FinishPreservingPanic
// ---------------------------------------------------------------------------

func TestFinishPreservingPanic_NoActivePanic_RunsFinalizer(t *testing.T) {
	ran := false
	// FinishPreservingPanic is meant to be called via defer; we invoke it
	// directly here while there is no active panic.
	func() {
		defer FinishPreservingPanic(func() { ran = true })
	}()
	if !ran {
		t.Fatal("expected finalizer to run when there is no active panic")
	}
}

func TestFinishPreservingPanic_NilFinalizer_NoActivePanic(t *testing.T) {
	// Passing nil must not panic and must not crash.
	func() {
		defer FinishPreservingPanic(nil)
	}()
}

func TestFinishPreservingPanic_ActivePanic_RepanicAfterFinalizer(t *testing.T) {
	sentinel := "upstream panic"
	finalizerRan := false

	func() {
		defer func() {
			// We expect the panic to have been re-raised by FinishPreservingPanic.
			v := recover()
			if v != sentinel {
				t.Errorf("expected re-panic with %q, got %v", sentinel, v)
			}
			if !finalizerRan {
				t.Error("expected finalizer to have run before re-panic")
			}
		}()
		defer FinishPreservingPanic(func() { finalizerRan = true })
		panic(sentinel)
	}()
}

func TestFinishPreservingPanic_ActivePanic_NilFinalizer(t *testing.T) {
	sentinel := "upstream panic nil finalizer"

	func() {
		defer func() {
			v := recover()
			if v != sentinel {
				t.Errorf("expected re-panic with %q, got %v", sentinel, v)
			}
		}()
		defer FinishPreservingPanic(nil)
		panic(sentinel)
	}()
}

// ---------------------------------------------------------------------------
// helpers.go — RunSafeFinalizer
// ---------------------------------------------------------------------------

func TestRunSafeFinalizer_NilFinalizer(t *testing.T) {
	// Must return without panic.
	RunSafeFinalizer(nil)
}

func TestRunSafeFinalizer_NormalExecution(t *testing.T) {
	ran := false
	RunSafeFinalizer(func() { ran = true })
	if !ran {
		t.Fatal("expected finalizer to run")
	}
}

func TestRunSafeFinalizer_PanicingFinalizer_Recovered(t *testing.T) {
	// A panic inside the finalizer must be absorbed; the function must return
	// normally to the caller.
	RunSafeFinalizer(func() { panic("finalizer panic") })
	// If we reach here the panic was recovered successfully.
}

// ---------------------------------------------------------------------------
// helpers.go — EndTrace
// ---------------------------------------------------------------------------

func TestEndTrace_NilSpan_NoOp(t *testing.T) {
	// Must not panic when span is nil.
	EndTrace(nil, RequestMetrics{Status: 200})
}

func TestEndTrace_NonNilSpan_CallsEnd(t *testing.T) {
	span := &stubSpan{}
	metrics := RequestMetrics{
		Status:    201,
		Bytes:     42,
		RequestID: "req-123",
	}
	EndTrace(span, metrics)
	if !span.ended {
		t.Fatal("expected span.End to be called")
	}
	if span.status != 201 {
		t.Fatalf("status = %d, want 201", span.status)
	}
	if span.bytes != 42 {
		t.Fatalf("bytes = %d, want 42", span.bytes)
	}
	if span.requestID != "req-123" {
		t.Fatalf("requestID = %q, want %q", span.requestID, "req-123")
	}
}

// ---------------------------------------------------------------------------
// helpers.go — ObservedPath
// ---------------------------------------------------------------------------

func TestObservedPath_RouteSet_ReturnsRoute(t *testing.T) {
	m := RequestMetrics{Route: "/users/:id", Path: "/users/42"}
	if got := m.ObservedPath(); got != "/users/:id" {
		t.Fatalf("ObservedPath() = %q, want %q", got, "/users/:id")
	}
}

func TestObservedPath_NoRoute_ReturnsFallbackPath(t *testing.T) {
	m := RequestMetrics{Path: "/users/42"}
	if got := m.ObservedPath(); got != "/users/42" {
		t.Fatalf("ObservedPath() = %q, want %q", got, "/users/42")
	}
}

func TestObservedPath_BothEmpty_ReturnsEmpty(t *testing.T) {
	m := RequestMetrics{}
	if got := m.ObservedPath(); got != "" {
		t.Fatalf("ObservedPath() = %q, want empty string", got)
	}
}

// ---------------------------------------------------------------------------
// policy.go — MiddlewareLogFields
// ---------------------------------------------------------------------------

func TestMiddlewareLogFields_NilRequest_ReturnsDefaultFields(t *testing.T) {
	fields := MiddlewareLogFields(nil, 500, 100*time.Millisecond)
	if fields["method"] != "" {
		t.Fatalf("method = %v, want empty string", fields["method"])
	}
	if fields["path"] != "" {
		t.Fatalf("path = %v, want empty string", fields["path"])
	}
	if fields["status"] != 500 {
		t.Fatalf("status = %v, want 500", fields["status"])
	}
	if fields["request_id"] != "" {
		t.Fatalf("request_id = %v, want empty string", fields["request_id"])
	}
	if fields["duration"] == nil {
		t.Fatal("expected duration key to be present")
	}
}

func TestMiddlewareLogFields_RealRequest_PopulatesFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", nil)
	dur := 250 * time.Millisecond
	fields := MiddlewareLogFields(req, 201, dur)

	if fields["method"] != http.MethodPost {
		t.Fatalf("method = %v, want POST", fields["method"])
	}
	if fields["path"] != "/api/v1/users" {
		t.Fatalf("path = %v, want /api/v1/users", fields["path"])
	}
	if fields["status"] != 201 {
		t.Fatalf("status = %v, want 201", fields["status"])
	}
	if fields["duration"] != dur.String() {
		t.Fatalf("duration = %v, want %s", fields["duration"], dur.String())
	}
	// request_id may be empty for a fresh request without middleware — that's fine.
	if _, ok := fields["request_id"]; !ok {
		t.Fatal("expected request_id key to be present")
	}
}

func TestMiddlewareLogFields_AllExpectedKeysPresent(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	fields := MiddlewareLogFields(req, 200, time.Second)

	requiredKeys := []string{"method", "path", "status", "duration", "request_id"}
	for _, k := range requiredKeys {
		if _, ok := fields[k]; !ok {
			t.Errorf("missing expected key %q in log fields", k)
		}
	}
}

// ---------------------------------------------------------------------------
// policy.go — redactValue
// ---------------------------------------------------------------------------

func TestRedactValue_NonSensitiveString_PassThrough(t *testing.T) {
	sensitiveKeys := map[string]struct{}{"password": {}}
	result := redactValue("hello world", sensitiveKeys)
	if result != "hello world" {
		t.Fatalf("redactValue(%q) = %v, want unchanged", "hello world", result)
	}
}

func TestRedactValue_Integer_PassThrough(t *testing.T) {
	sensitiveKeys := map[string]struct{}{"password": {}}
	result := redactValue(42, sensitiveKeys)
	if result != 42 {
		t.Fatalf("redactValue(42) = %v, want 42", result)
	}
}

func TestRedactValue_NestedMap_SensitiveKeyRedacted(t *testing.T) {
	sensitiveKeys := map[string]struct{}{"token": {}}
	input := map[string]any{
		"user":  "alice",
		"token": "supersecret",
	}
	result := redactValue(input, sensitiveKeys)
	out, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if out["token"] != sensitiveFieldMask {
		t.Fatalf("token = %v, want %q", out["token"], sensitiveFieldMask)
	}
	if out["user"] != "alice" {
		t.Fatalf("user = %v, want %q", out["user"], "alice")
	}
}

func TestRedactValue_NestedMap_NonSensitiveKeyPassThrough(t *testing.T) {
	sensitiveKeys := map[string]struct{}{"secret": {}}
	input := map[string]any{
		"name": "bob",
	}
	result := redactValue(input, sensitiveKeys)
	out, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if out["name"] != "bob" {
		t.Fatalf("name = %v, want %q", out["name"], "bob")
	}
}

func TestRedactValue_SliceOfStrings_PassThrough(t *testing.T) {
	sensitiveKeys := map[string]struct{}{"password": {}}
	input := []any{"a", "b", "c"}
	result := redactValue(input, sensitiveKeys)
	items, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(items) != 3 || items[0] != "a" || items[1] != "b" || items[2] != "c" {
		t.Fatalf("unexpected slice result: %v", items)
	}
}

func TestRedactValue_SliceContainingMap_SensitiveKeyRedacted(t *testing.T) {
	sensitiveKeys := map[string]struct{}{"password": {}}
	input := []any{
		map[string]any{"password": "hunter2", "name": "alice"},
	}
	result := redactValue(input, sensitiveKeys)
	items, ok := result.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("unexpected result: %v", result)
	}
	m, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any inside slice, got %T", items[0])
	}
	if m["password"] != sensitiveFieldMask {
		t.Fatalf("password = %v, want %q", m["password"], sensitiveFieldMask)
	}
	if m["name"] != "alice" {
		t.Fatalf("name = %v, want %q", m["name"], "alice")
	}
}

// ---------------------------------------------------------------------------
// policy.go — isSensitiveKey
// ---------------------------------------------------------------------------

func TestIsSensitiveKey_EmptyKey_ReturnsFalse(t *testing.T) {
	keys := map[string]struct{}{"password": {}}
	if isSensitiveKey("", keys) {
		t.Fatal("empty key should not be sensitive")
	}
}

func TestIsSensitiveKey_WhitespaceOnlyKey_ReturnsFalse(t *testing.T) {
	keys := map[string]struct{}{"password": {}}
	if isSensitiveKey("   ", keys) {
		t.Fatal("whitespace-only key should not be sensitive")
	}
}

func TestIsSensitiveKey_ExactMatch_ReturnsTrue(t *testing.T) {
	keys := map[string]struct{}{"token": {}}
	if !isSensitiveKey("token", keys) {
		t.Fatal("exact key match should return true")
	}
}

func TestIsSensitiveKey_CaseInsensitiveSubstring_ReturnsTrue(t *testing.T) {
	keys := map[string]struct{}{"password": {}}
	if !isSensitiveKey("User_Password", keys) {
		t.Fatal("case-insensitive substring match should return true")
	}
}

func TestIsSensitiveKey_PrefixMatch_ReturnsTrue(t *testing.T) {
	keys := map[string]struct{}{"token": {}}
	if !isSensitiveKey("access_token", keys) {
		t.Fatal("key containing sensitive substring should return true")
	}
}

func TestIsSensitiveKey_NoMatch_ReturnsFalse(t *testing.T) {
	keys := map[string]struct{}{"password": {}, "token": {}}
	if isSensitiveKey("username", keys) {
		t.Fatal("unrelated key should not be sensitive")
	}
}

func TestIsSensitiveKey_EmptySensitiveSet_ReturnsFalse(t *testing.T) {
	keys := map[string]struct{}{}
	if isSensitiveKey("password", keys) {
		t.Fatal("no sensitive keys defined — should return false")
	}
}

func TestIsSensitiveKey_UpperCaseKey_NormalizedAndMatches(t *testing.T) {
	keys := map[string]struct{}{"secret": {}}
	if !isSensitiveKey("MY_SECRET_KEY", keys) {
		t.Fatal("upper-case key containing 'secret' should match")
	}
}

// ---------------------------------------------------------------------------
// policy.go — RedactFields edge cases not covered by existing tests
// ---------------------------------------------------------------------------

func TestRedactFields_NilMap_ReturnsNil(t *testing.T) {
	result := RedactFields(nil)
	if result != nil {
		t.Fatalf("RedactFields(nil) = %v, want nil", result)
	}
}

func TestRedactFields_DefaultSensitiveKeys_AllRedacted(t *testing.T) {
	fields := map[string]any{
		"token":     "tok-abc",
		"secret":    "s3cr3t",
		"signature": "sig-xyz",
		"password":  "hunter2",
		"safe":      "visible",
	}
	out := RedactFields(fields)
	for _, k := range []string{"token", "secret", "signature", "password"} {
		if out[k] != sensitiveFieldMask {
			t.Errorf("key %q = %v, want %q", k, out[k], sensitiveFieldMask)
		}
	}
	if out["safe"] != "visible" {
		t.Errorf("safe = %v, want %q", out["safe"], "visible")
	}
}

func TestRedactFields_ExtraSensitiveKeyWithWhitespace_Ignored(t *testing.T) {
	// An extra key that is blank after trimming must not cause a crash.
	fields := map[string]any{"name": "alice"}
	out := RedactFields(fields, "   ", "")
	if out["name"] != "alice" {
		t.Fatalf("name = %v, want %q", out["name"], "alice")
	}
}

func TestRedactFields_ExtraSensitiveKey_CaseInsensitiveMatch(t *testing.T) {
	fields := map[string]any{"API_KEY": "my-api-key"}
	out := RedactFields(fields, "api_key")
	if out["API_KEY"] != sensitiveFieldMask {
		t.Fatalf("API_KEY = %v, want %q", out["API_KEY"], sensitiveFieldMask)
	}
}

func TestObserverCompletesBeforeReturningAndIsolatesPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := httptest.NewRecorder()
		observing, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
		go func() {
			defer close(returned)
			h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			})
			WithObserver(h, w, httptest.NewRequest(http.MethodGet, "/", nil), func(metrics RequestMetrics, _ *ResponseRecorder, _ *http.Request) {
				if metrics.Status != http.StatusAccepted {
					t.Errorf("observed status = %d", metrics.Status)
				}
				close(observing)
				<-release
				panic("observer fixture")
			})
		}()
		<-observing
		synctest.Wait()
		select {
		case <-returned:
			t.Error("returned before synchronous observer finished")
		default:
		}
		close(release)
		<-returned
		if w.Code != http.StatusAccepted || w.Body.Len() != 0 {
			t.Fatalf("status=%d body=%q", w.Code, w.Body)
		}
	})
}
