package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDContextRoundTrip(t *testing.T) {
	ctx := WithRequestID(t.Context(), "req-123")
	if got := RequestIDFromContext(ctx); got != "req-123" {
		t.Fatalf("expected request id %q, got %q", "req-123", got)
	}
}

func TestRequestIDTrimsWhitespace(t *testing.T) {
	ctx := WithRequestID(t.Context(), "  req-123  ")
	if got := RequestIDFromContext(ctx); got != "req-123" {
		t.Fatalf("expected trimmed request id %q, got %q", "req-123", got)
	}
}

func TestRequestIDRejectsControlCharacters(t *testing.T) {
	tests := []string{
		"req\n123",
		"req\t123",
		"req\x00123",
		"req\x7f123",
	}

	for _, id := range tests {
		t.Run(id, func(t *testing.T) {
			ctx := WithRequestID(t.Context(), id)
			if got := RequestIDFromContext(ctx); got != "" {
				t.Fatalf("expected unsafe request id to be ignored, got %q", got)
			}
		})
	}
}

func TestRequestIDAllowsVisiblePunctuation(t *testing.T) {
	id := "req-123_ABC.:/+="
	ctx := WithRequestID(t.Context(), id)
	if got := RequestIDFromContext(ctx); got != id {
		t.Fatalf("expected request id %q, got %q", id, got)
	}
}

func TestRequestIDLengthBoundary(t *testing.T) {
	accepted := strings.Repeat("a", maxRequestIDLength)
	if got := RequestIDFromContext(WithRequestID(t.Context(), accepted)); got != accepted {
		t.Fatalf("expected max-length request id to be accepted, got %q", got)
	}

	rejected := strings.Repeat("a", maxRequestIDLength+1)
	if got := RequestIDFromContext(WithRequestID(t.Context(), rejected)); got != "" {
		t.Fatalf("expected oversized request id to be ignored, got %q", got)
	}
}

func TestRequestIDRejectedReplacementPreservesExistingID(t *testing.T) {
	const original = "req-original"
	ctx := WithRequestID(t.Context(), original)
	for _, replacement := range []string{"", "   ", strings.Repeat("a", maxRequestIDLength+1)} {
		if got := RequestIDFromContext(WithRequestID(ctx, replacement)); got != original {
			t.Errorf("request id after rejected replacement = %q, want %q", got, original)
		}
	}
}

func TestRequestIDWithNilContext(t *testing.T) {
	if got := RequestIDFromContext(WithRequestID(nil, "  req-123  ")); got != "req-123" {
		t.Fatalf("request id with nil parent = %q, want req-123", got)
	}
}

func TestOversizedRequestIDIsNotEchoed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithRequestID(req.Context(), strings.Repeat("a", maxRequestIDLength+1)))

	rec := httptest.NewRecorder()
	if err := WriteResponse(rec, req, http.StatusOK, map[string]string{"ok": "true"}, nil); err != nil {
		t.Fatalf("unexpected write response error: %v", err)
	}
	if strings.Contains(rec.Body.String(), strings.Repeat("a", maxRequestIDLength+1)) {
		t.Fatalf("expected oversized request id not to be echoed, got %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	if err := WriteError(rec, req, NewErrorBuilder().Type(TypeInternal).Message("boom").RequestID(strings.Repeat("b", maxRequestIDLength+1)).Build()); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if strings.Contains(rec.Body.String(), strings.Repeat("b", maxRequestIDLength+1)) {
		t.Fatalf("expected oversized error request id not to be echoed, got %s", rec.Body.String())
	}
}

func TestRequestIDMultibyteBoundaryIsUsedByResponse(t *testing.T) {
	base := WithRequestID(t.Context(), "previous")
	for _, size := range []int{63, 64, 65} {
		id := strings.Repeat("é", size)
		want := id
		if size == 65 {
			want = "previous"
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(WithRequestID(base, id))
		w := httptest.NewRecorder()
		if err := WriteResponse(w, req, http.StatusOK, nil, nil); err != nil {
			t.Fatal(err)
		}
		var decoded response
		if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil || decoded.RequestID != want {
			t.Fatalf("length=%d request ID=%q error=%v", len(id), decoded.RequestID, err)
		}
	}
}

func TestRequestIDByteLimitTrimsBeforeChoosingInheritedValue(t *testing.T) {
	parent := WithRequestID(t.Context(), "parent")
	for _, size := range []int{127, 128, 129} {
		value := strings.Repeat("é", size/2) + strings.Repeat("a", size%2)
		want := value
		if size > 128 {
			want = "parent"
		}
		req := httptest.NewRequest("GET", "/", nil)
		req = req.WithContext(WithRequestID(parent, " "+value+" "))
		w := httptest.NewRecorder()
		if err := WriteResponse(w, req, 200, nil, nil); err != nil {
			t.Fatal(err)
		}
		var body response
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.RequestID != want {
			t.Fatalf("size=%d id=%q err=%v", size, body.RequestID, err)
		}
	}
}
