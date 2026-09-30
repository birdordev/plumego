package jwt

import (
	"context"
	"errors"
	"strings"
	"testing"

	kvstore "github.com/spcent/plumego/store/kv"
)

// FuzzJWTVerifyTokenFailsClosed feeds arbitrary token strings to
// VerifyToken. It must never panic, must never return claims alongside an
// error, and every error must be one of the recognized sentinels.
func FuzzJWTVerifyTokenFailsClosed(f *testing.F) {
	seeds := []string{
		"",
		"not.a.jwt",
		"aaa.bbb.ccc",
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.sig",
		strings.Repeat("a", maxJWTTokenLength+1),
		"AAA.AAA.AAA",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	store, err := kvstore.NewKVStore(kvstore.Config{DataDir: f.TempDir()})
	if err != nil {
		f.Fatalf("create key store: %v", err)
	}
	f.Cleanup(func() { _ = store.Close() })

	mgr, err := NewJWTManager(store, DefaultJWTConfig())
	if err != nil {
		f.Fatalf("create manager: %v", err)
	}
	// Warm the key cache so the signing-verification path is reachable.
	if _, err := mgr.GenerateTokenPair(context.Background(), IdentityClaims{Subject: "fuzz"}, AuthorizationClaims{}); err != nil {
		f.Fatalf("warm key cache: %v", err)
	}

	f.Fuzz(func(t *testing.T, token string) {
		claims, err := mgr.VerifyToken(t.Context(), token, TokenTypeAccess)
		if err != nil {
			if claims != nil {
				t.Fatalf("fail-closed violated: err=%v but claims=%#v", err, claims)
			}
			switch {
			case errors.Is(err, ErrInvalidToken),
				errors.Is(err, ErrTokenExpired),
				errors.Is(err, ErrTokenNotYetValid),
				errors.Is(err, ErrMissingSubject),
				errors.Is(err, ErrUnknownKey),
				errors.Is(err, ErrInvalidIssuer),
				errors.Is(err, ErrInvalidAudience):
			default:
				t.Fatalf("unexpected error from VerifyToken: %v", err)
			}
			return
		}
		if claims == nil {
			t.Fatal("nil claims without error")
		}
	})
}
