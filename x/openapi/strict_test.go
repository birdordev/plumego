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

func TestStrictSpecRejectsConflictingTemplatesAndInvalidResponses(t *testing.T) {
	routes := []router.RouteInfo{
		{Method: http.MethodGet, Path: "/items/:id"},
		{Method: http.MethodDelete, Path: "/items/:name"},
		{Method: http.MethodHead, Path: "/items/:id"},
	}
	body := JSONContent(Schema{Type: "object"})
	hints := map[string]Op{
		"GET /items/:id": {
			Params: []Param{PathParam("id", String), PathParam("id", String), PathParam("missing", String)},
			Responses: map[string]Response{
				"200": {Description: "Item", Content: body},
				"304": {Description: "Unchanged", Content: body},
			},
		},
		"DELETE /items/:name": {Responses: map[string]Response{"204": {Description: "Deleted", Content: body}}},
		"HEAD /items/:id":     {Responses: map[string]Response{"200": {Description: "Metadata", Content: body}}},
	}
	generator := New()
	if doc := generator.Generate(routes, hints); len(doc.Paths) != 2 {
		t.Fatal("普通模式应保留兼容行为")
	}
	_, err := generator.GenerateStrict(routes, hints)
	if err == nil {
		t.Fatal("严格模式接受了无效合同")
	}
	for _, want := range []string{
		"conflicting path templates /items/{id} and /items/{name}",
		`invalid path parameter "id"`, `invalid path parameter "missing"`,
		`response "304" for GET /items/{id}`,
		`response "204" for DELETE /items/{name}`,
		`response "200" for HEAD /items/{id}`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("缺少 %q：%v", want, err)
		}
	}

	delete(hints, "DELETE /items/:name")
	routes[1].Path = "/items/:id"
	hints["DELETE /items/:id"] = Op{Responses: map[string]Response{"204": {Description: "Deleted"}}}
	hints["HEAD /items/:id"] = Op{Responses: map[string]Response{"200": {Description: "Metadata"}}}
	hints["GET /items/:id"] = Op{Responses: map[string]Response{"200": {Description: "Item", Content: body}}}
	doc, err := generator.GenerateStrict(routes, hints)
	if err != nil || len(doc.Paths) != 1 {
		t.Fatalf("同一路径模板可声明多个方法：%v", err)
	}
}
