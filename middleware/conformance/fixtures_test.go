package conformance_test

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware"
)

func fixtureNoOpMiddleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func fixtureCanonicalErrorMiddleware(code string) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeInternal).
				Code(code).
				Message("conformance fixture error").
				Build())
		})
	}
}
