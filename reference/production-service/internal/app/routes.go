package app

import (
	"fmt"
	"net/http"

	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/middleware/auth"
	"github.com/birdordev/plumego/security/authn"
	"github.com/birdordev/plumego/x/tenant/resolve"
	"production-service/internal/handler"
)

// RegisterRoutes wires all HTTP routes for the production reference.
// One method, one path, one handler per line — same convention as standard-service.
// Handler dependencies are constructed here via explicit struct fields; no handler
// receives the full App or Config struct.
func (a *App) RegisterRoutes() error {
	svc := handler.ServiceHandler{
		ServiceName: a.Cfg.App.ServiceName,
		Logger:      a.Core.Logger(),
		Features: []string{
			"explicit_middleware",
			"security_headers",
			"abuse_guard",
			"request_metrics",
			"protected_tenant_api",
			"no_default_devtools",
		},
		// Checkers: nil — no external dependencies to probe in this reference.
		// Wire real health.ComponentChecker implementations here (e.g. database ping)
		// when this template is used as the base for a production service.
	}

	statusHandler := handler.StatusHandler{
		Logger: a.Core.Logger(),
		Cfg: handler.StatusConfig{
			ServiceName:    a.Cfg.App.ServiceName,
			Environment:    a.Cfg.App.Environment,
			BodyLimitBytes: a.Cfg.App.BodyLimitBytes,
			RequestTimeout: a.Cfg.App.RequestTimeout,
			RateLimit:      a.Cfg.App.RateLimit,
			RateBurst:      a.Cfg.App.RateBurst,
			Profiles: handler.ProfilesSummary{
				Kind:        a.Profiles.Kind(),
				Replacement: a.Profiles.Replacement(),
				Path:        a.Profiles.Path(),
			},
		},
	}

	profileHandler := handler.ProfileHandler{Profiles: a.Profiles, Logger: a.Core.Logger()}
	opsHandler := handler.OpsHandler{Metrics: a.Metrics, Logger: a.Core.Logger()}

	profileRoute, err := withTenantAPIAuth(a.Cfg.App.APIToken, http.HandlerFunc(profileHandler.GetProfile))
	if err != nil {
		return fmt.Errorf("configure tenant API route: %w", err)
	}
	opsRoute, err := withOpsAuth(a.Cfg.App.OpsToken, http.HandlerFunc(opsHandler.MetricStats))
	if err != nil {
		return fmt.Errorf("configure ops metrics route: %w", err)
	}

	// routeReg accumulates the first registration error so each route can be
	// written on a single line without per-call error checks.
	reg := newRouteReg(a.Core)
	reg.get("/", http.HandlerFunc(svc.Root))
	reg.get("/healthz", http.HandlerFunc(svc.Live))
	reg.get("/readyz", http.HandlerFunc(svc.Ready))
	reg.get("/api/status", http.HandlerFunc(statusHandler.Status))
	reg.get("/api/profile", profileRoute)
	reg.get("/ops/metrics", opsRoute)
	return reg.err
}

// withTenantAPIAuth wraps a handler with bearer-token auth and tenant-ID resolution.
// Returns an error at construction time so configuration problems surface at startup
// rather than silently degrading to 500 on every request.
// When token is empty, StaticToken accepts all calls; set APP_API_TOKEN in production.
func withTenantAPIAuth(token string, next http.Handler) (http.Handler, error) {
	authMw, err := auth.Authenticate(
		authn.StaticToken(token),
		auth.WithAuthRealm("production-api"),
	)
	if err != nil {
		return nil, fmt.Errorf("auth.Authenticate: %w", err)
	}
	return middleware.NewChain(
		authMw,
		resolve.Middleware(resolve.Options{}),
	).Build(next), nil
}

// withOpsAuth wraps a handler with bearer-token auth for the ops surface.
// Returns an error at construction time so misconfiguration fails fast at startup.
func withOpsAuth(token string, next http.Handler) (http.Handler, error) {
	authMw, err := auth.Authenticate(
		authn.StaticToken(token),
		auth.WithAuthRealm("production-ops"),
	)
	if err != nil {
		return nil, fmt.Errorf("auth.Authenticate: %w", err)
	}
	return authMw(next), nil
}

// routeAdder is the minimal interface shared by *core.App and *core.RouteGroup.
type routeAdder interface {
	Get(path string, h http.Handler) error
	Post(path string, h http.Handler) error
	Put(path string, h http.Handler) error
	Delete(path string, h http.Handler) error
}

// routeReg wraps a routeAdder and records the first registration error.
// This lets the route table be written one route per line without per-call
// error checks; inspect reg.err once after all registrations.
type routeReg struct {
	adder routeAdder
	err   error
}

func newRouteReg(adder routeAdder) *routeReg { return &routeReg{adder: adder} }

func (r *routeReg) get(path string, h http.Handler)    { r.record(r.adder.Get(path, h)) }
func (r *routeReg) post(path string, h http.Handler)   { r.record(r.adder.Post(path, h)) }
func (r *routeReg) put(path string, h http.Handler)    { r.record(r.adder.Put(path, h)) }
func (r *routeReg) delete(path string, h http.Handler) { r.record(r.adder.Delete(path, h)) }

// record stores the first non-nil error; subsequent errors are dropped.
func (r *routeReg) record(err error) {
	if r.err == nil {
		r.err = err
	}
}
