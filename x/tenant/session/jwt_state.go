package session

import (
	"errors"
	"strconv"
	"time"

	"github.com/birdordev/plumego/security/authn"
	"github.com/birdordev/plumego/security/jwt"
	kvstore "github.com/birdordev/plumego/store/kv"
)

const (
	revokedTokenPrefix   = "session:jwt:revoked:"
	subjectVersionPrefix = "session:jwt:subject_version:"
)

// JWTStateStore owns revocation and subject-version state for JWT-backed sessions.
type JWTStateStore struct {
	store *kvstore.KVStore
}

// NewJWTStateStore creates a JWT lifecycle store backed by the stable KV primitive.
func NewJWTStateStore(store *kvstore.KVStore) (*JWTStateStore, error) {
	if store == nil {
		return nil, errors.New("kv store is required")
	}
	return &JWTStateStore{store: store}, nil
}

// RevokeClaims records the token id from verified claims until token expiry.
func (s *JWTStateStore) RevokeClaims(claims *jwt.TokenClaims) error {
	if s == nil || s.store == nil {
		return errors.New("jwt state store is nil")
	}
	if claims == nil || claims.TokenID == "" {
		return authn.ErrInvalidToken
	}

	ttl := time.Until(time.Unix(claims.ExpiresAt, 0))
	if ttl < 0 {
		ttl = 0
	}
	return s.store.Set(revokedTokenPrefix+claims.TokenID, []byte("revoked"), ttl)
}

// IncrementSubjectVersion bumps the current subject version and returns the new value.
func (s *JWTStateStore) IncrementSubjectVersion(subject string) (int64, error) {
	if s == nil || s.store == nil {
		return 0, errors.New("jwt state store is nil")
	}
	current, err := s.SubjectVersion(subject)
	if err != nil {
		return 0, err
	}
	next := current + 1
	if err := s.store.Set(subjectVersionPrefix+subject, []byte(strconv.FormatInt(next, 10)), 0); err != nil {
		return 0, err
	}
	return next, nil
}

// SubjectVersion returns the current version for a subject. Missing subjects default to zero.
func (s *JWTStateStore) SubjectVersion(subject string) (int64, error) {
	if s == nil || s.store == nil {
		return 0, errors.New("jwt state store is nil")
	}
	if subject == "" {
		return 0, authn.ErrInvalidToken
	}

	raw, err := s.store.Get(subjectVersionPrefix + subject)
	if err != nil {
		if errors.Is(err, kvstore.ErrKeyNotFound) || errors.Is(err, kvstore.ErrKeyExpired) {
			return 0, nil
		}
		return 0, err
	}

	version, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return 0, authn.ErrInvalidToken
	}
	return version, nil
}

// ValidateClaims applies session-lifecycle checks to already verified JWT claims.
func (s *JWTStateStore) ValidateClaims(claims *jwt.TokenClaims) error {
	if s == nil || s.store == nil {
		return errors.New("jwt state store is nil")
	}
	if claims == nil || claims.TokenID == "" || claims.Identity.Subject == "" {
		return authn.ErrInvalidToken
	}

	_, err := s.store.Get(revokedTokenPrefix + claims.TokenID)
	if err == nil {
		return ErrSessionRevoked
	}
	if err != nil && !errors.Is(err, kvstore.ErrKeyNotFound) && !errors.Is(err, kvstore.ErrKeyExpired) {
		return ErrSessionRevoked
	}

	current, err := s.SubjectVersion(claims.Identity.Subject)
	if err != nil {
		return err
	}
	if current != claims.Identity.Version {
		return ErrTokenVersionMismatch
	}
	return nil
}
