// Package jwt provides JSON Web Token (JWT) generation, verification, and management
// with key rotation support.
//
// This package implements a JWT system supporting multiple token types:
//   - Access tokens: Short-lived tokens for API authentication (default: 15 minutes)
//   - Refresh tokens: Long-lived tokens for obtaining new access tokens (default: 7 days)
//
// Features:
//   - HMAC-SHA256 signing with automatic key rotation
//   - EdDSA (Ed25519) signing for enhanced security
//   - transport-agnostic token validation with explicit middleware adapters
//   - thread-safe key management with minimal lock contention
//
// Example usage:
//
//	import (
//		"context"
//
//		authmw "github.com/spcent/plumego/middleware/auth"
//		"github.com/spcent/plumego/security/jwt"
//	)
//
//	var store jwt.KeyStore = appJWTKeyStore()
//
//	config := jwt.DefaultJWTConfig()
//	manager, err := jwt.NewJWTManager(store, config)
//	if err != nil {
//		// Handle manager initialization error
//	}
//
//	identity := jwt.IdentityClaims{Subject: "user123"}
//	authz := jwt.AuthorizationClaims{Roles: []string{"user"}}
//	pair, err := manager.GenerateTokenPair(context.Background(), identity, authz)
//
//	// Verify a token
//	verified, err := manager.VerifyToken(context.Background(), pair.AccessToken, jwt.TokenTypeAccess)
//	if err != nil {
//		// Handle invalid or expired token
//	}
//
//	// Use with the canonical transport adapter
//	authMw, err := authmw.Authenticate(manager.Authenticator(jwt.TokenTypeAccess))
//	if err != nil {
//		return err
//	}
//	app.Use(authMw)
//
// # Lifecycle
//
// JWTManager is safe for concurrent use by multiple goroutines. Key rotation
// happens lazily during GenerateTokenPair when RotationInterval has elapsed;
// there is no background goroutine. The manager itself does not require
// explicit cleanup. If the KeyStore holds external resources (e.g. a database
// connection), the caller is responsible for closing those resources after the
// JWTManager is no longer used.
package jwt

import (
	"context"
	"errors"
	"sync"
	"time"
)

// JWTManager handles JWT token generation and verification.
//
// JWTManager provides a JWT signing and verification primitive with:
//   - Token generation (access and refresh tokens)
//   - Token verification and validation
//   - Key rotation
//
// Example:
//
//	var store jwt.KeyStore = appJWTKeyStore()
//	config := jwt.DefaultJWTConfig()
//	manager, err := jwt.NewJWTManager(store, config)
//	if err != nil {
//		// handle error
//	}
//
//	// Generate token pair
//	identity := jwt.IdentityClaims{Subject: "user-123"}
//	authz := jwt.AuthorizationClaims{Roles: []string{"admin"}}
//	pair, err := manager.GenerateTokenPair(ctx, identity, authz)
//
//	// Verify token
//	claims, err := manager.VerifyToken(ctx, pair.AccessToken, jwt.TokenTypeAccess)
//
//	// Use with middleware/auth.Authenticate(manager.Authenticator(jwt.TokenTypeAccess)).
//	// The constructor returns (middleware.Middleware, error).
type JWTManager struct {
	config JWTConfig
	store  KeyStore
	now    func() time.Time

	mu       sync.RWMutex
	keyCache map[string]JWTSigningKey
	active   string
	pending  *JWTSigningKey // Reused until both persistence and activation are acknowledged.
}

// NewJWTManager creates a new JWT manager with the given configuration and backing store.
//
// Example:
//
//	var store jwt.KeyStore = appJWTKeyStore()
//	config := jwt.DefaultJWTConfig()
//	manager, err := jwt.NewJWTManager(store, config)
//	if err != nil {
//		// handle error
//	}
func NewJWTManager(store KeyStore, config JWTConfig) (*JWTManager, error) {
	return NewJWTManagerContext(context.Background(), store, config)
}

// NewJWTManagerContext creates a JWT manager and lets context-aware stores abort startup work.
func NewJWTManagerContext(ctx context.Context, store KeyStore, config JWTConfig) (*JWTManager, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("jwt key store is required")
	}
	config = normalizeJWTConfig(config)
	if err := config.Validate(); err != nil {
		return nil, err
	}

	mgr := &JWTManager{
		config:   config,
		store:    store,
		now:      time.Now,
		keyCache: make(map[string]JWTSigningKey),
	}

	if err := mgr.loadKeys(ctx); err != nil {
		return nil, err
	}

	return mgr, nil
}

func (m *JWTManager) currentTime() time.Time {
	if m != nil && m.now != nil {
		return m.now()
	}
	return time.Now()
}

// GenerateTokenPair issues a new access/refresh token pair.
func (m *JWTManager) GenerateTokenPair(ctx context.Context, identity IdentityClaims, authz AuthorizationClaims) (TokenPair, error) {
	if err := contextErr(ctx); err != nil {
		return TokenPair{}, err
	}
	if identity.Subject == "" {
		return TokenPair{}, ErrMissingSubject
	}

	activeKey, err := m.activeKeyForGeneration(ctx)
	if err != nil {
		return TokenPair{}, err
	}

	if err := contextErr(ctx); err != nil {
		return TokenPair{}, err
	}

	now := m.currentTime().UTC()
	access, err := m.buildToken(activeKey, TokenTypeAccess, identity, authz, now, m.config.AccessExpiration)
	if err != nil {
		return TokenPair{}, err
	}
	refresh, err := m.buildToken(activeKey, TokenTypeRefresh, identity, authz, now, m.config.RefreshExpiration)
	if err != nil {
		return TokenPair{}, err
	}

	return TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int64(m.config.AccessExpiration.Seconds()),
		TokenType:    "Bearer",
	}, nil
}

// activeKeyForGeneration returns a clone of the active signing key.
// It uses a read lock for the common (no-rotation) path to avoid serialising
// concurrent GenerateTokenPair calls. When rotation is due it upgrades to a
// write lock and double-checks before executing the store I/O.
func (m *JWTManager) activeKeyForGeneration(ctx context.Context) (JWTSigningKey, error) {
	m.mu.RLock()
	activeKey, ok := m.keyCache[m.active]
	needsRotation := ok && m.config.RotationInterval > 0 &&
		m.currentTime().Sub(activeKey.CreatedAt) >= m.config.RotationInterval
	if ok && !needsRotation {
		cloned := cloneSigningKey(activeKey)
		m.mu.RUnlock()
		return cloned, nil
	}
	m.mu.RUnlock()

	// Rotation path or missing key: take write lock and double-check.
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureRotationUnsafe(ctx); err != nil {
		return JWTSigningKey{}, err
	}
	activeKey, ok = m.keyCache[m.active]
	if !ok {
		return JWTSigningKey{}, ErrUnknownKey
	}
	return cloneSigningKey(activeKey), nil
}

func (m *JWTManager) buildToken(key JWTSigningKey, tokenType TokenType, identity IdentityClaims, authz AuthorizationClaims, now time.Time, ttl time.Duration) (string, error) {
	jti, err := randomID()
	if err != nil {
		return "", err
	}
	claims := TokenClaims{
		TokenID:       jti,
		TokenType:     tokenType,
		Identity:      identity,
		Authorization: authz,
		Issuer:        m.config.Issuer,
		Audience:      m.config.Audience,
		IssuedAt:      now.Unix(),
		NotBefore:     now.Unix(),
		ExpiresAt:     now.Add(ttl).Unix(),
		KeyID:         key.ID,
	}
	return signJWT(key, claims)
}
