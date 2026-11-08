package scaffold

import (
	"fmt"
)

func getTemplateContent(file, name, module, template string) string {
	switch file {
	case "cmd/app/main.go":
		return getCanonicalMainGoContent(module, name)
	case "internal/app/app.go":
		return getCanonicalAppGoContent(module)
	case "internal/app/routes.go":
		if template == "tenant-api" {
			return getTenantAPIRoutesGoContent(module, name)
		}
		if template == "gateway" {
			return getGatewayRoutesGoContent(module, name)
		}
		if template == "realtime" {
			return getRealtimeRoutesGoContent(module, name)
		}
		if template == "ai-service" {
			return getAIServiceRoutesGoContent(module, name)
		}
		if template == "ops-service" {
			return getOpsServiceRoutesGoContent(module, name)
		}
		if template == "api" || template == "rest-api" {
			return getAPIRoutesGoContent(module, name)
		}
		return getCanonicalRoutesGoContent(module, name)
	case "internal/domain/item/item.go":
		return getCanonicalItemDomainContent()
	case "internal/handler/api.go":
		return getCanonicalAPIHandlerContent(name)
	case "internal/handler/health.go":
		return getCanonicalHealthHandlerContent()
	case "internal/handler/items.go":
		return getCanonicalItemHandlerContent(module)
	case "internal/config/config.go":
		return getCanonicalConfigGoContent(module)
	case "internal/resource/users.go":
		return getAPIUsersResourceContent()
	case "internal/scenario/profile.go":
		return getScenarioProfileContent(template)
	case "go.mod":
		return getGoModContent(module, ProjectOptions{})
	case "env.example":
		return getEnvExampleContent()
	case ".gitignore":
		return getGitignoreContent()
	case "README.md":
		return getReadmeContent(name, template)
	case "Makefile":
		return getMakefileContent()
	case ".github/workflows/ci.yml":
		return getCIWorkflowContent()
	case "AGENTS.md":
		return getAgentsContent(name)
	case "CLAUDE.md":
		return getClaudeContent(name)
	default:
		return ""
	}
}

func getCanonicalMainGoContent(module, name string) string {
	return fmt.Sprintf(`package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"%s/internal/app"
	"%s/internal/config"
)

func main() {
	if err := run(); err != nil {
		log.Printf("server stopped: %%v", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	a, err := app.New(cfg)
	if err != nil {
		return err
	}

	if err := a.RegisterRoutes(); err != nil {
		return err
	}

	log.Printf("Starting %s on %%s", cfg.Core.Addr)
	return a.Start(ctx)
}
`, module, module, name)
}

func getCanonicalAppGoContent(module string) string {
	return fmt.Sprintf(`// Package app wires together the application dependencies and manages the
// server lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/accesslog"
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/middleware/requestid"
	"%s/internal/config"
)

// App holds application-wide dependencies.
type App struct {
	Core *core.App
	Cfg  config.Config
}

// New constructs the App with explicit stable-root wiring only.
func New(cfg config.Config) (*App, error) {
	a := core.New(cfg.Core, core.AppDependencies{Logger: plumelog.NewLogger()})
	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: a.Logger()})
	if err != nil {
		return nil, fmt.Errorf("configure recovery middleware: %%w", err)
	}
	accesslogMw, err := accesslog.Middleware(accesslog.Config{Logger: a.Logger()})
	if err != nil {
		return nil, fmt.Errorf("configure access log middleware: %%w", err)
	}
	if err := a.Use(
		requestid.Middleware(),
		recoveryMw,
		accesslogMw,
	); err != nil {
		return nil, fmt.Errorf("register middleware: %%w", err)
	}

	return &App{Core: a, Cfg: cfg}, nil
}

// Start prepares the runtime and blocks while the HTTP server runs.
// When ctx is canceled, it triggers a graceful shutdown.
func (a *App) Start(ctx context.Context) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := a.Core.Prepare(); err != nil {
		return fmt.Errorf("prepare server: %%w", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		return fmt.Errorf("get server: %%w", err)
	}

	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownErr <- a.Core.Shutdown(context.Background())
	}()

	if a.Cfg.Core.TLS.Enabled {
		if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server stopped: %%w", err)
		}
		return nil
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server stopped: %%w", err)
	}
	select {
	case err := <-shutdownErr:
		if err != nil {
			return fmt.Errorf("shutdown server: %%w", err)
		}
	default:
	}
	return nil
}
`, module)
}

func getCanonicalRoutesGoContent(module, name string) string {
	return fmt.Sprintf(`package app

import (
	"net/http"

	"%s/internal/domain/item"
	"%s/internal/handler"
)

// RegisterRoutes wires all HTTP routes for the application.
func (a *App) RegisterRoutes() error {
	api := handler.APIHandler{}
	health := handler.HealthHandler{ServiceName: "%s"}
	items := handler.ItemHandler{Repo: item.NewMemoryStore()}

	if err := a.Core.Get("/", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/healthz", http.HandlerFunc(health.Live)); err != nil {
		return err
	}
	if err := a.Core.Get("/readyz", http.HandlerFunc(health.Ready)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/hello", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/status", http.HandlerFunc(api.Status)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/v1/greet", http.HandlerFunc(api.Greet)); err != nil {
		return err
	}
	if err := a.Core.Post("/api/v1/items", http.HandlerFunc(items.Create)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/v1/items/:id", http.HandlerFunc(items.GetByID)); err != nil {
		return err
	}
	return nil
}
`, module, module, name)
}

func getAPIRoutesGoContent(module, name string) string {
	return fmt.Sprintf(`package app

import (
	"net/http"

	"%s/internal/handler"
	"%s/internal/resource"

	"github.com/birdordev/plumego/x/rest"
)

// RegisterRoutes wires all HTTP routes for the application.
func (a *App) RegisterRoutes() error {
	api := handler.APIHandler{}
	health := handler.HealthHandler{ServiceName: "%s"}

	if err := a.Core.Get("/", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/healthz", http.HandlerFunc(health.Live)); err != nil {
		return err
	}
	if err := a.Core.Get("/readyz", http.HandlerFunc(health.Ready)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/hello", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/status", http.HandlerFunc(api.Status)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/v1/greet", http.HandlerFunc(api.Greet)); err != nil {
		return err
	}

	spec := rest.DefaultResourceSpec("users").WithPrefix("/api/users")
	users := rest.NewDBResource[resource.User](spec, resource.NewUserRepository())
	if err := a.Core.Get(spec.Prefix, http.HandlerFunc(users.Index)); err != nil {
		return err
	}
	if err := a.Core.Get(spec.Prefix+"/:id", http.HandlerFunc(users.Show)); err != nil {
		return err
	}
	if err := a.Core.Post(spec.Prefix, http.HandlerFunc(users.Create)); err != nil {
		return err
	}
	return nil
}
`, module, module, name)
}

func getTenantAPIRoutesGoContent(module, name string) string {
	return fmt.Sprintf(`package app

import (
	"net/http"

	"%s/internal/handler"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	"github.com/birdordev/plumego/x/tenant/policy"
	"github.com/birdordev/plumego/x/tenant/quota"
	"github.com/birdordev/plumego/x/tenant/ratelimit"
	"github.com/birdordev/plumego/x/tenant/resolve"
)

// RegisterRoutes wires all HTTP routes for the application.
func (a *App) RegisterRoutes() error {
	api := handler.APIHandler{}
	health := handler.HealthHandler{ServiceName: "%s"}

	if err := a.Core.Get("/", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/healthz", http.HandlerFunc(health.Live)); err != nil {
		return err
	}
	if err := a.Core.Get("/readyz", http.HandlerFunc(health.Ready)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/hello", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/status", http.HandlerFunc(api.Status)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/v1/greet", http.HandlerFunc(api.Greet)); err != nil {
		return err
	}

	tenantConfig := tenantcore.NewInMemoryConfigManager()
	tenantConfig.SetTenantConfig(tenantcore.Config{
		TenantID: "tenant-a",
		Policy: tenantcore.PolicyConfig{
			AllowedModels: []string{"gpt-4o"},
		},
		Quota: tenantcore.QuotaConfig{
			Limits: []tenantcore.QuotaLimit{
				{Window: tenantcore.QuotaWindowMinute, Requests: 100},
			},
		},
	})
	rateLimits := tenantcore.NewInMemoryRateLimitManager()
	rateLimits.SetRateLimit("tenant-a", tenantcore.RateLimitConfig{
		RequestsPerSecond: 10,
		Burst:             10,
	})
	tenantChain := middleware.NewChain(
		resolve.Middleware(resolve.Options{DisablePrincipal: true}),
		policy.Middleware(policy.Options{Evaluator: tenantcore.NewConfigPolicyEvaluator(tenantConfig)}),
		quota.Middleware(quota.Options{Manager: tenantcore.NewFixedWindowQuotaManager(tenantConfig)}),
		ratelimit.Middleware(ratelimit.Options{Limiter: tenantcore.NewTokenBucketRateLimiter(rateLimits)}),
	)
	if err := a.Core.Get("/api/models", tenantChain.Build(http.HandlerFunc(models))); err != nil {
		return err
	}
	return nil
}

type modelsResponse struct {
	TenantID string   `+"`json:\"tenant_id\"`"+`
	Models   []string `+"`json:\"models\"`"+`
}

func models(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantcore.TenantIDFromContext(r.Context())
	_ = contract.WriteResponse(w, r, http.StatusOK, modelsResponse{
		TenantID: tenantID,
		Models:   []string{"gpt-4o"},
	}, nil)
}
`, module, name)
}

func getGatewayRoutesGoContent(module, name string) string {
	return fmt.Sprintf(`package app

import (
	"net/http"
	"strings"

	"%s/internal/handler"

	"github.com/birdordev/plumego/x/gateway"
)

// RegisterRoutes wires all HTTP routes for the application.
func (a *App) RegisterRoutes() error {
	api := handler.APIHandler{}
	health := handler.HealthHandler{ServiceName: "%s"}

	if err := a.Core.Get("/", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/healthz", http.HandlerFunc(health.Live)); err != nil {
		return err
	}
	if err := a.Core.Get("/readyz", http.HandlerFunc(health.Ready)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/hello", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/status", http.HandlerFunc(api.Status)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/v1/greet", http.HandlerFunc(api.Greet)); err != nil {
		return err
	}

	proxy, err := gateway.NewGateway(gateway.GatewayConfig{
		Targets:     []string{gatewayLoopbackTarget(a.Cfg.Core.Addr)},
		PathRewrite: gateway.ReplacePrefix("/edge", "/api/status"),
		RetryCount:  0,
	})
	if err != nil {
		return err
	}
	if err := a.Core.Get("/edge", proxy); err != nil {
		return err
	}
	return nil
}

func gatewayLoopbackTarget(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr
	}
	return "http://" + addr
}
`, module, name)
}

func getRealtimeRoutesGoContent(module, name string) string {
	return fmt.Sprintf(`package app

import (
	"net/http"

	"%s/internal/handler"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/x/websocket"
)

// RegisterRoutes wires all HTTP routes for the application.
func (a *App) RegisterRoutes() error {
	api := handler.APIHandler{}
	health := handler.HealthHandler{ServiceName: "%s"}
	hub, err := websocket.NewHub(4, 1024)
	if err != nil {
		return err
	}

	if err := a.Core.Get("/", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/healthz", http.HandlerFunc(health.Live)); err != nil {
		return err
	}
	if err := a.Core.Get("/readyz", http.HandlerFunc(health.Ready)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/hello", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/status", http.HandlerFunc(api.Status)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/v1/greet", http.HandlerFunc(api.Greet)); err != nil {
		return err
	}
	if err := a.Core.Get("/realtime/metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = contract.WriteResponse(w, r, http.StatusOK, hub.Metrics(), nil)
	})); err != nil {
		return err
	}
	return nil
}
`, module, name)
}

func getAIServiceRoutesGoContent(module, name string) string {
	return fmt.Sprintf(`package app

import (
	"net/http"

	"%s/internal/handler"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/session"
	"github.com/birdordev/plumego/x/ai/tool"
)

// RegisterRoutes wires all HTTP routes for the application.
func (a *App) RegisterRoutes() error {
	api := handler.APIHandler{}
	health := handler.HealthHandler{ServiceName: "%s"}
	offline := provider.NewMockProvider("offline")
	offline.QueueResponse(&provider.CompletionResponse{
		ID:         "offline-demo",
		Model:      "mock-model",
		Role:       provider.RoleAssistant,
		Content:    []provider.ContentBlock{{Type: provider.ContentTypeText, Text: "offline ai response"}},
		StopReason: provider.StopReasonEndTurn,
	})
	sessions := session.NewManager(session.NewMemoryStorage())
	tools := tool.NewRegistry()
	_ = tools.Register(tool.NewEchoTool())

	if err := a.Core.Get("/", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/healthz", http.HandlerFunc(health.Live)); err != nil {
		return err
	}
	if err := a.Core.Get("/readyz", http.HandlerFunc(health.Ready)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/hello", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/status", http.HandlerFunc(api.Status)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/v1/greet", http.HandlerFunc(api.Greet)); err != nil {
		return err
	}
	if err := a.Core.Get("/ai/demo", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		created, err := sessions.Create(r.Context(), session.CreateOptions{
			TenantID: "demo-tenant",
			UserID:   "demo-user",
			AgentID:  "offline-agent",
			Model:    "mock-model",
		})
		if err != nil {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeInternal).
				Message("failed to create session").
				Build())
			return
		}
		resp, err := offline.Complete(r.Context(), &provider.CompletionRequest{
			Model: "mock-model",
			Messages: []provider.Message{{
				Role:    provider.RoleUser,
				Content: "hello",
			}},
			Tools: tools.ToProviderTools(r.Context()),
		})
		if err != nil {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeInternal).
				Message("offline provider failed").
				Build())
			return
		}
		_ = contract.WriteResponse(w, r, http.StatusOK, aiDemoResponse{
			SessionID: created.ID,
			Provider:  offline.Name(),
			Model:     resp.Model,
			ToolCount: len(tools.ListForContext(r.Context())),
			Content:   resp.GetText(),
		}, nil)
	})); err != nil {
		return err
	}
	return nil
}

type aiDemoResponse struct {
	SessionID string `+"`json:\"session_id\"`"+`
	Provider  string `+"`json:\"provider\"`"+`
	Model     string `+"`json:\"model\"`"+`
	ToolCount int    `+"`json:\"tool_count\"`"+`
	Content   string `+"`json:\"content\"`"+`
}
`, module, name)
}

func getOpsServiceRoutesGoContent(module, name string) string {
	return fmt.Sprintf(`package app

import (
	"net/http"
	"os"
	"time"

	"%s/internal/handler"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware/auth"
	"github.com/birdordev/plumego/security/authn"
	"github.com/birdordev/plumego/x/observability"
	"github.com/birdordev/plumego/x/observability/ops"
)

// RegisterRoutes wires all HTTP routes for the application.
func (a *App) RegisterRoutes() error {
	api := handler.APIHandler{}
	health := handler.HealthHandler{ServiceName: "%s"}
	collector := observability.NewPrometheusCollector("app")
	collector.ObserveHTTP(nil, "GET", "/healthz", http.StatusOK, 0, time.Millisecond)
	metricsExporter, err := observability.NewPrometheusExporter(collector)
	if err != nil {
		return err
	}
	metrics := metricsExporter.Handler()
	opsAuth, err := auth.Authenticate(authn.StaticToken(os.Getenv("OPS_TOKEN")), auth.WithAuthRealm("ops-service"))
	if err != nil {
		return err
	}

	if err := a.Core.Get("/", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/healthz", http.HandlerFunc(health.Live)); err != nil {
		return err
	}
	if err := a.Core.Get("/readyz", http.HandlerFunc(health.Ready)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/hello", http.HandlerFunc(api.Hello)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/status", http.HandlerFunc(api.Status)); err != nil {
		return err
	}
	if err := a.Core.Get("/api/v1/greet", http.HandlerFunc(api.Greet)); err != nil {
		return err
	}
	if err := a.Core.Get("/ops/metrics", opsAuth(metrics)); err != nil {
		return err
	}
	if err := a.Core.Get("/ops/admin", opsAuth(http.HandlerFunc(opsAdmin))); err != nil {
		return err
	}
	return nil
}

type opsAdminResponse struct {
	AdminRoutes string         `+"`json:\"admin_routes\"`"+`
	DebugRoutes string         `+"`json:\"debug_routes\"`"+`
	Metrics     string         `+"`json:\"metrics\"`"+`
	Queue       ops.QueueStats `+"`json:\"queue\"`"+`
}

func opsAdmin(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteResponse(w, r, http.StatusOK, opsAdminResponse{
		AdminRoutes: "protected",
		DebugRoutes: "not_mounted_by_default",
		Metrics:     "/ops/metrics",
		Queue: ops.QueueStats{
			Queue:     "demo",
			Queued:    0,
			Leased:    0,
			Dead:      0,
			Expired:   0,
			UpdatedAt: time.Now().UTC(),
		},
	}, nil)
}
`, module, name)
}
