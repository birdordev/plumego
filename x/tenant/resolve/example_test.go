package resolve_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	"github.com/birdordev/plumego/x/tenant/resolve"
)

func ExampleMiddleware_headerExtraction() {
	var source string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, tenantcore.TenantIDFromContext(r.Context()))
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "header-tenant")

	rec := httptest.NewRecorder()
	mw := resolve.Middleware(resolve.Options{
		DisablePrincipal: true,
		Hooks: tenantcore.Hooks{
			OnResolve: func(_ context.Context, info tenantcore.ResolveInfo) {
				source = info.Source
			},
		},
	})
	mw(handler).ServeHTTP(rec, req)

	fmt.Println(rec.Code)
	fmt.Print(rec.Body.String())
	fmt.Println(source)

	// Output:
	// 200
	// header-tenant
	// header
}

func ExampleMiddleware_customExtractor() {
	var source string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, tenantcore.TenantIDFromContext(r.Context()))
	})

	req := httptest.NewRequest(http.MethodGet, "/?tenant=query-tenant", nil)
	rec := httptest.NewRecorder()

	mw := resolve.Middleware(resolve.Options{
		Extractor: tenantcore.FromQuery("tenant"),
		Hooks: tenantcore.Hooks{
			OnResolve: func(_ context.Context, info tenantcore.ResolveInfo) {
				source = info.Source
			},
		},
	})
	mw(handler).ServeHTTP(rec, req)

	fmt.Println(rec.Code)
	fmt.Print(rec.Body.String())
	fmt.Println(source)

	// Output:
	// 200
	// query-tenant
	// extractor
}
