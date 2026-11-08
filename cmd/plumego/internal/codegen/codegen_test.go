package codegen

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/birdordev/plumego/cmd/plumego/internal/testassert"
)

func assertParseableGo(t *testing.T, filename string, content string) {
	t.Helper()

	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, filename, content, parser.AllErrors); err != nil {
		t.Errorf("%s parse error: %v\ncontent:\n%s", filename, err, content)
	}
}

func assertContainsAll(t *testing.T, content string, patterns []string) {
	t.Helper()

	for _, pattern := range patterns {
		if !strings.Contains(content, pattern) {
			t.Fatalf("generated code missing required pattern %q:\n%s", pattern, content)
		}
	}
}

func assertContainsNone(t *testing.T, content string, patterns []string) {
	t.Helper()

	for _, pattern := range patterns {
		if strings.Contains(content, pattern) {
			t.Fatalf("generated code contains disallowed pattern %q:\n%s", pattern, content)
		}
	}
}

// TestGenerateMiddlewareCode_NoTODO verifies the middleware template has no // TODO.
func TestGenerateMiddlewareCode_NoTODO(t *testing.T) {
	content := generateMiddlewareCode("Logging", "middleware")
	testassert.NoBareTODO(t, "middleware code", content)
}

// TestGenerateMiddlewareCode_Parseable verifies the middleware template is valid Go.
func TestGenerateMiddlewareCode_Parseable(t *testing.T) {
	content := generateMiddlewareCode("Logging", "middleware")
	assertParseableGo(t, "logging.go", content)
}

// TestGenerateMiddlewareCode_CallsNext verifies generated middleware calls next.ServeHTTP.
func TestGenerateMiddlewareCode_CallsNext(t *testing.T) {
	content := generateMiddlewareCode("Tracing", "middleware")
	if !strings.Contains(content, "next.ServeHTTP") {
		t.Errorf("middleware code does not call next.ServeHTTP:\n%s", content)
	}
}

// TestGenerateHandlerCode_NoTODO verifies the handler template has no // TODO
// for any combination of HTTP methods.
func TestGenerateHandlerCode_NoTODO(t *testing.T) {
	cases := [][]string{
		{"GET"},
		{"POST"},
		{"PUT"},
		{"DELETE"},
		{"GET", "POST"},
		{"GET", "POST", "PUT", "DELETE"},
	}
	for _, methods := range cases {
		content := generateHandlerCode("Order", "handlers", methods)
		testassert.NoBareTODO(t, "handler code for "+strings.Join(methods, ","), content)
	}
}

// TestGenerateHandlerCode_Parseable verifies all handler method combinations produce
// valid Go source.
func TestGenerateHandlerCode_Parseable(t *testing.T) {
	cases := [][]string{
		{"GET"},
		{"POST"},
		{"PUT"},
		{"DELETE"},
		{"GET", "POST", "PUT", "DELETE"},
	}
	for _, methods := range cases {
		content := generateHandlerCode("Product", "handlers", methods)
		assertParseableGo(t, "product.go", content)
	}
}

// TestGenerateHandlerCode_ServiceMethodsPresent verifies the service interface
// contains methods that match the requested HTTP operations.
func TestGenerateHandlerCode_ServiceMethodsPresent(t *testing.T) {
	tests := []struct {
		method string
		wantFn string
	}{
		{"GET", "Get(ctx context.Context"},
		{"POST", "Create(ctx context.Context"},
		{"PUT", "Update(ctx context.Context"},
		{"PATCH", "Patch(ctx context.Context"},
		{"DELETE", "Delete(ctx context.Context"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			content := generateHandlerCode("Widget", "handlers", []string{tt.method})
			if !strings.Contains(content, tt.wantFn) {
				t.Errorf("service interface missing %q:\n%s", tt.wantFn, content)
			}
		})
	}
}

// TestGenerateHandlerCode_HandlerCallsService verifies handler methods call the
// service rather than returning stub values.
func TestGenerateHandlerCode_HandlerCallsService(t *testing.T) {
	tests := []struct {
		method   string
		wantCall string
	}{
		{"GET", "h.Service.Get("},
		{"POST", "h.Service.Create("},
		{"PUT", "h.Service.Update("},
		{"PATCH", "h.Service.Patch("},
		{"DELETE", "h.Service.Delete("},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			content := generateHandlerCode("Widget", "handlers", []string{tt.method})
			if !strings.Contains(content, tt.wantCall) {
				t.Errorf("missing service call %q:\n%s", tt.wantCall, content)
			}
		})
	}
}

// TestGenerateHandlerCode_PATCHIsParseableAndUsesOmitempty verifies the PATCH
// template produces valid Go and uses json:"name,omitempty" to support partial updates.
func TestGenerateHandlerCode_PATCHIsParseableAndUsesOmitempty(t *testing.T) {
	content := generateHandlerCode("Order", "handlers", []string{"PATCH"})
	assertParseableGo(t, "order.go", content)
	if !strings.Contains(content, "omitempty") {
		t.Errorf("PATCH request struct should use omitempty for partial updates:\n%s", content)
	}
	if !strings.Contains(content, "h.Service.Patch(") {
		t.Errorf("PATCH handler should call h.Service.Patch:\n%s", content)
	}
}

// TestGenerateHandlerTestCode_PATCHIsParseable verifies the PATCH test template compiles.
func TestGenerateHandlerTestCode_PATCHIsParseable(t *testing.T) {
	content := generateHandlerTestCode("Order", "handlers", []string{"PATCH"})
	assertParseableGo(t, "order_test.go", content)
	if !strings.Contains(content, "MethodPatch") {
		t.Errorf("PATCH test should use http.MethodPatch:\n%s", content)
	}
}

func TestGenerateHandlerCode_UsesCanonicalHTTPContract(t *testing.T) {
	content := generateHandlerCode("Widget", "handlers", []string{"GET", "POST", "PUT", "DELETE"})

	required := []string{
		`"github.com/birdordev/plumego/contract"`,
		`contract.RequestContextFromContext(r.Context()).Params["id"]`,
		"contract.WriteResponse(",
		"contract.WriteError(",
		"json.NewDecoder(r.Body).Decode",
		"Code(contract.CodeInvalidJSON)",
	}
	assertContainsAll(t, content, required)

	disallowed := []string{
		"PathValue(",
		"http.Error(",
		"json.NewEncoder(w).Encode",
		`w.Header().Set("Content-Type", "application/json")`,
	}
	assertContainsNone(t, content, disallowed)
}

// TestGenerateHandlerTestCode_NoTODO verifies generated test files have no // TODO.
func TestGenerateHandlerTestCode_NoTODO(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "DELETE"}
	content := generateHandlerTestCode("Order", "handlers", methods)
	testassert.NoBareTODO(t, "handler test code", content)
}

// TestGenerateHandlerTestCode_Parseable verifies test files are valid Go.
func TestGenerateHandlerTestCode_Parseable(t *testing.T) {
	cases := [][]string{
		{"GET"},
		{"POST"},
		{"PUT"},
		{"DELETE"},
		{"GET", "POST", "PUT", "DELETE"},
	}
	for _, methods := range cases {
		content := generateHandlerTestCode("Order", "handlers", methods)
		assertParseableGo(t, "order_test.go", content)
	}
}

// TestGenerateHandlerTestCode_InjectsMock verifies the mock service is injected
// into each handler test.
func TestGenerateHandlerTestCode_InjectsMock(t *testing.T) {
	tests := []struct {
		method string
		wantFn string
	}{
		{"GET", "Service: &mockOrderService{}"},
		{"POST", "Service: &mockOrderService{}"},
		{"PUT", "Service: &mockOrderService{}"},
		{"DELETE", "Service: &mockOrderService{}"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			content := generateHandlerTestCode("Order", "handlers", []string{tt.method})
			if !strings.Contains(content, tt.wantFn) {
				t.Errorf("missing mock injection %q:\n%s", tt.wantFn, content)
			}
		})
	}
}

// TestGenerateModelCode_NoTODO verifies the model template has no // TODO.
func TestGenerateModelCode_NoTODO(t *testing.T) {
	for _, withVal := range []bool{false, true} {
		content := generateModelCode("Invoice", "invoice", withVal)
		label := "model code without validation"
		if withVal {
			label = "model code with validation"
		}
		testassert.NoBareTODO(t, label, content)
	}
}

// TestGenerateModelCode_Parseable verifies model code is valid Go.
func TestGenerateModelCode_Parseable(t *testing.T) {
	for _, withVal := range []bool{false, true} {
		content := generateModelCode("Invoice", "invoice", withVal)
		assertParseableGo(t, "invoice.go", content)
	}
}

func TestGenerateDefaultPathsUseCanonicalLayout(t *testing.T) {
	dir := t.TempDir()

	handler, err := Generate(dir, GenerateOptions{
		Type:    "handler",
		Name:    "User",
		Methods: "GET",
	})
	if err != nil {
		t.Fatalf("Generate handler failed: %v", err)
	}
	handlerPath := filepath.Join(dir, "internal", "handler", "user.go")
	if !slicesContains(handler.Files["created"], handlerPath) {
		t.Fatalf("handler path = %#v, want %s", handler.Files["created"], handlerPath)
	}
	if data, err := os.ReadFile(handlerPath); err != nil {
		t.Fatalf("read handler: %v", err)
	} else if !strings.Contains(string(data), "package handler") {
		t.Fatalf("handler should use package handler:\n%s", string(data))
	}

	middleware, err := Generate(dir, GenerateOptions{
		Type: "middleware",
		Name: "Audit",
	})
	if err != nil {
		t.Fatalf("Generate middleware failed: %v", err)
	}
	middlewarePath := filepath.Join(dir, "internal", "middleware", "audit.go")
	if !slicesContains(middleware.Files["created"], middlewarePath) {
		t.Fatalf("middleware path = %#v, want %s", middleware.Files["created"], middlewarePath)
	}
}

func TestGenerateRejectsInvalidInputsBeforeWriting(t *testing.T) {
	tests := []struct {
		name string
		opts GenerateOptions
	}{
		{
			name: "invalid name",
			opts: GenerateOptions{Type: "handler", Name: "bad-name", Methods: "GET"},
		},
		{
			name: "invalid package",
			opts: GenerateOptions{Type: "middleware", Name: "Audit", PackageName: "bad-package"},
		},
		{
			name: "unsupported method",
			opts: GenerateOptions{Type: "handler", Name: "User", Methods: "GET,TRACE"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := Generate(dir, tt.opts); err == nil {
				t.Fatal("expected generation to fail")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("read dir: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("generator wrote files despite invalid input: %#v", entries)
			}
		})
	}
}

func TestGenerateRejectsDirectoryOutputPath(t *testing.T) {
	dir := t.TempDir()
	if _, err := Generate(dir, GenerateOptions{
		Type:       "handler",
		Name:       "User",
		Methods:    "GET",
		OutputPath: dir,
	}); err == nil {
		t.Fatal("expected directory output path to fail")
	}
}

func TestGenerateWithTestsRejectsExistingTestFileWithoutForce(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "internal", "handler", "user.go")
	testPath := strings.TrimSuffix(outputPath, ".go") + "_test.go"
	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		t.Fatalf("mkdir handler dir: %v", err)
	}
	original := []byte("package handler\n\nfunc TestExisting(t *testing.T) {}\n")
	if err := os.WriteFile(testPath, original, 0o644); err != nil {
		t.Fatalf("write existing test file: %v", err)
	}

	if _, err := Generate(dir, GenerateOptions{
		Type:      "handler",
		Name:      "User",
		Methods:   "GET",
		WithTests: true,
	}); err == nil {
		t.Fatal("expected existing generated test file to fail without force")
	}

	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("primary output should not be written after test path validation failure, stat err=%v", err)
	}
	data, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("read existing test file: %v", err)
	}
	if string(data) != string(original) {
		t.Fatalf("existing test file was modified:\n%s", string(data))
	}
}

func TestGenerateWithTestsAllowsExistingTestFileWithForce(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "internal", "middleware", "audit.go")
	testPath := strings.TrimSuffix(outputPath, ".go") + "_test.go"
	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		t.Fatalf("mkdir middleware dir: %v", err)
	}
	if err := os.WriteFile(testPath, []byte("package middleware\n"), 0o644); err != nil {
		t.Fatalf("write existing test file: %v", err)
	}

	if _, err := Generate(dir, GenerateOptions{
		Type:      "middleware",
		Name:      "Audit",
		WithTests: true,
		Force:     true,
	}); err != nil {
		t.Fatalf("Generate with force failed: %v", err)
	}
	data, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("read generated test file: %v", err)
	}
	if !strings.Contains(string(data), "func TestAudit") {
		t.Fatalf("expected test file to be regenerated, got:\n%s", string(data))
	}
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
