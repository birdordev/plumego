package compression

import (
	"bytes"
	"compress/gzip"
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
