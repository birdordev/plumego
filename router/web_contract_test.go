package router_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/router"
)

func TestWebContractWithStandardRequestMetadataAndFallback(t *testing.T) {
	r := router.NewRouter(router.WithMethodNotAllowed(true), router.WithNotFoundHandler(http.HandlerFunc(
		func(w http.ResponseWriter, req *http.Request) {
			_ = contract.WriteError(w, req, contract.NewErrorBuilder().Type(contract.TypeNotFound).Build())
		})))
	if err := r.EnableStandardPathValues(); err != nil {
		t.Fatal(err)
	}
	if err := r.Group("/v1").AddRoute(http.MethodGet, "/files/:id/*path", http.HandlerFunc(
		func(w http.ResponseWriter, req *http.Request) {
			if req.Pattern != "/v1/files/:id/*path" || req.PathValue("id") != "42" ||
				req.PathValue("path") != "a/b" || router.Param(req, "id") != "42" {
				t.Errorf("unexpected metadata: pattern=%q id=%q path=%q", req.Pattern, req.PathValue("id"), req.PathValue("path"))
			}
			w.WriteHeader(http.StatusNoContent)
		})); err != nil {
		t.Fatal(err)
	}
	r.Freeze()
	if err := r.SetNotFoundHandler(nil); err == nil {
		t.Fatal("changed the fallback after freeze")
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/v1/files/42/a/b", 204},
		{http.MethodHead, "/v1/files/42/a/b", 204},
		{http.MethodPost, "/v1/files/42/a/b", 405},
		{http.MethodGet, "/unknown", 404},
		{http.MethodHead, "/unknown", 404},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.SetPathValue("id", "outer")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if req.PathValue("id") != "outer" || req.Pattern != "" {
			t.Fatal("router modified the input request")
		}
		if w.Code != tc.status || (tc.status >= 400 && w.Header().Get("Content-Type") != contract.ContentTypeJSON) ||
			(tc.method == http.MethodHead && w.Body.Len() != 0) ||
			(tc.status == 405 && w.Header().Get("Allow") != "GET, HEAD") {
			t.Errorf("%s %s: status=%d headers=%v body=%s", tc.method, tc.path, w.Code, w.Header(), w.Body)
		}
	}
}
