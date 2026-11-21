package requestid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
)

func TestMiddlewareAcceptsNilRequestHeader(t *testing.T) {
	for _, tc := range []struct {
		name   string
		opts   []Option
		header string
	}{
		{"default writeback", nil, "generated"},
		{"writeback disabled", []Option{WithRequestHeader(false)}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header = nil
			opts := append(tc.opts, WithGenerator(func() string { return "generated" }))
			calls := 0
			h := Middleware(opts...)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				calls++
				if got := r.Header.Get(contract.RequestIDHeader); got != tc.header || (r.Header == nil) != (tc.header == "") {
					t.Errorf("request header map=%v, want ID %q", r.Header, tc.header)
				}
				if id := contract.RequestIDFromContext(r.Context()); id != "generated" {
					t.Errorf("context ID=%q, want generated", id)
				}
			}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if id := w.Header().Get(contract.RequestIDHeader); id != "generated" || calls != 1 || req.Header != nil {
				t.Errorf("response ID=%q calls=%d original headers=%v", id, calls, req.Header)
			}
		})
	}
}

func TestMiddlewareUsesSameValidatedIDInHeadersContextAndJSON(t *testing.T) {
	oversized := strings.Repeat("x", 129)
	unicodeBoundary := strings.Repeat("é", 64)
	invalidUTF8 := "id\xff"
	for _, tc := range []struct {
		name, header, generated, want string
	}{
		{"valid header", strings.Repeat("a", 128), "generated", strings.Repeat("a", 128)},
		{"valid Unicode at byte limit", unicodeBoundary, "generated", unicodeBoundary},
		{"Unicode over byte limit", unicodeBoundary + "x", "generated", "generated"},
		{"header with invalid UTF-8", invalidUTF8, "generated", "generated"},
		{"generator with invalid UTF-8", "", invalidUTF8, ""},
		{"invalid header", oversized, "generated", "generated"},
		{"invalid custom ID", "", oversized, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var contextID, requestHeader string
			handler := Middleware(WithGenerator(func() string { return tc.generated }))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				contextID = contract.RequestIDFromContext(r.Context())
				requestHeader = r.Header.Get(contract.RequestIDHeader)
				if err := contract.WriteResponse(w, r, http.StatusOK, "ok", nil); err != nil {
					t.Fatal(err)
				}
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(contract.RequestIDHeader, tc.header)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			var body struct {
				ID string `json:"request_id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			header := w.Result().Header.Get(contract.RequestIDHeader)
			if header == "" || header == oversized || header != requestHeader || header != contextID || header != body.ID {
				t.Fatalf("response header=%q request header=%q context=%q JSON=%q", header, requestHeader, contextID, body.ID)
			}
			if tc.want != "" && header != tc.want {
				t.Fatalf("request ID=%q, want %q", header, tc.want)
			}
		})
	}
}

func TestAttachRequestIDIgnoresRejectedIDWithExistingContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(contract.WithRequestID(req.Context(), "existing"))
	w := httptest.NewRecorder()
	if got := AttachRequestID(w, req, strings.Repeat("x", 129), true); got != req ||
		w.Header().Get(contract.RequestIDHeader) != "" || req.Header.Get(contract.RequestIDHeader) != "" {
		t.Fatal("rejected ID changed the request or response headers")
	}
}

func TestMiddlewareUsesHeader(t *testing.T) {
	handler := Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := contract.RequestIDFromContext(r.Context()); got != "abc-123" {
			t.Fatalf("expected request id to be propagated, got %q", got)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(contract.RequestIDHeader, "abc-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(contract.RequestIDHeader); got != "abc-123" {
		t.Fatalf("expected response header to match, got %q", got)
	}
}

func TestMiddlewareGeneratesWhenMissing(t *testing.T) {
	handler := Middleware(WithGenerator(func() string { return "gen-1" }))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := contract.RequestIDFromContext(r.Context()); got != "gen-1" {
			t.Fatalf("expected generated request id, got %q", got)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(contract.RequestIDHeader); got != "gen-1" {
		t.Fatalf("expected generated header, got %q", got)
	}
}

func TestMiddlewareFallsBackWhenGeneratorReturnsEmpty(t *testing.T) {
	handler := Middleware(WithGenerator(func() string { return "" }))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := contract.RequestIDFromContext(r.Context()); got == "" {
			t.Fatal("expected fallback request id in context")
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(contract.RequestIDHeader); got == "" {
		t.Fatal("expected fallback request id header")
	}
}

func TestMiddlewareTrimsGeneratedRequestID(t *testing.T) {
	handler := Middleware(WithGenerator(func() string { return "  gen-2  " }))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := contract.RequestIDFromContext(r.Context()); got != "gen-2" {
			t.Fatalf("expected trimmed request id, got %q", got)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(contract.RequestIDHeader); got != "gen-2" {
		t.Fatalf("expected trimmed generated header, got %q", got)
	}
}

func TestAttachRequestIDSkipsBlankID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	got := AttachRequestID(rec, req, " \t ", true)
	if got != req {
		t.Fatal("expected blank request id to return original request")
	}
	if rec.Header().Get(contract.RequestIDHeader) != "" {
		t.Fatal("expected blank request id not to be written")
	}
}

func TestAttachRequestIDDetachesHeadersWhenWriting(t *testing.T) {
	for _, withHeaders := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header = nil
			if withHeaders {
				req.Header = http.Header{"X-Fixture": {"original"}}
			}
			out := AttachRequestID(nil, req, "test-id", enabled)
			if req.Header.Get(contract.RequestIDHeader) != "" ||
				(!withHeaders && req.Header != nil) || contract.RequestIDFromContext(out.Context()) != "test-id" {
				t.Fatalf("original=%v downstream=%v", req.Header, out.Header)
			}
			if enabled {
				if out.Header.Get(contract.RequestIDHeader) != "test-id" {
					t.Fatalf("downstream headers: %v", out.Header)
				}
				out.Header.Set("X-Fixture", "changed")
				if req.Header.Get("X-Fixture") == "changed" {
					t.Fatal("downstream header mutated original")
				}
			} else if out.Header.Get(contract.RequestIDHeader) != "" {
				t.Fatal("disabled downstream header was written")
			}
		}
	}
}

func TestInvalidGeneratedIDFallbackWithoutRequestHeaderWriteback(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Metadata", "fixture")
	var seen string
	calls := 0
	h := Middleware(WithRequestHeader(false), WithGenerator(func() string { calls++; return "bad\nfixture" }))(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			seen = contract.RequestIDFromContext(r.Context())
			if r.Header.Get(contract.RequestIDHeader) != "" || r.Header.Get("X-Metadata") != "fixture" {
				t.Errorf("headers=%v", r.Header)
			}
		}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if calls != 1 || seen == "" || w.Header().Get(contract.RequestIDHeader) != seen ||
		contract.RequestIDFromContext(req.Context()) != "" || req.Header.Get(contract.RequestIDHeader) != "" {
		t.Fatalf("calls=%d context=%q response=%v request=%v", calls, seen, w.Header(), req.Header)
	}
}

func TestRequestHeaderOptOutStillPropagatesGeneratedID(t *testing.T) {
	const id = "fixture-opt-out"
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	calls := 0
	h := Middleware(WithRequestHeader(false), WithGenerator(func() string {
		calls++
		return id
	}))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Header.Get(contract.RequestIDHeader) != "" || contract.RequestIDFromContext(r.Context()) != id {
			t.Fatalf("headers=%v context=%q", r.Header, contract.RequestIDFromContext(r.Context()))
		}
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if calls != 1 || req.Header.Get(contract.RequestIDHeader) != "" || w.Header().Get(contract.RequestIDHeader) != id {
		t.Fatalf("calls=%d request=%v response=%v", calls, req.Header, w.Header())
	}
}

func TestValidInboundRequestIDSkipsGeneratorWithWritebackDisabled(t *testing.T) {
	const id = "fixture-inbound"
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(contract.RequestIDHeader, id)
	calls := 0
	w := httptest.NewRecorder()
	h := Middleware(WithRequestHeader(false), WithGenerator(func() string { calls++; return "generated" }))(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if contract.RequestIDFromContext(r.Context()) != id || r.Header.Get(contract.RequestIDHeader) != id {
				t.Fatalf("headers=%v", r.Header)
			}
		}))
	h.ServeHTTP(w, req)
	if calls != 0 || w.Header().Get(contract.RequestIDHeader) != id || contract.RequestIDFromContext(req.Context()) != "" {
		t.Fatalf("generator=%d response=%v context=%v", calls, w.Header(), req.Context())
	}
}
