package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBufferedResponseRecorderDoesNotWriteThrough(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := NewBufferedResponseRecorder(underlying)

	rec.Header().Set("X-Test", "recorded")
	rec.WriteHeader(http.StatusAccepted)
	n, err := rec.Write([]byte("buffered"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != len("buffered") {
		t.Fatalf("expected %d bytes written, got %d", len("buffered"), n)
	}

	if rec.StatusCode() != http.StatusAccepted {
		t.Fatalf("expected recorded status %d, got %d", http.StatusAccepted, rec.StatusCode())
	}
	if string(rec.Body()) != "buffered" {
		t.Fatalf("expected recorded body %q, got %q", "buffered", string(rec.Body()))
	}
	if got := rec.Header().Get("X-Test"); got != "recorded" {
		t.Fatalf("expected recorded header, got %q", got)
	}

	if underlying.Body.Len() != 0 {
		t.Fatalf("expected no write-through body, got %q", underlying.Body.String())
	}
	if got := underlying.Header().Get("X-Test"); got != "" {
		t.Fatalf("expected no write-through header, got %q", got)
	}
}

func TestBufferedResponseRecorderDefaultsStatusOK(t *testing.T) {
	rec := NewBufferedResponseRecorder(nil)

	if rec.StatusCode() != http.StatusOK {
		t.Fatalf("expected default status %d, got %d", http.StatusOK, rec.StatusCode())
	}

	_, err := rec.Write([]byte("body"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if rec.StatusCode() != http.StatusOK {
		t.Fatalf("expected write to record status %d, got %d", http.StatusOK, rec.StatusCode())
	}
}

func TestBufferedResponseRecorderMaxBytesOverflow(t *testing.T) {
	rec := NewBufferedResponse(5)

	if _, err := rec.Write([]byte("hello")); err != nil {
		t.Fatalf("write within limit: %v", err)
	}
	if _, err := rec.Write([]byte("!")); err != http.ErrBodyNotAllowed {
		t.Fatalf("overflow error = %v, want %v", err, http.ErrBodyNotAllowed)
	}
	if !rec.Overflowed() {
		t.Fatal("expected recorder to report overflow")
	}
	if got := string(rec.Body()); got != "hello" {
		t.Fatalf("body = %q, want hello", got)
	}
}

func TestBufferedResponseRecorderWriteToReplacesHeaders(t *testing.T) {
	rec := NewBufferedResponse(0)
	rec.Header().Set("X-Buffered", "yes")
	rec.WriteHeader(http.StatusCreated)
	_, _ = rec.Write([]byte("ok"))

	dst := httptest.NewRecorder()
	dst.Header().Set("X-Stale", "remove")
	n, err := rec.WriteTo(dst)
	if err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if n != 2 {
		t.Fatalf("WriteTo bytes = %d, want 2", n)
	}
	if dst.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", dst.Code, http.StatusCreated)
	}
	if got := dst.Header().Get("X-Buffered"); got != "yes" {
		t.Fatalf("X-Buffered = %q, want yes", got)
	}
	if got := dst.Header().Get("X-Stale"); got != "" {
		t.Fatalf("X-Stale = %q, want removed", got)
	}
	if got := dst.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want ok", got)
	}
}

func TestBufferedResponseRecorderIgnoresInformationalStatus(t *testing.T) {
	for _, final := range []int{http.StatusCreated, http.StatusSwitchingProtocols} {
		rec := NewBufferedResponse(0)
		rec.WriteHeader(http.StatusContinue)
		rec.WriteHeader(http.StatusProcessing)
		rec.WriteHeader(http.StatusEarlyHints)
		rec.WriteHeader(final)
		rec.WriteHeader(http.StatusAccepted)
		if rec.StatusCode() != final {
			t.Fatalf("status = %d, want %d", rec.StatusCode(), final)
		}
	}
	rec := NewBufferedResponse(0)
	rec.WriteHeader(http.StatusEarlyHints)
	_, _ = rec.Write([]byte("body"))
	if rec.StatusCode() != http.StatusOK {
		t.Fatalf("implicit status = %d, want 200", rec.StatusCode())
	}
}
