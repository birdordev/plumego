package devserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/birdordev/plumego/x/observability/devtools"
)

const (
	defaultAnalyzerHTTPTimeout = 2 * time.Second
	probeAnalyzerHTTPTimeout   = 500 * time.Millisecond
	maxAnalyzerResponseBytes   = 10 * 1024 * 1024
)

// RouteInfo represents information about a route
type RouteInfo struct {
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Handler     string   `json:"handler,omitempty"`
	Middleware  []string `json:"middleware,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Analyzer analyzes the running application
type Analyzer struct {
	appURL       string
	client       *http.Client
	probeClient  *http.Client
	maxBodyBytes int64
}

// NewAnalyzer creates a new analyzer
func NewAnalyzer(appURL string) *Analyzer {
	return &Analyzer{
		appURL: strings.TrimRight(appURL, "/"),
		client: &http.Client{
			Timeout: defaultAnalyzerHTTPTimeout,
		},
		probeClient: &http.Client{
			Timeout: probeAnalyzerHTTPTimeout,
		},
		maxBodyBytes: maxAnalyzerResponseBytes,
	}
}

// GetRoutes attempts to fetch routes from the running application
func (a *Analyzer) GetRoutes() ([]RouteInfo, error) {
	// Try to fetch from plumego debug endpoint (JSON format)
	resp, err := a.httpClient().Get(a.urlForPath("/_debug/routes.json"))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch routes (app may not be running): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("debug endpoint returned status %d", resp.StatusCode)
	}

	// Parse response - plumego returns routes in data.routes format
	var response struct {
		Data struct {
			Routes []struct {
				Method string         `json:"method"`
				Path   string         `json:"path"`
				Meta   map[string]any `json:"meta"`
			} `json:"routes"`
		} `json:"data"`
	}

	if err := decodeAnalyzerJSON(resp.Body, a.responseLimit(), &response); err != nil {
		return nil, fmt.Errorf("failed to parse routes: %w", err)
	}

	// Convert to RouteInfo format
	var routes []RouteInfo
	for _, r := range response.Data.Routes {
		routes = append(routes, RouteInfo{
			Method: r.Method,
			Path:   r.Path,
		})
	}

	return routes, nil
}

// ProbeEndpoints probes common endpoints to discover routes
func (a *Analyzer) ProbeEndpoints() []RouteInfo {
	commonPaths := []string{
		"/",
		"/health",
		"/ping",
		"/api",
		"/api/status",
		"/api/health",
		"/_debug/routes",
		"/_debug/config",
		"/_debug/middleware",
	}

	var discovered []RouteInfo
	client := a.probeHTTPClient()

	for _, path := range commonPaths {
		// Try HEAD first (lightweight)
		req, _ := http.NewRequest("HEAD", a.urlForPath(path), nil)
		resp, err := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}

		if err == nil && resp.StatusCode < 500 {
			discovered = append(discovered, RouteInfo{
				Method:      "HEAD",
				Path:        path,
				Description: fmt.Sprintf("Discovered (status: %d)", resp.StatusCode),
			})
		}
	}

	return discovered
}

// GetAppSnapshot fetches the application's devtools config/runtime snapshot.
func (a *Analyzer) GetAppSnapshot() (devtools.ConfigSnapshot, error) {
	resp, err := a.httpClient().Get(a.urlForPath("/_debug/config"))
	if err != nil {
		return devtools.ConfigSnapshot{}, fmt.Errorf("failed to fetch config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return devtools.ConfigSnapshot{}, fmt.Errorf("config endpoint returned status %d", resp.StatusCode)
	}

	var payload struct {
		Data devtools.ConfigSnapshot `json:"data"`
	}
	if err := decodeAnalyzerJSON(resp.Body, a.responseLimit(), &payload); err != nil {
		return devtools.ConfigSnapshot{}, fmt.Errorf("failed to parse config: %w", err)
	}

	return payload.Data, nil
}

// HealthCheck checks if the application is healthy
func (a *Analyzer) HealthCheck() (bool, map[string]any, error) {
	resp, err := a.httpClient().Get(a.urlForPath("/health"))
	if err != nil {
		return false, nil, fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	healthy := resp.StatusCode == http.StatusOK

	var details map[string]any
	payload, err := readAnalyzerBody(resp.Body, a.responseLimit())
	if err != nil {
		return false, nil, fmt.Errorf("failed to read health response: %w", err)
	}
	if len(payload) == 0 || json.Unmarshal(payload, &details) != nil {
		// Health endpoint might not return JSON
		details = map[string]any{
			"status": resp.Status,
		}
	}

	return healthy, details, nil
}

// DevMetricsSnapshot contains dev HTTP + DB metrics.
type DevMetricsSnapshot struct {
	HTTP devtools.DevHTTPSnapshot `json:"http"`
	DB   devtools.DevDBSnapshot   `json:"db"`
}

// GetDevMetrics fetches dev metrics from the application.
func (a *Analyzer) GetDevMetrics() (*DevMetricsSnapshot, error) {
	resp, err := a.httpClient().Get(a.urlForPath("/_debug/metrics"))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics endpoint returned status %d", resp.StatusCode)
	}

	var payload struct {
		Enabled bool                     `json:"enabled"`
		HTTP    devtools.DevHTTPSnapshot `json:"http"`
		DB      devtools.DevDBSnapshot   `json:"db"`
	}

	if err := decodeAnalyzerJSON(resp.Body, a.responseLimit(), &payload); err != nil {
		return nil, fmt.Errorf("failed to parse metrics: %w", err)
	}

	if !payload.Enabled {
		return nil, fmt.Errorf("dev metrics disabled")
	}

	return &DevMetricsSnapshot{
		HTTP: payload.HTTP,
		DB:   payload.DB,
	}, nil
}

func (a *Analyzer) httpClient() *http.Client {
	if a.client != nil {
		return a.client
	}
	return &http.Client{Timeout: defaultAnalyzerHTTPTimeout}
}

func (a *Analyzer) probeHTTPClient() *http.Client {
	if a.probeClient != nil {
		return a.probeClient
	}
	return &http.Client{Timeout: probeAnalyzerHTTPTimeout}
}

func (a *Analyzer) responseLimit() int64 {
	if a.maxBodyBytes > 0 {
		return a.maxBodyBytes
	}
	return maxAnalyzerResponseBytes
}

func (a *Analyzer) urlForPath(path string) string {
	if strings.HasPrefix(path, "/") {
		return strings.TrimRight(a.appURL, "/") + path
	}
	return strings.TrimRight(a.appURL, "/") + "/" + path
}

func decodeAnalyzerJSON(body io.Reader, limit int64, out any) error {
	payload, err := readAnalyzerBody(body, limit)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return err
	}
	return nil
}

func readAnalyzerBody(body io.Reader, limit int64) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > limit {
		return nil, fmt.Errorf("response body exceeds %d bytes", limit)
	}
	return payload, nil
}
