package core

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spcent/plumego/contract"
	"github.com/spcent/plumego/middleware/requestid"
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
