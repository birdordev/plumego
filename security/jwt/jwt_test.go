package jwt

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	kvstore "github.com/birdordev/plumego/store/kv"
)

func newTestStore(t *testing.T) *kvstore.KVStore {
	t.Helper()
	store, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("failed to create kv store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

type recordingContextKeyStore struct {
	mu sync.Mutex

	data map[string][]byte

	keysContextErr error
	getContextErr  error
	setContextErr  error

	keysContextCalls int
	getContextCalls  int
	setContextCalls  int
}

func newRecordingContextKeyStore() *recordingContextKeyStore {
	return &recordingContextKeyStore{data: make(map[string][]byte)}
}

func (s *recordingContextKeyStore) GetContext(ctx context.Context, key string) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getContextCalls++
	if s.getContextErr != nil {
		return nil, s.getContextErr
	}
	value, ok := s.data[key]
	if !ok {
		return nil, errors.New("missing key")
	}
	return append([]byte(nil), value...), nil
}

func (s *recordingContextKeyStore) SetContext(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setContextCalls++
	if s.setContextErr != nil {
		return s.setContextErr
	}
	s.data[key] = append([]byte(nil), value...)
	_ = ttl
	return nil
}

func (s *recordingContextKeyStore) KeysContext(ctx context.Context) ([]string, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keysContextCalls++
	if s.keysContextErr != nil {
		return nil, s.keysContextErr
	}
	keys := make([]string, 0, len(s.data))
	for key := range s.data {
		keys = append(keys, key)
	}
	return keys, nil
}

// ========== basic function test ==========

func TestGenerateAndVerifyTokenPair(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	cfg.AccessExpiration = time.Minute
	cfg.RefreshExpiration = time.Hour

	mgr, err := NewJWTManager(store, cfg)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	identity := IdentityClaims{Subject: "user-1"}
	authz := AuthorizationClaims{
		Roles:       []string{"user"},
		Permissions: []string{"read"},
	}

	pair, err := mgr.GenerateTokenPair(t.Context(), identity, authz)
	if err != nil {
		t.Fatalf("failed to generate pair: %v", err)
	}

	// verify returned TokenPair structure
	if pair.TokenType != "Bearer" {
		t.Errorf("expected token_type=Bearer, got %s", pair.TokenType)
	}
	if pair.ExpiresIn != 60 {
		t.Errorf("expected expires_in=60, got %d", pair.ExpiresIn)
	}

	// verify access token claims
	accessClaims, err := mgr.VerifyToken(t.Context(), pair.AccessToken, TokenTypeAccess)
	if err != nil {
		t.Fatalf("failed to verify access token: %v", err)
	}
	if accessClaims.Identity.Subject != "user-1" {
		t.Errorf("expected subject=user-1, got %s", accessClaims.Identity.Subject)
	}
	if accessClaims.TokenType != TokenTypeAccess {
		t.Errorf("expected token_type=access, got %s", accessClaims.TokenType)
	}
	if len(accessClaims.Authorization.Roles) != 1 || accessClaims.Authorization.Roles[0] != "user" {
		t.Errorf("unexpected roles: %v", accessClaims.Authorization.Roles)
	}

	// verify refresh token claims
	refreshClaims, err := mgr.VerifyToken(t.Context(), pair.RefreshToken, TokenTypeRefresh)
	if err != nil {
		t.Fatalf("failed to verify refresh token: %v", err)
	}
	if refreshClaims.TokenType != TokenTypeRefresh {
		t.Errorf("expected token_type=refresh, got %s", refreshClaims.TokenType)
	}
}

func TestMissingSubject(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	mgr, err := NewJWTManager(store, cfg)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// missing subject should fail
	_, err = mgr.GenerateTokenPair(t.Context(), IdentityClaims{}, AuthorizationClaims{})
	if err != ErrMissingSubject {
		t.Errorf("expected ErrMissingSubject, got %v", err)
	}
}

// ========== key rotation test ==========

// ========== caller-owned identity version test ==========

// ========== clock skew tolerance test ==========

// ========== EdDSA Algorithm Test ==========

// ========== Error Scenario Test ==========

// ========== Authorization Policy Test ==========

// ========== Middleware Test ==========

// ========== Authorization Middleware Test ==========

// ========== Debug Mode Test ==========

// ========== URL Token Test ==========

// ========== Performance Test ==========

func BenchmarkGenerateTokenPair(b *testing.B) {
	store, _ := kvstore.NewKVStore(kvstore.Config{DataDir: b.TempDir()})
	cfg := DefaultJWTConfig()
	mgr, _ := NewJWTManager(store, cfg)

	identity := IdentityClaims{Subject: "bench-user"}
	authz := AuthorizationClaims{Roles: []string{"user"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mgr.GenerateTokenPair(b.Context(), identity, authz)
	}
}

func BenchmarkVerifyToken(b *testing.B) {
	store, _ := kvstore.NewKVStore(kvstore.Config{DataDir: b.TempDir()})
	cfg := DefaultJWTConfig()
	mgr, _ := NewJWTManager(store, cfg)

	identity := IdentityClaims{Subject: "bench-user"}
	pair, _ := mgr.GenerateTokenPair(b.Context(), identity, AuthorizationClaims{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mgr.VerifyToken(b.Context(), pair.AccessToken, TokenTypeAccess)
	}
}

func BenchmarkGenerateTokenPairEdDSA(b *testing.B) {
	store, _ := kvstore.NewKVStore(kvstore.Config{DataDir: b.TempDir()})
	cfg := DefaultJWTConfig()
	cfg.Algorithm = AlgorithmEdDSA
	mgr, _ := NewJWTManager(store, cfg)

	identity := IdentityClaims{Subject: "bench-user"}
	authz := AuthorizationClaims{Roles: []string{"user"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mgr.GenerateTokenPair(b.Context(), identity, authz)
	}
}

// ========== Contract Adapter Test ==========

// ========== Helper Functions ==========

func mustExtractKid(t *testing.T, token string) string {
	t.Helper()
	header := mustExtractHeader(t, token)
	kid, _ := header["kid"].(string)
	return kid
}

func mustExtractHeader(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("invalid token format")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var hdr map[string]any
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	return hdr
}

func mustSignToken(t *testing.T, mgr *JWTManager, claims TokenClaims) string {
	t.Helper()

	mgr.mu.RLock()
	key := mgr.keyCache[mgr.active]
	mgr.mu.RUnlock()
	claims.KeyID = key.ID

	token, err := signJWT(key, claims)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func mustSignTokenWithoutClaimKeyOverride(t *testing.T, mgr *JWTManager, claims TokenClaims) string {
	t.Helper()

	mgr.mu.RLock()
	key := mgr.keyCache[mgr.active]
	mgr.mu.RUnlock()

	token, err := signJWT(key, claims)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func mustSignTokenWithHeader(t *testing.T, mgr *JWTManager, claims TokenClaims, overrides map[string]any) string {
	t.Helper()

	mgr.mu.RLock()
	key := mgr.keyCache[mgr.active]
	mgr.mu.RUnlock()
	claims.KeyID = key.ID

	header := map[string]any{
		"alg": key.Algorithm,
		"typ": "JWT",
		"kid": key.ID,
	}
	for k, v := range overrides {
		header[k] = v
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	headerPart := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadPart := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := headerPart + "." + payloadPart

	if key.Algorithm != AlgorithmHS256 {
		t.Fatalf("test helper supports HS256 only, got %s", key.Algorithm)
	}
	mac := hmac.New(sha256.New, key.Secret)
	mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
