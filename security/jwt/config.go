package jwt

import (
	"errors"
	"fmt"
	"time"

	"github.com/spcent/plumego/security/authn"
)

// TokenType represents the semantic purpose of a JWT.
//
// JWTs can serve different purposes in an authentication system:
//   - Access tokens: Short-lived tokens for API access
//   - Refresh tokens: Long-lived tokens for obtaining new access tokens
//
// Example:
//
//	import "github.com/spcent/plumego/security/jwt"
//
//	// Verify an access token
//	claims, err := manager.VerifyToken(ctx, token, jwt.TokenTypeAccess)
type TokenType string

const (
	// TokenTypeAccess is used for short-lived access tokens.
	TokenTypeAccess TokenType = "access"
	// TokenTypeRefresh is used for long-lived refresh tokens.
	TokenTypeRefresh TokenType = "refresh"
)

// Algorithm represents a supported signing algorithm.
//
// Supported algorithms:
//   - HS256: HMAC with SHA-256 (symmetric, uses shared secret)
//   - EdDSA: Ed25519 (asymmetric, uses public/private key pair)
//
// Example:
//
//	import "github.com/spcent/plumego/security/jwt"
//
//	config := jwt.DefaultJWTConfig()
//	config.Algorithm = jwt.AlgorithmEdDSA
type Algorithm string

const (
	AlgorithmHS256 Algorithm = "HS256"
	AlgorithmEdDSA Algorithm = "EdDSA"
)

// Errors returned by JWT operations.
var (
	ErrInvalidToken     = authn.ErrInvalidToken
	ErrTokenExpired     = authn.ErrExpiredToken
	ErrTokenNotYetValid = errors.New("token not yet valid")
	ErrUnknownKey       = errors.New("unknown signing key")
	ErrKeyExpired       = errors.New("signing key expired")
	ErrMissingSubject   = errors.New("subject is required")
	ErrInvalidIssuer    = errors.New("invalid issuer")
	ErrInvalidAudience  = errors.New("invalid audience")
)

const (
	keyPrefix    = "jwt:keys:"
	activeKeyKey = "jwt:active"
)

const (
	maxJWTTokenLength   = 16 * 1024
	maxJWTSegmentLength = 8 * 1024
)

// JWTConfig holds JWT configuration.
//
// Example:
//
//	import "github.com/spcent/plumego/security/jwt"
//
//	config := jwt.DefaultJWTConfig()
//	config.Issuer = "my-app"
//	config.AccessExpiration = 15 * time.Minute
//	config.RefreshExpiration = 7 * 24 * time.Hour
//	config.Algorithm = jwt.AlgorithmHS256
type JWTConfig struct {
	// Issuer is the JWT issuer (iss claim)
	Issuer string

	// Audience is the JWT audience (aud claim)
	Audience string

	// AccessExpiration is the lifetime of access tokens
	AccessExpiration time.Duration

	// RefreshExpiration is the lifetime of refresh tokens
	RefreshExpiration time.Duration

	// RotationInterval is how often to rotate signing keys
	RotationInterval time.Duration

	// Algorithm is the signing algorithm
	Algorithm Algorithm

	// ClockSkew is the tolerance for clock skew
	ClockSkew time.Duration
}

// Validate checks that the configuration is usable before the manager starts.
func (c JWTConfig) Validate() error {
	if c.AccessExpiration <= 0 {
		return fmt.Errorf("jwt: AccessExpiration must be positive, got %v", c.AccessExpiration)
	}
	if c.RefreshExpiration <= 0 {
		return fmt.Errorf("jwt: RefreshExpiration must be positive, got %v", c.RefreshExpiration)
	}
	if c.RefreshExpiration < c.AccessExpiration {
		return fmt.Errorf("jwt: RefreshExpiration (%v) must be >= AccessExpiration (%v)", c.RefreshExpiration, c.AccessExpiration)
	}
	if c.RotationInterval < 0 {
		return fmt.Errorf("jwt: RotationInterval must be non-negative, got %v", c.RotationInterval)
	}
	if c.ClockSkew < 0 {
		return fmt.Errorf("jwt: ClockSkew must be non-negative, got %v", c.ClockSkew)
	}
	if c.Algorithm != "" && c.Algorithm != AlgorithmHS256 && c.Algorithm != AlgorithmEdDSA {
		return fmt.Errorf("jwt: unsupported Algorithm %q, must be %q or %q", c.Algorithm, AlgorithmHS256, AlgorithmEdDSA)
	}
	return nil
}

// DefaultJWTConfig returns sane defaults.
//
// Defaults:
//   - Issuer: "plumego"
//   - Audience: "plumego-client"
//   - AccessExpiration: 15 minutes
//   - RefreshExpiration: 7 days
//   - RotationInterval: 24 hours
//   - Algorithm: HS256
//   - ClockSkew: 5 seconds
//
// Example:
//
//	config := jwt.DefaultJWTConfig()
func DefaultJWTConfig() JWTConfig {
	return JWTConfig{
		Issuer:            "plumego",
		Audience:          "plumego-client",
		AccessExpiration:  15 * time.Minute,
		RefreshExpiration: 7 * 24 * time.Hour,
		RotationInterval:  24 * time.Hour,
		Algorithm:         AlgorithmHS256,
		ClockSkew:         5 * time.Second,
	}
}

func normalizeJWTConfig(config JWTConfig) JWTConfig {
	if config.Algorithm == "" {
		config.Algorithm = AlgorithmHS256
	}
	return config
}
