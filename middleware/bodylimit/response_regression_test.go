package bodylimit

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOverrunStopsFlushAndHijack(t *testing.T) {
	for _, operation := range []string{"flush", "hijack"} {
		t.Run(operation, func(t *testing.T) {
			handler := Middleware(Config{MaxBytes: 2})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				if operation == "flush" {
					w.(http.Flusher).Flush()
				} else {
					if _, _, err := w.(http.Hijacker).Hijack(); !errors.Is(err, http.ErrNotSupported) {
						t.Errorf("hijack error = %v, want not supported", err)
					}
				}
				_, _ = w.Write([]byte("late body"))
			}))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("oversized")))
			if rec.Code != http.StatusRequestEntityTooLarge || rec.Flushed || strings.Contains(rec.Body.String(), "late body") {
				t.Fatalf("terminal response changed: status=%d flushed=%v body=%q", rec.Code, rec.Flushed, rec.Body.String())
			}
		})
	}
}

func TestOverrunCannotReplaceFlushedResponse(t *testing.T) {
	handler := Middleware(Config{MaxBytes: 2})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		_, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte("late body"))
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("oversized")))
	if rec.Code != http.StatusOK || !rec.Flushed || rec.Body.Len() != 0 {
		t.Fatalf("flushed response changed: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

type unwrapOnlyWriter struct{ http.ResponseWriter }

func (w unwrapOnlyWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type flushErrorWriter struct {
	http.ResponseWriter
	err error
}

func (w flushErrorWriter) FlushError() error { return w.err }

func TestBodyLimitResponseControllerFlush(t *testing.T) {
	flushErr := errors.New("flush failed")
	for _, tc := range []struct {
		name      string
		wrap      func(*httptest.ResponseRecorder) http.ResponseWriter
		body      string
		wantErr   error
		wantFlush bool
	}{
		{"direct", func(r *httptest.ResponseRecorder) http.ResponseWriter { return r }, "x", nil, true},
		{"unwrap", func(r *httptest.ResponseRecorder) http.ResponseWriter { return unwrapOnlyWriter{r} }, "x", nil, true},
		{"unsupported", func(r *httptest.ResponseRecorder) http.ResponseWriter { return struct{ http.ResponseWriter }{r} }, "x", http.ErrNotSupported, false},
		{"underlying error", func(r *httptest.ResponseRecorder) http.ResponseWriter { return flushErrorWriter{r, flushErr} }, "x", flushErr, false},
		{"overrun", func(r *httptest.ResponseRecorder) http.ResponseWriter { return r }, "xx", errRequestTooLarge, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler := Middleware(Config{MaxBytes: 1})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				if err := http.NewResponseController(w).Flush(); !errors.Is(err, tc.wantErr) {
					t.Errorf("flush error = %v, want %v", err, tc.wantErr)
				}
			}))
			handler.ServeHTTP(tc.wrap(rec), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body)))
			if rec.Flushed != tc.wantFlush {
				t.Errorf("flushed = %t, want %t", rec.Flushed, tc.wantFlush)
			}
			if tc.wantErr == errRequestTooLarge && rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("status = %d, want 413", rec.Code)
			}
		})
	}
}
