package resolve

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/security/authn"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	tenanttransport "github.com/birdordev/plumego/x/tenant/transport"
)

// Options configures tenant resolution.
type Options struct {
	// HeaderName is the HTTP header to extract tenant ID from (default: "X-Tenant-ID").
	HeaderName string
	// Extractor is a custom function to extract tenant ID from the request.
	// When set, takes precedence over HeaderName.
	Extractor tenantcore.TenantExtractor
	// AllowMissing allows requests to proceed when no tenant ID is found.
	AllowMissing bool
	// DisablePrincipal disables tenant resolution from the authn.Principal in the request context.
	DisablePrincipal bool
	// Hooks provides callbacks for tenant resolution events.
	Hooks tenantcore.Hooks
	// OnMissing is called when tenant ID is missing and AllowMissing is false.
	// If nil, a standard 401 JSON error is returned.
	OnMissing func(http.ResponseWriter, *http.Request)
}

// Middleware resolves tenant id from request and stores it in context.
// Resolution order: authn.Principal from context, then custom Extractor, then HeaderName header.
func Middleware(options Options) middleware.Middleware {
	header := tenanttransport.HeaderOrDefault(options.HeaderName, tenanttransport.DefaultTenantHeader)
	requireTenant := !options.AllowMissing
	allowFromPrincipal := !options.DisablePrincipal

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var tenantID string
			source := ""

			if allowFromPrincipal {
				if p := authn.PrincipalFromContext(r.Context()); p != nil && p.TenantID != "" {
					tenantID = p.TenantID
					source = "principal"
				}
			}

			if tenantID == "" {
				if options.Extractor != nil {
					if id, err := options.Extractor(r); err == nil && id != "" {
						tenantID = id
						source = "extractor"
					}
				} else if headerValue := r.Header.Get(header); headerValue != "" {
					tenantID = headerValue
					source = "header"
				}
			}

			if tenantID == "" {
				if !requireTenant {
					next.ServeHTTP(w, r)
					return
				}
				if options.OnMissing != nil {
					options.OnMissing(w, r)
					return
				}
				_ = contract.WriteError(w, r, contract.NewErrorBuilder().
					Type(contract.TypeUnauthorized).
					Code(tenanttransport.CodeRequired).
					Message("tenant id is required").
					Build())
				return
			}

			if err := tenantcore.ValidateTenantID(tenantID); err != nil {
				_ = contract.WriteError(w, r, contract.NewErrorBuilder().
					Type(contract.TypeInvalidFormat).
					Code(tenanttransport.CodeInvalidID).
					Message("invalid tenant ID format").
					Build())
				return
			}

			r = tenantcore.RequestWithTenantID(r, tenantID)
			options.Hooks.Resolve(r.Context(), tenantcore.ResolveInfo{
				TenantID: tenantID,
				Source:   source,
			})

			next.ServeHTTP(w, r)
		})
	}
}
