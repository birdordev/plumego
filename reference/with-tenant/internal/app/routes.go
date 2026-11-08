package app

import (
	"github.com/birdordev/plumego/middleware"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	"github.com/birdordev/plumego/x/tenant/policy"
	"github.com/birdordev/plumego/x/tenant/quota"
	"github.com/birdordev/plumego/x/tenant/ratelimit"
	"github.com/birdordev/plumego/x/tenant/resolve"
	"with-tenant/internal/handler"
)

// RegisterRoutes wires all HTTP routes for the with-tenant demo.
//
// The tenant middleware chain (resolve → policy → quota → ratelimit) is applied
// per-route so only tenant-aware routes pay the cost. This is the same pattern
// used for per-route auth guards in standard-service.
func (a *App) RegisterRoutes() error {
	tenantChain := middleware.NewChain(
		resolve.Middleware(resolve.Options{DisablePrincipal: true}),
		policy.Middleware(policy.Options{
			Evaluator: tenantcore.NewConfigPolicyEvaluator(a.Cfg.TenantConfig),
		}),
		quota.Middleware(quota.Options{
			Manager: tenantcore.NewFixedWindowQuotaManager(a.Cfg.TenantConfig),
		}),
		ratelimit.Middleware(ratelimit.Options{
			Limiter: tenantcore.NewTokenBucketRateLimiter(a.Cfg.RateLimits),
		}),
	)

	return a.Core.Get("/api/models", tenantChain.Build(handler.NewModelsHandler(a.Core.Logger())))
}
