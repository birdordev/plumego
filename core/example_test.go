package core_test

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/birdordev/plumego/core"
)

func ExampleNew() {
	cfg := core.DefaultConfig()
	app := core.New(cfg, core.AppDependencies{})

	if err := app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-App", "plumego")
			next.ServeHTTP(w, r)
		})
	}); err != nil {
		log.Fatal(err)
	}

	if err := app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})); err != nil {
		log.Fatal(err)
	}

	if err := app.Prepare(); err != nil {
		log.Fatal(err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))

	fmt.Println(rec.Code)
	fmt.Println(rec.Header().Get("X-App"))
	fmt.Print(rec.Body.String())

	// Output:
	// 200
	// plumego
	// pong
}
