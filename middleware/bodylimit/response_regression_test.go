package bodylimit

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOverrunUsesFirstFinalStatus(t *testing.T) {
	const errorBody = "failure explanation"
	for _, tc := range []struct {
		name        string
		statuses    []int
		initialBody string
		wantStatus  int
		wantBody    string
		wantHints   int32
	}{
		{"committed error", []int{400, 200}, "", 400, errorBody, 0},
		{"committed success", []int{200, 400}, "", 200, "", 0},
		{"early hints then error", []int{103, 400, 200}, "", 400, errorBody, 1},
		{"implicit success", []int{400}, "prefix", 200, "prefix", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := Middleware(Config{MaxBytes: 3})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.wantStatus == http.StatusBadRequest {
					w.Header().Set("Content-Length", strconv.Itoa(len(errorBody)))
				}
				if tc.initialBody != "" {
					_, _ = io.WriteString(w, tc.initialBody)
				}
				for _, status := range tc.statuses {
					w.WriteHeader(status)
				}
				_, _ = io.ReadAll(r.Body)
				_, _ = io.WriteString(w, errorBody)
			}))
			server := httptest.NewUnstartedServer(handler)
			server.Config.ErrorLog = log.New(io.Discard, "", 0)
			server.Start()
			defer server.Close()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, strings.NewReader("four"))
			if err != nil {
				t.Fatal(err)
			}
			var hints atomic.Int32
			trace := &httptrace.ClientTrace{
				Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
					if code == http.StatusEarlyHints {
						hints.Add(1)
					}
					return nil
				},
			}
			resp, err := server.Client().Do(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != tc.wantStatus || string(body) != tc.wantBody || hints.Load() != tc.wantHints {
				t.Fatalf("status=%d body=%q early hints=%d read error=%v", resp.StatusCode, body, hints.Load(), err)
			}
		})
	}
}

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
