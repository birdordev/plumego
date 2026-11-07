package protocol

import (
	"context"
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
