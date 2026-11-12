package contract

import (
	"context"
	"reflect"
	"testing"
)

func TestRequestContextFromContext(t *testing.T) {
	result := RequestContextFromContext(t.Context())
	if len(result.Params) != 0 {
		t.Fatal("expected empty RequestContext")
	}

	rc := RequestContext{
		Params:       map[string]string{"id": "123"},
		RoutePattern: "/users/:id",
		RouteName:    "user_show",
	}
	ctx := WithRequestContext(t.Context(), rc)
	result = RequestContextFromContext(ctx)
	if result.Params == nil || result.Params["id"] != "123" {
		t.Fatal("expected RequestContext with params")
	}
	if result.RoutePattern != "/users/:id" || result.RouteName != "user_show" {
		t.Fatal("expected route fields from RequestContext")
	}
}

func TestWithRequestContextNilContext(t *testing.T) {
	rc := RequestContext{
		Params:       map[string]string{"id": "123"},
		RoutePattern: "/users/:id",
		RouteName:    "user_show",
	}

	ctx := WithRequestContext(nil, rc)
	result := RequestContextFromContext(ctx)
	if result.Params == nil || result.Params["id"] != "123" {
		t.Fatal("expected RequestContext to survive nil parent context")
	}
	if result.RoutePattern != "/users/:id" || result.RouteName != "user_show" {
		t.Fatal("expected route fields from nil-parent RequestContext")
	}
}

func TestRequestContextParamsAreCopied(t *testing.T) {
	params := map[string]string{"id": "42"}
	ctx := WithRequestContext(t.Context(), RequestContext{
		Params:       params,
		RoutePattern: "/users/:id",
		RouteName:    "users.show",
	})

	params["id"] = "mutated"

	rc := RequestContextFromContext(ctx)
	if rc.Params["id"] != "42" {
		t.Fatalf("expected stored params to be isolated, got %q", rc.Params["id"])
	}

	rc.Params["id"] = "returned-mutated"
	again := RequestContextFromContext(ctx)
	if again.Params["id"] != "42" {
		t.Fatalf("expected returned params mutation to be isolated, got %q", again.Params["id"])
	}
	if again.RoutePattern != "/users/:id" || again.RouteName != "users.show" {
		t.Fatalf("expected route metadata to be preserved, got %+v", again)
	}
}

func TestRequestParamFromContext(t *testing.T) {
	ctx := WithRequestContext(t.Context(), RequestContext{
		Params: map[string]string{"id": "42"},
	})

	if got := RequestParamFromContext(ctx, "id"); got != "42" {
		t.Fatalf("RequestParamFromContext id = %q, want %q", got, "42")
	}
	if got := RequestParamFromContext(ctx, "missing"); got != "" {
		t.Fatalf("RequestParamFromContext missing = %q, want empty", got)
	}
	if got := RequestParamFromContext(ctx, ""); got != "" {
		t.Fatalf("RequestParamFromContext empty key = %q, want empty", got)
	}
	if got := RequestParamFromContext(nil, "id"); got != "" {
		t.Fatalf("RequestParamFromContext nil context = %q, want empty", got)
	}
}

func TestRequestParamFromContextDoesNotExposeMutableMap(t *testing.T) {
	params := map[string]string{"id": "42"}
	ctx := WithRequestContext(t.Context(), RequestContext{Params: params})

	params["id"] = "mutated"

	if got := RequestParamFromContext(ctx, "id"); got != "42" {
		t.Fatalf("RequestParamFromContext id = %q, want copied value", got)
	}
}

func TestRequestContextStableCarrierFields(t *testing.T) {
	rt := reflect.TypeOf(RequestContext{})
	want := []string{"Params", "RoutePattern", "RouteName"}
	if rt.NumField() != len(want) {
		t.Fatalf("RequestContext field count = %d, want %d", rt.NumField(), len(want))
	}
	for i, name := range want {
		if got := rt.Field(i).Name; got != name {
			t.Fatalf("RequestContext field %d = %s, want %s", i, got, name)
		}
	}
}

func TestWithRouteStatePreservesContext(t *testing.T) {
	state := new(RouteState)
	state.AddParam("id", "7")
	ctx := WithRouteState(nil, state)
	if got := RequestParamFromContext(ctx, "id"); got != "7" {
		t.Fatalf("parameter = %q, want 7", got)
	}

	parent, cancel := context.WithCancel(WithRequestID(t.Context(), "request-42"))
	defer cancel()
	ctx = WithRouteState(parent, state)
	cancel()
	if ctx.Err() != context.Canceled || RequestIDFromContext(ctx) != "request-42" {
		t.Fatal("route state did not preserve parent cancellation and metadata")
	}
}
