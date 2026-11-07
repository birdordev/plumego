package jwt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spcent/plumego/security/authn"
)

func TestIdentityVersionIsCallerOwned(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	cfg.AccessExpiration = time.Minute

	mgr, err := NewJWTManager(store, cfg)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	pair, err := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "user-3", Version: 7}, AuthorizationClaims{})
	if err != nil {
		t.Fatalf("generate pair: %v", err)
	}

	claims, err := mgr.VerifyToken(t.Context(), pair.AccessToken, TokenTypeAccess)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if claims.Identity.Version != 7 {
		t.Errorf("expected version=7, got %d", claims.Identity.Version)
	}
}

func TestJWTManagerHonorsCanceledContext(t *testing.T) {
	store := newTestStore(t)
	mgr, err := NewJWTManager(store, DefaultJWTConfig())
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	pair, err := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "user-canceled"}, AuthorizationClaims{})
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := mgr.GenerateTokenPair(ctx, IdentityClaims{Subject: "user-canceled"}, AuthorizationClaims{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GenerateTokenPair canceled error = %v, want context.Canceled", err)
	}
	if _, err := mgr.VerifyToken(ctx, pair.AccessToken, TokenTypeAccess); !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyToken canceled error = %v, want context.Canceled", err)
	}
}

func TestJWTManagerClockControlsIssuedClaims(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	cfg.AccessExpiration = 10 * time.Minute
	fixed := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	mgr, err := NewJWTManager(store, cfg)
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}
	mgr.now = func() time.Time { return fixed }

	pair, err := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "clock-user"}, AuthorizationClaims{})
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}
	claims, err := mgr.VerifyToken(t.Context(), pair.AccessToken, TokenTypeAccess)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}

	if claims.IssuedAt != fixed.Unix() {
		t.Fatalf("IssuedAt = %d, want %d", claims.IssuedAt, fixed.Unix())
	}
	if claims.NotBefore != fixed.Unix() {
		t.Fatalf("NotBefore = %d, want %d", claims.NotBefore, fixed.Unix())
	}
	if claims.ExpiresAt != fixed.Add(cfg.AccessExpiration).Unix() {
		t.Fatalf("ExpiresAt = %d, want %d", claims.ExpiresAt, fixed.Add(cfg.AccessExpiration).Unix())
	}
}

func TestCheckPolicy(t *testing.T) {
	tests := []struct {
		name   string
		policy AuthZPolicy
		auth   AuthorizationClaims
		want   bool
	}{
		{
			name: "all roles and permissions match",
			policy: AuthZPolicy{
				AllRoles:       []string{"admin"},
				AllPermissions: []string{"read", "write"},
			},
			auth: AuthorizationClaims{
				Roles:       []string{"admin", "user"},
				Permissions: []string{"read", "write", "delete"},
			},
			want: true,
		},
		{
			name: "missing required role",
			policy: AuthZPolicy{
				AllRoles: []string{"admin"},
			},
			auth: AuthorizationClaims{
				Roles: []string{"user"},
			},
			want: false,
		},
		{
			name: "any role match",
			policy: AuthZPolicy{
				AnyRole: []string{"admin", "moderator"},
			},
			auth: AuthorizationClaims{
				Roles: []string{"moderator", "user"},
			},
			want: true,
		},
		{
			name: "any permission match",
			policy: AuthZPolicy{
				AnyPermission: []string{"write", "delete"},
			},
			auth: AuthorizationClaims{
				Permissions: []string{"read", "delete"},
			},
			want: true,
		},
		{
			name:   "empty policy denies by default",
			policy: AuthZPolicy{},
			auth: AuthorizationClaims{
				Roles: []string{"user"},
			},
			want: false,
		},
		{
			name: "empty policy allows when explicit",
			policy: AuthZPolicy{
				AllowEmpty: true,
			},
			auth: AuthorizationClaims{
				Roles: []string{"user"},
			},
			want: true,
		},
		{
			name: "case insensitive matching",
			policy: AuthZPolicy{
				AllRoles: []string{"Admin"},
			},
			auth: AuthorizationClaims{
				Roles: []string{"admin"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkPolicy(tt.policy, tt.auth)
			if got != tt.want {
				t.Errorf("checkPolicy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthenticatorWithMiddlewareAuth(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	mgr, _ := NewJWTManager(store, cfg)

	pair, _ := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "user-mw"}, AuthorizationClaims{Roles: []string{"user"}})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := TokenClaimsFromContext(r.Context())
		if claims == nil {
			t.Errorf("expected claims in context")
			return
		}
		if claims.Identity.Subject != "user-mw" {
			t.Errorf("unexpected subject in claims: %s", claims.Identity.Subject)
		}
		w.WriteHeader(http.StatusOK)
	})

	authenticator := mustAuthMiddleware(t, mgr.Authenticator(TokenTypeAccess))
	wrapped := authenticator(handler)

	// test valid token
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	// test missing token
	req = httptest.NewRequest("GET", "/protected", nil)
	rec = httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}

	// test invalid token
	req = httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	rec = httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}
}

func TestTokenClaimsContextCopiesMutableSlices(t *testing.T) {
	claims := &TokenClaims{
		Identity: IdentityClaims{Subject: "user-context-copy"},
		Authorization: AuthorizationClaims{
			Roles:       []string{"admin"},
			Permissions: []string{"read:all"},
		},
	}

	ctx := WithTokenClaims(t.Context(), claims)
	claims.Authorization.Roles[0] = "mutated-role"
	claims.Authorization.Permissions[0] = "mutated-permission"

	got := TokenClaimsFromContext(ctx)
	if got == nil {
		t.Fatal("expected claims from context")
	}
	if got.Authorization.Roles[0] != "admin" {
		t.Fatalf("stored role alias = %q, want admin", got.Authorization.Roles[0])
	}
	if got.Authorization.Permissions[0] != "read:all" {
		t.Fatalf("stored permission alias = %q, want read:all", got.Authorization.Permissions[0])
	}

	got.Authorization.Roles[0] = "returned-role"
	got.Authorization.Permissions[0] = "returned-permission"
	again := TokenClaimsFromContext(ctx)
	if again.Authorization.Roles[0] != "admin" {
		t.Fatalf("context role mutated through returned claims = %q", again.Authorization.Roles[0])
	}
	if again.Authorization.Permissions[0] != "read:all" {
		t.Fatalf("context permission mutated through returned claims = %q", again.Authorization.Permissions[0])
	}
}

func TestPolicyAuthorizerWithMiddlewareAuth(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
	})
	policy := AuthZPolicy{
		AllRoles: []string{"admin"},
	}
	authMiddleware := mustAuthorizeMiddleware(t, PolicyAuthorizer{Policy: policy}, "", "")
	wrapped := authMiddleware(handler)

	// test valid claims
	claims := &TokenClaims{
		Identity: IdentityClaims{Subject: "user-authz"},
		Authorization: AuthorizationClaims{
			Roles: []string{"admin"},
		},
	}

	req := httptest.NewRequest("GET", "/admin", nil)
	ctx := authn.WithPrincipal(req.Context(), PrincipalFromClaims(claims))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	// test invalid claims
	claims.Authorization.Roles = []string{"user"}
	req = httptest.NewRequest("GET", "/admin", nil)
	ctx = authn.WithPrincipal(req.Context(), PrincipalFromClaims(claims))
	req = req.WithContext(ctx)
	rec = httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", rec.Code)
	}
}

func TestDebugMode(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	// DebugMode has been removed for security - tokens are always validated strictly
	mgr, _ := NewJWTManager(store, cfg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	authenticator := mustAuthMiddleware(t, mgr.Authenticator(TokenTypeAccess))
	wrapped := authenticator(handler)

	// test expired token
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer expired.token.here")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	// Security fix: debug mode no longer returns detailed error messages
	// to prevent information leakage. Generic error is always returned.
	body := rec.Body.String()
	if !strings.Contains(body, "invalid token") {
		t.Errorf("expected generic error message, got: %s", body)
	}
}

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "valid bearer header",
			header: "Bearer token123",
			want:   "token123",
		},
		{
			name:   "bearer with extra spaces",
			header: "Bearer   token456  ",
			want:   "token456",
		},
		{
			name:   "no authorization header",
			header: "",
			want:   "",
		},
		{
			name:   "non-bearer authorization",
			header: "Basic dXNlcjpwYXNz",
			want:   "",
		},
		{
			name:   "case insensitive bearer",
			header: "BEARER token789",
			want:   "token789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}

			got := authn.ExtractBearerToken(req)
			if got != tt.want {
				t.Errorf("ExtractBearerToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContractAuthenticator(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	mgr, err := NewJWTManager(store, cfg)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	pair, _ := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "user-contract"}, AuthorizationClaims{Roles: []string{"user"}})

	authenticator := mgr.Authenticator(TokenTypeAccess)
	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)

	principal, err := authenticator.Authenticate(req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if principal == nil || principal.Subject != "user-contract" {
		t.Fatalf("unexpected principal: %#v", principal)
	}
	if len(principal.Roles) != 1 || principal.Roles[0] != "user" {
		t.Fatalf("unexpected roles: %#v", principal.Roles)
	}
}

func TestContractAuthenticatorMissingToken(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	mgr, _ := NewJWTManager(store, cfg)

	authenticator := mgr.Authenticator(TokenTypeAccess)
	req := httptest.NewRequest(http.MethodGet, "/secure", nil)

	_, err := authenticator.Authenticate(req)
	if err != authn.ErrUnauthenticated {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
}

func TestPolicyAuthorizer(t *testing.T) {
	authorizer := PolicyAuthorizer{Policy: AuthZPolicy{AllRoles: []string{"admin"}}}
	principal := &authn.Principal{Roles: []string{"admin"}}

	if err := authorizer.Authorize(principal, "", ""); err != nil {
		t.Fatalf("expected authorized, got %v", err)
	}

	principal.Roles = []string{"user"}
	if err := authorizer.Authorize(principal, "", ""); err != authn.ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}
