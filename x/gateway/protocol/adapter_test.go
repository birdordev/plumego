package protocol

import (
	"context"
	"slices"
	"strings"
	"testing"
)

type testAdapter struct {
	name string
	path string
}

func (a *testAdapter) Name() string { return a.name }

func (a *testAdapter) Handles(req *HTTPRequest) bool {
	return req != nil && strings.HasPrefix(req.URL, a.path)
}

func (a *testAdapter) Transform(context.Context, *HTTPRequest) (Request, error) {
	return &GRPCRequest{}, nil
}

func (a *testAdapter) Execute(context.Context, Request) (Response, error) {
	return &GRPCResponse{Status: 200}, nil
}

func (a *testAdapter) Encode(context.Context, Response, ResponseWriter) error { return nil }

func TestRegistryFind(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&testAdapter{name: "graphql", path: "/graphql"})
	reg.Register(&testAdapter{name: "grpc", path: "/grpc"})

	got := reg.Find(&HTTPRequest{URL: "/grpc/users"})
	if got == nil || got.Name() != "grpc" {
		t.Fatalf("expected grpc adapter, got %+v", got)
	}
}

func TestRegistryAdaptersReturnsCopy(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&testAdapter{name: "a", path: "/a"})

	adapters := reg.Adapters()
	if len(adapters) != 1 {
		t.Fatalf("expected 1 adapter, got %d", len(adapters))
	}

	// Appending to the returned slice (potentially in place) must not
	// affect the registry's internal state.
	_ = append(adapters, &testAdapter{name: "b", path: "/b"})
	if reg.Count() != 1 {
		t.Fatalf("registry adapters should not be affected by external append")
	}
}

func TestGraphQLResponseErrorMetadataMatchesErrors(t *testing.T) {
	for _, count := range []int{0, 2} {
		res := &GraphQLResponse{
			Data:       map[string]any{"fixture": true},
			Errors:     make([]GraphQLError, count),
			Extensions: map[string]any{"trace": "fixture"},
		}
		var generic Response = res
		metadata := generic.Metadata()
		if res.HasErrors() != (count > 0) {
			t.Errorf("HasErrors for %d errors = %t", count, res.HasErrors())
		}
		if count > 0 {
			if metadata["has_errors"] != true || metadata["error_count"] != count {
				t.Errorf("error metadata = %+v", metadata)
			}
		} else {
			if _, ok := metadata["has_errors"]; ok {
				t.Errorf("unexpected error flag: %+v", metadata)
			}
			if _, ok := metadata["error_count"]; ok {
				t.Errorf("unexpected error count: %+v", metadata)
			}
		}
		extensions, ok := metadata["extensions"].(map[string]any)
		if !ok || extensions["trace"] != "fixture" {
			t.Errorf("extensions = %v", metadata["extensions"])
		}
	}
}

func TestGraphQLProtocolAccessors(t *testing.T) {
	var request Request = &GraphQLRequest{}
	var response Response = &GraphQLResponse{}
	if headers := request.Headers(); request.Method() != "query" || headers == nil || len(headers) != 0 || request.Body() != nil {
		t.Error("unexpected GraphQL request defaults")
	}
	if headers := response.Headers(); response.StatusCode() != 200 || headers == nil || len(headers) != 0 || response.Body() != nil {
		t.Error("unexpected GraphQL response defaults")
	}

	reqBody := strings.NewReader("request")
	req := &GraphQLRequest{
		Query: "{ title }", OperationName: "Lookup",
		Variables: map[string]any{"limit": 2}, Extensions: map[string]any{"local": true},
		HeaderMap: map[string][]string{"Content-Type": {"application/json"}}, BodyReader: reqBody,
	}
	request = req
	if request.Method() != "Lookup" || request.Body() != reqBody || !slices.Equal(request.Headers()["Content-Type"], []string{"application/json"}) {
		t.Error("request did not expose its configured method, body, and headers")
	}
	metadata := request.Metadata()
	variables, variablesOK := metadata["variables"].(map[string]any)
	extensions, extensionsOK := metadata["extensions"].(map[string]any)
	if metadata["query"] != req.Query || metadata["operation_name"] != req.OperationName ||
		!variablesOK || variables["limit"] != 2 || !extensionsOK || extensions["local"] != true {
		t.Errorf("request metadata = %v", metadata)
	}

	respBody := strings.NewReader("response")
	response = &GraphQLResponse{
		Status: 202, HeaderMap: map[string][]string{"Content-Type": {"application/json"}}, BodyReader: respBody,
	}
	if response.StatusCode() != 202 || response.Body() != respBody || !slices.Equal(response.Headers()["Content-Type"], []string{"application/json"}) {
		t.Error("response did not expose its configured status, body, and headers")
	}
}
