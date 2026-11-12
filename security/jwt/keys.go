package jwt

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// storedSigningKey retains rotation ancestry without changing the public key type.
// Only rotations from an existing active key have recoverable ancestry.
type storedSigningKey struct {
	JWTSigningKey
	RotationFrom *string `json:"rotation_from,omitempty"`
}

// loadKeys reads signing keys before the manager is published to callers.
func (m *JWTManager) loadKeys(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	keys, err := m.storeKeys(ctx)
	if err != nil {
		return err
	}
	hasActiveKey := false
	var candidates []storedSigningKey
	for _, key := range keys {
		if err := contextErr(ctx); err != nil {
			return err
		}
		if key == activeKeyKey {
			hasActiveKey = true
			continue
		}
		if strings.HasPrefix(key, keyPrefix) {
			raw, err := m.storeGet(ctx, key)
			if err != nil {
				return err
			}
			var stored storedSigningKey
			if err := json.Unmarshal(raw, &stored); err != nil {
				// Decoder errors can contain persisted values, including key material.
				return fmt.Errorf("failed to decode signing key: %w", ErrInvalidToken)
			}
			signingKey := stored.JWTSigningKey
			if err := validateSigningKey(signingKey); err != nil {
				return fmt.Errorf("invalid signing key: %w", err)
			}
			m.keyCache[signingKey.ID] = signingKey
			if stored.RotationFrom != nil {
				candidates = append(candidates, stored)
			}
		}
	}

	if hasActiveKey {
		activeRaw, err := m.storeGet(ctx, activeKeyKey)
		if err != nil {
			return fmt.Errorf("failed to read active signing key: %w", err)
		}
		m.active = strings.TrimSpace(string(activeRaw))
	}
	// Only explicit ancestry identifies an unacknowledged candidate. Legacy
	// keys and previously active keys must remain available for verification.
	m.pending = nil
	for _, candidate := range candidates {
		key := candidate.JWTSigningKey
		if *candidate.RotationFrom != m.active || key.ID == m.active || key.Algorithm != m.config.Algorithm {
			continue
		}
		if m.pending == nil || key.CreatedAt.After(m.pending.CreatedAt) || (key.CreatedAt.Equal(m.pending.CreatedAt) && key.ID < m.pending.ID) {
			key = cloneSigningKey(key)
			m.pending = &key
		}
	}

	return m.ensureActiveKey(ctx)
}

func (m *JWTManager) ensureActiveKey(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if _, ok := m.activeSigningKey(); ok {
		return nil
	}

	_, err := m.rotateKeyLocked(ctx)
	return err
}

// RotateKey generates and activates a new signing key while keeping the previous keys for verification.
func (m *JWTManager) RotateKey() (SigningKeyMetadata, error) {
	return m.RotateKeyContext(context.Background())
}

// RotateKeyContext generates and activates a new signing key with caller cancellation.
func (m *JWTManager) RotateKeyContext(ctx context.Context) (SigningKeyMetadata, error) {
	if err := contextErr(ctx); err != nil {
		return SigningKeyMetadata{}, err
	}
	m.rotationMu.Lock()
	defer m.rotationMu.Unlock()

	key, err := m.rotateKeyLocked(ctx)
	if err != nil {
		return SigningKeyMetadata{}, err
	}
	return signingKeyMetadata(key), nil
}

// rotateKeyLocked requires rotationMu after the manager is published.
func (m *JWTManager) rotateKeyLocked(ctx context.Context) (JWTSigningKey, error) {
	if err := contextErr(ctx); err != nil {
		return JWTSigningKey{}, err
	}
	if m.pending == nil {
		key, err := m.generateSigningKey(m.config.Algorithm)
		if err != nil {
			return JWTSigningKey{}, err
		}
		m.pending = &key
	}
	key := *m.pending
	m.mu.RLock()
	_, persisted := m.keyCache[key.ID]
	previous := m.active
	m.mu.RUnlock()
	if !persisted {
		if err := m.persistKey(ctx, key, previous); err != nil {
			return JWTSigningKey{}, err
		}
	}
	if err := m.storeSet(ctx, activeKeyKey, []byte(key.ID), 0); err != nil {
		return JWTSigningKey{}, err
	}
	m.mu.Lock()
	m.active = key.ID
	m.mu.Unlock()
	m.pending = nil
	return cloneSigningKey(key), nil
}

// persistKey writes a candidate before adding it to the verification cache.
func (m *JWTManager) persistKey(ctx context.Context, key JWTSigningKey, previous string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	stored := storedSigningKey{JWTSigningKey: key}
	if previous != "" {
		stored.RotationFrom = &previous
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	if err := m.storeSet(ctx, keyPrefix+key.ID, encoded, 0); err != nil {
		return err
	}
	m.mu.Lock()
	m.keyCache[key.ID] = cloneSigningKey(key)
	m.mu.Unlock()
	return nil
}

// generateSigningKey creates a candidate without changing manager state.
func (m *JWTManager) generateSigningKey(alg Algorithm) (JWTSigningKey, error) {
	kid, err := randomID()
	if err != nil {
		return JWTSigningKey{}, err
	}
	key := JWTSigningKey{ID: kid, Algorithm: alg, CreatedAt: m.currentTime().UTC()}
	switch alg {
	case AlgorithmHS256:
		secret := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, secret); err != nil {
			return JWTSigningKey{}, fmt.Errorf("generate hs256 secret: %w", err)
		}
		key.Secret = secret
	case AlgorithmEdDSA:
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return JWTSigningKey{}, fmt.Errorf("generate eddsa key: %w", err)
		}
		key.Secret = priv
		key.Public = pub
	default:
		return JWTSigningKey{}, fmt.Errorf("unsupported algorithm: %s", alg)
	}
	return key, nil
}

func randomID() (string, error) {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func cloneSigningKey(key JWTSigningKey) JWTSigningKey {
	copied := key
	copied.Secret = append([]byte(nil), key.Secret...)
	copied.Public = append([]byte(nil), key.Public...)
	return copied
}

func signingKeyMetadata(key JWTSigningKey) SigningKeyMetadata {
	return SigningKeyMetadata{
		ID:        key.ID,
		Algorithm: key.Algorithm,
		Public:    append([]byte(nil), key.Public...),
		CreatedAt: key.CreatedAt,
	}
}

func validateSigningKey(key JWTSigningKey) error {
	if key.ID == "" {
		return ErrUnknownKey
	}
	switch key.Algorithm {
	case AlgorithmHS256:
		if len(key.Secret) != 32 {
			return ErrInvalidToken
		}
	case AlgorithmEdDSA:
		if len(key.Secret) != ed25519.PrivateKeySize || len(key.Public) != ed25519.PublicKeySize {
			return ErrInvalidToken
		}
		derived := ed25519.NewKeyFromSeed(key.Secret[:ed25519.SeedSize])
		if !hmac.Equal(key.Secret, derived) || !hmac.Equal(key.Public, derived[ed25519.SeedSize:]) {
			return ErrInvalidToken
		}
	default:
		return ErrInvalidToken
	}
	return nil
}

func (m *JWTManager) storeKeys(ctx context.Context) ([]string, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	return m.store.KeysContext(ctx)
}

func (m *JWTManager) storeGet(ctx context.Context, key string) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	return m.store.GetContext(ctx, key)
}

func (m *JWTManager) storeSet(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	return m.store.SetContext(ctx, key, value, ttl)
}

// ensureRotationLocked rotates the active signing key when due. It requires rotationMu.
func (m *JWTManager) ensureRotationLocked(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	activeKey, ok := m.activeSigningKey()
	if !ok {
		return ErrUnknownKey
	}
	if m.config.RotationInterval <= 0 {
		return nil
	}
	if m.currentTime().Sub(activeKey.CreatedAt) >= m.config.RotationInterval {
		_, err := m.rotateKeyLocked(ctx)
		return err
	}
	return nil
}
