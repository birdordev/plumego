package jwt

import (
	"context"
	"slices"
)

// WithTokenClaims stores JWT claims in request context for downstream handlers.
func WithTokenClaims(ctx context.Context, claims *TokenClaims) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, tokenClaimsContextKey{}, cloneTokenClaims(claims))
}

// TokenClaimsFromContext returns a copy of JWT claims from request context when
// present. The copy preserves nil and non-nil empty slices.
func TokenClaimsFromContext(ctx context.Context) *TokenClaims {
	if ctx == nil {
		return nil
	}
	claims, _ := ctx.Value(tokenClaimsContextKey{}).(*TokenClaims)
	return cloneTokenClaims(claims)
}

type tokenClaimsContextKey struct{}

func cloneTokenClaims(claims *TokenClaims) *TokenClaims {
	if claims == nil {
		return nil
	}
	copied := *claims
	copied.Authorization.Roles = slices.Clone(claims.Authorization.Roles)
	copied.Authorization.Permissions = slices.Clone(claims.Authorization.Permissions)
	return &copied
}
