package compression

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"reflect"
	"testing"
)

type statusHistoryWriter struct {
	header http.Header
	codes  []int
	body   bytes.Buffer
}

func (w *statusHistoryWriter) Header() http.Header         { return w.header }
func (w *statusHistoryWriter) WriteHeader(code int)        { w.codes = append(w.codes, code) }
func (w *statusHistoryWriter) Write(p []byte) (int, error) { return w.body.Write(p) }

func TestInformationalHeadersPreserveFinalResponse(t *testing.T) {
	for _, status := range []int{201, 404, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			dst := &statusHistoryWriter{header: make(http.Header)}
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Link", "</style.css>; rel=preload")
				w.WriteHeader(100)
				w.WriteHeader(103)
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("payload"))
			})).ServeHTTP(dst, req)
			if !reflect.DeepEqual(dst.codes, []int{100, 103, status}) {
				t.Fatalf("codes=%v", dst.codes)
			}
			body := dst.body.Bytes()
			if dst.header.Get("Content-Encoding") == "gzip" {
				reader, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				body, err = io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
			}
			if string(body) != "payload" {
				t.Fatalf("body=%q", body)
			}
		})
	}
}

func TestInformationalHeadersRealHTTP(t *testing.T) {
	for _, status := range []int{201, 404, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Link", "</style.css>; rel=preload")
				w.WriteHeader(103)
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("payload"))
			})))
			defer server.Close()
			var interim []int
			trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, _ textproto.MIMEHeader) error { interim = append(interim, code); return nil }}
			req, err := http.NewRequestWithContext(httptrace.WithClientTrace(t.Context(), trace), "GET", server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != status || string(body) != "payload" || !reflect.DeepEqual(interim, []int{103}) {
				t.Fatalf("status=%d interim=%v body=%q", resp.StatusCode, interim, body)
			}
		})
	}
}

func TestInformationalHeaderIsolation(t *testing.T) {
	for _, status := range []int{200, 404, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			handler := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "1")
				w.Header().Set("X-Interim", "first")
				w.WriteHeader(103)
				w.Header().Del("Content-Length")
				w.Header().Del("X-Interim")
				w.WriteHeader(103)
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("X-Final", "kept")
				w.WriteHeader(status)
				if n, err := w.Write([]byte("payload")); err != nil || n != 7 {
					t.Errorf("write: n=%d err=%v", n, err)
				}
			}))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Outer", "kept")
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			var interim []http.Header
			trace := &httptrace.ClientTrace{Got1xxResponse: func(_ int, header textproto.MIMEHeader) error {
				interim = append(interim, http.Header(header).Clone())
				return nil
			}}
			req, err := http.NewRequestWithContext(httptrace.WithClientTrace(t.Context(), trace), "GET", server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Accept-Encoding", "gzip")
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var body io.Reader = resp.Body
			if resp.Header.Get("Content-Encoding") == "gzip" {
				reader, err := gzip.NewReader(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				body = reader
			}
			payload, err := io.ReadAll(body)
			if err != nil || string(payload) != "payload" || resp.StatusCode != status {
				t.Fatalf("status=%d body=%q err=%v", resp.StatusCode, payload, err)
			}
			if len(interim) != 2 || interim[0].Get("X-Interim") != "first" || interim[1].Get("X-Interim") != "" {
				t.Fatalf("informational headers=%v", interim)
			}
			if resp.Header.Get("X-Interim") != "" || resp.Header.Get("X-Final") != "kept" || resp.Header.Get("X-Outer") != "kept" {
				t.Fatalf("final headers=%v", resp.Header)
			}
		})
	}
}

func TestEmptyResponseCommitsFinalHeaders(t *testing.T) {
	for _, informational := range []bool{false, true} {
		for _, contentType := range []string{"", "text/plain"} {
			t.Run(fmt.Sprintf("informational=%v/content-type=%s", informational, contentType), func(t *testing.T) {
				server := httptest.NewServer(Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if informational {
						w.Header().Set("X-Interim", "temporary")
						w.WriteHeader(103)
						w.Header().Del("X-Interim")
					}
					if contentType != "" {
						w.Header().Set("Content-Type", contentType)
					}
					w.Header().Set("X-Final", "kept")
				})))
				defer server.Close()
				req, err := http.NewRequestWithContext(t.Context(), "GET", server.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Accept-Encoding", "gzip")
				resp, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil || len(body) != 0 || resp.StatusCode != 200 || resp.Header.Get("X-Final") != "kept" || resp.Header.Get("X-Interim") != "" || resp.Header.Get("Content-Type") != contentType || resp.Header.Get("Content-Encoding") != "" {
					t.Fatalf("status=%d headers=%v body=%q err=%v", resp.StatusCode, resp.Header, body, err)
				}
			})
		}
	}
}
