package websocket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/birdordev/plumego/security/password"
)

// TokenAuthenticator verifies client tokens and returns token claims.
//
// Implement this interface to provide custom JWT/OIDC/session policy logic, or
// use NewSimpleHS256TokenAuth for the built-in compact HS256 verifier.
//
// Example:
//
//	auth, err := websocket.NewSimpleHS256TokenAuth(secret)
//	if err != nil {
//		return err
//	}
type TokenAuthenticator interface {
	VerifyJWT(token string) (map[string]any, error)
}

// RoomAuthorizer authorizes access to a room.
type RoomAuthorizer interface {
	CheckRoomPassword(room, provided string) bool
}

// RoomAuthorization describes a room authorization decision after transport
// validation and token verification.
type RoomAuthorization struct {
	Request      *http.Request
	Room         string
	Password     string
	User         *UserInfo
	TokenClaims  map[string]any
	Anonymous    bool
	QueryTokenOK bool
}

// RoomRequestAuthorizer authorizes access to a room with request and user
// context. Implement this interface when room access depends on authenticated
// claims, request metadata, or policy beyond a shared room password.
type RoomRequestAuthorizer interface {
	AuthorizeRoom(RoomAuthorization) bool
}

// SimpleRoomAuth stores metadata about rooms (password).
//
// This is a lightweight room-password helper. It stores hashed passwords for
// each configured room and allows rooms with no configured password. Use
// NewSecureRoomAuth or a custom RoomRequestAuthorizer for stricter production
// policy.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	auth := websocket.NewSimpleRoomAuth()
//	if err := auth.SetRoomPassword("chat-room", "my-secret-password"); err != nil {
//		panic(err)
//	}
//
//	// Check if password is correct
//	if auth.CheckRoomPassword("chat-room", "my-secret-password") {
//		// Allow access
//	}
type SimpleRoomAuth struct {
	roomPasswords map[string]string // room -> password (hashed)
	mu            sync.RWMutex
}

// NewSimpleRoomAuth creates a simple room-password authorizer.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	auth := websocket.NewSimpleRoomAuth()
func NewSimpleRoomAuth() *SimpleRoomAuth {
	return &SimpleRoomAuth{
		roomPasswords: make(map[string]string),
	}
}

// SetRoomPassword sets the password for a room.
// The password is hashed before storing for security.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	auth := websocket.NewSimpleRoomAuth()
//	if err := auth.SetRoomPassword("chat-room", "my-secret-password"); err != nil {
//		panic(err)
//	}
func (s *SimpleRoomAuth) SetRoomPassword(room, pwd string) error {
	hashed, err := password.HashPassword(pwd)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.roomPasswords[room] = string(hashed)
	return nil
}

func (s *SimpleRoomAuth) CheckRoomPassword(room, provided string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if hashed, ok := s.roomPasswords[room]; ok {
		err := password.CheckPassword(hashed, provided)
		return err == nil
	}
	// no password set => allowed
	return true
}

// SimpleHS256TokenAuth verifies compact HS256 JWTs.
type SimpleHS256TokenAuth struct {
	jwtSecret []byte
}

// NewSimpleHS256TokenAuth creates a compact HS256 token authenticator.
func NewSimpleHS256TokenAuth(secret []byte) (*SimpleHS256TokenAuth, error) {
	if err := ValidateSecurityConfig(SecurityConfig{
		JWTSecret:          secret,
		MinJWTSecretLength: minWebSocketSecretLen,
	}); err != nil {
		return nil, err
	}
	return &SimpleHS256TokenAuth{jwtSecret: cloneBytes(secret)}, nil
}

// VerifyJWT verifies a compact HS256 token and returns the payload map.
//
// It validates the HS256 signature and optional exp claim. It does not validate
// issuer, audience, nbf, iat, or custom required claims; provide a custom
// TokenAuthenticator for those policy requirements.
func (s *SimpleHS256TokenAuth) VerifyJWT(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}
	hdrb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var hdr map[string]any
	if err = json.Unmarshal(hdrb, &hdr); err != nil {
		return nil, ErrInvalidToken
	}
	if alg, ok := hdr["alg"].(string); !ok || alg != "HS256" {
		return nil, ErrInvalidToken
	}
	payloadb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var payload map[string]any
	if err = json.Unmarshal(payloadb, &payload); err != nil {
		return nil, ErrInvalidToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, s.jwtSecret)
	// "header.payload" is the prefix of the original token string up to the
	// second dot. Slicing avoids the extra allocation from parts[0]+"."+parts[1].
	signingLen := len(parts[0]) + 1 + len(parts[1])
	mac.Write([]byte(token[:signingLen]))
	expected := mac.Sum(nil)
	if !hmac.Equal(expected, sig) {
		return nil, ErrInvalidToken
	}
	// Verify exp if present
	if expv, ok := payload["exp"]; ok {
		switch t := expv.(type) {
		case float64:
			if t < 0 || t != float64(int64(t)) {
				return nil, fmt.Errorf("%w: exp claim must be a non-negative integer", ErrInvalidToken)
			}
			if time.Now().Unix() > int64(t) {
				return nil, ErrTokenExpired
			}
		case int64:
			if t < 0 {
				return nil, fmt.Errorf("%w: exp claim must be a non-negative integer", ErrInvalidToken)
			}
			if time.Now().Unix() > t {
				return nil, ErrTokenExpired
			}
		default:
			return nil, fmt.Errorf("%w: exp claim must be numeric", ErrInvalidToken)
		}
	}
	return payload, nil
}

// ExtractUserInfo extracts UserInfo from JWT payload.
//
// The "roles" claim is parsed correctly regardless of whether the JWT was
// decoded into map[string]any (where JSON arrays become []interface{}) or
// directly into a typed struct.
func ExtractUserInfo(payload map[string]any) *UserInfo {
	userInfo := &UserInfo{
		Claims: payload,
	}
	// Extract common claims if present
	if id, ok := payload["sub"].(string); ok {
		userInfo.ID = id
	}
	if name, ok := payload["name"].(string); ok {
		userInfo.Name = name
	}
	if email, ok := payload["email"].(string); ok {
		userInfo.Email = email
	}
	// json.Unmarshal into map[string]any always produces []interface{} for JSON
	// arrays, never []string. Handle both representations for forward compat.
	switch v := payload["roles"].(type) {
	case []any:
		for _, r := range v {
			if s, ok := r.(string); ok {
				userInfo.Roles = append(userInfo.Roles, s)
			}
		}
	case []string:
		userInfo.Roles = v
	}
	return userInfo
}
