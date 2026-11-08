package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func generateMetricsMiddleware(dir string, opts GenerateOptions) (*GenerateResult, error) {
	result := &GenerateResult{
		Type:  "metrics-middleware",
		Name:  opts.Name,
		Files: make(map[string][]string),
		Imports: []string{
			"net/http",
			"time",
			"github.com/birdordev/plumego/metrics",
		},
	}

	outputPath := opts.OutputPath
	if outputPath == "" {
		name := strings.ToLower(opts.Name)
		if name == "" {
			name = "metrics"
		}
		outputPath = filepath.Join(dir, "internal", "middleware", name+".go")
	}

	packageName := opts.PackageName
	if packageName == "" {
		packageName = "middleware"
	}
	if err := validateGoIdentifier("package", packageName); err != nil {
		return nil, err
	}

	outputPaths := []string{outputPath}
	if opts.WithTests {
		outputPaths = append(outputPaths, strings.TrimSuffix(outputPath, ".go")+"_test.go")
	}
	if err := validateOutputPaths(outputPaths, opts.Force); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(outputPath, []byte(generateMetricsMiddlewareCode(packageName)), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}
	result.Files["created"] = []string{outputPath}

	if opts.WithTests {
		testPath := outputPaths[1]
		if err := os.WriteFile(testPath, []byte(generateMetricsMiddlewareTestCode(packageName)), 0644); err != nil {
			return nil, fmt.Errorf("failed to write test file: %w", err)
		}
		result.Files["created"] = append(result.Files["created"], testPath)
	}

	return result, nil
}

func generateMetricsMiddlewareCode(pkg string) string {
	return fmt.Sprintf(`package %s

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/metrics"
)

// MetricsMiddleware returns transport middleware that records per-request HTTP metrics.
// It accepts the narrow metrics.HTTPObserver interface so any collector implementation works.
// Wire it near the top of your middleware chain, before route-specific middleware:
//
//	app.Use(MetricsMiddleware(deps.Metrics))
func MetricsMiddleware(obs metrics.HTTPObserver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			obs.ObserveHTTP(r.Context(), r.Method, r.URL.Path, rw.status, 0, time.Since(start))
		})
	}
}

// statusRecorder captures the response status code written by the downstream handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}


func (rr *statusRecorder) Unwrap() http.ResponseWriter {
	return rr.ResponseWriter
}

func (rr *statusRecorder) Flush() {
	if f, ok := rr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rr *statusRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}
`, pkg)
}

func generateMetricsMiddlewareTestCode(pkg string) string {
	return fmt.Sprintf(`package %s

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/metrics"
)

type recordingObserver struct {
	method   string
	path     string
	status   int
	duration time.Duration
}

func (o *recordingObserver) ObserveHTTP(_ context.Context, method, path string, status, _ int, duration time.Duration) {
	o.method = method
	o.path = path
	o.status = status
	o.duration = duration
}

var _ metrics.HTTPObserver = (*recordingObserver)(nil)

func TestMetricsMiddleware(t *testing.T) {
	obs := &recordingObserver{}
	handler := MetricsMiddleware(obs)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if obs.method != http.MethodGet {
		t.Errorf("want method GET, got %%s", obs.method)
	}
	if obs.path != "/api/items" {
		t.Errorf("want path /api/items, got %%s", obs.path)
	}
	if obs.status != http.StatusOK {
		t.Errorf("want status 200, got %%d", obs.status)
	}
	if obs.duration <= 0 {
		t.Errorf("want positive duration, got %%v", obs.duration)
	}
}

func TestMetricsMiddleware_CapturesStatus(t *testing.T) {
	obs := &recordingObserver{}
	handler := MetricsMiddleware(obs)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if obs.status != http.StatusNotFound {
		t.Errorf("want status 404, got %%d", obs.status)
	}
}
`, pkg)
}
