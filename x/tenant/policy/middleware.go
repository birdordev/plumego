package policy

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	mw "github.com/birdordev/plumego/middleware"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	tenanttransport "github.com/birdordev/plumego/x/tenant/transport"
)

// Options configures tenant policy checks.
type Options struct {
	Evaluator   tenantcore.PolicyEvaluator
	ModelHeader string
	ToolHeader  string
	Hooks       tenantcore.Hooks
	OnDenied    func(http.ResponseWriter, *http.Request, tenantcore.PolicyResult)
}

// Middleware enforces tenant policy checks.
func Middleware(options Options) mw.Middleware {
	modelHeader := tenanttransport.HeaderOrDefault(options.ModelHeader, tenanttransport.DefaultModelHeader)
	toolHeader := tenanttransport.HeaderOrDefault(options.ToolHeader, tenanttransport.DefaultToolHeader)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if options.Evaluator == nil {
				next.ServeHTTP(w, r)
				return
			}

			tenantID := tenantcore.TenantIDFromContext(r.Context())
			req := tenantcore.PolicyRequest{
				Model:  r.Header.Get(modelHeader),
				Tool:   r.Header.Get(toolHeader),
				Method: r.Method,
				Path:   r.URL.Path,
			}
			result, err := options.Evaluator.Evaluate(r.Context(), tenantID, req)
			allowed := err == nil && result.Allowed
			status := http.StatusForbidden
			if allowed {
				status = http.StatusOK
			}

			options.Hooks.Policy(r.Context(), tenantcore.PolicyDecision{
				TenantID: tenantID,
				Allowed:  allowed,
				Reason:   result.Reason,
				Model:    req.Model,
				Tool:     req.Tool,
				Method:   req.Method,
				Path:     req.Path,
				Status:   status,
				Err:      err,
			})

			if allowed {
				next.ServeHTTP(w, r)
				return
			}

			if options.OnDenied != nil {
				options.OnDenied(w, r, result)
				return
			}

			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeForbidden).
				Code(tenanttransport.CodePolicyDenied).
				Message("tenant policy denied request").
				Build())
		})
	}
}
