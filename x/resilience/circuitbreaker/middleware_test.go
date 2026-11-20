package circuitbreaker

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"
)

func TestMiddlewareWithNilErrorHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const timeout = time.Second
		started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		handler := MiddlewareWithErrorHandler(Config{MinRequests: 1, Timeout: timeout}, nil)(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/trip":
					w.WriteHeader(http.StatusInternalServerError)
				case "/probe":
					close(started)
					<-release
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Error("rejected request reached downstream")
				}
			}),
		)
		request := func(path string, want int) {
			t.Helper()
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != want {
				t.Errorf("%s status = %d, want %d", path, w.Code, want)
			}
		}
		request("/trip", http.StatusInternalServerError)
		request("/reject", http.StatusServiceUnavailable)
		time.Sleep(timeout) // Advance the synctest clock past the open interval.
		go func() {
			defer close(done)
			request("/probe", http.StatusNoContent)
		}()
		<-started
		request("/reject", http.StatusTooManyRequests)
		close(release)
		<-done
	})

	t.Run("explicit handler", func(t *testing.T) {
		calls := 0
		h := MiddlewareWithErrorHandler(Config{MinRequests: 1}, func(w http.ResponseWriter, _ *http.Request, _ *CircuitBreaker, err error) {
			calls++
			if !errors.Is(err, ErrCircuitOpen) {
				t.Errorf("rejection = %v, want ErrCircuitOpen", err)
			}
			w.WriteHeader(http.StatusTeapot)
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if calls != 1 || w.Code != http.StatusTeapot {
			t.Errorf("handler calls = %d, status = %d", calls, w.Code)
		}
	})
}
