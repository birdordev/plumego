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
	p1 := &Principal{Subject: "user-1"}
	p2 := &Principal{Subject: "user-2"}

	ctx := WithPrincipal(t.Context(), p1)
	ctx = WithPrincipal(ctx, p2)

	got := PrincipalFromContext(ctx)
	if got == nil {
		t.Fatal("expected non-nil principal")
	}
	if got.Subject != "user-2" {
		t.Errorf("expected latest principal (user-2), got %q", got.Subject)
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
