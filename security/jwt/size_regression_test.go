package jwt

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestIssuedTokenSizeBounds(t *testing.T) {
	m, err := NewJWTManager(NewInMemoryKeyStore(), DefaultJWTConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 1000, 7000, 20000} {
		pair, err := m.GenerateTokenPair(context.Background(), IdentityClaims{Subject: "user"}, AuthorizationClaims{Roles: []string{strings.Repeat("a", size)}})
		if size >= 7000 {
			if !errors.Is(err, ErrInvalidToken) || pair.AccessToken != "" || pair.RefreshToken != "" {
				t.Fatalf("size=%d: expected classified error and no token", size)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for kind, token := range map[TokenType]string{TokenTypeAccess: pair.AccessToken, TokenTypeRefresh: pair.RefreshToken} {
			if _, err = m.VerifyToken(context.Background(), token, kind); err != nil {
				t.Fatalf("size=%d verify=%v", size, err)
			}
		}
	}
	// Exercise the exact compact segment boundary shared with verification.
	key, err := m.activeKeyForGeneration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var last string
	for size := 5700; size < 6200; size++ {
		token, err := signJWT(key, TokenClaims{Identity: IdentityClaims{Subject: strings.Repeat("x", size)}})
		if err != nil {
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatal(err)
			}
			if last == "" {
				t.Fatal("boundary not exercised")
			}
			parts, _ := splitCompactJWT(last)
			if len(parts[1]) < maxJWTSegmentLength-3 {
				t.Fatalf("last payload length=%d", len(parts[1]))
			}
			return
		}
		last = token
	}
	t.Fatal("expected segment limit")
}
