package cors

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

type copyReader struct{ io.Reader }

func TestCORSReaderFromCommitsVaryOnWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  io.Reader
		err  error
		vary string
		body string
	}{
		{name: "empty", src: strings.NewReader(""), vary: "Accept-Language"},
		{name: "read error", src: iotest.ErrReader(io.ErrUnexpectedEOF), err: io.ErrUnexpectedEOF, vary: "Accept-Language"},
		{name: "with body", src: strings.NewReader("ok"), vary: "Accept-Language", body: "ok"},
		{name: "wildcard", src: strings.NewReader(""), vary: "*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := Middleware(CORSOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.body != "" {
					w.Header().Set("Vary", tc.vary)
				}
				n, err := io.Copy(w, copyReader{tc.src})
				if n != int64(len(tc.body)) || !errors.Is(err, tc.err) {
					t.Errorf("copy = %d, %v", n, err)
				}
				if tc.body == "" {
					w.Header().Set("Vary", tc.vary)
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			dst := httptest.NewRecorder()
			handler.ServeHTTP(dst, httptest.NewRequest(http.MethodGet, "/", nil))
			got := strings.Join(dst.Result().Header.Values("Vary"), ",")
			if tc.vary == "*" {
				if got != "*" {
					t.Errorf("Vary = %q, want *", got)
				}
			} else if !strings.Contains(got, "Origin") || !strings.Contains(got, tc.vary) {
				t.Errorf("Vary = %q, want Origin and %s", got, tc.vary)
			}
			if dst.Body.String() != tc.body {
				t.Errorf("body = %q, want %q", dst.Body.String(), tc.body)
			}
		})
	}
}

func dummyHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func TestCORSMiddleware(t *testing.T) {
	opts := CORSOptions{
		AllowedOrigins:   []string{"http://allowed.com"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
		ExposeHeaders:    []string{"X-My-Custom-Header"},
		MaxAge:           10 * time.Minute,
	}

	handler := Middleware(opts)(http.HandlerFunc(dummyHandler))

	t.Run("No Origin header (non-CORS)", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		if resp.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("expected no CORS header, got %s", resp.Header.Get("Access-Control-Allow-Origin"))
		}
	})

	t.Run("Allowed Origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", "http://allowed.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		resp := w.Result()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://allowed.com" {
			t.Errorf("expected Allow-Origin http://allowed.com, got %s", got)
		}
		if resp.Header.Get("Access-Control-Allow-Credentials") != "true" {
			t.Errorf("expected Allow-Credentials true")
		}
		if !strings.Contains(resp.Header.Get("Access-Control-Expose-Headers"), "X-My-Custom-Header") {
			t.Errorf("expected Expose-Headers to contain X-My-Custom-Header, got %s", resp.Header.Get("Access-Control-Expose-Headers"))
		}
	})

	t.Run("Disallowed Origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", "http://evil.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected pass-through 200, got %d", resp.StatusCode)
		}
		if resp.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("expected no Allow-Origin header, got %s", resp.Header.Get("Access-Control-Allow-Origin"))
		}
	})

	t.Run("Preflight Allowed", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/test", nil)
		req.Header.Set("Origin", "http://allowed.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "Content-Type, Authorization")

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("expected 204, got %d", resp.StatusCode)
		}
		if !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "POST") {
			t.Errorf("expected Allow-Methods to contain POST, got %s", resp.Header.Get("Access-Control-Allow-Methods"))
		}
		if !strings.Contains(resp.Header.Get("Access-Control-Allow-Headers"), "Content-Type") {
			t.Errorf("expected Allow-Headers to contain Content-Type, got %s", resp.Header.Get("Access-Control-Allow-Headers"))
		}
		if resp.Header.Get("Access-Control-Max-Age") != "600" {
			t.Errorf("expected Max-Age=600, got %s", resp.Header.Get("Access-Control-Max-Age"))
		}
		for _, vary := range []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
			if !headerValuesContain(resp.Header.Values("Vary"), vary) {
				t.Errorf("expected Vary to contain %s, got %v", vary, resp.Header.Values("Vary"))
			}
		}
	})

	t.Run("Preflight Disallowed Method Passes Through", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/test", nil)
		req.Header.Set("Origin", "http://allowed.com")
		req.Header.Set("Access-Control-Request-Method", "DELETE")

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected pass-through 200, got %d", resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("expected no Allow-Origin for disallowed preflight method, got %s", got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Methods"); got != "" {
			t.Errorf("expected no Allow-Methods for disallowed preflight method, got %s", got)
		}
	})

	t.Run("Preflight Disallowed Header Passes Through", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/test", nil)
		req.Header.Set("Origin", "http://allowed.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "Content-Type, X-Not-Allowed")

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected pass-through 200, got %d", resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("expected no Allow-Origin for disallowed preflight header, got %s", got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Headers"); got != "" {
			t.Errorf("expected no Allow-Headers for disallowed preflight header, got %s", got)
		}
	})

	t.Run("Preflight Blank Requested Headers Passes Through", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/test", nil)
		req.Header.Set("Origin", "http://allowed.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", " , ")

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected pass-through 200, got %d", resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("expected no Allow-Origin for blank requested headers, got %s", got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Headers"); got != "" {
			t.Errorf("expected no Allow-Headers for blank requested headers, got %s", got)
		}
	})

	t.Run("Preflight Wildcard Headers Normalizes Requested Headers", func(t *testing.T) {
		handler := Middleware(CORSOptions{
			AllowedOrigins: []string{"http://allowed.com"},
			AllowedMethods: []string{"POST"},
			AllowedHeaders: []string{"*"},
		})(http.HandlerFunc(dummyHandler))

		req := httptest.NewRequest("OPTIONS", "/test", nil)
		req.Header.Set("Origin", "http://allowed.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", " X-One , X-Two ")

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("expected 204, got %d", resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Headers"); got != "X-One, X-Two" {
			t.Errorf("expected requested headers to be normalized, got %q", got)
		}
	})

	t.Run("Preflight Wildcard Blank Requested Headers Passes Through", func(t *testing.T) {
		handler := Middleware(CORSOptions{
			AllowedOrigins: []string{"http://allowed.com"},
			AllowedMethods: []string{"POST"},
			AllowedHeaders: []string{"*"},
		})(http.HandlerFunc(dummyHandler))

		req := httptest.NewRequest("OPTIONS", "/test", nil)
		req.Header.Set("Origin", "http://allowed.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", " , ")

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected pass-through 200, got %d", resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("expected no Allow-Origin for blank wildcard requested headers, got %s", got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Headers"); got != "" {
			t.Errorf("expected no Allow-Headers for blank wildcard requested headers, got %s", got)
		}
	})

	t.Run("AllowCredentials with * origins", func(t *testing.T) {
		opts2 := CORSOptions{
			AllowedOrigins:   []string{"*"},
			AllowCredentials: true,
		}
		handler2 := Middleware(opts2)(http.HandlerFunc(dummyHandler))

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", "http://foo.com")
		w := httptest.NewRecorder()

		handler2.ServeHTTP(w, req)
		resp := w.Result()

		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://foo.com" {
			t.Errorf("expected echoed origin http://foo.com, got %s", got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("expected credentials header true, got %s", got)
		}
	})
}

func TestCORSMiddleware_DeduplicatesExistingVary(t *testing.T) {
	opts := CORSOptions{AllowedOrigins: []string{"http://allowed.com"}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding, Origin")
		Middleware(opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, r)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Origin", "http://allowed.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	values := w.Result().Header.Values("Vary")
	if countHeaderToken(values, "Origin") != 1 {
		t.Fatalf("expected one Origin Vary token, got %v", values)
	}
	if !headerValuesContain(values, "Accept-Encoding") {
		t.Fatalf("expected existing Accept-Encoding Vary token, got %v", values)
	}
}

func TestCORSMiddleware_NormalizesOptionLists(t *testing.T) {
	handler := Middleware(CORSOptions{
		AllowedOrigins: []string{" https://app.example ", " "},
		AllowedMethods: []string{" POST ", ""},
		AllowedHeaders: []string{" Content-Type ", "\t"},
		ExposeHeaders:  []string{" X-Trace-ID ", ""},
	})(http.HandlerFunc(dummyHandler))

	t.Run("actual request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Origin", "https://app.example")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		resp := rec.Result()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://app.example" {
			t.Fatalf("Allow-Origin = %q, want trimmed origin match", got)
		}
		if got := resp.Header.Get("Access-Control-Expose-Headers"); got != "X-Trace-ID" {
			t.Fatalf("Expose-Headers = %q, want X-Trace-ID", got)
		}
	})

	t.Run("preflight", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/test", nil)
		req.Header.Set("Origin", "https://app.example")
		req.Header.Set("Access-Control-Request-Method", "POST")
		req.Header.Set("Access-Control-Request-Headers", "Content-Type")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		resp := rec.Result()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
		}
		if got := resp.Header.Get("Access-Control-Allow-Methods"); got != "POST" {
			t.Fatalf("Allow-Methods = %q, want POST", got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Headers"); got != "Content-Type" {
			t.Fatalf("Allow-Headers = %q, want Content-Type", got)
		}
	})
}

func TestCORSMiddleware_StrictDefaultOptionsRequiresExplicitOrigins(t *testing.T) {
	opts, err := StrictDefaultOptions(" ", "http://allowed.com", "\t")
	if err != nil {
		t.Fatalf("StrictDefaultOptions returned error: %v", err)
	}
	if len(opts.AllowedOrigins) != 1 || opts.AllowedOrigins[0] != "http://allowed.com" {
		t.Fatalf("AllowedOrigins = %v, want trimmed explicit origin", opts.AllowedOrigins)
	}
	handler := Middleware(opts)(http.HandlerFunc(dummyHandler))

	t.Run("allowed explicit origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Origin", "http://allowed.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		resp := w.Result()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://allowed.com" {
			t.Fatalf("Allow-Origin = %q, want explicit origin", got)
		}
	})

	t.Run("disallowed origin passes through without wildcard", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Origin", "http://other.com")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want pass-through 200", resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Allow-Origin = %q, want empty for disallowed origin", got)
		}
	})
}

func TestStrictDefaultOptionsReturnsErrorWithoutValidOrigins(t *testing.T) {
	for _, origins := range [][]string{
		nil,
		[]string{},
		{" ", "\t"},
	} {
		opts, err := StrictDefaultOptions(origins...)
		if !errors.Is(err, ErrStrictDefaultOriginsRequired) {
			t.Fatalf("StrictDefaultOptions(%v) error = %v, want %v", origins, err, ErrStrictDefaultOriginsRequired)
		}
		if len(opts.AllowedOrigins) != 0 {
			t.Fatalf("StrictDefaultOptions(%v) options = %+v, want zero", origins, opts)
		}
	}
}

func TestStrictDefaultOptionsRejectsWildcardOrigin(t *testing.T) {
	for _, origins := range [][]string{
		{"*"},
		{" https://app.example ", "*"},
	} {
		opts, err := StrictDefaultOptions(origins...)
		if !errors.Is(err, ErrStrictDefaultWildcardOrigin) {
			t.Fatalf("StrictDefaultOptions(%v) error = %v, want %v", origins, err, ErrStrictDefaultWildcardOrigin)
		}
		if len(opts.AllowedOrigins) != 0 {
			t.Fatalf("StrictDefaultOptions(%v) options = %+v, want zero", origins, opts)
		}
	}
}

func TestStrictDefaultOptionsReturnsTrimmedOrigins(t *testing.T) {
	opts, err := StrictDefaultOptions(" https://app.example ", "", "\t")
	if err != nil {
		t.Fatalf("StrictDefaultOptions returned error: %v", err)
	}
	if len(opts.AllowedOrigins) != 1 || opts.AllowedOrigins[0] != "https://app.example" {
		t.Fatalf("AllowedOrigins = %v, want trimmed origin", opts.AllowedOrigins)
	}
	if len(opts.AllowedMethods) == 0 {
		t.Fatal("AllowedMethods defaults were not applied")
	}
	if len(opts.AllowedHeaders) == 0 {
		t.Fatal("AllowedHeaders defaults were not applied")
	}
}

func TestCORSMiddleware_ResponseBody(t *testing.T) {
	opts := CORSOptions{AllowedOrigins: []string{"*"}}
	handler := Middleware(opts)(http.HandlerFunc(dummyHandler))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://anything.com")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("expected body 'ok', got %s", string(body))
	}
}

func headerValuesContain(values []string, want string) bool {
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(item), want) {
				return true
			}
		}
	}
	return false
}

func countHeaderToken(values []string, want string) int {
	var count int
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(item), want) {
				count++
			}
		}
	}
	return count
}

func TestCORSVaryIncludesFallbackResponses(t *testing.T) {
	const allowed = "https://app.example"
	for _, tc := range []struct {
		name, method, origin, requestedMethod, headers string
		preflight, allow, wildcard                     bool
	}{
		{name: "allowed", method: http.MethodGet, origin: allowed, allow: true},
		{name: "denied", method: http.MethodGet, origin: "https://other.example"},
		{name: "missing origin", method: http.MethodGet},
		{name: "preflight", method: http.MethodOptions, origin: allowed, requestedMethod: "GET", preflight: true, allow: true},
		{name: "denied method", method: http.MethodOptions, origin: allowed, requestedMethod: "DELETE"},
		{name: "denied header", method: http.MethodOptions, origin: allowed, requestedMethod: "GET", headers: "X-Unsupported"},
		{name: "ordinary options", method: http.MethodOptions, origin: allowed, allow: true},
		{name: "wildcard", method: http.MethodGet, wildcard: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, "/", nil)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Access-Control-Request-Method", tc.requestedMethod)
			req.Header.Set("Access-Control-Request-Headers", tc.headers)
			calls := 0
			h := Middleware(CORSOptions{AllowedOrigins: []string{allowed}, AllowedMethods: []string{"GET"}})(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.Header().Set("Vary", "Accept-Encoding")
					if tc.name == "allowed" {
						w.Header().Add("Vary", "Origin")
					}
					if tc.wildcard {
						w.Header().Set("Vary", "*")
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Fatal(err)
					}
				}))
			h.ServeHTTP(rec, req)
			res := rec.Result()
			vary := res.Header.Values("Vary")
			if tc.wildcard {
				if len(vary) != 1 || vary[0] != "*" {
					t.Fatalf("Vary = %v", vary)
				}
			} else {
				if countHeaderToken(vary, "Origin") != 1 {
					t.Errorf("Vary = %v, want Origin once", vary)
				}
				if !tc.preflight && !headerValuesContain(vary, "Accept-Encoding") {
					t.Errorf("Vary = %v, lost downstream value", vary)
				}
				if tc.method == http.MethodOptions && (!headerValuesContain(vary, "Access-Control-Request-Method") ||
					!headerValuesContain(vary, "Access-Control-Request-Headers")) {
					t.Errorf("Vary = %v, missing preflight dimensions", vary)
				}
			}
			wantStatus, wantCalls := http.StatusOK, 1
			if tc.preflight {
				wantStatus, wantCalls = http.StatusNoContent, 0
			}
			wantOrigin := ""
			if tc.allow {
				wantOrigin = allowed
			}
			if res.StatusCode != wantStatus || calls != wantCalls || res.Header.Get("Access-Control-Allow-Origin") != wantOrigin {
				t.Fatalf("status = %d, next calls = %d, headers = %v", res.StatusCode, calls, res.Header)
			}
		})
	}
}
