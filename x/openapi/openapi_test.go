package openapi

import (
	"net/http"
	"testing"

	"github.com/birdordev/plumego/router"
)

func TestGenerateEmptyRouteListReturnsEmptyPaths(t *testing.T) {
	doc := New().Generate(nil, nil)

	if doc.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI = %q, want 3.1.0", doc.OpenAPI)
	}
	if len(doc.Paths) != 0 {
		t.Fatalf("Paths = %#v, want empty", doc.Paths)
	}
}

func TestGenerateStaticRouteWithoutHintUsesDefaultResponse(t *testing.T) {
	doc := New().Generate([]router.RouteInfo{{
		Method: http.MethodGet,
		Path:   "/healthz",
	}}, nil)

	op := doc.Paths["/healthz"].Get
	if op == nil {
		t.Fatal("GET /healthz operation missing")
	}
	if got := op.Responses["200"].Description; got != "OK" {
		t.Fatalf("default response description = %q, want OK", got)
	}
}

func TestGenerateParameterizedRouteUsesOpenAPIPathTemplate(t *testing.T) {
	doc := New().Generate([]router.RouteInfo{{
		Method: http.MethodGet,
		Path:   "/users/:id",
	}}, nil)

	if _, ok := doc.Paths["/users/{id}"]; !ok {
		t.Fatalf("Paths = %#v, want /users/{id}", doc.Paths)
	}
}

func TestGenerateRouteWithOpHintUsesSummaryAndParams(t *testing.T) {
	doc := New().Generate([]router.RouteInfo{{
		Method: http.MethodGet,
		Path:   "/users/:id",
	}}, map[string]Op{
		"GET /users/:id": {
			Summary: "Show user",
			Params: []Param{
				PathParam("id", String),
			},
		},
	})

	op := doc.Paths["/users/{id}"].Get
	if op == nil {
		t.Fatal("GET /users/{id} operation missing")
	}
	if op.Summary != "Show user" {
		t.Fatalf("Summary = %q, want Show user", op.Summary)
	}
	if len(op.Parameters) != 1 || op.Parameters[0].Name != "id" || op.Parameters[0].In != "path" {
		t.Fatalf("Parameters = %#v, want id path param", op.Parameters)
	}
}

func TestQueryParamCreatesOptionalQueryParam(t *testing.T) {
	p := QueryParam("limit", Integer)
	if p.Name != "limit" {
		t.Fatalf("Name = %q, want limit", p.Name)
	}
	if p.In != "query" {
		t.Fatalf("In = %q, want query", p.In)
	}
	if p.Required {
		t.Fatal("QueryParam should not be required")
	}
}

func TestHeaderParamCreatesOptionalHeaderParam(t *testing.T) {
	p := HeaderParam("X-Request-ID", String)
	if p.Name != "X-Request-ID" {
		t.Fatalf("Name = %q, want X-Request-ID", p.Name)
	}
	if p.In != "header" {
		t.Fatalf("In = %q, want header", p.In)
	}
	if p.Required {
		t.Fatal("HeaderParam should not be required")
	}
}

func TestGenerateMultipleRoutesProducePathKeys(t *testing.T) {
	doc := New().Generate([]router.RouteInfo{
		{Method: http.MethodGet, Path: "/users"},
		{Method: http.MethodPost, Path: "/users"},
		{Method: http.MethodDelete, Path: "/users/:id"},
	}, nil)

	if len(doc.Paths) != 2 {
		t.Fatalf("path count = %d, want 2: %#v", len(doc.Paths), doc.Paths)
	}
	if doc.Paths["/users"].Get == nil || doc.Paths["/users"].Post == nil {
		t.Fatalf("/users item = %#v, want GET and POST", doc.Paths["/users"])
	}
	if doc.Paths["/users/{id}"].Delete == nil {
		t.Fatalf("/users/{id} item = %#v, want DELETE", doc.Paths["/users/{id}"])
	}
}
