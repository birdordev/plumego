package router

import (
	"bytes"
	"io"
	"net/http"
	"strings"
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
	buf   bytes.Buffer
}

func (w *routerFailingWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls >= w.fail {
		return 0, io.ErrClosedPipe
	}
	return w.buf.Write(p)
}

func TestRouterPrintStopsOnWriteError(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/first", http.NotFoundHandler())
	mustAddRoute(r, http.MethodGet, "/second", http.NotFoundHandler())
	for _, fail := range []int{1, 2, 3} {
		w := &routerFailingWriter{fail: fail}
		r.Print(w)
		if w.calls != fail {
			t.Errorf("writer failed at call %d, got %d calls", fail, w.calls)
		}
		want := []string{"", "Registered Routes:\n", "Registered Routes:\nGET    /first\n"}[fail-1]
		if got := w.buf.String(); got != want {
			t.Errorf("writer failed at call %d: output=%q, want %q", fail, got, want)
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

type printFixtureWriter func([]byte) (int, error)

func (f printFixtureWriter) Write(p []byte) (int, error) { return f(p) }

func TestPrintRetriesWithNewSnapshotAfterRegistrationAndFailure(t *testing.T) {
	r := NewRouter()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	for _, route := range []string{"/a", "/c"} {
		mustAddRoute(r, "GET", route, handler)
	}
	var seen []string
	r.Print(printFixtureWriter(func(data []byte) (int, error) {
		seen = append(seen, string(data))
		if len(seen) == 2 {
			mustAddRoute(r, "GET", "/b", handler)
		}
		if len(seen) == 3 {
			return 0, io.ErrClosedPipe
		}
		return len(data), nil
	}))
	if len(seen) != 3 || !strings.Contains(seen[2], "/c") {
		t.Fatalf("old snapshot=%v", seen)
	}
	var out bytes.Buffer
	r.Print(&out)
	if !strings.Contains(out.String(), "/a\nGET    /b\nGET    /c") || serveRouter(r, "GET", "/b").Code != 204 {
		t.Fatalf("retry=%s", out.String())
	}
}
