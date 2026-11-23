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

func TestStrictOperationIDOverrideAndDuplicates(t *testing.T) {
	routes := []router.RouteInfo{
		{Method: http.MethodGet, Path: "/items", Meta: router.RouteMeta{Name: "listItems"}},
		{Method: http.MethodPost, Path: "/items", Meta: router.RouteMeta{Name: "addItem"}},
	}
	get := Op{Responses: map[string]Response{"200": {Description: "OK"}}}
	post := Op{OperationID: "listItems", Responses: map[string]Response{"201": {Description: "Created"}}}
	hints := map[string]Op{"listItems": get, "addItem": post}
	g := New()
	if _, err := g.GenerateStrict(routes, hints); err == nil ||
		!strings.Contains(err.Error(), `GET /items and POST /items share operationId "listItems"`) {
		t.Fatalf("duplicate operation ID error = %v", err)
	}
	post.OperationID = "createItem"
	hints["addItem"] = post
	doc, err := g.GenerateStrict(routes, hints)
	if err != nil {
		t.Fatal(err)
	}
	if item := doc.Paths["/items"]; item.Get.OperationID != "listItems" || item.Post.OperationID != "createItem" {
		t.Fatalf("GET ID = %q, POST ID = %q", item.Get.OperationID, item.Post.OperationID)
	}
	for _, format := range []struct {
		marshal func(Document) ([]byte, error)
		want    string
	}{
		{MarshalJSON, `"operationId": "createItem"`},
		{MarshalYAML, `operationId: "createItem"`},
	} {
		data, err := format.marshal(doc)
		if err != nil || !strings.Contains(string(data), format.want) {
			t.Fatalf("missing %q in %s: %v", format.want, data, err)
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

func TestStrictValidatesComponentsAndUnusedHints(t *testing.T) {
	routes := []router.RouteInfo{{Method: http.MethodGet, Path: "/items", Meta: router.RouteMeta{Name: "listItems"}}}
	op := Op{Security: []SecurityRequirement{{"bearer": {}}}, Responses: map[string]Response{"200": {Description: "OK", Content: JSONContent(Schema{Ref: "#/components/schemas/Item"})}}}
	hints := map[string]Op{"listItems": op, "stale": op}
	g := New()
	if _, err := g.GenerateStrict(routes, hints); err == nil || !strings.Contains(err.Error(), "unmatched operation hint") {
		t.Fatalf("error=%v", err)
	}
	delete(hints, "stale")
	if _, err := g.GenerateStrict(routes, hints); err == nil || !strings.Contains(err.Error(), "undefined security scheme") || !strings.Contains(err.Error(), "undefined schema") {
		t.Fatalf("error=%v", err)
	}
	g.WithComponents(&Components{Schemas: map[string]Schema{"Item": String}, SecuritySchemes: map[string]SecurityScheme{"bearer": {Type: "http", Scheme: "bearer"}}})
	doc, err := g.GenerateStrict(routes, hints)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Paths["/items"].Get.OperationID != "listItems" {
		t.Fatalf("operation=%#v", doc.Paths["/items"].Get)
	}
	for _, marshal := range []func(Document) ([]byte, error){MarshalJSON, MarshalYAML} {
		data, err := marshal(doc)
		if err != nil || !strings.Contains(string(data), "schemas") ||
			!strings.Contains(string(data), "#/components/schemas/Item") || !strings.Contains(string(data), "listItems") {
			t.Fatalf("render %s %v", data, err)
		}
	}
}

func TestStrictValidatesSecuritySchemeDefinitions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scheme SecurityScheme
		want   string
	}{
		{"missing type", SecurityScheme{}, "invalid security scheme type"},
		{"missing HTTP scheme", SecurityScheme{Type: "http"}, "valid scheme token"},
		{"invalid HTTP scheme", SecurityScheme{Type: "http", Scheme: "Bearer token"}, "valid scheme token"},
		{"missing API key name", SecurityScheme{Type: "apiKey", In: "header"}, "requires a name"},
		{"invalid API key location", SecurityScheme{Type: "apiKey", In: "body", Name: "key"}, "location must be"},
		{"OAuth2", SecurityScheme{Type: "oauth2"}, "outside the supported security scheme model"},
		{"OpenID Connect", SecurityScheme{Type: "openIdConnect"}, "outside the supported security scheme model"},
		{"Bearer", SecurityScheme{Type: "http", Scheme: "bearer"}, ""},
		{"custom HTTP token", SecurityScheme{Type: "http", Scheme: "Example-Auth+v1"}, ""},
		{"API key in header", SecurityScheme{Type: "apiKey", In: "header", Name: "X-API-Key"}, ""},
		{"API key in query", SecurityScheme{Type: "apiKey", In: "query", Name: "key"}, ""},
		{"API key in cookie", SecurityScheme{Type: "apiKey", In: "cookie", Name: "key"}, ""},
		{"mutual TLS", SecurityScheme{Type: "mutualTLS"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := New().WithComponents(&Components{SecuritySchemes: map[string]SecurityScheme{"testScheme": tc.scheme}})
			routes := []router.RouteInfo{{Method: http.MethodGet, Path: "/items"}}
			hints := map[string]Op{"GET /items": {
				Security:  []SecurityRequirement{{"testScheme": {}}},
				Responses: map[string]Response{"200": {Description: "OK"}},
			}}
			if got := g.Generate(routes, hints).Components.SecuritySchemes["testScheme"]; got != tc.scheme {
				t.Fatalf("permissive mode changed input: %+v", got)
			}
			_, err := g.GenerateStrict(routes, hints)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), `security scheme "testScheme"`) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStrictValidatesParameterLocations(t *testing.T) {
	routes := []router.RouteInfo{{Method: http.MethodGet, Path: "/items/:id"}}
	const operation = "GET /items/:id"
	idParams := []Param{PathParam("id", String), QueryParam("id", String), HeaderParam("id", String), {In: "cookie", Name: "id", Schema: String}}
	op := Op{Params: idParams, Responses: map[string]Response{"200": {Description: "OK"}}}
	if _, err := New().GenerateStrict(routes, map[string]Op{operation: op}); err != nil {
		t.Fatalf("same name across locations: %v", err)
	}
	op.Params = append(op.Params, QueryParam("id", String), HeaderParam("id", String),
		Param{In: "cookie", Name: "id"}, Param{In: "query", Name: " "}, Param{In: "form", Name: "name"})
	_, err := New().GenerateStrict(routes, map[string]Op{operation: op})
	if err == nil {
		t.Fatal("strict generation accepted invalid parameters")
	}
	for _, want := range []string{`duplicate query parameter "id"`, `duplicate header parameter "id"`,
		`duplicate cookie parameter "id"`, "missing name for query parameter", `invalid location "form"`, "GET /items/{id}"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestStrictResolvesNestedLocalSchemaPointers(t *testing.T) {
	g := New().WithComponents(&Components{Schemas: map[string]Schema{
		"List": {Type: "array", Items: &Schema{Type: "object", Properties: map[string]Schema{"a/b~c": String}}},
	}})
	routes := []router.RouteInfo{{Method: http.MethodGet, Path: "/items"}}
	for _, tc := range []struct {
		ref string
		err string
	}{
		{"#/components/schemas/L%69st/items/properties/a~1b~0c", ""},
		{"#/components/schemas/List/items/properties/unknown", "undefined schema reference"},
		{"#/components/schemas/List/items/type", "must resolve to an object or boolean"},
		{"#/%6dissing", "undefined schema reference"},
		{"#/%ZZ", "undefined schema reference"},
		{"models.json#/components/schemas/External", ""},
	} {
		_, err := g.GenerateStrict(routes, map[string]Op{"GET /items": {
			Responses: map[string]Response{"200": {Description: "OK", Content: JSONContent(Schema{Ref: tc.ref})}},
		}})
		if tc.err == "" && err != nil || tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("ref %q returned %v, want %q", tc.ref, err, tc.err)
		}
	}
}

func TestStrictRejectsInvalidComponentNames(t *testing.T) {
	g := New().WithComponents(&Components{
		Schemas:         map[string]Schema{"bad/name": String, "my.org.Item_1-v2": String},
		SecuritySchemes: map[string]SecurityScheme{"": {Type: "mutualTLS"}},
	})
	_, err := g.GenerateStrict(nil, nil)
	if err == nil || !strings.Contains(err.Error(), `invalid schema component name "bad/name"`) ||
		!strings.Contains(err.Error(), `invalid security scheme component name ""`) || strings.Contains(err.Error(), "my.org") {
		t.Fatalf("component name errors = %v", err)
	}
}
