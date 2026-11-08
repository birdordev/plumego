package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// generateEndpoint generates the canonical plumego handler shape:
// a func that injects a service interface and returns http.HandlerFunc.
// Unlike generateHandler (struct-based), this follows docs/reference/canonical-style-guide.md.
func generateEndpoint(dir string, opts GenerateOptions) (*GenerateResult, error) {
	result := &GenerateResult{
		Type:    "endpoint",
		Name:    opts.Name,
		Files:   make(map[string][]string),
		Imports: []string{"encoding/json", "net/http", "github.com/birdordev/plumego/contract"},
	}
	outputPath := opts.OutputPath
	if outputPath == "" {
		outputPath = filepath.Join(dir, "internal", "handler", strings.ToLower(opts.Name)+".go")
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
	content := generateEndpointCode(opts.Name, packageName)
	if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}
	result.Files["created"] = []string{outputPath}
	if opts.WithTests {
		testPath := outputPaths[1]
		testContent := generateEndpointTestCode(opts.Name, packageName)
		if err := os.WriteFile(testPath, []byte(testContent), 0644); err != nil {
			return nil, fmt.Errorf("failed to write test file: %w", err)
		}
		result.Files["created"] = append(result.Files["created"], testPath)
	}
	return result, nil
}

func generateEndpointCode(name, pkg string) string {
	lower := strings.ToLower(name)
	return fmt.Sprintf(`package %s

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/birdordev/plumego/contract"
)

// %s is the domain entity for this endpoint group.
type %s struct {
	ID   string `+"`json:\"id\"`"+`
	Name string `+"`json:\"name\"`"+`
}

// Create%sRequest is the request body for creating a %s.
type Create%sRequest struct {
	Name string `+"`json:\"name\"`"+`
}

// %sService defines the operations needed by this endpoint group.
type %sService interface {
	Get%s(ctx context.Context, id string) (*%s, error)
	Create%s(ctx context.Context, req Create%sRequest) (*%s, error)
}

// HandleGet%s returns the canonical GET handler for %s.
// Route wiring: app.Get("/api/v1/%s/{id}", HandleGet%s(deps.%sService))
func HandleGet%s(svc %sService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeValidation).
				Message("id is required").
				Build())
			return
		}
		result, err := svc.Get%s(r.Context(), id)
		if err != nil {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeInternal).
				Message("internal error").
				Build())
			return
		}
		_ = contract.WriteResponse(w, r, http.StatusOK, result, nil)
	}
}

// HandleCreate%s returns the canonical POST handler for %s.
// Route wiring: app.Post("/api/v1/%s", HandleCreate%s(deps.%sService))
func HandleCreate%s(svc %sService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Create%sRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeValidation).
				Message("invalid request body").
				Build())
			return
		}
		result, err := svc.Create%s(r.Context(), req)
		if err != nil {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeInternal).
				Message("internal error").
				Build())
			return
		}
		_ = contract.WriteResponse(w, r, http.StatusCreated, result, nil)
	}
}
`,
		pkg,
		name, name,
		name, lower,
		name,
		name, name,
		name, name,
		name, name, name,
		name, lower, lower, name, name,
		name, name,
		name,
		name, lower, lower, name, name,
		name, name,
		name,
		name,
	)
}

func generateEndpointTestCode(name, pkg string) string {
	lower := strings.ToLower(name)
	return fmt.Sprintf(`package %s_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	%s "github.com/birdordev/plumego/cmd/plumego/internal/handler"
)

type mock%sService struct{}

func (m *mock%sService) Get%s(_ context.Context, id string) (*%s.%s, error) {
	return &%s.%s{ID: id, Name: "stub"}, nil
}

func (m *mock%sService) Create%s(_ context.Context, req %s.Create%sRequest) (*%s.%s, error) {
	return &%s.%s{ID: "1", Name: req.Name}, nil
}

func Test%sHandleGet%s(t *testing.T) {
	svc := &mock%sService{}
	h := %s.HandleGet%s(svc)
	req := httptest.NewRequest(http.MethodGet, "/%s/123", nil)
	req.SetPathValue("id", "123")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %%d: %%s", w.Code, w.Body)
	}
}

func Test%sHandleCreate%s(t *testing.T) {
	svc := &mock%sService{}
	h := %s.HandleCreate%s(svc)
	body := strings.NewReader(`+"`"+`{"name":"test"}`+"`"+`)
	req := httptest.NewRequest(http.MethodPost, "/%s", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %%d: %%s", w.Code, w.Body)
	}
}
`,
		pkg, pkg,
		name,
		name, name, pkg, name,
		pkg, name,
		name, name, pkg, name, pkg, name,
		pkg, name,
		name, name,
		name, pkg, name,
		lower,
		name, name,
		name, pkg, name,
		lower,
	)
}
