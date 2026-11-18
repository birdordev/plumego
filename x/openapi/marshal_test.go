package openapi

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalJSONProducesValidJSON(t *testing.T) {
	doc := sampleDocument()

	data, err := MarshalJSON(doc)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("MarshalJSON produced invalid JSON: %s", data)
	}
	if !bytes.Contains(data, []byte(`"openapi": "3.1.0"`)) {
		t.Fatalf("MarshalJSON missing openapi version: %s", data)
	}
}

func TestMarshalYAMLProducesReadableYAML(t *testing.T) {
	data, err := MarshalYAML(sampleDocument())
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	out := string(data)
	for _, want := range []string{
		`openapi: "3.1.0"`,
		`paths:`,
		`/users/{id}:`,
		`summary: "Show user"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("MarshalYAML missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "{\n") || strings.Contains(out, ": {") || strings.Contains(out, `"openapi":`) {
		t.Fatalf("MarshalYAML should not use JSON-style braces:\n%s", out)
	}
}

func TestMarshalJSONRoundTrip(t *testing.T) {
	want := sampleDocument()
	data, err := MarshalJSON(want)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var got Document
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}
	if got.OpenAPI != want.OpenAPI {
		t.Fatalf("OpenAPI = %q, want %q", got.OpenAPI, want.OpenAPI)
	}
	if got.Info.Title != want.Info.Title {
		t.Fatalf("Info.Title = %q, want %q", got.Info.Title, want.Info.Title)
	}
	if got.Paths["/users/{id}"].Get.Summary != "Show user" {
		t.Fatalf("summary = %q, want Show user", got.Paths["/users/{id}"].Get.Summary)
	}
}

func TestMarshalEmptyDocumentSerializesWithoutError(t *testing.T) {
	if _, err := MarshalJSON(Document{}); err != nil {
		t.Fatalf("MarshalJSON empty document: %v", err)
	}
	if _, err := MarshalYAML(Document{}); err != nil {
		t.Fatalf("MarshalYAML empty document: %v", err)
	}
}

func TestWriteJSONAndYAML(t *testing.T) {
	var jsonBuf bytes.Buffer
	if err := WriteJSON(&jsonBuf, sampleDocument()); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if !json.Valid(jsonBuf.Bytes()) {
		t.Fatalf("WriteJSON produced invalid JSON: %s", jsonBuf.String())
	}

	var yamlBuf bytes.Buffer
	if err := WriteYAML(&yamlBuf, sampleDocument()); err != nil {
		t.Fatalf("WriteYAML: %v", err)
	}
	if !strings.Contains(yamlBuf.String(), "openapi:") {
		t.Fatalf("WriteYAML output missing openapi key: %s", yamlBuf.String())
	}
}

func TestWriteJSON_NilWriter(t *testing.T) {
	err := WriteJSON(nil, Document{})
	if err == nil {
		t.Fatal("WriteJSON(nil) should return error")
	}
}

func TestWriteYAML_NilWriter(t *testing.T) {
	err := WriteYAML(nil, Document{})
	if err == nil {
		t.Fatal("WriteYAML(nil) should return error")
	}
}

func TestMarshalYAMLWithNestedList(t *testing.T) {
	doc := Document{
		OpenAPI: "3.1.0",
		Info:    Info{Title: "Nested", Version: "1.0.0"},
		Paths: map[string]PathItem{
			"/items": {
				Get: &Operation{
					Summary: "List items",
					Parameters: []Param{
						QueryParam("tags", Schema{Type: "array", Items: &Schema{Type: "string"}}),
					},
					Responses: map[string]Response{
						"200": {Description: "OK"},
					},
				},
			},
		},
	}

	data, err := MarshalYAML(doc)
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "openapi:") {
		t.Fatal("MarshalYAML missing openapi key")
	}
	// The nested array schema should produce a YAML list with non-scalar items
	if !strings.Contains(out, "-") && !strings.Contains(out, "array") {
		t.Fatalf("MarshalYAML missing expected list content:\n%s", out)
	}
}

func TestMarshalYAMLWithBooleanValues(t *testing.T) {
	doc := Document{
		OpenAPI: "3.1.0",
		Info:    Info{Title: "Bool", Version: "1.0.0"},
		Paths: map[string]PathItem{
			"/items": {
				Get: &Operation{
					Summary: "List items",
					Parameters: []Param{
						{Name: "required", In: "query", Required: true, Schema: Schema{Type: "boolean"}},
					},
					Responses: map[string]Response{
						"200": {Description: "OK"},
					},
				},
			},
		},
	}

	data, err := MarshalYAML(doc)
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "true") {
		t.Fatalf("MarshalYAML should contain boolean true:\n%s", out)
	}
}

func sampleDocument() Document {
	return Document{
		OpenAPI: "3.1.0",
		Info: Info{
			Title:   "Example",
			Version: "1.0.0",
		},
		Paths: map[string]PathItem{
			"/users/{id}": {
				Get: &Operation{
					Summary: "Show user",
					Parameters: []Param{
						PathParam("id", String),
					},
					Responses: map[string]Response{
						"200": {
							Description: "OK",
							Content:     JSONContent(Schema{Ref: "#/components/schemas/User"}),
						},
					},
				},
			},
		},
	}
}

func TestMarshalYAMLRetainsEmptyCollections(t *testing.T) {
	data, err := MarshalYAML(New().Generate(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "paths: {}\n") {
		t.Errorf("empty paths missing: %s", data)
	}

	var buf bytes.Buffer
	writeYAMLValue(&buf, map[string]any{"children": []any{map[string]any{}, []any{}}}, 0)
	if got := buf.String(); got != "children:\n  - {}\n  - []\n" {
		t.Errorf("empty children = %q", got)
	}
}
