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

// loadKeys reads signing keys from the KV store and ensures an active key exists.
func (m *JWTManager) loadKeys(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

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

	return m.ensureActiveKeyUnsafe(ctx)
}

func (m *JWTManager) ensureActiveKeyUnsafe(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if m.active != "" {
		if _, ok := m.keyCache[m.active]; ok {
			return nil
		}
	}

	_, err := m.rotateKeyUnsafe(ctx)
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
	m.mu.Lock()
	defer m.mu.Unlock()

	key, err := m.rotateKeyUnsafe(ctx)
	if err != nil {
		return SigningKeyMetadata{}, err
	}
	return signingKeyMetadata(key), nil
}

// rotateKeyUnsafe is the unsafe version of RotateKey, assuming the caller holds the lock.
func (m *JWTManager) rotateKeyUnsafe(ctx context.Context) (JWTSigningKey, error) {
	if err := contextErr(ctx); err != nil {
		return JWTSigningKey{}, err
	}
	if m.pending == nil {
		key, err := m.generateKeyUnsafe(m.config.Algorithm)
		if err != nil {
			return JWTSigningKey{}, err
		}
		m.pending = &key
	}
	key := *m.pending
	if _, persisted := m.keyCache[key.ID]; !persisted {
		if err := m.persistKeyUnsafe(ctx, key); err != nil {
			return JWTSigningKey{}, err
		}
	}
	if err := m.storeSet(ctx, activeKeyKey, []byte(key.ID), 0); err != nil {
		return JWTSigningKey{}, err
	}
	m.active = key.ID
	m.pending = nil
	return cloneSigningKey(key), nil
}

// persistKeyUnsafe is the unsafe version of persistKey, assuming the caller holds the lock.
func (m *JWTManager) persistKeyUnsafe(ctx context.Context, key JWTSigningKey) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	previous := m.active
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
	m.keyCache[key.ID] = cloneSigningKey(key)
	return nil
}

// generateKeyUnsafe is the unsafe version of generateKey, assuming the caller holds the lock.
func (m *JWTManager) generateKeyUnsafe(alg Algorithm) (JWTSigningKey, error) {
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

// ensureRotationUnsafe rotates the active signing key if the configured interval has elapsed.
func (m *JWTManager) ensureRotationUnsafe(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	activeKey, ok := m.keyCache[m.active]
	if !ok {
		return ErrUnknownKey
	}
	if m.config.RotationInterval <= 0 {
		return nil
	}
	if m.currentTime().Sub(activeKey.CreatedAt) >= m.config.RotationInterval {
		_, err := m.rotateKeyUnsafe(ctx)
		return err
	}
	return nil
}
