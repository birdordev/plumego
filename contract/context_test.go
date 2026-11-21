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

func TestRequestParamFromContextLooksUpEmptyKey(t *testing.T) {
	withMap := WithRequestContext(t.Context(), RequestContext{Params: map[string]string{"": "map"}})
	withInline := new(RouteState)
	withInline.AddParam("", "inline")
	withOverflow := new(RouteState)
	withOverflow.AddParam("", "first")
	for range maxInlineParams - 1 {
		withOverflow.AddParam("unused", "value")
	}
	withOverflow.AddParam("", "last")
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"snapshot", withMap, "map"},
		{"inline", WithRouteState(withMap, withInline), "inline"},
		{"overflow duplicate", WithRouteState(withMap, withOverflow), "last"},
		{"route state masks snapshot", WithRouteState(withMap, new(RouteState)), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct := RequestParamFromContext(tc.ctx, "")
			snapshot := RequestContextFromContext(tc.ctx).Params[""]
			if direct != tc.want || snapshot != tc.want {
				t.Errorf("direct = %q, snapshot = %q, want %q", direct, snapshot, tc.want)
			}
		})
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

func TestRouteStateAccessorsAgreeOnDuplicateParams(t *testing.T) {
	for _, entries := range []struct{ first, last int }{
		{0, 1}, {0, maxInlineParams}, {maxInlineParams, maxInlineParams + 1},
	} {
		var state RouteState
		for i := 0; i <= entries.last; i++ {
			switch i {
			case entries.first:
				state.AddParam("id", "first")
			case entries.last:
				state.AddParam("id", "last")
			default:
				state.AddParam("unused", "value")
			}
		}
		ctx := WithRouteState(t.Context(), &state)
		if got, snapshot := RequestParamFromContext(ctx, "id"), RequestContextFromContext(ctx).Params["id"]; got != "last" || got != snapshot {
			t.Errorf("entries %v: direct = %q, snapshot = %q", entries, got, snapshot)
		}
	}
}

func TestNestedRequestMetadataKeepsIndependentCarriers(t *testing.T) {
	params := map[string]string{"id": "inherited", "other": "stable"}
	parent := WithRequestID(t.Context(), "request-id")
	ctx := WithRequestContext(parent, RequestContext{RoutePattern: "/a/:id", Params: params})
	params["other"] = "changed"
	if got := RequestParamFromContext(ctx, "other"); got != "stable" {
		t.Fatalf("metadata did not snapshot source map: %q", got)
	}
	state := new(RouteState)
	state.AddParam("id", "from route")
	ctx = WithRouteState(ctx, state)
	if got := RequestIDFromContext(ctx); got != "request-id" {
		t.Fatalf("request id = %q", got)
	}
	if got := RequestParamFromContext(ctx, "id"); got != "from route" {
		t.Fatalf("active route param = %q", got)
	}
	if RequestIDFromContext(parent) != "request-id" || RequestParamFromContext(parent, "id") != "" {
		t.Fatal("parent carriers changed")
	}
}

func TestNilRouteStateFallsBackToLegacyMetadata(t *testing.T) {
	base := WithRequestID(WithRequestContext(t.Context(), RequestContext{RouteName: "legacy", Params: map[string]string{"id": "legacy"}}), "fixture")
	state := new(RouteState)
	state.SetName("active")
	state.AddParam("id", "active")
	active := WithRouteState(base, state)
	for _, tc := range []struct {
		ctx  context.Context
		want string
	}{
		{active, "active"}, {WithRouteState(active, nil), "legacy"},
	} {
		if got := RequestContextFromContext(tc.ctx); got.RouteName != tc.want ||
			RequestParamFromContext(tc.ctx, "id") != tc.want || RequestIDFromContext(tc.ctx) != "fixture" {
			t.Fatalf("context metadata=%+v", got)
		}
	}
}
