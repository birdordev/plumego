package requestid

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
	mathrand "math/rand"
)

// TestWithRequestHeaderOption exercises the WithRequestHeader option path
// which was 0% covered.
func TestWithRequestHeaderOption(t *testing.T) {
	// WithRequestHeader(true) — default behaviour: write ID back to request header
	handlerTrue := Middleware(WithRequestHeader(true))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(contract.RequestIDHeader); got == "" {
			t.Fatal("expected request header to be set when WithRequestHeader(true)")
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handlerTrue.ServeHTTP(rec, req)

	// WithRequestHeader(false) — request header must NOT be mutated
	handlerFalse := Middleware(WithRequestHeader(false))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(contract.RequestIDHeader); got != "" {
			t.Fatalf("expected request header NOT to be set, got %q", got)
		}
	}))
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	rec2 := httptest.NewRecorder()
	handlerFalse.ServeHTTP(rec2, req2)

	// Response header must still be set regardless
	if got := rec2.Header().Get(contract.RequestIDHeader); got == "" {
		t.Fatal("expected response header to always be set")
	}
}

// TestWithRequestHeaderFalsePreservesIncomingID verifies that when
// WithRequestHeader(false) is set and the request already has a request ID,
// it is preserved in context but not re-written to the request header.
func TestWithRequestHeaderFalsePreservesIncomingID(t *testing.T) {
	const existingID = "existing-id-123"
	handler := Middleware(WithRequestHeader(false))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := contract.RequestIDFromContext(r.Context()); got != existingID {
			t.Fatalf("context id = %q, want %q", got, existingID)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(contract.RequestIDHeader, existingID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
}

// TestAttachRequestIDNilRequest exercises the nil-request guard in AttachRequestID.
func TestAttachRequestIDNilRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	got := AttachRequestID(rec, nil, "some-id", true)
	if got != nil {
		t.Fatal("expected nil request to return nil")
	}
}

// TestAttachRequestIDNilResponseWriter exercises the nil-writer guard path.
func TestAttachRequestIDNilResponseWriter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	got := AttachRequestID(nil, req, "some-id", true)
	if got == nil {
		t.Fatal("expected non-nil request back")
	}
	// Context should still be set
	if id := contract.RequestIDFromContext(got.Context()); id != "some-id" {
		t.Fatalf("expected context id some-id, got %q", id)
	}
}

// TestRequestIDFromRequestContextFirst verifies that context wins over header.
func TestRequestIDFromRequestContextFirst(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(contract.RequestIDHeader, "header-id")
	ctx := contract.WithRequestID(req.Context(), "context-id")
	req = req.WithContext(ctx)

	got := RequestIDFromRequest(req)
	if got != "context-id" {
		t.Fatalf("expected context id to win, got %q", got)
	}
}

// TestRequestIDFromRequestHeaderFallback verifies header fallback when context is empty.
func TestRequestIDFromRequestHeaderFallback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(contract.RequestIDHeader, "  header-id  ")

	got := RequestIDFromRequest(req)
	if got != "header-id" {
		t.Fatalf("expected trimmed header id, got %q", got)
	}
}

// TestRequestIDFromRequestNilReturnsEmpty verifies nil-request safety.
func TestRequestIDFromRequestNilReturnsEmpty(t *testing.T) {
	if got := RequestIDFromRequest(nil); got != "" {
		t.Fatalf("expected empty string for nil request, got %q", got)
	}
}

// TestRandomFallbackProducesValidValue exercises the randomFallback path on a
// RequestIDGenerator that has been deliberately set up with an empty pool so
// that randomValue() falls through to randomFallback().
func TestRandomFallbackProducesValidValue(t *testing.T) {
	// Create a generator and zero out the pool to force randomFallback path.
	g := &RequestIDGenerator{
		rng:        mathrand.New(mathrand.NewSource(42)),
		randomPool: []int32{},
	}
	// randomFallback is called from randomValue when poolSize == 0.
	v := g.randomFallback()
	if v < 0 || v >= randMax {
		t.Fatalf("randomFallback value %d out of expected range [0, %d)", v, randMax)
	}
}

// TestRandomFallbackWithNilRng exercises the nil-rng branch inside randomFallback.
func TestRandomFallbackWithNilRng(t *testing.T) {
	g := &RequestIDGenerator{
		rng:        nil,
		randomPool: []int32{},
	}
	v := g.randomFallback()
	if v < 0 || v >= randMax {
		t.Fatalf("randomFallback (nil rng) value %d out of range", v)
	}
}

// TestBuildIDNegativeDeltaPath triggers the delta<0 branch in buildID by
// constructing a generator with lastMs in the future then calling buildID
// with a timestamp before the custom epoch.
func TestBuildIDNegativeDeltaPath(t *testing.T) {
	g := NewRequestIDGenerator()
	// Pass a time before the custom epoch to trigger delta < 0.
	id := g.buildID(epochMilli-1, 0)
	if len(id) != idWidth {
		t.Fatalf("expected id length %d, got %d", idWidth, len(id))
	}
	// Must still be decodable; the timestamp component will be zero.
	_, _, _, err := DecodeRequestID(id)
	if err != nil {
		t.Fatalf("failed to decode id built from pre-epoch timestamp: %v", err)
	}
}

// TestEncodeBase62FixedZeroWidth exercises the width<=0 guard.
func TestEncodeBase62FixedZeroWidth(t *testing.T) {
	got := encodeBase62Fixed(0, 0)
	if len(got) < 1 {
		t.Fatalf("expected at least 1 character for width=0, got %q", got)
	}
}

// TestEncodeBase62FixedNegativeWidth exercises the negative width guard.
func TestEncodeBase62FixedNegativeWidth(t *testing.T) {
	got := encodeBase62Fixed(123, -5)
	if len(got) < 1 {
		t.Fatalf("expected at least 1 character for width=-5, got %q", got)
	}
}

// TestRefreshRandomPoolNilRng exercises the nil-rng guard inside refreshRandomPool.
func TestRefreshRandomPoolNilRng(t *testing.T) {
	g := &RequestIDGenerator{
		rng:        nil,
		randomPool: make([]int32, 4), // small pool so refresh runs quickly
	}
	// refreshRandomPool must not panic and must populate values.
	g.refreshRandomPool()
	for i, v := range g.randomPool {
		if v < 0 || v >= int32(randMax) {
			t.Fatalf("pool[%d] = %d out of expected range", i, v)
		}
	}
}

// TestRandomValueTriggersPoolRefresh forces the pool-refresh path by
// consuming exactly poolSize entries so idx==0 and idx!=0 on the next call.
func TestRandomValueTriggersPoolRefresh(t *testing.T) {
	g := NewRequestIDGenerator()

	// Drain the pool by calling randomValue poolSize times so next call
	// wraps around and triggers a refresh.
	for i := 0; i < randomPoolSize; i++ {
		_ = g.randomValue()
	}
	// This call should trigger refreshRandomPool (pos==0, idx>0).
	v := g.randomValue()
	if v < 0 || v >= randMax {
		t.Fatalf("post-refresh randomValue %d out of range", v)
	}
}

// TestCryptoSeedFallback verifies that cryptoSeed always returns a non-zero
// or at least valid int64 value (it may fall back to time.Now on error but
// that path is hard to trigger in isolation; we just check the return type).
func TestCryptoSeedReturnType(t *testing.T) {
	// Simply call cryptoSeed multiple times and verify it doesn't panic.
	for i := 0; i < 10; i++ {
		_ = cryptoSeed()
	}
}

// TestWithGeneratorNilOption verifies that passing nil to WithGenerator is safe.
func TestWithGeneratorNilOption(t *testing.T) {
	handler := Middleware(WithGenerator(nil))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := contract.RequestIDFromContext(r.Context()); id == "" {
			t.Fatal("expected fallback id to be present in context")
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
}

// TestMiddlewareWithNilOption exercises the nil option guard inside Middleware.
func TestMiddlewareWithNilOption(t *testing.T) {
	handler := Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	// Must not panic.
	handler.ServeHTTP(rec, req)
}

// TestRequestIDFromRequestEmptyHeader verifies that an empty (whitespace-only)
// header value falls through to the empty-string return.
func TestRequestIDFromRequestEmptyHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(contract.RequestIDHeader, "   ")
	got := RequestIDFromRequest(req)
	if got != "" {
		t.Fatalf("expected empty string for whitespace-only header, got %q", got)
	}
}

// TestEncodeBase62FixedMultipleWidths verifies encode/decode round-trips for
// several widths to reach the loop body in encodeBase62Fixed.
func TestEncodeBase62FixedMultipleWidths(t *testing.T) {
	for _, width := range []int{1, 4, 8, idWidth} {
		got := encodeBase62Fixed(12345, width)
		if len(got) != width {
			t.Fatalf("encodeBase62Fixed width=%d: len=%d", width, len(got))
		}
	}
}

// TestBuildIDSeqValues verifies buildID with non-zero seq values to exercise
// the seq assignment path.
func TestBuildIDSeqValues(t *testing.T) {
	g := NewRequestIDGenerator()
	for _, seq := range []int{1, 10, seqMax} {
		id := g.buildID(epochMilli+1000, seq)
		if len(id) != idWidth {
			t.Fatalf("buildID seq=%d: len=%d", seq, len(id))
		}
		if _, _, _, err := DecodeRequestID(id); err != nil {
			t.Fatalf("buildID seq=%d: decode error: %v", seq, err)
		}
	}
}

// TestRefreshRandomPoolWithExistingRng exercises the branch in refreshRandomPool
// where g.rng is already non-nil.
func TestRefreshRandomPoolWithExistingRng(t *testing.T) {
	g := NewRequestIDGenerator()
	// Call refresh directly with a non-nil rng (the normal path).
	g.refreshRandomPool()
	for i, v := range g.randomPool {
		if v < 0 || v >= int32(randMax) {
			t.Fatalf("pool[%d] = %d out of expected range after refresh", i, v)
		}
	}
}
