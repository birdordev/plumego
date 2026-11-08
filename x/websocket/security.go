package websocket

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"sync/atomic"

	"github.com/birdordev/plumego/security/password"
)

// SecurityConfig defines security-related configurations for WebSocket
type SecurityConfig struct {
	// JWTSecret is the secret key for JWT verification (HS256)
	// Minimum length: 32 bytes (256 bits) recommended
	JWTSecret []byte

	// MinJWTSecretLength enforces minimum JWT secret length
	// Default: 32 bytes
	MinJWTSecretLength int

	// RoomPasswordConfig defines strength requirements for room passwords
	RoomPasswordConfig password.PasswordStrengthConfig

	// EnforcePasswordStrength if true, rejects weak passwords
	// Default: true when AllowWeakRoomPasswords is false.
	EnforcePasswordStrength bool

	// AllowWeakRoomPasswords explicitly relaxes room password strength checks.
	// Keep false for production.
	AllowWeakRoomPasswords bool

	// MaxMessageSize limits incoming message size (bytes)
	// Default: 16MB
	MaxMessageSize int64

	// EnableDebugLogging enables detailed logging for debugging
	// Should be false in production
	EnableDebugLogging bool

	// Logger is optional and caller-provided. When nil, auth/security helpers do
	// not emit logs.
	Logger *log.Logger
}

// SecurityMetrics tracks security-related metrics for a SecureRoomAuth instance.
type SecurityMetrics struct {
	// JWTVerificationFailures counts JWT verifications that failed.
	JWTVerificationFailures uint64 `json:"jwt_verification_failures"`
	// WeakRoomPasswords counts rejected weak passwords.
	WeakRoomPasswords uint64 `json:"weak_room_passwords"`
	// SuccessfulAuthentications counts successful JWT verifications.
	SuccessfulAuthentications uint64 `json:"successful_authentications"`
}

// ValidateSecurityConfig validates the security configuration
func ValidateSecurityConfig(cfg SecurityConfig) error {
	// Set default minimum if not specified
	if cfg.MinJWTSecretLength == 0 {
		cfg.MinJWTSecretLength = 32
	}

	if len(cfg.JWTSecret) < cfg.MinJWTSecretLength {
		return fmt.Errorf("%w: got %d bytes, minimum %d bytes required",
			ErrWeakJWTSecret, len(cfg.JWTSecret), cfg.MinJWTSecretLength)
	}

	if containsWeakSecretPattern(cfg.JWTSecret) {
		if cfg.Logger != nil {
			cfg.Logger.Printf("websocket: JWT secret contains common weak patterns")
		}
	}

	return nil
}

func containsWeakSecretPattern(secret []byte) bool {
	for _, pattern := range [][]byte{
		[]byte("secret"),
		[]byte("password"),
		[]byte("123456"),
	} {
		if bytes.Contains(secret, pattern) {
			return true
		}
	}
	return false
}

// ValidateWebSocketKey validates the Sec-WebSocket-Key header
func ValidateWebSocketKey(key string) error {
	if key == "" {
		return ErrInvalidWebSocketKey
	}

	// Key must be base64 encoded
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return fmt.Errorf("%w: not valid base64", ErrInvalidWebSocketKey)
	}

	// Decoded key must be exactly 16 bytes
	if len(decoded) != 16 {
		return fmt.Errorf("%w: decoded length must be 16 bytes, got %d", ErrInvalidWebSocketKey, len(decoded))
	}

	return nil
}

// ValidateRoomPassword checks password strength
func ValidateRoomPassword(pwd string, config password.PasswordStrengthConfig, enforce bool) error {
	if pwd == "" {
		return NewValidationError("password", "cannot be empty")
	}

	// Check strength - use the password package's function
	isStrong := password.ValidatePasswordStrength(pwd, config)
	if !isStrong {
		if enforce {
			return ErrWeakRoomPassword
		}
	}

	return nil
}

// SecureRoomAuth combines room-password authorization with compact HS256 token
// verification and per-instance metrics.
type SecureRoomAuth struct {
	roomAuth  *SimpleRoomAuth
	tokenAuth *SimpleHS256TokenAuth

	securityConfig SecurityConfig

	// Per-instance metrics (lock-free atomics)
	jwtVerificationFailures   atomic.Uint64
	weakRoomPasswords         atomic.Uint64
	successfulAuthentications atomic.Uint64
}

// NewSecureRoomAuth creates a secure room auth with validation
func NewSecureRoomAuth(secret []byte, cfg SecurityConfig) (*SecureRoomAuth, error) {
	effectiveSecret := secret
	if len(cfg.JWTSecret) > 0 {
		if len(secret) > 0 && !bytes.Equal(secret, cfg.JWTSecret) {
			return nil, fmt.Errorf("%w: provided secret and config JWTSecret do not match", ErrInvalidConfig)
		}
		effectiveSecret = cfg.JWTSecret
	}
	effectiveSecret = cloneBytes(effectiveSecret)
	cfg.JWTSecret = effectiveSecret

	// Validate config
	if err := ValidateSecurityConfig(cfg); err != nil {
		return nil, err
	}

	// Set defaults
	if cfg.MinJWTSecretLength == 0 {
		cfg.MinJWTSecretLength = 32
	}
	if cfg.MaxMessageSize == 0 {
		cfg.MaxMessageSize = 16 << 20 // 16MB
	}
	if cfg.RoomPasswordConfig.MinLength == 0 {
		cfg.RoomPasswordConfig = password.DefaultPasswordStrengthConfig()
	}
	if cfg.AllowWeakRoomPasswords {
		cfg.EnforcePasswordStrength = false
	} else {
		cfg.EnforcePasswordStrength = true
	}

	tokenAuth, err := NewSimpleHS256TokenAuth(effectiveSecret)
	if err != nil {
		return nil, err
	}
	cfg.JWTSecret = nil

	return &SecureRoomAuth{
		roomAuth:       NewSimpleRoomAuth(),
		tokenAuth:      tokenAuth,
		securityConfig: cfg,
	}, nil
}

// MaxMessageSize returns the configured max inbound message size for optional
// server-side read-limit enforcement.
func (s *SecureRoomAuth) MaxMessageSize() int64 {
	return s.securityConfig.MaxMessageSize
}

// SetRoomPassword overrides with security validation
func (s *SecureRoomAuth) SetRoomPassword(room, pwd string) error {
	if err := ValidateRoomPassword(pwd, s.securityConfig.RoomPasswordConfig, s.securityConfig.EnforcePasswordStrength); err != nil {
		s.weakRoomPasswords.Add(1)
		return err
	}
	isStrong := password.ValidatePasswordStrength(pwd, s.securityConfig.RoomPasswordConfig)
	if !isStrong && s.securityConfig.Logger != nil {
		s.securityConfig.Logger.Printf("websocket: weak room password accepted for room %q", room)
	}
	return s.roomAuth.SetRoomPassword(room, pwd)
}

// CheckRoomPassword checks room access with the configured password authorizer.
func (s *SecureRoomAuth) CheckRoomPassword(room, provided string) bool {
	return s.roomAuth.CheckRoomPassword(room, provided)
}

// VerifyJWT overrides with additional logging and per-instance metrics
func (s *SecureRoomAuth) VerifyJWT(token string) (map[string]any, error) {
	payload, err := s.tokenAuth.VerifyJWT(token)
	if err != nil {
		if s.securityConfig.EnableDebugLogging && s.securityConfig.Logger != nil {
			s.securityConfig.Logger.Printf("websocket: JWT verification failed: %v", err)
		}
		s.jwtVerificationFailures.Add(1)
		return nil, err
	}
	s.successfulAuthentications.Add(1)
	return payload, nil
}

// GetMetrics returns a snapshot of this instance's security metrics.
func (s *SecureRoomAuth) GetMetrics() SecurityMetrics {
	return SecurityMetrics{
		JWTVerificationFailures:   s.jwtVerificationFailures.Load(),
		WeakRoomPasswords:         s.weakRoomPasswords.Load(),
		SuccessfulAuthentications: s.successfulAuthentications.Load(),
	}
}

// ResetMetrics resets all per-instance security metrics to zero.
func (s *SecureRoomAuth) ResetMetrics() {
	s.jwtVerificationFailures.Store(0)
	s.weakRoomPasswords.Store(0)
	s.successfulAuthentications.Store(0)
}

// GenerateSecureSecret generates a cryptographically secure random secret
func GenerateSecureSecret(length int) ([]byte, error) {
	if length < 32 {
		return nil, NewValidationError("secret", "length should be at least 32 bytes")
	}
	secret := make([]byte, length)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
}
