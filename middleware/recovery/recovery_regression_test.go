package recovery

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"strings"
	"testing"

	"github.com/birdordev/plumego/log"
)

func TestRecoveryInformationalResponseDoesNotCommitFinalStatus(t *testing.T) {
	mw, err := Middleware(Config{Logger: log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{http.StatusContinue, http.StatusProcessing, http.StatusEarlyHints} {
		server := httptest.NewServer(mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code); panic("handler panic") })))
		resp, err := server.Client().Get(server.URL)
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusInternalServerError || len(body) == 0 {
			t.Fatalf("after %d: status=%d body=%q", code, resp.StatusCode, body)
		}
	}
}

func TestRecoveryPreservesAbortHandler(t *testing.T) {
	mw, err := Middleware(Config{Logger: log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})})
	if err != nil {
		t.Fatal(err)
	}
	for _, partial := range []bool{false, true} {
		handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if partial {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("partial"))
				w.(http.Flusher).Flush()
			}
			panic(http.ErrAbortHandler)
		}))
		func() {
			defer func() {
				if value := recover(); value != http.ErrAbortHandler {
					t.Errorf("panic=%v, want ErrAbortHandler", value)
				}
			}()
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		}()
		server := httptest.NewServer(handler)
		resp, err := server.Client().Get(server.URL)
		var readErr error
		if resp != nil {
			_, readErr = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		server.Close()
		if err == nil && readErr == nil {
			t.Errorf("abort with partial=%v completed as a normal HTTP response", partial)
		}
	}
}

func TestRecoveryAfterEarlyHintsWritesOneRedactedFinalResponse(t *testing.T) {
	var diagnostics bytes.Buffer
	mw, err := Middleware(Config{Logger: log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatJSON, Output: &diagnostics})})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Interim-Fixture", "hint")
		w.WriteHeader(http.StatusEarlyHints)
		w.Header().Del("X-Interim-Fixture")
		panic("ordinary fixture panic")
	})))
	defer srv.Close()
	hints := 0
	trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
		if code == 103 && header.Get("X-Interim-Fixture") == "hint" {
			hints++
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(t.Context(), trace), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	srv.Close()
	if err != nil || hints != 1 || resp.StatusCode != http.StatusInternalServerError || resp.Header.Get("X-Interim-Fixture") != "" ||
		!bytes.Contains(body, []byte("internal_error")) || bytes.Contains(body, []byte("ordinary fixture panic")) ||
		strings.Count(diagnostics.String(), "\n") != 1 {
		t.Fatalf("status=%d hints=%d body=%q diagnostics=%q err=%v", resp.StatusCode, hints, body, diagnostics.String(), err)
	}
}
