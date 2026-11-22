package openapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/router"
)

func TestStrictSpecMatchesRegisteredHandlers(t *testing.T) {
	r := router.NewRouter()
	hints := make(map[string]Op)
	for _, tc := range []struct {
		method, path, requestPath, status, description string
		code                                           int
	}{
		{"POST", "/items", "/items", "201", "Created", http.StatusCreated},
		{"DELETE", "/items/:id", "/items/item", "204", "Deleted", http.StatusNoContent},
		{"GET", "/account", "/account", "401", "Authentication required", http.StatusUnauthorized},
	} {
		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.code) })
		if err := r.AddRoute(tc.method, tc.path, handler); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.requestPath, nil))
		if rec.Code != tc.code {
			t.Fatal(rec.Code)
		}
		hints[tc.method+" "+tc.path] = Op{Responses: map[string]Response{tc.status: {Description: tc.description}}}
	}
	if _, err := New().GenerateStrict(r.Routes(), nil); err == nil || !strings.Contains(err.Error(), "POST /items") {
		t.Fatalf("missing contract reports %v", err)
	}
	doc, err := New().GenerateStrict(r.Routes(), hints)
	if err != nil {
		t.Fatal(err)
	}
	for _, marshal := range []func(Document) ([]byte, error){MarshalJSON, MarshalYAML} {
		encoded, err := marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		for _, status := range []string{"201", "204", "401"} {
			if !strings.Contains(string(encoded), status) {
				t.Errorf("missing status %s in %s", status, encoded)
			}
		}
	}
}
