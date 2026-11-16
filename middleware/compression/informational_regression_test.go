package compression

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"reflect"
	"testing"
)

func TestUnsupportedInitialFlushLeavesFinalResponseOpen(t *testing.T) {
	const body = "still eligible for compression"
	h := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Response", "initial")
		if err := http.NewResponseController(w).Flush(); !errors.Is(err, http.ErrNotSupported) {
			t.Errorf("Flush error = %v, want ErrNotSupported", err)
		}
		w.Header().Set("X-Response", "final")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusAccepted)
		if _, err := io.WriteString(w, body); err != nil {
			t.Error(err)
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(struct{ http.ResponseWriter }{w}, req)
	res := w.Result()
	if res.StatusCode != http.StatusAccepted || res.Header.Get("X-Response") != "final" || res.Header.Get("Vary") != "Accept-Encoding" || res.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("status=%d, headers=%v", res.StatusCode, res.Header)
	}
	decoded, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer decoded.Close()
	got, err := io.ReadAll(decoded)
	if err != nil || string(got) != body {
		t.Fatalf("decoded body=%q, err=%v", got, err)
	}
}

func TestBodylessStatusesBypassGzip(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		for _, writeBody := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/write=%t", status, writeBody), func(t *testing.T) {
				server := httptest.NewServer(Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/plain")
					w.Header().Set("ETag", `"revision"`)
					w.WriteHeader(status)
					if writeBody {
						if n, err := io.WriteString(w, "body"); n != 0 || !errors.Is(err, http.ErrBodyNotAllowed) {
							t.Errorf("Write = %d, %v", n, err)
						}
					}
				})))
				defer server.Close()
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
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
				if err != nil || resp.StatusCode != status || len(body) != 0 || resp.Header.Get("Content-Encoding") != "" {
					t.Fatalf("status=%d body=%q headers=%v read error=%v", resp.StatusCode, body, resp.Header, err)
				}
				if resp.Header.Get("ETag") != `"revision"` {
					t.Fatalf("headers = %v", resp.Header)
				}
			})
		}
	}
}

func TestGzipNegotiatesAcrossAcceptEncodingLines(t *testing.T) {
	handler := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "sample")
	}))
	for _, tc := range []struct {
		name   string
		lines  []string
		coding string
	}{
		{"combined", []string{"br, gzip"}, "gzip"},
		{"separate", []string{"br", "gzip"}, "gzip"},
		{"explicit refusal", []string{"*", "gzip;q=0"}, ""},
		{"combined refusal", []string{"*, gzip;q=0"}, ""},
		{"wildcard", []string{"br", "*;q=0.5"}, "gzip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, line := range tc.lines {
				req.Header.Add("Accept-Encoding", line)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if got := w.Result().Header.Get("Content-Encoding"); got != tc.coding {
				t.Fatalf("Content-Encoding = %q, want %q", got, tc.coding)
			}
		})
	}
}

func TestGzipFiltersMetadataOnlyWhenEncodingChanges(t *testing.T) {
	const body = "original response content"
	for _, tc := range []struct {
		name, acceptEncoding, etag, wantETag string
		maxBuffer                            int
		compressed                           bool
	}{
		{name: "gzip strong", acceptEncoding: "gzip", etag: `"strong"`, compressed: true},
		{name: "gzip weak", acceptEncoding: "gzip", etag: `W/"weak"`, wantETag: `W/"weak"`, compressed: true},
		{name: "identity", acceptEncoding: "identity", etag: `"strong"`, wantETag: `"strong"`},
		{name: "buffer bypass", acceptEncoding: "gzip", etag: `"strong"`, wantETag: `"strong"`, maxBuffer: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := Middleware(Config{MaxBufferBytes: tc.maxBuffer})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("Content-Digest", "original content digest")
				w.Header().Set("ETag", tc.etag)
				w.Header().Set("Trailer", "Repr-Digest, X-Result, Content-MD5")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, body)
				w.Header().Set("Repr-Digest", "original representation digest")
				w.Header().Set("Content-MD5", "original content MD5")
				w.Header().Set("X-Result", "complete")
				w.Header().Set(http.TrailerPrefix+"digest", "original digest")
			}))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-MD5", "outer digest")
				inner.ServeHTTP(w, r)
			}))
			defer server.Close()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Accept-Encoding", tc.acceptEncoding)
			res, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if encoded := res.Header.Get("Content-Encoding") == "gzip"; encoded != tc.compressed {
				t.Fatalf("Content-Encoding = %q, compressed = %v", res.Header.Get("Content-Encoding"), tc.compressed)
			}
			var reader io.Reader = res.Body
			if tc.compressed {
				zr, err := gzip.NewReader(res.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer zr.Close()
				reader = zr
			}
			got, err := io.ReadAll(reader)
			if err != nil || string(got) != body {
				t.Fatalf("body = %q, err = %v", got, err)
			}
			if _, err := io.Copy(io.Discard, res.Body); err != nil {
				t.Fatal(err)
			}
			if res.Header.Get("ETag") != tc.wantETag || res.Trailer.Get("X-Result") != "complete" {
				t.Fatalf("headers = %v, trailers = %v", res.Header, res.Trailer)
			}
			if tc.compressed {
				for _, field := range []string{"Content-Digest", "Content-Md5", "Repr-Digest", "Digest"} {
					_, header := res.Header[field]
					_, trailer := res.Trailer[field]
					if header || trailer {
						t.Errorf("stale field %s: headers=%v, trailers=%v", field, res.Header, res.Trailer)
					}
				}
			} else if res.Header.Get("Content-Digest") != "original content digest" ||
				res.Header.Get("Content-MD5") != "outer digest" ||
				res.Trailer.Get("Repr-Digest") != "original representation digest" ||
				res.Trailer.Get("Content-MD5") != "original content MD5" ||
				res.Trailer.Get("Digest") != "original digest" {
				t.Fatalf("lost metadata on identity response: headers=%v, trailers=%v", res.Header, res.Trailer)
			}
		})
	}
}

func TestFinalHeaderSnapshotAndTrailers(t *testing.T) {
	for _, coding := range []string{"identity", "gzip"} {
		for _, flush := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/flush=%t", coding, flush), func(t *testing.T) {
				server := httptest.NewServer(Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("X-Phase", "early")
					w.WriteHeader(http.StatusEarlyHints)
					w.Header().Set("X-Phase", "final")
					w.Header().Set("Trailer", "X-Checksum")
					w.WriteHeader(http.StatusCreated)
					w.Header().Set("X-Phase", "late")
					w.Header().Set("Content-Type", "application/octet-stream")
					if flush {
						if err := http.NewResponseController(w).Flush(); err != nil {
							t.Error(err)
						}
					}
					if _, err := io.WriteString(w, "payload"); err != nil {
						t.Error(err)
					}
					w.Header().Set("X-Checksum", "done")
					w.Header().Set(http.TrailerPrefix+"X-Dynamic", "also done")
				})))
				defer server.Close()

				var earlyPhase string
				trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
					if code == http.StatusEarlyHints {
						earlyPhase = header.Get("X-Phase")
					}
					return nil
				}}
				req, err := http.NewRequestWithContext(httptrace.WithClientTrace(t.Context(), trace), http.MethodGet, server.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Accept-Encoding", coding)
				resp, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				raw, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				if earlyPhase != "early" || resp.StatusCode != http.StatusCreated || resp.Header.Get("X-Phase") != "final" {
					t.Fatalf("interim=%q status=%d headers=%v", earlyPhase, resp.StatusCode, resp.Header)
				}
				if got := resp.Trailer.Get("X-Checksum"); got != "done" || resp.Trailer.Get("X-Dynamic") != "also done" {
					t.Fatalf("trailers = %v", resp.Trailer)
				}
				if got := resp.Header.Get("Content-Encoding"); (got == "gzip") != (coding == "gzip" && !flush) {
					t.Fatalf("Content-Encoding = %q", got)
				} else if got == "gzip" {
					reader, err := gzip.NewReader(bytes.NewReader(raw))
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Close()
					raw, err = io.ReadAll(reader)
					if err != nil {
						t.Fatal(err)
					}
				}
				if string(raw) != "payload" {
					t.Errorf("body = %q", raw)
				}
			})
		}
	}
}

func TestIdentityEmptyCopyPreservesVary(t *testing.T) {
	handler := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		src := struct{ io.Reader }{bytes.NewReader(nil)}
		if n, err := io.Copy(w, src); n != 0 || err != nil {
			t.Errorf("copy = %d, %v", n, err)
		}
		w.Header().Set("Vary", "Accept-Language")
		w.WriteHeader(http.StatusNoContent)
	}))
	dst := httptest.NewRecorder()
	handler.ServeHTTP(dst, httptest.NewRequest(http.MethodGet, "/", nil))
	values := dst.Result().Header.Values("Vary")
	if !headerValuesContain(values, "Accept-Language") || !headerValuesContain(values, "Accept-Encoding") {
		t.Fatalf("Vary = %v", values)
	}
}

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
