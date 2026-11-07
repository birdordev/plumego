package recovery

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spcent/plumego/log"
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
