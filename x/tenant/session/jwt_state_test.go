package session

import (
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/security/jwt"
	kvstore "github.com/birdordev/plumego/store/kv"
)

func newJWTStateStore(t *testing.T) (*JWTStateStore, *jwt.JWTManager) {
	t.Helper()

	store, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("create kv store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	state, err := NewJWTStateStore(store)
	if err != nil {
		t.Fatalf("create jwt state store: %v", err)
	}

	mgr, err := jwt.NewJWTManager(store, jwt.DefaultJWTConfig())
	if err != nil {
		t.Fatalf("create jwt manager: %v", err)
	}

	return state, mgr
}

func TestJWTStateStoreRejectsRevokedClaims(t *testing.T) {
	state, mgr := newJWTStateStore(t)

	pair, err := mgr.GenerateTokenPair(t.Context(), jwt.IdentityClaims{Subject: "user-1"}, jwt.AuthorizationClaims{})
	if err != nil {
		t.Fatalf("generate pair: %v", err)
	}
	claims, err := mgr.VerifyToken(t.Context(), pair.AccessToken, jwt.TokenTypeAccess)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}

	if err := state.RevokeClaims(claims); err != nil {
		t.Fatalf("revoke claims: %v", err)
	}
	if err := state.ValidateClaims(claims); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("expected ErrSessionRevoked, got %v", err)
	}
}

func TestJWTStateStoreRejectsStaleSubjectVersion(t *testing.T) {
	state, mgr := newJWTStateStore(t)

	pair, err := mgr.GenerateTokenPair(t.Context(), jwt.IdentityClaims{Subject: "user-2", Version: 0}, jwt.AuthorizationClaims{})
	if err != nil {
		t.Fatalf("generate pair: %v", err)
	}
	claims, err := mgr.VerifyToken(t.Context(), pair.AccessToken, jwt.TokenTypeAccess)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}

	version, err := state.IncrementSubjectVersion("user-2")
	if err != nil {
		t.Fatalf("increment version: %v", err)
	}
	if version != 1 {
		t.Fatalf("expected version 1, got %d", version)
	}

	if err := state.ValidateClaims(claims); !errors.Is(err, ErrTokenVersionMismatch) {
		t.Fatalf("expected ErrTokenVersionMismatch, got %v", err)
	}

	freshPair, err := mgr.GenerateTokenPair(t.Context(), jwt.IdentityClaims{Subject: "user-2", Version: version}, jwt.AuthorizationClaims{})
	if err != nil {
		t.Fatalf("generate fresh pair: %v", err)
	}
	freshClaims, err := mgr.VerifyToken(t.Context(), freshPair.AccessToken, jwt.TokenTypeAccess)
	if err != nil {
		t.Fatalf("verify fresh token: %v", err)
	}
	if err := state.ValidateClaims(freshClaims); err != nil {
		t.Fatalf("fresh claims should validate: %v", err)
	}
}

func TestJWTStateStoreSubjectVersionDefaultsToZero(t *testing.T) {
	state, _ := newJWTStateStore(t)

	version, err := state.SubjectVersion("missing")
	if err != nil {
		t.Fatalf("subject version: %v", err)
	}
	if version != 0 {
		t.Fatalf("expected zero version, got %d", version)
	}
}

func TestJWTStateStoreRevocationTTLExpires(t *testing.T) {
	store, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("create kv store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	state, err := NewJWTStateStoreWithConfig(store, jwt.JWTConfig{ClockSkew: 0})
	if err != nil {
		t.Fatalf("create state store: %v", err)
	}

	claims := &jwt.TokenClaims{
		TokenID: "tok-1",
		Identity: jwt.IdentityClaims{
			Subject: "user-3",
		},
		ExpiresAt: time.Now().Add(2 * time.Second).Unix(),
	}
	if err := state.RevokeClaims(claims); err != nil {
		t.Fatalf("revoke claims: %v", err)
	}
	time.Sleep(2200 * time.Millisecond)
	if err := state.ValidateClaims(claims); err != nil {
		t.Fatalf("expired revocation marker should not fail validation, got %v", err)
	}
}

func TestJWTStateStoreDoesNotPersistExpiredRevocation(t *testing.T) {
	state, _ := newJWTStateStore(t)
	claims := &jwt.TokenClaims{TokenID: "expired", ExpiresAt: time.Now().Add(-time.Minute).Unix()}
	if err := state.RevokeClaims(claims); err != nil {
		t.Fatal(err)
	}
	if _, err := state.store.Get(revokedTokenPrefix + claims.TokenID); !errors.Is(err, kvstore.ErrKeyNotFound) {
		t.Fatalf("lookup expired revocation = %v, want ErrKeyNotFound", err)
	}
}

func TestJWTStateStoreHonorsConfiguredClockSkew(t *testing.T) {
	original, _ := newJWTStateStore(t)
	state, err := NewJWTStateStoreWithConfig(original.store, jwt.JWTConfig{ClockSkew: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	claims := &jwt.TokenClaims{TokenID: "recent", Identity: jwt.IdentityClaims{Subject: "user"}, ExpiresAt: time.Now().Add(-10 * time.Second).Unix()}
	if err := state.RevokeClaims(claims); err != nil {
		t.Fatal(err)
	}
	if err := state.ValidateClaims(claims); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("want revoked, got %v", err)
	}
	claims.TokenID = "expired"
	claims.ExpiresAt = time.Now().Add(-2 * time.Minute).Unix()
	if err := state.RevokeClaims(claims); err != nil {
		t.Fatal(err)
	}
	if _, err := state.store.Get(revokedTokenPrefix + claims.TokenID); !errors.Is(err, kvstore.ErrKeyNotFound) {
		t.Fatalf("want absent marker, got %v", err)
	}
}
