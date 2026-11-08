package router_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/birdordev/plumego/router"
)

func ExampleRouter_Group() {
	r := router.NewRouter()
	api := r.Group("/api")

	stamp := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Scope", "api")
			next.ServeHTTP(w, r)
		})
	}

	handler := stamp(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(router.Param(r, "id")))
	}))

	if err := api.AddRoute(http.MethodGet, "/users/:id", handler); err != nil {
		panic(err)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users/42", nil))

	fmt.Println(rec.Body.String())
	fmt.Println(rec.Header().Get("X-Scope"))

	// Output:
	// 42
	// api
}
