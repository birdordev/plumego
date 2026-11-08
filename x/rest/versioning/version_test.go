package versioning

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestStripVersionFromPath(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		pathPrefix   string
		version      int
		expectedPath string
	}{
		{
			name:         "Strip /v2/ from path",
			path:         "/v2/users/123",
			pathPrefix:   "/v",
			version:      2,
			expectedPath: "/users/123",
		},
		{
			name:         "Strip /v3/ from path",
			path:         "/v3/products",
			pathPrefix:   "/v",
			version:      3,
			expectedPath: "/products",
		},
		{
			name:         "Path without version",
			path:         "/users/123",
			pathPrefix:   "/v",
			version:      1,
			expectedPath: "/users/123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripVersionFromPath(tt.path, tt.pathPrefix, tt.version)
			if result != tt.expectedPath {
				t.Errorf("Expected %s, got %s", tt.expectedPath, result)
			}
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
