package jwt

import (
	"context"
	"errors"
	"testing"
	"time"
)

type pointerFailureStore struct {
	*InMemoryKeyStore
	fail bool
}

var errActivePointer = errors.New("active pointer unavailable")

func (s *pointerFailureStore) SetContext(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if s.fail && key == activeKeyKey {
		return errActivePointer
	}
	return s.InMemoryKeyStore.SetContext(ctx, key, value, ttl)
}
func TestRotationPointerFailurePreservesActiveKey(t *testing.T) {
	store := &pointerFailureStore{InMemoryKeyStore: NewInMemoryKeyStore()}
	manager, err := NewJWTManager(store, DefaultJWTConfig())
	if err != nil {
		t.Fatal(err)
	}
	original := manager.active
	store.fail = true
	if _, err := manager.RotateKey(); !errors.Is(err, errActivePointer) {
		t.Fatalf("rotation error=%v", err)
	}
	if manager.active != original {
		t.Fatal("failed rotation published an active key")
	}
	pair, err := manager.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "test"}, AuthorizationClaims{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := manager.VerifyToken(t.Context(), pair.AccessToken, TokenTypeAccess)
	if err != nil {
		t.Fatal(err)
	}
	if claims.KeyID != original {
		t.Fatal("signing key changed after failed rotation")
	}
	raw, err := store.GetContext(t.Context(), activeKeyKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != original {
		t.Fatal("persisted active pointer changed")
	}
	store.fail = false
	meta, err := manager.RotateKey()
	if err != nil {
		t.Fatal(err)
	}
	raw, err = store.GetContext(t.Context(), activeKeyKey)
	if err != nil {
		t.Fatal(err)
	}
	if manager.active == original || string(raw) != meta.ID || manager.active != meta.ID {
		t.Fatal("successful retry did not publish the new key")
	}
}
func TestAutomaticRotationRetriesPointerFailure(t *testing.T) {
	store := &pointerFailureStore{InMemoryKeyStore: NewInMemoryKeyStore()}
	manager, err := NewJWTManager(store, DefaultJWTConfig())
	if err != nil {
		t.Fatal(err)
	}
	now := manager.currentTime().Add(48 * time.Hour)
	manager.now = func() time.Time { return now }
	store.fail = true
	for i := 0; i < 2; i++ {
		if _, err := manager.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "test"}, AuthorizationClaims{}); !errors.Is(err, errActivePointer) {
			t.Fatalf("attempt %d error=%v", i, err)
		}
	}
}
