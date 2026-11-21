package core

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware/requestid"
)

func TestRequestIDMiddleware(t *testing.T) {
	app := newTestApp()
	_ = app.Use(requestid.Middleware())
	mustRegisterRoute(t, app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	app.ServeHTTP(rec, req)

	if rec.Header().Get(contract.RequestIDHeader) == "" {
		t.Fatalf("expected %s to be set", contract.RequestIDHeader)
	}
}

func TestUseNilValidationIsAtomicBeforeAndAfterFreeze(t *testing.T) {
	app := newTestApp()
	var seen []string
	first := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, "first")
			next.ServeHTTP(w, r)
		})
	}
	second := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, "second")
			next.ServeHTTP(w, r)
		})
	}
	if err := app.Use(first, nil, second); err == nil {
		t.Fatal("nil middleware accepted")
	}
	if err := app.Use(first, second); err != nil {
		t.Fatal(err)
	}
	if err := app.Get("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })); err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := app.Use(first, nil, second); err == nil {
		t.Fatal("mutation after prepare")
	}
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !slices.Equal(seen, []string{"first", "second"}) {
		t.Fatalf("middleware execution=%v", seen)
	}
}
