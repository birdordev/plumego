package jwt

import (
	"context"
	"time"
)

// IdentityClaims captures authentication (who the subject is).
//
// Example:
//
//	import "github.com/birdordev/plumego/security/jwt"
//
//	identity := jwt.IdentityClaims{
//		Subject: "user-123",
//		Version: 1,
//	}
type IdentityClaims struct {
	Subject string `json:"sub"`
	Version int64  `json:"ver"`
}

// AuthorizationClaims captures authorization data (what the subject can do).
//
// Example:
//
//	import "github.com/birdordev/plumego/security/jwt"
//
//	authz := jwt.AuthorizationClaims{
//		Roles:       []string{"admin", "user"},
//		Permissions: []string{"read:users", "write:users"},
//	}
type AuthorizationClaims struct {
	Roles       []string `json:"roles,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

// TokenClaims represents a full JWT payload.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/jwt"
//
//	claims := jwt.TokenClaims{
//		TokenID:   "token-123",
//		TokenType: jwt.TokenTypeAccess,
//		Identity: jwt.IdentityClaims{
//			Subject: "user-123",
//			Version: 1,
//		},
//		Authorization: jwt.AuthorizationClaims{
//			Roles: []string{"admin"},
//		},
//		Issuer:    "plumego",
//		Audience:  "plumego-client",
//		IssuedAt:  time.Now().Unix(),
//		ExpiresAt: time.Now().Add(15 * time.Minute).Unix(),
//	}
type TokenClaims struct {
	TokenID       string              `json:"jti"`
	TokenType     TokenType           `json:"token_type"`
	Identity      IdentityClaims      `json:"identity"`
	Authorization AuthorizationClaims `json:"authorization"`
	Issuer        string              `json:"iss"`
	Audience      string              `json:"aud"`
	IssuedAt      int64               `json:"iat"`
	NotBefore     int64               `json:"nbf"`
	ExpiresAt     int64               `json:"exp"`
	KeyID         string              `json:"kid"`
}

// JWTSigningKey represents persisted signing key material.
//
// Application code should treat this type as sensitive when implementing a
// KeyStore. Public key-management APIs return SigningKeyMetadata instead.
//
// Example:
//
//	key := jwt.JWTSigningKey{
//		ID:        "key-123",
//		Algorithm: jwt.AlgorithmHS256,
//		Secret:    []byte("my-secret-key"),
//		CreatedAt: time.Now(),
//	}
type JWTSigningKey struct {
	ID        string    `json:"id"`
	Algorithm Algorithm `json:"alg"`
	Secret    []byte    `json:"secret,omitempty"`
	Public    []byte    `json:"public,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SigningKeyMetadata describes a signing key without exposing private material.
type SigningKeyMetadata struct {
	ID        string    `json:"id"`
	Algorithm Algorithm `json:"alg"`
	Public    []byte    `json:"public,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// TokenPair contains generated access and refresh tokens.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/jwt"
//
//	pair, err := manager.GenerateTokenPair(ctx, identity, authz)
//	if err != nil {
//		// handle error
//	}
//	// Send pair.AccessToken and pair.RefreshToken to client
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"` // expiration time in seconds
	TokenType    string `json:"token_type"` // always "Bearer"
}

// KeyStore is the minimal storage behavior JWTManager needs for signing keys.
type KeyStore interface {
	GetContext(ctx context.Context, key string) ([]byte, error)
	SetContext(ctx context.Context, key string, value []byte, ttl time.Duration) error
	KeysContext(ctx context.Context) ([]string, error)
}
