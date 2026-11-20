package openapi

import (
	"encoding/json"
	"net/http"
	"strings"
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

func TestGenerateIncludesTrace(t *testing.T) {
	r := router.NewRouter()
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/diagnostics/:id"},
		{http.MethodTrace, "/diagnostics/:id"},
		{http.MethodGet, "/healthz"},
	} {
		if err := r.AddRoute(route.method, route.path, handler); err != nil {
			t.Fatal(err)
		}
	}

	doc := New().Generate(r.Routes(), map[string]Op{
		"TRACE /diagnostics/:id": {Summary: "Describe diagnostic route"},
	})
	data, err := MarshalJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Paths map[string]map[string]Operation
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	operations := decoded.Paths["/diagnostics/{id}"]
	trace, ok := operations["trace"]
	if !ok || trace.Summary != "Describe diagnostic route" || len(trace.Parameters) != 1 ||
		trace.Parameters[0].Name != "id" || trace.Parameters[0].In != "path" || !trace.Parameters[0].Required {
		t.Fatalf("decoded TRACE = %+v, want hinted operation with required id path parameter", trace)
	}
	if _, ok := operations["get"]; !ok {
		t.Error("GET missing from shared path")
	}
	if _, ok := decoded.Paths["/healthz"]["trace"]; ok {
		t.Error("unexpected TRACE on a GET-only path")
	}

	yaml, err := MarshalYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"    trace:\n      parameters:\n",
		"          in: \"path\"\n          name: \"id\"\n          required: true\n",
		"      summary: \"Describe diagnostic route\"\n",
	} {
		if !strings.Contains(string(yaml), want) {
			t.Errorf("YAML is missing %q:\n%s", want, yaml)
		}
	}
	if strings.Count(string(yaml), "    trace:\n") != 1 {
		t.Errorf("want exactly one TRACE operation in YAML:\n%s", yaml)
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

func TestGenerateOwnsRecursiveSchemaAndOperationContent(t *testing.T) {
	properties := make(map[string]Schema)
	properties["self"] = Schema{Properties: properties}
	item := &Schema{Type: "object", Properties: properties}
	item.Items = item
	content := map[string]MediaType{"application/json": {Schema: Schema{Items: item}}}
	hint := Op{Body: &RequestBody{Description: "original", Content: content}, Responses: map[string]Response{"200": {Content: content}}}
	op := operationFromHint(hint)
	second := operationFromHint(hint)
	hint.Body.Description = "edited"
	item.Type = "changed"
	if op.RequestBody.Description != "original" {
		t.Fatal("body is shared")
	}
	copyItem := op.RequestBody.Content["application/json"].Schema.Items
	if copyItem == item || copyItem.Items != copyItem || copyItem.Type != "object" {
		t.Fatal("recursive schema was not copied")
	}
	copyItem.Properties["local"] = Schema{Type: "string"}
	if _, ok := copyItem.Properties["self"].Properties["local"]; !ok {
		t.Fatal("map cycle was not preserved")
	}
	if _, ok := properties["local"]; ok {
		t.Fatal("input map was mutated")
	}
	if _, ok := second.Responses["200"].Content["application/json"].Schema.Items.Properties["local"]; ok {
		t.Fatal("second result was mutated")
	}
}

func TestGenerateCompletesRequiredPathParameters(t *testing.T) {
	const routePath = "/teams/:team/users/:id"
	const openAPIPath = "/teams/{team}/users/{id}"
	hint := Op{Params: []Param{
		{In: "path", Name: "id", Description: "User number", Schema: Schema{Type: "integer"}},
		QueryParam("team", Schema{Type: "string"}),
	}}
	routes := []router.RouteInfo{
		{Method: http.MethodGet, Path: routePath},
		{Method: http.MethodPost, Path: routePath},
		{Method: http.MethodGet, Path: "/assets/*path"},
	}
	doc := New().Generate(routes, map[string]Op{"GET " + routePath: hint})
	assertParameter := func(got Param, name, location, kind, description string, required bool) {
		t.Helper()
		if got.Name != name || got.In != location || got.Required != required || got.Schema.Type != kind || got.Description != description {
			t.Errorf("unexpected parameter: %+v", got)
		}
	}
	assertDocument := func(doc Document) {
		t.Helper()
		item := doc.Paths[openAPIPath]
		if item.Get == nil || item.Post == nil || len(item.Get.Parameters) != 3 || len(item.Post.Parameters) != 2 {
			t.Fatalf("unexpected operations: %+v", item)
		}
		assertParameter(item.Get.Parameters[0], "id", "path", "integer", "User number", true)
		assertParameter(item.Get.Parameters[1], "team", "query", "string", "", false)
		assertParameter(item.Get.Parameters[2], "team", "path", "string", "", true)
		assertParameter(item.Post.Parameters[0], "team", "path", "string", "", true)
		assertParameter(item.Post.Parameters[1], "id", "path", "string", "", true)
		wildcard := doc.Paths["/assets/{path}"].Get
		if wildcard == nil || len(wildcard.Parameters) != 1 {
			t.Fatalf("unexpected wildcard operation: %+v", wildcard)
		}
		assertParameter(wildcard.Parameters[0], "path", "path", "string", "", true)
	}
	assertDocument(doc)
	if len(hint.Params) != 2 || hint.Params[0].Required {
		t.Fatalf("modified hint: %+v", hint)
	}

	encoded, err := MarshalJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Document
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	assertDocument(decoded)

	yaml, err := MarshalYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(yaml), "required: true"); got != 5 {
		t.Errorf("YAML contains %d required parameters, want 5:\n%s", got, yaml)
	}
}

func TestGeneratePrefersNamedRouteHint(t *testing.T) {
	const path = "/items/:id"
	route := router.RouteInfo{Method: http.MethodGet, Path: path, Meta: router.RouteMeta{Name: "item.get"}}
	hints := map[string]Op{
		"item.get":                  {Summary: "Named operation", Params: []Param{QueryParam("named", String)}},
		http.MethodGet + " " + path: {Summary: "Fallback operation", Params: []Param{QueryParam("fallback", String)}},
	}
	for _, tc := range []struct{ routeName, summary, query string }{
		{"item.get", "Named operation", "named"},
		{"missing", "Fallback operation", "fallback"},
	} {
		route.Meta.Name = tc.routeName
		op := New().Generate([]router.RouteInfo{route}, hints).Paths["/items/{id}"].Get
		if op == nil || op.Summary != tc.summary || len(op.Parameters) != 2 ||
			op.Parameters[0].Name != tc.query || op.Parameters[0].In != "query" ||
			op.Parameters[1].Name != "id" || op.Parameters[1].In != "path" || !op.Parameters[1].Required {
			t.Fatalf("operation for %q = %+v", tc.routeName, op)
		}
	}
}

func TestGenerateIsolatesOperationsUsingSameHint(t *testing.T) {
	routes := []router.RouteInfo{
		{Method: http.MethodPost, Path: "/first", Meta: router.RouteMeta{Name: "shared"}},
		{Method: http.MethodPost, Path: "/second", Meta: router.RouteMeta{Name: "shared"}},
	}
	properties := map[string]Schema{"name": {Type: "string"}}
	body := &RequestBody{Description: "original", Content: JSONContent(Schema{Type: "object", Properties: properties})}
	hints := map[string]Op{"shared": {Body: body}}
	doc := New().Generate(routes, hints)
	first, second := doc.Paths["/first"].Post, doc.Paths["/second"].Post
	if first == nil || second == nil || first.RequestBody == nil || second.RequestBody == nil {
		t.Fatalf("generated operations missing request bodies: %+v, %+v", first, second)
	}
	first.RequestBody.Description = "changed"
	first.RequestBody.Content["application/json"].Schema.Properties["name"] = Schema{Type: "integer"}
	delete(first.RequestBody.Content, "application/json")
	if body.Description != "original" || properties["name"].Type != "string" ||
		second.RequestBody.Description != "original" ||
		second.RequestBody.Content["application/json"].Schema.Properties["name"].Type != "string" {
		t.Fatal("changing an operation affected the source hint or another generated operation")
	}
}

func TestGenerateSkipsMethodsWithoutOperations(t *testing.T) {
	doc := New().Generate([]router.RouteInfo{
		{Method: http.MethodConnect, Path: "/only-connect"},
		{Method: http.MethodGet, Path: "/mixed"},
		{Method: http.MethodConnect, Path: "/mixed"},
	}, map[string]Op{"GET /mixed": {Summary: "keep me"}})
	if len(doc.Paths) != 1 || doc.Paths["/mixed"].Get == nil || doc.Paths["/mixed"].Get.Summary != "keep me" {
		t.Fatalf("paths: %+v", doc.Paths)
	}
}

func TestGenerateKeepsDistinctOperationsOnSharedPath(t *testing.T) {
	const path = "/items/:id"
	methods := map[string]string{
		http.MethodPut: "put", http.MethodPatch: "patch", http.MethodHead: "head", http.MethodOptions: "options",
	}
	r := router.NewRouter()
	hints := make(map[string]Op)
	for method, name := range methods {
		if err := r.AddRoute(method, path, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})); err != nil {
			t.Fatal(err)
		}
		hints[method+" "+path] = Op{Summary: name}
	}
	encoded, err := MarshalJSON(New().Generate(r.Routes(), hints))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]Operation
	}
	if err := json.Unmarshal(encoded, &doc); err != nil {
		t.Fatal(err)
	}
	ops := doc.Paths["/items/{id}"]
	if len(ops) != len(methods) || len(doc.Paths) != 1 {
		t.Fatalf("paths = %+v", doc.Paths)
	}
	for _, name := range methods {
		op, ok := ops[name]
		if !ok || op.Summary != name || len(op.Parameters) != 1 || op.Parameters[0].Name != "id" || !op.Parameters[0].Required {
			t.Errorf("%s operation = %+v", name, op)
		}
	}
}

func TestGenerateIncludesRootRoute(t *testing.T) {
	r := router.NewRouter()
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, path := range []string{"/", "/healthz"} {
		if err := r.AddRoute(http.MethodGet, path, handler); err != nil {
			t.Fatal(err)
		}
	}
	doc := New().Generate(r.Routes(), nil)
	if len(doc.Paths) != 2 || doc.Paths["/healthz"].Get == nil {
		t.Fatalf("expected root and health routes, got %+v", doc.Paths)
	}
	root := doc.Paths["/"]
	if root.Get == nil || root != (PathItem{Get: root.Get}) {
		t.Fatalf("unexpected root operations: %+v", root)
	}
	if len(root.Get.Parameters) != 0 || root.Get.Responses["200"].Description != "OK" {
		t.Errorf("unexpected root operation: %+v", root.Get)
	}
}
