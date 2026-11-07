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
