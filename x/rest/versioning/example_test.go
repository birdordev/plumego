package versioning_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/birdordev/plumego/x/rest/versioning"
)

func ExampleMiddleware() {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, versioning.VersionFromContext(r.Context()))
	})
	handler := versioning.Middleware(versioning.Config{
		Strategy:          versioning.StrategyAcceptHeader,
		VendorPrefix:      "application/vnd.myapi",
		DefaultVersion:    1,
		SupportedVersions: []int{1, 2, 3},
	})(next)

	req := httptest.NewRequest(http.MethodGet, "/users/123", nil)
	req.Header.Set("Accept", "application/vnd.myapi.v2+json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	fmt.Print(rec.Body.String())

	// Output: 2
}
