package router

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/recovery"
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
		{"content type spans writes", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "<ht")
			_, _ = io.WriteString(w, "ml>document</html>")
		}},
		{"explicit content type spans writes", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(w, "<ht")
			_, _ = io.WriteString(w, "ml>document</html>")
		}},
		{"content type spans writes before flush", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "<ht")
			_, _ = io.WriteString(w, "ml>document</html>")
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Error(err)
			}
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

func TestHeadBodylessStatusesAcceptEmptyWrite(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		for _, tc := range []struct{ name, method string }{
			{"direct", ""},
			{"explicit HEAD", http.MethodHead},
			{"GET fallback", http.MethodGet},
		} {
			t.Run(http.StatusText(status)+"/"+tc.name, func(t *testing.T) {
				var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					if n, err := w.Write(nil); n != 0 || err != nil {
						t.Errorf("empty write = (%d, %v), want (0, nil)", n, err)
					}
					if n, err := w.Write([]byte("forbidden")); n != 0 || err != http.ErrBodyNotAllowed {
						t.Errorf("nonempty write = (%d, %v), want (0, ErrBodyNotAllowed)", n, err)
					}
				})
				if tc.method != "" {
					rt := NewRouter()
					if err := rt.AddRoute(tc.method, "/", handler); err != nil {
						t.Fatal(err)
					}
					handler = rt
				}
				server := httptest.NewServer(handler)
				defer server.Close()
				for range 2 {
					res, err := server.Client().Head(server.URL)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(res.Body)
					_ = res.Body.Close()
					if res.StatusCode != status || len(body) != 0 || err != nil {
						t.Fatalf("status=%d body=%q error=%v, want %d and empty body", res.StatusCode, body, err, status)
					}
				}
			})
		}
	}
}

type headHijackWriter struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w headHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}
func TestHeadHijackRejectsWritesWithoutCommittingHeaders(t *testing.T) {
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()
	dst := httptest.NewRecorder()
	writer := &noBodyWriter{ResponseWriter: headHijackWriter{dst, conn}}
	writer.WriteHeader(http.StatusCreated)
	if _, _, err := http.NewResponseController(writer).Hijack(); err != nil {
		t.Fatal(err)
	}
	for _, p := range [][]byte{nil, []byte("body")} {
		if n, err := writer.Write(p); n != 0 || err != http.ErrHijacked {
			t.Errorf("Write(%q) = (%d, %v), want (0, ErrHijacked)", p, n, err)
		}
	}
	writer.commit(true)
	if dst.Code == http.StatusCreated || dst.Body.Len() != 0 {
		t.Fatal("response committed after hijack")
	}
}

func TestHeadFailedHijackPreservesResponse(t *testing.T) {
	dst := httptest.NewRecorder()
	writer := &noBodyWriter{ResponseWriter: dst}
	writer.WriteHeader(http.StatusAccepted)
	if _, _, err := http.NewResponseController(writer).Hijack(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("Hijack error=%v, want ErrNotSupported", err)
	}
	if n, err := writer.Write([]byte("normal")); n != 6 || err != nil {
		t.Fatalf("Write = (%d, %v), want (6, nil)", n, err)
	}
	writer.commit(true)
	if dst.Code != http.StatusAccepted || dst.Result().Header.Get("Content-Length") != "6" || dst.Body.Len() != 0 {
		t.Errorf("status=%d headers=%v body=%q, want 202, length 6, and empty body", dst.Code, dst.Result().Header, dst.Body)
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

func TestHeadControllerThroughWrappedServer(t *testing.T) {
	r := NewRouter()
	if err := r.AddRoute(http.MethodGet, "/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Test", "first")
		w.WriteHeader(http.StatusAccepted)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		w.Header().Set("X-Test", "late")
		_, _ = io.WriteString(w, "discarded")
	})); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.ServeHTTP(headUnwrapWriter{headUnwrapWriter{w}}, req)
	}))
	defer server.Close()
	resp, err := server.Client().Head(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusAccepted || resp.Header.Get("X-Test") != "first" || len(body) != 0 {
		t.Fatalf("status=%d headers=%v body=%q err=%v", resp.StatusCode, resp.Header, body, err)
	}
}

type headFlushResult struct {
	http.ResponseWriter
	err error
}

func (w headFlushResult) FlushError() error { return w.err }

func TestHeadSelectedStatusFlushCommitBoundary(t *testing.T) {
	transportFailure := errors.New("flush failed")
	for _, tc := range []struct {
		name       string
		err        error
		wantLength string
	}{
		{"no capability", nil, "3"},
		{"runtime refusal", http.ErrNotSupported, ""},
		{"transport failure", transportFailure, ""},
		{"success", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRouter()
			if err := r.AddRoute(http.MethodGet, "/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusAccepted)
				want := tc.err
				if tc.name == "no capability" {
					want = http.ErrNotSupported
				}
				if err := http.NewResponseController(w).Flush(); !errors.Is(err, want) {
					t.Errorf("Flush() = %v, want %v", err, want)
				}
				_, _ = io.WriteString(w, "abc")
			})); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			var dst http.ResponseWriter = headFlushResult{ResponseWriter: rec, err: tc.err}
			if tc.name == "no capability" {
				dst = headBasicWriter{rec}
			}
			r.ServeHTTP(dst, httptest.NewRequest(http.MethodHead, "/", nil))
			if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 || rec.Result().Header.Get("Content-Length") != tc.wantLength {
				t.Fatalf("status=%d body=%q headers=%v", rec.Code, rec.Body.String(), rec.Result().Header)
			}
		})
	}
}

func TestHeadDeclaredTrailersDoNotBecomeResponseFields(t *testing.T) {
	r := NewRouter()
	if err := r.AddRoute(http.MethodGet, "/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Trailer", "X-Checksum")
		_, _ = io.WriteString(w, "data")
		w.Header().Set("X-Checksum", "checksum")
		w.Header().Set(http.TrailerPrefix+"X-Late", "late")
	})); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(r)
	defer server.Close()
	get, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(get.Body)
	_ = get.Body.Close()
	if err != nil || string(body) != "data" || get.Trailer.Get("X-Checksum") != "checksum" {
		t.Fatalf("GET body=%q trailers=%v error=%v", body, get.Trailer, err)
	}
	head, err := server.Client().Head(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer head.Body.Close()
	body, err = io.ReadAll(head.Body)
	if err != nil || len(body) != 0 || head.StatusCode != get.StatusCode ||
		head.Header.Get("X-Checksum") != "" || head.Header.Get("X-Late") != "" ||
		head.Header.Get("Content-Length") != "" {
		t.Fatalf("HEAD body=%q status=%d header=%v error=%v", body, head.StatusCode, head.Header, err)
	}
}

type headCommitRecorder struct {
	*httptest.ResponseRecorder
	headers int
}

func (w *headCommitRecorder) WriteHeader(status int) {
	w.headers++
	w.ResponseRecorder.WriteHeader(status)
}

func TestAcceptanceHeadGoexitDoesNotCommitImplicitSuccess(t *testing.T) {
	for _, tc := range []struct {
		method   string
		exit     bool
		selected int
		want     int
	}{
		{http.MethodGet, true, 0, 0},
		{http.MethodHead, true, 0, 0},
		{http.MethodHead, false, 0, 1},
		{http.MethodHead, true, http.StatusAccepted, 1},
	} {
		r := NewRouter()
		if err := r.AddRoute(http.MethodGet, "/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if tc.selected != 0 {
				w.WriteHeader(tc.selected)
			}
			if tc.exit {
				runtime.Goexit()
			}
		})); err != nil {
			t.Fatal(err)
		}
		w := &headCommitRecorder{ResponseRecorder: httptest.NewRecorder()}
		done := make(chan struct{})
		go func() {
			defer close(done)
			r.ServeHTTP(w, httptest.NewRequest(tc.method, "/", nil))
		}()
		<-done
		if w.headers != tc.want || tc.selected != 0 && w.Code != tc.selected || w.Body.Len() != 0 {
			t.Fatalf("method=%s exit=%t selected=%d commits=%d status=%d", tc.method, tc.exit, tc.selected, w.headers, w.Code)
		}
	}
}

type headStatusSequence struct {
	*httptest.ResponseRecorder
	statuses []int
}

func (w *headStatusSequence) WriteHeader(code int) {
	w.statuses = append(w.statuses, code)
	if code >= 200 {
		w.ResponseRecorder.WriteHeader(code)
	}
}

func TestHeadInformationAndFinalHeaderSnapshotAreSeparate(t *testing.T) {
	r := NewRouter()
	if err := r.AddRoute(http.MethodGet, "/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.Header().Set("X-Selected", "first")
		w.Header().Set("Trailer", "X-Checksum")
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("X-Selected", "late")
		w.Header().Set("X-Checksum", "fixture")
		_, _ = io.WriteString(w, "fixture")
	})); err != nil {
		t.Fatal(err)
	}
	w := &headStatusSequence{ResponseRecorder: httptest.NewRecorder()}
	r.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/", nil))
	if !slices.Equal(w.statuses, []int{103, 202}) || w.Header().Get("X-Selected") != "first" ||
		w.Header().Get("X-Checksum") != "" || w.Header().Get("Content-Length") != "" || w.Body.Len() != 0 {
		t.Fatalf("statuses=%v header=%v body=%q", w.statuses, w.Header(), w.Body)
	}
}

func TestHeadLengthOverrunDoesNotReplaceFinalHeader(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, "GET", "/fixture", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "2")
		w.WriteHeader(http.StatusAccepted)
		if n, err := io.WriteString(w, "ok"); n != 2 || err != nil {
			t.Errorf("initial write=%d %v", n, err)
		}
		if n, err := io.Copy(w, strings.NewReader("x")); n != 0 || !errors.Is(err, http.ErrContentLength) {
			t.Errorf("excess=%d %v", n, err)
		}
		if n, err := w.Write(nil); n != 0 || err != nil {
			t.Errorf("empty write=%d %v", n, err)
		}
	}))
	server := httptest.NewServer(r)
	defer server.Close()
	for _, method := range []string{"GET", "HEAD"} {
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL+"/fixture", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		want := "ok"
		if method == "HEAD" {
			want = ""
		}
		if readErr != nil || string(body) != want || resp.ContentLength != 2 || resp.StatusCode != 202 {
			t.Fatalf("%s status=%d len=%d body=%q read=%v", method, resp.StatusCode, resp.ContentLength, body, readErr)
		}
	}
}

func TestHeadReadFromFailureKeepsConsumedLengthAndContentType(t *testing.T) {
	cause := errors.New("fixture read")
	r := NewRouter()
	mustAddRoute(r, "GET", "/sample", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		data := io.MultiReader(strings.NewReader("sample text"), iotest.ErrReader(cause))
		if n, err := io.Copy(w, data); n != 11 || !errors.Is(err, cause) {
			t.Errorf("copy=%d err=%v", n, err)
		}
	}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("HEAD", "/sample", nil))
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "11" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("HEAD=%d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}
}
