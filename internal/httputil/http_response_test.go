package httputil

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

type hijackErrorWriter struct {
	flushUnwrapper
	err error
}

func (w hijackErrorWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, w.err
}

func TestHijackDelegationPreservesErrors(t *testing.T) {
	dst := flushUnwrapper{httptest.NewRecorder()}
	hijackErr := errors.New("connection already taken")
	// An explicit Hijack method takes precedence over Unwrap.
	writer := hijackErrorWriter{flushUnwrapper: dst, err: hijackErr}
	if _, _, err := HijackIfSupported(flushUnwrapper{writer}); err != hijackErr {
		t.Fatalf("Hijack error = %v, want %v", err, hijackErr)
	}
	if _, _, err := NewObservationRecorder(dst).Hijack(); err != http.ErrNotSupported {
		t.Fatalf("Hijack error = %v, want the ErrNotSupported sentinel", err)
	}
}

type flushUnwrapper struct {
	http.ResponseWriter
}

func (w flushUnwrapper) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type flushErrorWriter struct {
	*httptest.ResponseRecorder
	err error
}

func (w flushErrorWriter) FlushError() error {
	w.Flush()
	return w.err
}

func TestResponseWriterFlushDelegation(t *testing.T) {
	for _, wrapper := range []struct {
		name string
		wrap func(http.ResponseWriter) http.ResponseWriter
	}{
		{"observation", func(w http.ResponseWriter) http.ResponseWriter { return NewObservationRecorder(w) }},
		{"base", func(w http.ResponseWriter) http.ResponseWriter { return &BaseWrappedResponseWriter{ResponseWriter: w} }},
	} {
		t.Run(wrapper.name, func(t *testing.T) {
			t.Run("unsupported", func(t *testing.T) {
				rec := httptest.NewRecorder()
				w := wrapper.wrap(struct{ http.ResponseWriter }{rec})
				if err := http.NewResponseController(w).Flush(); !errors.Is(err, http.ErrNotSupported) {
					t.Fatalf("Flush() = %v, want ErrNotSupported", err)
				}
				if got := rec.Header().Get(HeaderContentTypeNoSniff); got != "" {
					t.Fatalf("unsupported Flush changed headers: %q", got)
				}
				w.WriteHeader(http.StatusAccepted)
				if rec.Code != http.StatusAccepted {
					t.Fatalf("status = %d, want 202", rec.Code)
				}
			})
			t.Run("wrapped error", func(t *testing.T) {
				rec := httptest.NewRecorder()
				flushErr := errors.New("flush failed")
				w := wrapper.wrap(flushUnwrapper{flushErrorWriter{rec, flushErr}})
				if err := http.NewResponseController(w).Flush(); !errors.Is(err, flushErr) {
					t.Fatalf("Flush() = %v, want %v", err, flushErr)
				}
				w.WriteHeader(http.StatusAccepted)
				if !rec.Flushed || rec.Code != http.StatusOK {
					t.Fatalf("flushed = %t, status = %d, want true and 200", rec.Flushed, rec.Code)
				}
			})
		})
	}
}

func TestEnsureNoSniff(t *testing.T) {
	rec := httptest.NewRecorder()
	EnsureNoSniff(rec.Header())
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected nosniff, got %q", got)
	}
}

func TestEnsureNoSniffPreservesExistingValue(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("X-Content-Type-Options", "custom")
	EnsureNoSniff(rec.Header())
	if got := rec.Header().Get("X-Content-Type-Options"); got != "custom" {
		t.Fatalf("expected existing value to be preserved, got %q", got)
	}
}

func TestSafeWriteSetsNoSniffAndWritesBody(t *testing.T) {
	rec := httptest.NewRecorder()
	n, err := SafeWrite(rec, []byte("ok"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 bytes written, got %d", n)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected nosniff, got %q", got)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("expected body ok, got %q", got)
	}
}

func TestResponseRecorderWriteAndStatus(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := NewResponseRecorder(underlying)
	rec.Header().Set("X-Test", "1")

	rec.WriteHeader(http.StatusCreated)
	n, err := rec.Write([]byte("body"))
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if n != 4 {
		t.Fatalf("expected 4 bytes written, got %d", n)
	}
	if rec.StatusCode() != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.StatusCode())
	}
	if rec.BytesWritten() != 4 {
		t.Fatalf("expected bytes written 4, got %d", rec.BytesWritten())
	}
	if got := string(rec.Body()); got != "body" {
		t.Fatalf("expected captured body, got %q", got)
	}
	if got := underlying.Header().Get("X-Test"); got != "1" {
		t.Fatalf("expected forwarded header, got %q", got)
	}
	if got := underlying.Header().Get(HeaderContentTypeNoSniff); got != ContentTypeNoSniffValue {
		t.Fatalf("expected nosniff, got %q", got)
	}
}

func TestResponseRecorderDefaultStatusAndRepeatedWriteHeader(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := NewResponseRecorder(underlying)

	if rec.StatusCode() != http.StatusOK {
		t.Fatalf("expected default status 200, got %d", rec.StatusCode())
	}

	rec.WriteHeader(http.StatusAccepted)
	rec.WriteHeader(http.StatusTeapot)
	if rec.StatusCode() != http.StatusAccepted {
		t.Fatalf("expected first status to win, got %d", rec.StatusCode())
	}
	if underlying.Code != http.StatusAccepted {
		t.Fatalf("expected underlying status 202, got %d", underlying.Code)
	}
}

func TestResponseRecorderImplicitWriteHeader(t *testing.T) {
	underlying := httptest.NewRecorder()
	rec := NewResponseRecorder(underlying)

	n, err := rec.Write([]byte("x"))
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 byte written, got %d", n)
	}
	if rec.StatusCode() != http.StatusOK {
		t.Fatalf("expected implicit status 200, got %d", rec.StatusCode())
	}
	if underlying.Code != http.StatusOK {
		t.Fatalf("expected underlying status 200, got %d", underlying.Code)
	}
}

func TestCopyHeadersOverlaysExistingValues(t *testing.T) {
	dst := http.Header{}
	dst.Add("X-Test", "stale")
	src := http.Header{}
	src.Add("X-Test", "fresh")
	src.Add("Set-Cookie", "a=1")
	src.Add("Set-Cookie", "b=2")

	CopyHeaders(dst, src)

	if got := dst.Values("X-Test"); len(got) != 1 || got[0] != "fresh" {
		t.Fatalf("X-Test values = %v, want [fresh]", got)
	}
	if got := dst.Values("Set-Cookie"); len(got) != 2 || got[0] != "a=1" || got[1] != "b=2" {
		t.Fatalf("Set-Cookie values = %v, want [a=1 b=2]", got)
	}
}
