package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func generateHealthHandler(dir string, opts GenerateOptions) (*GenerateResult, error) {
	result := &GenerateResult{
		Type:  "health-handler",
		Name:  opts.Name,
		Files: make(map[string][]string),
		Imports: []string{
			"net/http",
			"time",
			"github.com/birdordev/plumego/contract",
			"github.com/birdordev/plumego/health",
		},
	}

	outputPath := opts.OutputPath
	if outputPath == "" {
		name := strings.ToLower(opts.Name)
		if name == "" {
			name = "health"
		}
		outputPath = filepath.Join(dir, "internal", "handler", name+".go")
	}

	packageName := opts.PackageName
	if packageName == "" {
		packageName = "handler"
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

	if err := os.WriteFile(outputPath, []byte(generateHealthHandlerCode(packageName)), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}
	result.Files["created"] = []string{outputPath}

	if opts.WithTests {
		testPath := outputPaths[1]
		if err := os.WriteFile(testPath, []byte(generateHealthHandlerTestCode(packageName)), 0644); err != nil {
			return nil, fmt.Errorf("failed to write test file: %w", err)
		}
		result.Files["created"] = append(result.Files["created"], testPath)
	}

	return result, nil
}

func generateHealthHandlerCode(pkg string) string {
	return fmt.Sprintf(`package %s

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
)

// HandleHealth returns an HTTP handler that reports aggregate service health.
// Wire it at /health or /healthz:
//
//	app.Get("/health", HandleHealth(checker1, checker2))
func HandleHealth(checkers ...health.ComponentChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type componentResult struct {
			Status  health.HealthState `+"`"+`json:"status"`+"`"+`
			Message string             `+"`"+`json:"message,omitempty"`+"`"+`
		}

		type healthResponse struct {
			Status     health.HealthState           `+"`"+`json:"status"`+"`"+`
			Timestamp  time.Time                    `+"`"+`json:"timestamp"`+"`"+`
			Components map[string]componentResult   `+"`"+`json:"components,omitempty"`+"`"+`
		}

		aggregate := health.StatusHealthy
		components := make(map[string]componentResult, len(checkers))

		for _, c := range checkers {
			cr := componentResult{Status: health.StatusHealthy}
			if err := c.Check(r.Context()); err != nil {
				cr.Status = health.StatusUnhealthy
				cr.Message = err.Error()
				aggregate = health.StatusUnhealthy
			}
			components[c.Name()] = cr
		}

		httpStatus := http.StatusOK
		if aggregate == health.StatusUnhealthy {
			httpStatus = http.StatusServiceUnavailable
		}

		_ = contract.WriteResponse(w, r, httpStatus, healthResponse{
			Status:     aggregate,
			Timestamp:  time.Now(),
			Components: components,
		}, nil)
	}
}
`, pkg)
}

func generateHealthHandlerTestCode(pkg string) string {
	return fmt.Sprintf(`package %s

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type alwaysHealthy struct{ name string }

func (c *alwaysHealthy) Name() string                { return c.name }
func (c *alwaysHealthy) Check(_ context.Context) error { return nil }

type alwaysUnhealthy struct{ name string }

func (c *alwaysUnhealthy) Name() string                { return c.name }
func (c *alwaysUnhealthy) Check(_ context.Context) error { return errors.New("unavailable") }

func TestHandleHealth_Healthy(t *testing.T) {
	h := HandleHealth(&alwaysHealthy{name: "db"})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %%d", rec.Code)
	}
}

func TestHandleHealth_Unhealthy(t *testing.T) {
	h := HandleHealth(&alwaysUnhealthy{name: "db"})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %%d", rec.Code)
	}
}

func TestHandleHealth_Mixed(t *testing.T) {
	h := HandleHealth(&alwaysHealthy{name: "cache"}, &alwaysUnhealthy{name: "db"})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when any checker fails, got %%d", rec.Code)
	}
}
`, pkg)
}
