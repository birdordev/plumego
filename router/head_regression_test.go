package router

import (
	"bufio"
	"errors"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/recovery"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type headBasicWriter struct{ http.ResponseWriter }
type headUnwrapWriter struct{ http.ResponseWriter }

func (w headUnwrapWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestHeadUnsupportedFlushLeavesResponseOpen(t *testing.T) {
	r := NewRouter()
	if err := r.AddRoute(http.MethodGet, "/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !errors.Is(http.NewResponseController(w).Flush(), http.ErrNotSupported) {
			t.Error("expected unsupported flush")
		}
		w.WriteHeader(http.StatusAccepted)
	})); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		dst := httptest.NewRecorder()
		r.ServeHTTP(headBasicWriter{dst}, httptest.NewRequest(method, "/", nil))
		if dst.Code != http.StatusAccepted {
			t.Errorf("%s status=%d, want 202", method, dst.Code)
		}
	}
	wrap, err := recovery.Middleware(recovery.Config{Logger: plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard})})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		dst := httptest.NewRecorder()
		wrap(r).ServeHTTP(headBasicWriter{dst}, httptest.NewRequest(method, "/", nil))
		if dst.Code != http.StatusAccepted {
			t.Errorf("%s through recovery status=%d, want 202", method, dst.Code)
		}
	}
}

func TestHeadFlushThroughUnwrapCommitsSelectedStatus(t *testing.T) {
	dst := httptest.NewRecorder()
	writer := &noBodyWriter{ResponseWriter: headUnwrapWriter{dst}}
	writer.WriteHeader(http.StatusAccepted)
	if err := http.NewResponseController(writer).Flush(); err != nil {
		t.Fatal(err)
	}
	if dst.Code != http.StatusAccepted || !dst.Flushed {
		t.Fatalf("status=%d flushed=%t", dst.Code, dst.Flushed)
	}
}

func TestHeadPreservesImplicitCommit(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"implicit", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hello")); w.WriteHeader(418) }},
		{"explicit", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test", "original")
			w.WriteHeader(201)
			w.Header().Set("X-Test", "late")
			_, _ = w.Write([]byte("hello"))
		}},
		{"multiple", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("he"))
			_, _ = w.Write([]byte("llo"))
		}},
		{"readfrom", func(w http.ResponseWriter, r *http.Request) {
			if reader, ok := w.(io.ReaderFrom); ok {
				_, _ = reader.ReadFrom(strings.NewReader("hello"))
			} else {
				_, _ = io.Copy(w, strings.NewReader("hello"))
			}
		}},
		{"empty", func(w http.ResponseWriter, r *http.Request) {}},
		{"length suppressed", func(w http.ResponseWriter, _ *http.Request) {
			w.Header()["Content-Length"] = nil
			_, _ = io.WriteString(w, "hello")
		}},
		{"explicit length", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "5")
			_, _ = io.WriteString(w, "hello")
		}},
		{"declared trailer", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Trailer", "X-Checksum")
			_, _ = io.WriteString(w, "hello")
			w.Header().Set("X-Checksum", "checksum")
		}},
		{"prefixed trailer", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(http.TrailerPrefix+"X-Checksum", "checksum")
			_, _ = io.WriteString(w, "hello")
		}},
		{"explicit length with trailer", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Trailer", "X-Checksum")
			w.Header().Set("Content-Length", "5")
			_, _ = io.WriteString(w, "hello")
		}},
		{"transfer encoding", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Transfer-Encoding", "chunked")
			_, _ = w.Write([]byte("hello"))
		}},
		{"flush", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(202)
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte("hello"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := NewRouter()
			if err := rt.AddRoute("GET", "/", tc.handler); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(rt)
			defer server.Close()
			get, err := server.Client().Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, get.Body)
			get.Body.Close()
			if (tc.name == "declared trailer" || tc.name == "prefixed trailer") && get.Trailer.Get("X-Checksum") != "checksum" {
				t.Fatalf("GET trailer = %q", get.Trailer.Get("X-Checksum"))
			}
			for range 2 {
				head, err := server.Client().Head(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(head.Body)
				head.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if len(body) != 0 || head.StatusCode != get.StatusCode {
					t.Fatalf("GET=%d HEAD=%d body=%q", get.StatusCode, head.StatusCode, body)
				}
				for _, key := range []string{"Content-Type", "Content-Length", "X-Test"} {
					if got, want := head.Header.Get(key), get.Header.Get(key); got != want {
						t.Errorf("%s: HEAD=%q GET=%q", key, got, want)
					}
				}
			}
		})
	}
}

func TestHeadBodyAndLengthConstraints(t *testing.T) {
	for _, status := range []int{204, 304} {
		writer := &noBodyWriter{ResponseWriter: httptest.NewRecorder()}
		writer.WriteHeader(status)
		if n, err := writer.Write([]byte("forbidden")); n != 0 || err != http.ErrBodyNotAllowed {
			t.Fatalf("status=%d n=%d err=%v", status, n, err)
		}
		writer.commit(true)
	}
	dst := httptest.NewRecorder()
	dst.Header().Set("Content-Length", "2")
	writer := &noBodyWriter{ResponseWriter: dst}
	if n, err := writer.Write([]byte("long")); n != 0 || err != http.ErrContentLength {
		t.Fatalf("n=%d err=%v", n, err)
	}
	writer.commit(true)
	suppressed := httptest.NewRecorder()
	suppressed.Header()["Content-Type"] = nil
	writer = &noBodyWriter{ResponseWriter: suppressed}
	_, _ = writer.Write([]byte("hello"))
	writer.commit(true)
	if got := suppressed.Result().Header.Get("Content-Type"); got != "" {
		t.Fatalf("explicitly suppressed content type=%q", got)
	}
}

type headHijackWriter struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w headHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}
func TestHeadHijackDoesNotCommitHeaders(t *testing.T) {
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()
	writer := &noBodyWriter{ResponseWriter: headHijackWriter{httptest.NewRecorder(), conn}}
	if _, _, err := http.NewResponseController(writer).Hijack(); err != nil {
		t.Fatal(err)
	}
	writer.commit(true)
	if writer.status != 0 {
		t.Fatal("headers committed after hijack")
	}
}

func TestHeadPreservesCommitBeforeRecoveredPanic(t *testing.T) {
	rt := NewRouter()
	if err := rt.AddRoute("GET", "/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
		panic("after commitment")
	})); err != nil {
		t.Fatal(err)
	}
	recoverMiddleware, err := recovery.Middleware(recovery.Config{Logger: plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard})})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(recoverMiddleware(rt))
	defer server.Close()
	for _, method := range []string{"GET", "HEAD"} {
		req, err := http.NewRequest(method, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || resp.Header.Get("Content-Length") != "5" {
			t.Fatalf("%s status=%d headers=%v", method, resp.StatusCode, resp.Header)
		}
	}
}
