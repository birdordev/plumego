package scaffold

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/birdordev/plumego/cmd/plumego/internal/testassert"
)

var allTemplates = []string{
	"minimal",
	"api",
	"rest-api",
	"tenant-api",
	"gateway",
	"realtime",
	"ai-service",
	"ops-service",
	"fullstack",
	"microservice",
	"canonical",
}

func assertParseableGo(t *testing.T, filename string, content string) {
	t.Helper()

	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, filename, content, parser.AllErrors); err != nil {
		t.Errorf("%s parse error: %v\ncontent:\n%s", filename, err, content)
	}
}

func requiresMainPackage(file string) bool {
	return strings.HasPrefix(file, "cmd/") && filepath.Ext(file) == ".go"
}

func assertFileOmitted(t *testing.T, files []string, file string, reason string) {
	t.Helper()

	if slices.Contains(files, file) {
		t.Fatalf("%s should not emit %s", reason, file)
	}
}

func assertContainsAll(t *testing.T, content string, patterns []string) {
	t.Helper()

	for _, pattern := range patterns {
		if !strings.Contains(content, pattern) {
			t.Fatalf("content missing required pattern %q:\n%s", pattern, content)
		}
	}
}

func assertContainsNone(t *testing.T, content string, patterns []string) {
	t.Helper()

	for _, pattern := range patterns {
		if strings.Contains(content, pattern) {
			t.Fatalf("content contains disallowed pattern %q:\n%s", pattern, content)
		}
	}
}

// TestGetTemplateFiles_NoEmpty verifies every template returns at least one file.
func TestGetTemplateFiles_NoEmpty(t *testing.T) {
	for _, tmpl := range allTemplates {
		t.Run(tmpl, func(t *testing.T) {
			files := GetTemplateFiles(tmpl)
			if len(files) == 0 {
				t.Errorf("template %q returned no files", tmpl)
			}
		})
	}
}

// TestTemplateContent_NoTODO verifies no generated file contains a bare // TODO comment.
func TestTemplateContent_NoTODO(t *testing.T) {
	const (
		testName   = "myapp"
		testModule = "example.com/myapp"
	)
	for _, tmpl := range allTemplates {
		t.Run(tmpl, func(t *testing.T) {
			files := GetTemplateFiles(tmpl)
			for _, file := range files {
				content := getTemplateContent(file, testName, testModule, tmpl)
				testassert.NoBareTODO(t, "template="+tmpl+" file="+file, content)
			}
		})
	}
}

// TestTemplateContent_GoFilesParseable verifies all generated .go files are
// syntactically valid Go source.
func TestTemplateContent_GoFilesParseable(t *testing.T) {
	const (
		testName   = "myapp"
		testModule = "example.com/myapp"
	)
	for _, tmpl := range allTemplates {
		t.Run(tmpl, func(t *testing.T) {
			files := GetTemplateFiles(tmpl)
			for _, file := range files {
				if filepath.Ext(file) != ".go" {
					continue
				}
				content := getTemplateContent(file, testName, testModule, tmpl)
				if content == "" {
					continue
				}
				assertParseableGo(t, file, content)
			}
		})
	}
}

// TestTemplateContent_CorrectPackageNames verifies Go files declare a package
// name that matches their directory name (or "main" for cmd/ paths).
func TestTemplateContent_CorrectPackageNames(t *testing.T) {
	const (
		testName   = "myapp"
		testModule = "example.com/myapp"
	)
	fset := token.NewFileSet()
	for _, tmpl := range allTemplates {
		files := GetTemplateFiles(tmpl)
		for _, file := range files {
			if filepath.Ext(file) != ".go" {
				continue
			}
			content := getTemplateContent(file, testName, testModule, tmpl)
			if content == "" {
				continue
			}
			f, err := parser.ParseFile(fset, file, content, 0)
			if err != nil {
				continue // parse errors caught elsewhere
			}
			pkg := f.Name.Name
			if pkg == "" {
				t.Errorf("template=%q file=%q has empty package name", tmpl, file)
				continue
			}
			if requiresMainPackage(file) {
				if pkg != "main" {
					t.Errorf("template=%q file=%q: expected package main for cmd path, got %q",
						tmpl, file, pkg)
				}
			}
		}
	}
}

func TestTemplateContent_DoesNotFallbackToLegacyHTTPApp(t *testing.T) {
	legacyFiles := []string{
		"internal/httpapp/app.go",
		"internal/httpapp/routes.go",
		"internal/httpapp/handlers/user.go",
		"internal/domain/user/service.go",
	}

	for _, file := range legacyFiles {
		content := getTemplateContent(file, "myapp", "example.com/myapp", "canonical")
		if content != "" {
			t.Fatalf("legacy fallback file %q should not generate content:\n%s", file, content)
		}
	}

	mainContent := getTemplateContent("cmd/app/main.go", "myapp", "example.com/myapp", "unknown-template")
	assertContainsAll(t, mainContent, []string{"func main()", "func run() error"})
	assertContainsNone(t, mainContent, []string{"log.Fatal(", "http.ListenAndServe("})
}

func TestGetTemplateFiles_MicroserviceDoesNotEmitLegacyHTTPHelpers(t *testing.T) {
	files := GetTemplateFiles("microservice")
	assertFileOmitted(t, files, "internal/platform/httpjson/response.go", "microservice template")
	assertFileOmitted(t, files, "internal/platform/httperr/error.go", "microservice template")
}

func TestTemplateContent_UsesCanonicalHTTPContract(t *testing.T) {
	const (
		testName   = "myapp"
		testModule = "example.com/myapp"
	)

	disallowed := []string{
		"internal/platform/httpjson",
		"internal/platform/httperr",
		"PathValue(",
		"http.Error(",
		"http.ListenAndServe(",
		"log.Fatal(",
		"log.Fatalf(",
		"json.NewEncoder(w).Encode",
		`w.Header().Set("Content-Type", "application/json")`,
		`"encoding error"`,
	}

	for _, tmpl := range allTemplates {
		files := GetTemplateFiles(tmpl)
		for _, file := range files {
			if filepath.Ext(file) != ".go" {
				continue
			}
			content := getTemplateContent(file, testName, testModule, tmpl)
			assertContainsNone(t, content, disallowed)
		}
	}
}

func TestCanonicalAppTemplate_HandlesStartupErrorsExplicitly(t *testing.T) {
	content := getTemplateContent("internal/app/app.go", "myapp", "example.com/myapp", "canonical")

	assertContainsAll(t, content, []string{
		`"errors"`,
		`"net/http"`,
		`if err := a.Use(`,
		`return nil, fmt.Errorf("register middleware: %w", err)`,
		`func (a *App) Start(ctx context.Context) (err error)`,
		`shutdownErr := make(chan error, 1)`,
		`errors.Is(err, http.ErrServerClosed)`,
		`return fmt.Errorf("shutdown server: %w", err)`,
	})
}

func TestAPITemplate_UsesCanonicalBootstrapWithRestProfile(t *testing.T) {
	files := GetTemplateFiles("api")
	assertContainsAll(t, strings.Join(files, "\n"), []string{
		"internal/app/app.go",
		"internal/app/routes.go",
		"internal/handler/api.go",
		"internal/handler/health.go",
		"internal/config/config.go",
		"internal/resource/users.go",
	})
	assertContainsNone(t, strings.Join(files, "\n"), []string{
		"internal/httpapp/",
		"internal/domain/user/",
	})

	routes := getTemplateContent("internal/app/routes.go", "myapp", "example.com/myapp", "api")
	assertContainsAll(t, routes, []string{
		`"github.com/birdordev/plumego/x/rest"`,
		`rest.DefaultResourceSpec("users").WithPrefix("/api/users")`,
		`rest.NewDBResource[resource.User](spec, resource.NewUserRepository())`,
		`a.Core.Get(spec.Prefix, http.HandlerFunc(users.Index))`,
		`a.Core.Get(spec.Prefix+"/:id", http.HandlerFunc(users.Show))`,
		`a.Core.Post(spec.Prefix, http.HandlerFunc(users.Create))`,
	})
}

func TestScenarioProfiles_GenerateRunnableRoutes(t *testing.T) {
	restRoutes := getTemplateContent("internal/app/routes.go", "myapp", "example.com/myapp", "rest-api")
	assertContainsAll(t, restRoutes, []string{
		`"github.com/birdordev/plumego/x/rest"`,
		`rest.NewDBResource[resource.User](spec, resource.NewUserRepository())`,
		`a.Core.Get(spec.Prefix, http.HandlerFunc(users.Index))`,
		`a.Core.Post(spec.Prefix, http.HandlerFunc(users.Create))`,
	})

	tenantRoutes := getTemplateContent("internal/app/routes.go", "myapp", "example.com/myapp", "tenant-api")
	assertContainsAll(t, tenantRoutes, []string{
		`"github.com/birdordev/plumego/middleware"`,
		`"github.com/birdordev/plumego/x/tenant/resolve"`,
		`"github.com/birdordev/plumego/x/tenant/policy"`,
		`"github.com/birdordev/plumego/x/tenant/quota"`,
		`"github.com/birdordev/plumego/x/tenant/ratelimit"`,
		`tenantChain := middleware.NewChain(`,
		`a.Core.Get("/api/models", tenantChain.Build(http.HandlerFunc(models)))`,
		`tenantcore.TenantIDFromContext(r.Context())`,
	})

	gatewayRoutes := getTemplateContent("internal/app/routes.go", "myapp", "example.com/myapp", "gateway")
	assertContainsAll(t, gatewayRoutes, []string{
		`"github.com/birdordev/plumego/x/gateway"`,
		`gateway.NewGateway(gateway.GatewayConfig{`,
		`PathRewrite: gateway.ReplacePrefix("/edge", "/api/status")`,
		`a.Core.Get("/edge", proxy)`,
	})

	realtimeRoutes := getTemplateContent("internal/app/routes.go", "myapp", "example.com/myapp", "realtime")
	assertContainsAll(t, realtimeRoutes, []string{
		`"github.com/birdordev/plumego/x/websocket"`,
		`hub, err := websocket.NewHub(4, 1024)`,
		`a.Core.Get("/realtime/metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request)`,
		`hub.Metrics()`,
	})

	aiRoutes := getTemplateContent("internal/app/routes.go", "myapp", "example.com/myapp", "ai-service")
	assertContainsAll(t, aiRoutes, []string{
		`"github.com/birdordev/plumego/x/ai/provider"`,
		`"github.com/birdordev/plumego/x/ai/session"`,
		`"github.com/birdordev/plumego/x/ai/tool"`,
		`provider.NewMockProvider("offline")`,
		`session.NewManager(session.NewMemoryStorage())`,
		`tools.Register(tool.NewEchoTool())`,
		`a.Core.Get("/ai/demo", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request)`,
	})

	opsRoutes := getTemplateContent("internal/app/routes.go", "myapp", "example.com/myapp", "ops-service")
	assertContainsAll(t, opsRoutes, []string{
		`"github.com/birdordev/plumego/x/observability"`,
		`"github.com/birdordev/plumego/x/observability/ops"`,
		`observability.NewPrometheusCollector("app")`,
		`auth.Authenticate(authn.StaticToken(os.Getenv("OPS_TOKEN"))`,
		`a.Core.Get("/ops/metrics", opsAuth(metrics))`,
		`a.Core.Get("/ops/admin", opsAuth(http.HandlerFunc(opsAdmin)))`,
		`DebugRoutes: "not_mounted_by_default"`,
	})
}

func TestScenarioProfiles_UseCanonicalScaffoldWithExplicitCapabilityProfile(t *testing.T) {
	tests := []struct {
		template string
		want     []string
	}{
		{template: "rest-api", want: []string{`"github.com/birdordev/plumego/x/rest"`}},
		{template: "tenant-api", want: []string{
			`"github.com/birdordev/plumego/x/tenant/resolve"`,
			`"github.com/birdordev/plumego/x/tenant/policy"`,
			`"github.com/birdordev/plumego/x/tenant/quota"`,
			`"github.com/birdordev/plumego/x/tenant/ratelimit"`,
		}},
		{template: "gateway", want: []string{`"github.com/birdordev/plumego/x/gateway"`}},
		{template: "realtime", want: []string{
			`"github.com/birdordev/plumego/x/websocket"`,
			`"github.com/birdordev/plumego/x/messaging"`,
		}},
		{template: "ai-service", want: []string{
			`"github.com/birdordev/plumego/x/ai/provider"`,
			`"github.com/birdordev/plumego/x/ai/session"`,
			`"github.com/birdordev/plumego/x/ai/streaming"`,
			`"github.com/birdordev/plumego/x/ai/tool"`,
		}},
		{template: "ops-service", want: []string{
			`"github.com/birdordev/plumego/x/observability"`,
			`"github.com/birdordev/plumego/x/observability/ops"`,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.template, func(t *testing.T) {
			files := GetTemplateFiles(tt.template)
			assertContainsAll(t, strings.Join(files, "\n"), []string{
				"cmd/app/main.go",
				"internal/app/app.go",
				"internal/app/routes.go",
				"internal/handler/api.go",
				"internal/handler/health.go",
				"internal/config/config.go",
				"internal/scenario/profile.go",
			})

			mainContent := getTemplateContent("cmd/app/main.go", "myapp", "example.com/myapp", tt.template)
			assertContainsAll(t, mainContent, []string{
				`"example.com/myapp/internal/app"`,
				`"example.com/myapp/internal/config"`,
			})

			profile := getTemplateContent("internal/scenario/profile.go", "myapp", "example.com/myapp", tt.template)
			assertContainsAll(t, profile, tt.want)
			assertContainsNone(t, profile, []string{
				"func init(",
				"DefaultProvider",
				"Global",
			})
		})
	}
}

func TestTemplateContent_UsesLocalResponseDTOs(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		template string
		want     []string
	}{
		{
			name:     "minimal health handler",
			file:     "internal/handler/health.go",
			template: "minimal",
			want: []string{
				`"github.com/birdordev/plumego/contract"`,
				"type healthResponse struct",
				"contract.WriteResponse(",
			},
		},
		{
			name:     "api health handler",
			file:     "internal/handler/health.go",
			template: "api",
			want: []string{
				"type healthResponse struct",
				"contract.WriteResponse(",
			},
		},
		{
			name:     "fullstack hello handler",
			file:     "internal/handler/api.go",
			template: "fullstack",
			want: []string{
				"type helloResponse struct",
				"contract.WriteResponse(",
			},
		},
		{
			name:     "canonical health handler",
			file:     "internal/handler/health.go",
			template: "canonical",
			want: []string{
				"type healthResponse struct",
				"contract.WriteResponse(",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := getTemplateContent(tt.file, "myapp", "example.com/myapp", tt.template)
			assertContainsAll(t, content, tt.want)
		})
	}
}

func TestCanonicalTemplate_MatchesReferenceRouteShape(t *testing.T) {
	content := getTemplateContent("internal/app/routes.go", "myapp", "example.com/myapp", "canonical")

	assertContainsAll(t, content, []string{
		`"net/http"`,
		`"example.com/myapp/internal/domain/item"`,
		`a.Core.Get("/", http.HandlerFunc(api.Hello))`,
		`a.Core.Get("/healthz", http.HandlerFunc(health.Live))`,
		`a.Core.Get("/readyz", http.HandlerFunc(health.Ready))`,
		`a.Core.Get("/api/hello", http.HandlerFunc(api.Hello))`,
		`a.Core.Get("/api/status", http.HandlerFunc(api.Status))`,
		`a.Core.Get("/api/v1/greet", http.HandlerFunc(api.Greet))`,
		`a.Core.Post("/api/v1/items", http.HandlerFunc(items.Create))`,
		`a.Core.Get("/api/v1/items/:id", http.HandlerFunc(items.GetByID))`,
	})
}

func TestCanonicalTemplate_FileSetMatchesReferenceContract(t *testing.T) {
	files := GetTemplateFiles("canonical")
	want := []string{
		"cmd/app/main.go",
		"internal/app/app.go",
		"internal/app/routes.go",
		"internal/domain/item/item.go",
		"internal/handler/api.go",
		"internal/handler/health.go",
		"internal/handler/items.go",
		"internal/config/config.go",
		"go.mod",
		"env.example",
		".gitignore",
		"README.md",
		"Makefile",
		".github/workflows/ci.yml",
		"AGENTS.md",
		"CLAUDE.md",
	}

	if !slices.Equal(files, want) {
		t.Fatalf("canonical file set drifted from reference contract:\n got: %#v\nwant: %#v", files, want)
	}
}

func TestTemplatesEmitProjectControlPlaneFiles(t *testing.T) {
	for _, tmpl := range allTemplates {
		t.Run(tmpl, func(t *testing.T) {
			files := GetTemplateFiles(tmpl)
			assertContainsAll(t, strings.Join(files, "\n"), []string{
				"Makefile",
				".github/workflows/ci.yml",
				"AGENTS.md",
				"CLAUDE.md",
			})
		})
	}
}

func TestProjectControlPlaneContent(t *testing.T) {
	makefile := getTemplateContent("Makefile", "myapp", "example.com/myapp", "canonical")
	assertContainsAll(t, makefile, []string{
		"gates: fmt-check vet test build",
		"go test -timeout 20s ./...",
		"go vet ./...",
		"go build -o bin/app ./cmd/app",
	})

	workflow := getTemplateContent(".github/workflows/ci.yml", "myapp", "example.com/myapp", "canonical")
	assertContainsAll(t, workflow, []string{
		"name: ci",
		"go-version-file: go.mod",
		"run: make gates",
	})

	agents := getTemplateContent("AGENTS.md", "myapp", "example.com/myapp", "canonical")
	assertContainsAll(t, agents, []string{
		"# AGENTS.md - myapp",
		"contract.WriteResponse",
		"contract.WriteError",
		"make gates",
	})

	claude := getTemplateContent("CLAUDE.md", "myapp", "example.com/myapp", "canonical")
	assertContainsAll(t, claude, []string{
		"# CLAUDE.md - myapp",
		"AGENTS.md",
		"make gates",
	})
}

func TestStableTemplatesUseCanonicalFileSet(t *testing.T) {
	want := canonicalTemplateFiles()
	for _, tmpl := range []string{"minimal", "fullstack", "microservice"} {
		if got := GetTemplateFiles(tmpl); !slices.Equal(got, want) {
			t.Fatalf("%s file set = %#v, want canonical %#v", tmpl, got, want)
		}
	}
}

func TestGetTemplateFilesUnknownTemplateFailsClosed(t *testing.T) {
	if files := GetTemplateFiles("unknown-template"); len(files) != 0 {
		t.Fatalf("unknown template returned files: %#v", files)
	}
}

func TestGoModContentUsesDeterministicRequirementAndOptionalReplace(t *testing.T) {
	content := getGoModContent("example.com/myapp", ProjectOptions{PlumegoReplace: "/repo/plumego"})

	assertContainsAll(t, content, []string{
		"module example.com/myapp",
		"require github.com/birdordev/plumego v0.0.0",
		"replace github.com/birdordev/plumego => /repo/plumego",
	})
}

func TestCreateProjectWritesGoModWithoutGoModInit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "myapp")
	files, err := CreateProject(dir, "myapp", "example.com/myapp", "canonical", false, ProjectOptions{
		PlumegoReplace: "/repo/plumego",
	})
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	if !slices.Contains(files, "go.mod") {
		t.Fatalf("expected go.mod in created files: %#v", files)
	}

	content, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("read generated go.mod: %v", err)
	}
	assertContainsAll(t, string(content), []string{
		"module example.com/myapp",
		"require github.com/birdordev/plumego v0.0.0",
		"replace github.com/birdordev/plumego => /repo/plumego",
	})
}

func TestCreateProjectRejectsInvalidProjectInputsBeforeWriting(t *testing.T) {
	tests := []struct {
		name   string
		module string
	}{
		{name: "bad/name", module: "example.com/app"},
		{name: "myapp", module: "https://example.com/app"},
		{name: "myapp", module: "example.com/bad module"},
	}

	for _, tt := range tests {
		t.Run(tt.name+" "+tt.module, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "out")
			if _, err := CreateProject(dir, tt.name, tt.module, "canonical", false); err == nil {
				t.Fatal("expected invalid scaffold input to fail")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("invalid scaffold input should not create output dir, stat err=%v", err)
			}
		})
	}
}

func TestCreateProjectRejectsUnknownTemplateBeforeWriting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	if _, err := CreateProject(dir, "myapp", "example.com/myapp", "unknown-template", false); err == nil {
		t.Fatal("expected unknown template error")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("unknown template should not create output dir, stat err=%v", err)
	}
}

func TestCreateProjectReportsGitInitFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "myapp")
	t.Setenv("PATH", t.TempDir())

	files, err := CreateProject(dir, "myapp", "example.com/myapp", "canonical", true)
	if err == nil {
		t.Fatal("expected git init failure")
	}
	if !strings.Contains(err.Error(), "failed to initialize git repository") {
		t.Fatalf("unexpected git init error: %v", err)
	}
	if !slices.Contains(files, "go.mod") {
		t.Fatalf("expected created files before git init failure, got %#v", files)
	}
}

func TestCreateProjectCleanExistingRemovesStaleTemplateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "myapp")
	if _, err := CreateProject(dir, "myapp", "example.com/myapp", "api", false); err != nil {
		t.Fatalf("create api project: %v", err)
	}
	stale := filepath.Join(dir, "internal", "resource", "users.go")
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("expected api resource file: %v", err)
	}

	if _, err := CreateProject(dir, "myapp", "example.com/myapp", "canonical", false, ProjectOptions{CleanExisting: true}); err != nil {
		t.Fatalf("clean existing canonical project: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expected stale api resource file to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "handler", "api.go")); err != nil {
		t.Fatalf("expected canonical handler file to exist: %v", err)
	}
}

func TestCanonicalTemplate_APIHandlerMatchesReferenceSurface(t *testing.T) {
	content := getTemplateContent("internal/handler/api.go", "myapp", "example.com/myapp", "canonical")

	assertContainsAll(t, content, []string{
		"type helloResponse struct",
		"type greetResponse struct",
		"type statusResponse struct",
		"func (h APIHandler) Hello(",
		"func (h APIHandler) Greet(",
		"func (h APIHandler) Status(",
		`Extensions: "excluded_from_canonical_path"`,
		`contract.WriteError(`,
		`contract.WriteResponse(`,
	})
}
