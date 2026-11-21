package authn

import (
	"context"
	"testing"
)

func TestWithPrincipalRoundTrip(t *testing.T) {
	p := &Principal{
		Subject:  "user-1",
		TenantID: "tenant-a",
		Roles:    []string{"admin"},
		Scopes:   []string{"read", "write"},
	}

	ctx := WithPrincipal(t.Context(), p)
	got := PrincipalFromContext(ctx)
	if got == nil {
		t.Fatal("expected non-nil principal from context")
	}
	if got.Subject != "user-1" {
		t.Errorf("Subject = %q, want user-1", got.Subject)
	}
	if got.TenantID != "tenant-a" {
		t.Errorf("TenantID = %q, want tenant-a", got.TenantID)
	}
}

func TestPrincipalContextCopiesMutableFields(t *testing.T) {
	p := &Principal{
		Subject: "user-1",
		Roles:   []string{"admin"},
		Scopes:  []string{"read"},
		Claims:  map[string]string{"token_id": "tok-1"},
	}

	ctx := WithPrincipal(t.Context(), p)
	p.Roles[0] = "mutated-role"
	p.Scopes[0] = "mutated-scope"
	p.Claims["token_id"] = "mutated-token"

	got := PrincipalFromContext(ctx)
	if got == nil {
		t.Fatal("expected non-nil principal")
	}
	if got.Roles[0] != "admin" {
		t.Fatalf("stored role alias = %q, want admin", got.Roles[0])
	}
	if got.Scopes[0] != "read" {
		t.Fatalf("stored scope alias = %q, want read", got.Scopes[0])
	}
	if got.Claims["token_id"] != "tok-1" {
		t.Fatalf("stored claim alias = %q, want tok-1", got.Claims["token_id"])
	}

	got.Roles[0] = "returned-role"
	got.Scopes[0] = "returned-scope"
	got.Claims["token_id"] = "returned-token"
	again := PrincipalFromContext(ctx)
	if again.Roles[0] != "admin" {
		t.Fatalf("context role mutated through returned principal = %q", again.Roles[0])
	}
	if again.Scopes[0] != "read" {
		t.Fatalf("context scope mutated through returned principal = %q", again.Scopes[0])
	}
	if again.Claims["token_id"] != "tok-1" {
		t.Fatalf("context claim mutated through returned principal = %q", again.Claims["token_id"])
	}
}

func TestPrincipalContextPreservesEmptySlices(t *testing.T) {
	for _, in := range [][]string{nil, {}} {
		ctx := WithPrincipal(t.Context(), &Principal{Roles: in, Scopes: in})
		got := PrincipalFromContext(ctx)
		if len(got.Roles) != 0 || len(got.Scopes) != 0 ||
			(got.Roles == nil) != (in == nil) || (got.Scopes == nil) != (in == nil) {
			t.Errorf("roles=%#v scopes=%#v, want %#v", got.Roles, got.Scopes, in)
		}
	}
}

func TestPrincipalContextPreservesEmptyClaims(t *testing.T) {
	for _, claims := range []map[string]string{nil, {}} {
		ctx := WithPrincipal(t.Context(), &Principal{Claims: claims})
		if claims != nil {
			claims["caller"] = "value"
		}
		got := PrincipalFromContext(ctx)
		if got == nil || (got.Claims == nil) != (claims == nil) || len(got.Claims) != 0 {
			t.Fatalf("stored principal = %#v, want empty claims with shape of %#v", got, claims)
		}
		if got.Claims != nil {
			got.Claims["reader"] = "value"
			if again := PrincipalFromContext(ctx); again == nil || again.Claims == nil || len(again.Claims) != 0 {
				t.Fatalf("reader changed stored claims: %#v", again)
			}
		}
	}
}

func TestWithPrincipalNilContext(t *testing.T) {
	p := &Principal{Subject: "user-1"}

	ctx := WithPrincipal(nil, p)
	got := PrincipalFromContext(ctx)
	if got == nil {
		t.Fatal("expected non-nil principal from nil parent context")
	}
	if got.Subject != "user-1" {
		t.Errorf("Subject = %q, want user-1", got.Subject)
	}
}

func TestPrincipalFromContextNil(t *testing.T) {
	got := PrincipalFromContext(t.Context())
	if got != nil {
		t.Fatalf("expected nil principal from empty context, got %+v", got)
	}
}

// foreignCtxKey simulates a context key owned by another package: it must
// never collide with this package's unexported principal key.
type foreignCtxKey struct{}

func TestPrincipalFromContextWrongType(t *testing.T) {
	ctx := context.WithValue(t.Context(), foreignCtxKey{}, "not-a-principal")
	got := PrincipalFromContext(ctx)
	if got != nil {
		t.Fatalf("expected nil for missing principal, got %+v", got)
	}
}

func TestWithPrincipalOverwrite(t *testing.T) {
	parent := WithPrincipal(t.Context(), &Principal{Subject: "user-1", Roles: []string{"reader"}, Claims: map[string]string{"fixture": "root"}})
	child := WithPrincipal(parent, &Principal{Subject: "user-2", Roles: []string{"editor"}, Claims: map[string]string{"fixture": "child"}})
	for _, ctx := range []context.Context{parent, child} {
		read := PrincipalFromContext(ctx)
		read.Roles[0] = "changed"
		read.Claims["fixture"] = "changed"
	}
	p, c := PrincipalFromContext(parent), PrincipalFromContext(child)
	if p.Subject != "user-1" || p.Roles[0] != "reader" || p.Claims["fixture"] != "root" ||
		c.Subject != "user-2" || c.Roles[0] != "editor" || c.Claims["fixture"] != "child" {
		t.Fatalf("parent=%+v child=%+v", p, c)
	}
}

func TestPrincipalFromContextNilPrincipalStored(t *testing.T) {
	ctx := WithPrincipal(t.Context(), nil)
	got := PrincipalFromContext(ctx)
	if got != nil {
		t.Fatalf("expected nil for explicitly stored nil principal, got %+v", got)
	}
}

func TestPrincipalFromContextNilContext(t *testing.T) {
	if got := PrincipalFromContext(nil); got != nil {
		t.Fatalf("expected nil for nil context, got %+v", got)
	}
}

func TestPrincipalCopyPreservesNilAndEmptyContainers(t *testing.T) {
	ctx := WithPrincipal(t.Context(), &Principal{Roles: []string{}, Claims: map[string]string{}})
	first := PrincipalFromContext(ctx)
	if first.Roles == nil || first.Claims == nil || first.Scopes != nil {
		t.Fatalf("first=%+v", first)
	}
	first.Claims["fixture"] = "local"
	first.Roles = append(first.Roles, "fixture")
	second := PrincipalFromContext(ctx)
	if len(second.Claims) != 0 || len(second.Roles) != 0 || second.Claims == nil || second.Roles == nil {
		t.Fatalf("second=%+v", second)
	}
}

func TestPrincipalSnapshotDistinguishesNilAndEmptyAttributes(t *testing.T) {
	nilClaims := PrincipalFromContext(WithPrincipal(t.Context(), &Principal{Subject: "fixture"}))
	if nilClaims.Claims != nil || nilClaims.Roles != nil || nilClaims.Scopes != nil {
		t.Fatalf("nil snapshot=%+v", nilClaims)
	}
	ctx := WithPrincipal(t.Context(), &Principal{Subject: "fixture", Claims: map[string]string{}, Roles: []string{}, Scopes: []string{}})
	empty := PrincipalFromContext(ctx)
	if empty.Claims == nil || empty.Roles == nil || empty.Scopes == nil {
		t.Fatalf("empty snapshot=%+v", empty)
	}
	empty.Claims["fixture"] = "changed"
	empty.Roles = append(empty.Roles, "reader")
	empty.Subject = "changed"
	if again := PrincipalFromContext(ctx); again.Subject != "fixture" || len(again.Claims) != 0 || len(again.Roles) != 0 || again.Roles == nil {
		t.Fatal("caller mutation changed the stored principal")
	}
}
