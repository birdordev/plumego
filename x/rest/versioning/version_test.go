package versioning

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	"github.com/birdordev/plumego/contract"
)

type versioningErrorResponse struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func TestStrategyAcceptHeader(t *testing.T) {
	config := Config{
		Strategy:          StrategyAcceptHeader,
		VendorPrefix:      "application/vnd.myapi",
		DefaultVersion:    1,
		SupportedVersions: []int{1, 2, 3},
	}

	middleware := Middleware(config)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := VersionFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(string(rune('0' + version))))
	}))

	tests := []struct {
		name            string
		acceptHeader    string
		expectedVersion int
	}{
		{
			name:            "Version 2 from Accept header",
			acceptHeader:    "application/vnd.myapi.v2+json",
			expectedVersion: 2,
		},
		{
			name:            "Version 3 from Accept header",
			acceptHeader:    "application/vnd.myapi.v3+json",
			expectedVersion: 3,
		},
		{
			name:            "No version - use default",
			acceptHeader:    "application/json",
			expectedVersion: 1,
		},
		{
			name:            "Empty Accept - use default",
			acceptHeader:    "",
			expectedVersion: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.acceptHeader != "" {
				req.Header.Set("Accept", tt.acceptHeader)
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", w.Code)
			}

			body := w.Body.String()
			expectedBody := string(rune('0' + tt.expectedVersion))
			if body != expectedBody {
				t.Errorf("Expected version %d, got %s", tt.expectedVersion, body)
			}
		})
	}
}

func TestStrategyURLPath(t *testing.T) {
	config := Config{
		Strategy:             StrategyURLPath,
		PathPrefix:           "/v",
		DefaultVersion:       1,
		SupportedVersions:    []int{1, 2, 3},
		StripVersionFromPath: true,
	}

	middleware := Middleware(config)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := VersionFromContext(r.Context())
		path := r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(string(rune('0'+version)) + ":" + path))
	}))

	tests := []struct {
		name            string
		path            string
		expectedVersion int
		expectedPath    string
	}{
		{
			name:            "Version 2 from path",
			path:            "/v2/users/123",
			expectedVersion: 2,
			expectedPath:    "/users/123",
		},
		{
			name:            "Version 3 from path",
			path:            "/v3/products",
			expectedVersion: 3,
			expectedPath:    "/products",
		},
		{
			name:            "No version - use default",
			path:            "/users",
			expectedVersion: 1,
			expectedPath:    "/users",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", w.Code)
			}

			body := w.Body.String()
			expectedBody := string(rune('0'+tt.expectedVersion)) + ":" + tt.expectedPath
			if body != expectedBody {
				t.Errorf("Expected %s, got %s", expectedBody, body)
			}
		})
	}
}

func TestStrategyQueryParam(t *testing.T) {
	config := Config{
		Strategy:          StrategyQueryParam,
		ParamName:         "version",
		DefaultVersion:    1,
		SupportedVersions: []int{1, 2, 3},
	}

	middleware := Middleware(config)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := VersionFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(string(rune('0' + version))))
	}))

	tests := []struct {
		name            string
		url             string
		expectedVersion int
	}{
		{
			name:            "Version 2 from query param",
			url:             "/test?version=2",
			expectedVersion: 2,
		},
		{
			name:            "Version 3 from query param",
			url:             "/test?version=3",
			expectedVersion: 3,
		},
		{
			name:            "No version - use default",
			url:             "/test",
			expectedVersion: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", w.Code)
			}

			body := w.Body.String()
			expectedBody := string(rune('0' + tt.expectedVersion))
			if body != expectedBody {
				t.Errorf("Expected version %d, got %s", tt.expectedVersion, body)
			}
		})
	}
}

func TestStrategyCustomHeader(t *testing.T) {
	config := Config{
		Strategy:          StrategyCustomHeader,
		HeaderName:        "X-API-Version",
		DefaultVersion:    1,
		SupportedVersions: []int{1, 2, 3},
	}

	middleware := Middleware(config)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := VersionFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(string(rune('0' + version))))
	}))

	tests := []struct {
		name            string
		headerValue     string
		expectedVersion int
	}{
		{
			name:            "Version 2 from header",
			headerValue:     "2",
			expectedVersion: 2,
		},
		{
			name:            "Version 3 from header",
			headerValue:     "3",
			expectedVersion: 3,
		},
		{
			name:            "No header - use default",
			headerValue:     "",
			expectedVersion: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.headerValue != "" {
				req.Header.Set("X-API-Version", tt.headerValue)
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", w.Code)
			}

			body := w.Body.String()
			expectedBody := string(rune('0' + tt.expectedVersion))
			if body != expectedBody {
				t.Errorf("Expected version %d, got %s", tt.expectedVersion, body)
			}
		})
	}
}

func TestUnsupportedVersionUsesVersioningCode(t *testing.T) {
	handler := Middleware(Config{
		DefaultVersion:    1,
		SupportedVersions: []int{2},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotAcceptable {
		t.Fatalf("expected status %d, got %d", http.StatusNotAcceptable, rec.Code)
	}

	var payload versioningErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if got := payload.Error.Code; got != contract.CodeNotAcceptable {
		t.Fatalf("expected code %q, got %q", contract.CodeNotAcceptable, got)
	}
}

func TestUnsupportedVersion(t *testing.T) {
	config := Config{
		Strategy:          StrategyCustomHeader,
		HeaderName:        "X-API-Version",
		DefaultVersion:    1,
		SupportedVersions: []int{1, 2, 3},
	}

	middleware := Middleware(config)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Version", "99") // Unsupported version

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotAcceptable {
		t.Errorf("Expected status 406, got %d", w.Code)
	}
}

func TestCustomExtractor(t *testing.T) {
	// Custom extractor that reads version from subdomain
	extractor := func(r *http.Request) int {
		host := r.Host
		if host == "v2.api.example.com" {
			return 2
		}
		if host == "v3.api.example.com" {
			return 3
		}
		return 0
	}

	middleware := CustomExtractor(extractor, 1, []int{1, 2, 3})

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := VersionFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(string(rune('0' + version))))
	}))

	tests := []struct {
		name            string
		host            string
		expectedVersion int
	}{
		{
			name:            "Version 2 from subdomain",
			host:            "v2.api.example.com",
			expectedVersion: 2,
		},
		{
			name:            "Version 3 from subdomain",
			host:            "v3.api.example.com",
			expectedVersion: 3,
		},
		{
			name:            "Default version",
			host:            "api.example.com",
			expectedVersion: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tt.host+"/test", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", w.Code)
			}

			body := w.Body.String()
			expectedBody := string(rune('0' + tt.expectedVersion))
			if body != expectedBody {
				t.Errorf("Expected version %d, got %s", tt.expectedVersion, body)
			}
		})
	}
}

func TestURLPathVersionSegment(t *testing.T) {
	for _, tc := range []struct {
		target  string
		prefix  string
		version int
		path    string
		escaped string
	}{
		{"/v2", "/v", 2, "/", "/"},
		{"/v2/items", "/v", 2, "/items", "/items"},
		{"/v2report", "/v", 1, "/v2report", "/v2report"},
		{"/v02/items/a%2Fb", "/v", 2, "/items/a/b", "/items/a%2Fb"},
		{"/v2%2Fitems", "/v", 1, "/v2/items", "/v2%2Fitems"},
		{"/api-%30%32/items", "/api-", 2, "/items", "/items"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			h := Middleware(Config{Strategy: StrategyURLPath, PathPrefix: tc.prefix})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				if v := VersionFromContext(r.Context()); v != tc.version || r.URL.Path != tc.path || r.URL.EscapedPath() != tc.escaped {
					t.Errorf("version=%d path=%q escaped=%q, want %d %q %q", v, r.URL.Path, r.URL.EscapedPath(), tc.version, tc.path, tc.escaped)
				}
			}))
			h.ServeHTTP(httptest.NewRecorder(), req)
		})
	}
}

func TestResponseHeaders(t *testing.T) {
	config := Config{
		Strategy:       StrategyCustomHeader,
		HeaderName:     "X-API-Version",
		DefaultVersion: 1,
	}

	middleware := Middleware(config)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Version", "2")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Check that response includes version header
	versionHeader := w.Header().Get("X-API-Version")
	if versionHeader != "2" {
		t.Errorf("Expected X-API-Version: 2 in response, got %s", versionHeader)
	}
}

func TestVersionConfigOwnsSupportedVersions(t *testing.T) {
	for _, tc := range []struct {
		name           string
		makeMiddleware func([]int) func(http.Handler) http.Handler
	}{
		{"Middleware", func(versions []int) func(http.Handler) http.Handler {
			return Middleware(Config{Strategy: StrategyCustomHeader, DefaultVersion: 2, SupportedVersions: versions})
		}},
		{"CustomExtractor", func(versions []int) func(http.Handler) http.Handler {
			return CustomExtractor(func(r *http.Request) int {
				return extractFromHeader(r, "X-API-Version")
			}, 2, versions)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := func(versions []int, mutate func()) {
				t.Helper()
				wrap := tc.makeMiddleware(versions)
				mutate()
				next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				})
				handler := wrap(next)
				for _, query := range []struct {
					version string
					want    int
				}{
					{"", http.StatusNoContent},
					{"2", http.StatusNoContent},
					{"3", http.StatusNotAcceptable},
				} {
					req := httptest.NewRequest(http.MethodGet, "/", nil)
					if query.version != "" {
						req.Header.Set("X-API-Version", query.version)
					}
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, req)
					want := query.want
					if len(versions) == 0 {
						want = http.StatusNoContent
					}
					if w.Code != want {
						t.Errorf("version %q: status = %d, want %d", query.version, w.Code, want)
					}
					if want == http.StatusNotAcceptable {
						var response versioningErrorResponse
						if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Error.Code != contract.CodeNotAcceptable {
							t.Errorf("rejection error = %s, decode error = %v", w.Body.String(), err)
						}
					}
				}
			}
			versions := []int{2}
			check(versions, func() { versions[0] = 3 })
			check(nil, func() {})
			check([]int{}, func() {})
		})
	}
}

func TestStrategyAcceptHeaderMultipleLines(t *testing.T) {
	for _, tt := range []struct {
		name    string
		accept  []string
		version int
		status  int
	}{
		{"version on second line", []string{"application/json", "application/vnd.api.v2+json"}, 2, http.StatusNoContent},
		{"combined field", []string{"application/json, application/vnd.api.v2+json"}, 2, http.StatusNoContent},
		{"first match wins", []string{"application/vnd.api.v3+json", "application/vnd.api.v2+json"}, 3, http.StatusNoContent},
		{"first match without suffix", []string{"application/vnd.api.v3 ", "application/vnd.api.v2+json"}, 3, http.StatusNoContent},
		{"unsupported version on later line", []string{"application/json", "application/vnd.api.v4+json"}, 0, http.StatusNotAcceptable},
		{"no matching version", []string{"application/json", "text/plain"}, 1, http.StatusNoContent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			seen := 0
			h := Middleware(Config{SupportedVersions: []int{1, 2, 3}})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = VersionFromContext(r.Context())
				w.WriteHeader(http.StatusNoContent)
			}))
			r := httptest.NewRequest(http.MethodGet, "/items", nil)
			for _, value := range tt.accept {
				r.Header.Add("Accept", value)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			wantHeader := ""
			if tt.version != 0 {
				wantHeader = strconv.Itoa(tt.version)
			}
			if header := w.Header().Get("X-API-Version"); w.Code != tt.status || seen != tt.version || header != wantHeader {
				t.Errorf("status=%d handler version=%d header=%q; want %d, %d, %q", w.Code, seen, header, tt.status, tt.version, wantHeader)
			}
		})
	}
}

func TestAcceptVersionResponsesVaryByAccept(t *testing.T) {
	for _, tt := range []struct {
		name       string
		strategy   Strategy
		accept     string
		vary, want []string
		status     int
	}{
		{"match", StrategyAcceptHeader, "application/vnd.api.v2+json", []string{"Origin", "DPR"}, []string{"Origin", "DPR", "Accept"}, http.StatusNoContent},
		{"default", StrategyAcceptHeader, "", nil, []string{"Accept"}, http.StatusNoContent},
		{"rejected", StrategyAcceptHeader, "application/vnd.api.v3+json", []string{"Origin"}, []string{"Origin", "Accept"}, http.StatusNotAcceptable},
		{"present", StrategyAcceptHeader, "application/vnd.api.v2+json", []string{"Origin, accept"}, []string{"Origin, accept"}, http.StatusNoContent},
		{"wildcard", StrategyAcceptHeader, "", []string{"*"}, []string{"*"}, http.StatusNoContent},
		{"query strategy", StrategyQueryParam, "", []string{"Origin"}, []string{"Origin"}, http.StatusNoContent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := Middleware(Config{Strategy: tt.strategy, SupportedVersions: []int{1, 2}})(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
			)
			r := httptest.NewRequest(http.MethodGet, "/items", nil)
			r.Header.Set("Accept", tt.accept)
			w := httptest.NewRecorder()
			for _, value := range tt.vary {
				w.Header().Add("Vary", value)
			}
			h.ServeHTTP(w, r)
			if got := w.Result().Header.Values("Vary"); w.Code != tt.status || !slices.Equal(got, tt.want) {
				t.Errorf("status = %d, Vary = %v; want %d, %v", w.Code, got, tt.status, tt.want)
			}
		})
	}
}

func TestAcceptVersionMergesVaryAtCommit(t *testing.T) {
	h := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Vary", "Origin")
		w.WriteHeader(http.StatusNoContent)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/items", nil))
	if got := w.Result().Header.Values("Vary"); !slices.Equal(got, []string{"Origin", "Accept"}) {
		t.Errorf("Vary = %v, want both Origin and Accept", got)
	}
}

func TestAcceptVersionUsesMediaTypeOnly(t *testing.T) {
	for _, tt := range []struct {
		name   string
		accept []string
		want   int
	}{
		{"media parameter", []string{`text/plain; note="application/vnd.api.v2+json"`}, 1},
		{"quoted comma and escaped quote", []string{`text/plain; note="escaped \",application/vnd.api.v2+json,still quoted", application/vnd.api.v3+json`}, 3},
		{"prefix lookalike", []string{"aapplication/vnd.api.v2+json"}, 1},
		{"version on next line", []string{`text/plain; note="application/vnd.api.v2+json"`, "Application/Vnd.Api.V12+JSON; charset=utf-8"}, 12},
		{"first type wins", []string{"application/vnd.api.v3; q=0.1, application/vnd.api.v2+json"}, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			seen := 0
			h := Middleware(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = VersionFromContext(r.Context())
				w.WriteHeader(http.StatusNoContent)
			}))
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, value := range tt.accept {
				r.Header.Add("Accept", value)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusNoContent || seen != tt.want || w.Header().Get("X-API-Version") != strconv.Itoa(tt.want) ||
				w.Header().Get("Vary") != "Accept" {
				t.Fatalf("status=%d context version=%d headers=%v, want version %d", w.Code, seen, w.Header(), tt.want)
			}
		})
	}
}

func TestCustomExtractorWithoutCallbackUsesDefault(t *testing.T) {
	for _, tc := range []struct {
		supported int
		status    int
		calls     int
	}{
		{2, http.StatusNoContent, 1}, {3, http.StatusNotAcceptable, 0},
	} {
		calls := 0
		h := CustomExtractor(nil, 2, []int{tc.supported})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if got := VersionFromContext(r.Context()); got != 2 {
				t.Errorf("version=%d", got)
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		out := httptest.NewRecorder()
		h.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/", nil))
		if calls != tc.calls || out.Code != tc.status {
			t.Errorf("calls=%d, status=%d", calls, out.Code)
		}
		if tc.calls == 1 && out.Header().Get("X-API-Version") != "2" {
			t.Error("default version header missing")
		}
	}
}

func TestMiddlewareVersionCallbacksOwnTheirBranches(t *testing.T) {
	var calls []string
	handler := Middleware(Config{
		Strategy:          StrategyURLPath,
		SupportedVersions: []int{2},
		OnVersionMismatch: func(w http.ResponseWriter, r *http.Request, version int) {
			calls = append(calls, "mismatch")
			if version != 3 || r.URL.Path != "/v3/items" {
				t.Errorf("mismatch received version=%d path=%q", version, r.URL.Path)
			}
			w.WriteHeader(http.StatusGone)
		},
		OnVersionExtracted: func(r *http.Request, version int) {
			calls = append(calls, "extracted")
			if version != 2 || r.URL.Path != "/items" || VersionFromContext(r.Context()) != 0 {
				t.Errorf("extracted received version=%d path=%q context version=%d", version, r.URL.Path, VersionFromContext(r.Context()))
			}
		},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "next")
		if r.URL.Path != "/items" || VersionFromContext(r.Context()) != 2 {
			t.Errorf("next received path=%q version=%d", r.URL.Path, VersionFromContext(r.Context()))
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	out := httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/v3/items", nil))
	if out.Code != http.StatusGone || !slices.Equal(calls, []string{"mismatch"}) {
		t.Errorf("unsupported version status=%d callbacks=%v", out.Code, calls)
	}
	calls = nil
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/v2/items", nil))
	if out.Code != http.StatusNoContent || out.Header().Get("X-API-Version") != "2" ||
		!slices.Equal(calls, []string{"extracted", "next"}) {
		t.Errorf("supported version status=%d header=%q callbacks=%v", out.Code, out.Header().Get("X-API-Version"), calls)
	}
}

func TestUnknownVersionStrategyUsesConfiguredDefault(t *testing.T) {
	for _, tc := range []struct {
		name      string
		supported []int
		status    int
		calls     int
	}{
		{"supported default", []int{3}, http.StatusNoContent, 1},
		{"unsupported default", []int{2}, http.StatusNotAcceptable, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handler := Middleware(Config{
				Strategy: Strategy(1234), SupportedVersions: tc.supported, DefaultVersion: 3,
			})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if got := VersionFromContext(r.Context()); got != 3 {
					t.Errorf("downstream version=%d; want 3", got)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/items", nil))
			if out.Code != tc.status || calls != tc.calls {
				t.Errorf("status=%d calls=%d; want %d and %d", out.Code, calls, tc.status, tc.calls)
			}
			if tc.calls == 1 && out.Header().Get("X-API-Version") != "3" {
				t.Errorf("version header = %q", out.Header().Get("X-API-Version"))
			}
			if tc.calls == 0 {
				var result versioningErrorResponse
				if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil || result.Error.Code != contract.CodeNotAcceptable {
					t.Errorf("rejection body=%q error=%v", out.Body, err)
				}
			}
		})
	}
}
