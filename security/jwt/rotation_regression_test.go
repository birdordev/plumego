package jwt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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

// rotationFailureStore can model both rejected writes and lost acknowledgments.
type rotationFailureStore struct {
	*InMemoryKeyStore
	failPointer       bool
	failBlob          bool
	commitBeforeError bool
	blobIDs           []string
}

func (s *rotationFailureStore) SetContext(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	blob := strings.HasPrefix(key, keyPrefix)
	if blob {
		s.blobIDs = append(s.blobIDs, strings.TrimPrefix(key, keyPrefix))
	}
	if (blob && s.failBlob) || (key == activeKeyKey && s.failPointer) {
		if s.commitBeforeError {
			if err := s.InMemoryKeyStore.SetContext(ctx, key, value, ttl); err != nil {
				return err
			}
		}
		return errActivePointer
	}
	return s.InMemoryKeyStore.SetContext(ctx, key, value, ttl)
}

func TestFailedRotationsReuseCandidate(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, blob := range []bool{false, true} {
			for _, committed := range []bool{false, true} {
				t.Run(fmt.Sprintf("automatic=%v/blob=%v/committed=%v", automatic, blob, committed), func(t *testing.T) {
					store := &rotationFailureStore{InMemoryKeyStore: NewInMemoryKeyStore()}
					manager, err := NewJWTManager(store, DefaultJWTConfig())
					if err != nil {
						t.Fatal(err)
					}
					original := manager.active
					store.blobIDs = nil
					store.failBlob, store.failPointer, store.commitBeforeError = blob, !blob, committed
					if automatic {
						now := manager.currentTime().Add(48 * time.Hour)
						manager.now = func() time.Time { return now }
					}
					for i := 0; i < 10; i++ {
						if automatic {
							_, err = manager.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "test"}, AuthorizationClaims{})
						} else {
							_, err = manager.RotateKey()
						}
						if !errors.Is(err, errActivePointer) || manager.active != original {
							t.Fatalf("attempt %d: err=%v active changed=%v", i, err, manager.active != original)
						}
					}
					keys, err := store.KeysContext(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if len(keys) > 3 || len(manager.keyCache) > 2 {
						t.Fatalf("unbounded candidates: stored=%d cached=%d", len(keys), len(manager.keyCache))
					}
					if len(store.blobIDs) == 0 {
						t.Fatal("no candidate attempted")
					}
					candidate := store.blobIDs[0]
					for _, id := range store.blobIDs {
						if id != candidate {
							t.Fatal("retry generated a different candidate")
						}
					}
					store.failPointer, store.failBlob = false, false
					meta, err := manager.RotateKey()
					if err != nil || meta.ID != candidate || manager.active != candidate {
						t.Fatalf("candidate recovery failed: err=%v", err)
					}
				})
			}
		}
	}
}

func TestRotationCandidateSurvivesRestart(t *testing.T) {
	for _, blob := range []bool{false, true} {
		for _, committed := range []bool{false, true} {
			if blob && !committed {
				continue
			} // A rejected blob has no durable candidate to recover.
			t.Run(fmt.Sprintf("blob=%v/committed=%v", blob, committed), func(t *testing.T) {
				store := &rotationFailureStore{InMemoryKeyStore: NewInMemoryKeyStore()}
				manager, err := NewJWTManager(store, DefaultJWTConfig())
				if err != nil {
					t.Fatal(err)
				}
				pair, err := manager.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "test"}, AuthorizationClaims{})
				if err != nil {
					t.Fatal(err)
				}
				store.blobIDs = nil
				store.failBlob, store.failPointer, store.commitBeforeError = blob, !blob, committed
				if _, err := manager.RotateKey(); !errors.Is(err, errActivePointer) {
					t.Fatalf("rotation err=%v", err)
				}
				candidate := store.blobIDs[0]
				restarted, err := NewJWTManager(store, DefaultJWTConfig())
				if err != nil {
					t.Fatal(err)
				}
				store.failBlob, store.failPointer = false, false
				if restarted.active != candidate {
					meta, err := restarted.RotateKey()
					if err != nil || meta.ID != candidate {
						t.Fatalf("restart did not reuse candidate: err=%v", err)
					}
				}
				keys, err := store.KeysContext(t.Context())
				if err != nil || len(keys) != 3 {
					t.Fatalf("stored keys=%d err=%v", len(keys), err)
				}
				if _, err := restarted.VerifyToken(t.Context(), pair.AccessToken, TokenTypeAccess); err != nil {
					t.Fatalf("previous token invalid: %v", err)
				}
			})
		}
	}
}

type blockingRotationStore struct {
	*InMemoryKeyStore
	entered chan struct{}
	release chan struct{}
	block   sync.Once
}

func (s *blockingRotationStore) SetContext(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if s.entered != nil && key == activeKeyKey {
		s.block.Do(func() {
			close(s.entered)
			<-s.release
		})
	}
	return s.InMemoryKeyStore.SetContext(ctx, key, value, ttl)
}

func TestVerifyContinuesWhileRotationWaitsForStore(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprintf("automatic=%t", automatic), func(t *testing.T) {
			store := &blockingRotationStore{InMemoryKeyStore: NewInMemoryKeyStore()}
			cfg := DefaultJWTConfig()
			cfg.RotationInterval = time.Minute
			manager, err := NewJWTManager(store, cfg)
			if err != nil {
				t.Fatal(err)
			}
			identity := IdentityClaims{Subject: "example"}
			oldPair, err := manager.GenerateTokenPair(t.Context(), identity, AuthorizationClaims{})
			if err != nil {
				t.Fatal(err)
			}

			entered, release := make(chan struct{}), make(chan struct{})
			store.entered, store.release = entered, release
			now := manager.currentTime().Add(time.Minute)
			manager.now = func() time.Time { return now }
			rotated := make(chan error, 1)
			go func() {
				if automatic {
					_, err := manager.GenerateTokenPair(t.Context(), identity, AuthorizationClaims{})
					rotated <- err
					return
				}
				_, err := manager.RotateKeyContext(t.Context())
				rotated <- err
			}()
			<-entered

			verified := make(chan error, 1)
			go func() {
				_, err := manager.VerifyToken(t.Context(), oldPair.AccessToken, TokenTypeAccess)
				verified <- err
			}()
			select {
			case err := <-verified:
				if err != nil {
					t.Errorf("old token failed verification: %v", err)
				}
			case <-time.After(time.Second):
				t.Error("verification blocked on rotation store")
			}

			issued := make(chan error, 1)
			go func() {
				_, err := manager.GenerateTokenPair(t.Context(), identity, AuthorizationClaims{})
				issued <- err
			}()
			close(release)
			if err := <-rotated; err != nil {
				t.Fatal(err)
			}
			if err := <-issued; err != nil {
				t.Fatal(err)
			}
			keys, err := store.KeysContext(t.Context())
			if err != nil || len(keys) != 3 {
				t.Fatalf("expected two signing keys and the active pointer, got %d entries: %v", len(keys), err)
			}
			if _, err := manager.VerifyToken(t.Context(), oldPair.AccessToken, TokenTypeAccess); err != nil {
				t.Fatalf("old token failed after activation: %v", err)
			}
		})
	}
}
