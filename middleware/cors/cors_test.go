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

func TestCORSPreflightCombinesRequestedHeaderLines(t *testing.T) {
	const requestHeaders = "Access-Control-Request-Headers"
	const wantHeaders = "X-Region, X-Format"
	for _, config := range []struct {
		name    string
		allowed []string
	}{
		{"explicit", []string{"X-Region", "X-Format"}},
		{"wildcard", []string{"*"}},
	} {
		t.Run(config.name, func(t *testing.T) {
			calls := 0
			handler := Middleware(CORSOptions{
				AllowedOrigins: []string{"https://ui.example"},
				AllowedHeaders: config.allowed,
			})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(http.StatusAccepted)
			}))

			for _, form := range []struct {
				name        string
				values      []string
				wantStatus  int
				wantHeaders string
				wantCalls   int
			}{
				{"single field", []string{wantHeaders}, http.StatusNoContent, wantHeaders, 0},
				{"multiple fields", []string{"X-Region", "X-Format"}, http.StatusNoContent, wantHeaders, 0},
				{"empty first field", []string{"", wantHeaders}, http.StatusNoContent, wantHeaders, 0},
				{"only blank fields", []string{" , ", " "}, http.StatusAccepted, "", 1},
			} {
				t.Run(form.name, func(t *testing.T) {
					req := httptest.NewRequest(http.MethodOptions, "/", nil)
					req.Header.Set("Origin", "https://ui.example")
					req.Header.Set("Access-Control-Request-Method", http.MethodGet)
					for _, value := range form.values {
						req.Header.Add(requestHeaders, value)
					}
					rec := httptest.NewRecorder()
					calls = 0
					handler.ServeHTTP(rec, req)
					if got := rec.Header().Get("Access-Control-Allow-Headers"); rec.Code != form.wantStatus || got != form.wantHeaders {
						t.Fatalf("status = %d, allowed headers = %q, want %d and %q", rec.Code, got, form.wantStatus, form.wantHeaders)
					}
					if calls != form.wantCalls {
						t.Fatalf("downstream calls = %d, want %d", calls, form.wantCalls)
					}
					vary := rec.Header().Values("Vary")
					for _, field := range []string{"Origin", "Access-Control-Request-Method", requestHeaders} {
						if !headerValuesContain(vary, field) {
							t.Errorf("Vary = %v, missing %q", vary, field)
						}
					}
				})
			}
		})
	}
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

func TestPreflightUsesExactMethodName(t *testing.T) {
	handler := Middleware(CORSOptions{AllowedOrigins: []string{"https://example.test"}, AllowedMethods: []string{"GET", "CUSTOM"}})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	for _, tc := range []struct {
		method string
		status int
	}{
		{"GET", http.StatusNoContent},
		{"CUSTOM", http.StatusNoContent},
		{"get", http.StatusAccepted},
		{"Custom", http.StatusAccepted},
	} {
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		req.Header.Set("Origin", "https://example.test")
		req.Header.Set("Access-Control-Request-Method", tc.method)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		want := ""
		if tc.status == http.StatusNoContent {
			want = "GET, CUSTOM"
		}
		if rec.Code != tc.status || rec.Header().Get("Access-Control-Allow-Methods") != want {
			t.Errorf("%s: status=%d headers=%v", tc.method, rec.Code, rec.Header())
		}
	}
}

func TestCORSAddsRequiredVaryAfterDownstreamReplacement(t *testing.T) {
	const origin = "https://app.example"
	calls := 0
	h := Middleware(CORSOptions{AllowedOrigins: []string{origin}, AllowedMethods: []string{http.MethodGet}})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Vary", "Accept-Encoding")
		w.WriteHeader(http.StatusCreated)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if vary := w.Result().Header.Values("Vary"); countHeaderToken(vary, "Origin") != 1 ||
		!headerValuesContain(vary, "Accept-Encoding") || w.Header().Get("Access-Control-Allow-Origin") != origin ||
		w.Code != http.StatusCreated || calls != 1 {
		t.Fatalf("status=%d headers=%v calls=%d", w.Code, w.Header(), calls)
	}
	req = httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent || calls != 1 {
		t.Fatalf("preflight status=%d calls=%d", w.Code, calls)
	}
}

func TestCORSAllowedOriginStillWorksAfterAnotherOrigin(t *testing.T) {
	const approved = "https://approved.example"
	calls := 0
	handler := Middleware(CORSOptions{AllowedOrigins: []string{approved}, AllowedMethods: []string{http.MethodGet}})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusAccepted)
	}))
	for _, origin := range []string{"https://other.example", approved, approved} {
		r := httptest.NewRequest(http.MethodOptions, "/", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", http.MethodGet)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if origin == approved {
			if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != approved {
				t.Fatalf("approved status=%d headers=%v", w.Code, w.Header())
			}
		} else if w.Code != http.StatusAccepted || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("unapproved status=%d headers=%v", w.Code, w.Header())
		}
	}
	if calls != 1 {
		t.Fatalf("downstream calls=%d", calls)
	}
}

func TestCORSConstructionDetachesMethodsAndExposedHeaders(t *testing.T) {
	methods, headers, exposed := []string{"PUT"}, []string{"X-Fixture"}, []string{"X-Result"}
	origins := []string{"https://example.test"}
	calls := 0
	handler := Middleware(CORSOptions{AllowedOrigins: origins, AllowedMethods: methods, AllowedHeaders: headers, ExposeHeaders: exposed})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(202) }))
	methods[0], headers[0], exposed[0], origins[0] = "GET", "X-Changed", "X-Changed", "https://other.test"
	preflight := httptest.NewRequest("OPTIONS", "/", nil)
	preflight.Header.Set("Origin", "https://example.test")
	preflight.Header.Set("Access-Control-Request-Method", "PUT")
	preflight.Header.Set("Access-Control-Request-Headers", "x-fixture")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, preflight)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Methods") != "PUT" || w.Header().Get("Access-Control-Allow-Headers") != "x-fixture" || calls != 0 {
		t.Fatalf("preflight=%d headers=%v calls=%d", w.Code, w.Header(), calls)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("Origin", "https://example.test")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != 202 || calls != 1 || w.Header().Get("Access-Control-Expose-Headers") != "X-Result" {
		t.Fatalf("simple=%d %v calls=%d", w.Code, w.Header(), calls)
	}
}

func TestPreflightKeepsRequestedHeaderCaseAndOrder(t *testing.T) {
	calls := 0
	handler := Middleware(CORSOptions{
		AllowedOrigins: []string{"https://fixture.example"}, AllowedMethods: []string{"GET"}, AllowedHeaders: []string{"X-First", "X-Second"},
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://fixture.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Add("Access-Control-Request-Headers", "X-Second, x-first")
	req.Header.Add("Access-Control-Request-Headers", "X-FIRST")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 204 || calls != 0 || rec.Header().Get("Access-Control-Allow-Headers") != "X-Second, x-first, X-FIRST" {
		t.Fatalf("status=%d headers=%v calls=%d", rec.Code, rec.Header(), calls)
	}
}

func TestAllowedOriginMergesExistingVaryFields(t *testing.T) {
	const origin = "https://example.test"
	calls := 0
	h := Middleware(CORSOptions{AllowedOrigins: []string{origin}})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Origin", origin)
	for _, prior := range []string{"Accept", "Accept, origin"} {
		w := httptest.NewRecorder()
		w.Header().Add("Vary", prior)
		w.Header().Add("Vary", "Accept-Language")
		h.ServeHTTP(w, r)
		tokens := map[string]int{}
		for _, value := range w.Result().Header.Values("Vary") {
			for _, token := range strings.Split(value, ",") {
				tokens[strings.ToLower(strings.TrimSpace(token))]++
			}
		}
		if tokens["origin"] != 1 || tokens["accept"] != 1 || tokens["accept-language"] != 1 ||
			w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatalf("prior=%q vary=%v response=%v", prior, tokens, w.Result())
		}
	}
	if calls != 2 {
		t.Fatalf("next calls=%d", calls)
	}
}

func TestOptionsNeedsRequestedMethodToHandlePreflight(t *testing.T) {
	const origin = "https://example.test"
	calls := 0
	h := Middleware(CORSOptions{AllowedOrigins: []string{origin}})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusAccepted)
	}))
	for _, want := range []int{http.StatusAccepted, http.StatusNoContent} {
		r := httptest.NewRequest(http.MethodOptions, "/", nil)
		r.Header.Set("Origin", origin)
		if want == http.StatusNoContent {
			r.Header.Set("Access-Control-Request-Method", "GET")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want || w.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatalf("status=%d headers=%v want=%d", w.Code, w.Header(), want)
		}
	}
	if calls != 1 {
		t.Fatalf("next=%d", calls)
	}
}
