package router

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestRouterPrintIncludesWildcardLabel(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, MethodAny, "/any/*path", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	var buf bytes.Buffer
	r.Print(&buf)

	assertOutputContains(t, buf.String(), "ANY    /any/*path   [wildcard]")
}

type routerCallbackWriter struct {
	buf bytes.Buffer
	fn  func()
}

type routerFailingWriter struct {
	calls int
	fail  int
}

func (w *routerFailingWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls >= w.fail {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestRouterPrintStopsOnWriteError(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/first", http.NotFoundHandler())
	mustAddRoute(r, http.MethodGet, "/second", http.NotFoundHandler())
	for _, fail := range []int{1, 2} {
		w := &routerFailingWriter{fail: fail}
		r.Print(w)
		if w.calls != fail {
			t.Errorf("writer failed at call %d, got %d calls", fail, w.calls)
		}
	}
}

func (w *routerCallbackWriter) Write(p []byte) (int, error) {
	if w.fn != nil {
		fn := w.fn
		w.fn = nil
		fn()
	}
	return w.buf.Write(p)
}

func TestRouterPrintWritesAfterSnapshot(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/users", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	writer := &routerCallbackWriter{fn: r.Freeze}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Print(writer)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Print blocked while writer called back into router")
	}

	assertOutputContains(t, writer.buf.String(), "GET    /users")
}
