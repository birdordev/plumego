package jwt

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClockSkewTolerance(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	cfg.AccessExpiration = 1 * time.Second
	cfg.ClockSkew = 1 * time.Second // allow 1 second clock skew

	mgr, err := NewJWTManager(store, cfg)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	pair, _ := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "user-skew"}, AuthorizationClaims{})

	// Sleep inside the skew window (exp + skew = 2s) so the still-valid check
	// does not sit on the truncation boundary. A longer sleep could tip the
	// second-truncated timestamp past exp+skew on a loaded machine.
	time.Sleep(1200 * time.Millisecond)

	// verify token is valid within clock skew
	_, err = mgr.VerifyToken(t.Context(), pair.AccessToken, TokenTypeAccess)
	if err != nil {
		t.Errorf("token should be valid within clock skew: %v", err)
	}

	// sleep past the window (total 3.3s > 1s expiration + 2s clock skew)
	time.Sleep(2100 * time.Millisecond)

	// token should be expired
	_, err = mgr.VerifyToken(t.Context(), pair.AccessToken, TokenTypeAccess)
	if err != ErrTokenExpired {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

func TestNotBeforeWithClockSkew(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	cfg.ClockSkew = 2 * time.Second

	mgr, err := NewJWTManager(store, cfg)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// manually build a token with nbf in the future
	mgr.mu.Lock()
	activeKey := mgr.keyCache[mgr.active]
	identity := IdentityClaims{Subject: "user-nbf", Version: 0}
	authz := AuthorizationClaims{}

	jti, _ := randomID()
	now := time.Now().UTC()
	claims := TokenClaims{
		TokenID:       jti,
		TokenType:     TokenTypeAccess,
		Identity:      identity,
		Authorization: authz,
		Issuer:        cfg.Issuer,
		Audience:      cfg.Audience,
		IssuedAt:      now.Unix(),
		NotBefore:     now.Add(3 * time.Second).Unix(), // nbf=3s
		ExpiresAt:     now.Add(time.Hour).Unix(),
		KeyID:         activeKey.ID,
	}
	token, _ := signJWT(activeKey, claims)
	mgr.mu.Unlock()

	// verify token should fail immediately (nbf=3s, elapsed=0s, skew=2s, 0 < 3-2)
	_, err = mgr.VerifyToken(t.Context(), token, TokenTypeAccess)
	if err != ErrTokenNotYetValid {
		t.Errorf("expected ErrTokenNotYetValid, got %v", err)
	}

	// wait for token to become valid (nbf=3s, elapsed=1.5s, skew=2s, 1.5 < 3-2)
	time.Sleep(1500 * time.Millisecond)

	// token should be valid now (nbf=3s, elapsed=1.5s, skew=2s, 1.5 < 3-2)
	_, err = mgr.VerifyToken(t.Context(), token, TokenTypeAccess)
	if err != nil {
		t.Errorf("token should be valid within clock skew: %v", err)
	}
}

func TestInvalidToken(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	mgr, _ := NewJWTManager(store, cfg)

	tests := []struct {
		name  string
		token string
		want  error
	}{
		{"empty", "", ErrInvalidToken},
		{"malformed", "not.a.jwt", ErrInvalidToken},
		{"invalid base64", "aaa.bbb.ccc", ErrInvalidToken},
		{"missing parts", "aaa.bbb", ErrInvalidToken},
		{"extra parts", "aaa.bbb.ccc.ddd", ErrInvalidToken},
		{"empty header segment", ".bbb.ccc", ErrInvalidToken},
		{"empty payload segment", "aaa..ccc", ErrInvalidToken},
		{"empty signature segment", "aaa.bbb.", ErrInvalidToken},
		{"oversized token", strings.Repeat("a", maxJWTTokenLength+1), ErrInvalidToken},
		{"oversized segment", strings.Repeat("a", maxJWTSegmentLength+1) + ".bbb.ccc", ErrInvalidToken},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mgr.VerifyToken(t.Context(), tt.token, TokenTypeAccess)
			if err != tt.want {
				t.Errorf("expected error %v, got %v", tt.want, err)
			}
		})
	}
}

func TestVerifyTokenRejectsInvalidHeaderAndPayloadKeyID(t *testing.T) {
	store := newTestStore(t)
	mgr, err := NewJWTManager(store, DefaultJWTConfig())
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	now := time.Now().UTC()
	claims := TokenClaims{
		TokenID:       "token-header-shape",
		TokenType:     TokenTypeAccess,
		Identity:      IdentityClaims{Subject: "user-header-shape"},
		Authorization: AuthorizationClaims{},
		Issuer:        mgr.config.Issuer,
		Audience:      mgr.config.Audience,
		IssuedAt:      now.Unix(),
		NotBefore:     now.Unix(),
		ExpiresAt:     now.Add(time.Hour).Unix(),
	}

	t.Run("invalid typ", func(t *testing.T) {
		token := mustSignTokenWithHeader(t, mgr, claims, map[string]any{"typ": "not-jwt"})
		_, err := mgr.VerifyToken(t.Context(), token, TokenTypeAccess)
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("VerifyToken error = %v, want ErrInvalidToken", err)
		}
	})

	t.Run("payload kid mismatch", func(t *testing.T) {
		mutated := claims
		mutated.KeyID = "different-key"
		token := mustSignTokenWithoutClaimKeyOverride(t, mgr, mutated)
		_, err := mgr.VerifyToken(t.Context(), token, TokenTypeAccess)
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("VerifyToken error = %v, want ErrInvalidToken", err)
		}
	})
}

func TestJWTManagerClockControlsVerificationExpiry(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	cfg.AccessExpiration = time.Minute
	cfg.ClockSkew = 0
	fixed := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	mgr, err := NewJWTManager(store, cfg)
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}
	mgr.now = func() time.Time { return fixed }

	pair, err := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "expired-user"}, AuthorizationClaims{})
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	mgr.now = func() time.Time { return fixed.Add(cfg.AccessExpiration + time.Second) }
	if _, err := mgr.VerifyToken(t.Context(), pair.AccessToken, TokenTypeAccess); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("VerifyToken error = %v, want ErrTokenExpired", err)
	}
}

func TestTokenTypeMismatch(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	mgr, _ := NewJWTManager(store, cfg)

	pair, _ := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "user-type"}, AuthorizationClaims{})

	// verify using access token to verify refresh token should fail
	_, err := mgr.VerifyToken(t.Context(), pair.AccessToken, TokenTypeRefresh)
	if err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for type mismatch, got %v", err)
	}
}

func TestInvalidIssuerAudience(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	cfg.Issuer = "plumego"
	cfg.Audience = "plumego-client"

	mgr, _ := NewJWTManager(store, cfg)

	// generate normal token
	pair, _ := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "user-iss"}, AuthorizationClaims{})

	// verify with modified issuer should fail
	mgr.config.Issuer = "different-issuer"
	_, err := mgr.VerifyToken(t.Context(), pair.AccessToken, TokenTypeAccess)
	if err != ErrInvalidIssuer {
		t.Errorf("expected ErrInvalidIssuer, got %v", err)
	}

	// restore issuer, modify audience should fail
	mgr.config.Issuer = "plumego"
	mgr.config.Audience = "different-audience"
	_, err = mgr.VerifyToken(t.Context(), pair.AccessToken, TokenTypeAccess)
	if err != ErrInvalidAudience {
		t.Errorf("expected ErrInvalidAudience, got %v", err)
	}
}

func TestVerifyTokenRequiresConfiguredIssuerAudienceAndSubject(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	mgr, err := NewJWTManager(store, cfg)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	now := time.Now().UTC()
	mgr.now = func() time.Time { return now }

	baseClaims := TokenClaims{
		TokenID:       "token-semantic",
		TokenType:     TokenTypeAccess,
		Identity:      IdentityClaims{Subject: "user-semantic"},
		Authorization: AuthorizationClaims{},
		Issuer:        cfg.Issuer,
		Audience:      cfg.Audience,
		IssuedAt:      now.Unix(),
		NotBefore:     now.Unix(),
		ExpiresAt:     now.Add(time.Hour).Unix(),
	}

	tests := []struct {
		name   string
		mutate func(*TokenClaims)
		want   error
	}{
		{
			name: "missing issuer",
			mutate: func(claims *TokenClaims) {
				claims.Issuer = ""
			},
			want: ErrInvalidIssuer,
		},
		{
			name: "missing audience",
			mutate: func(claims *TokenClaims) {
				claims.Audience = ""
			},
			want: ErrInvalidAudience,
		},
		{
			name: "missing subject",
			mutate: func(claims *TokenClaims) {
				claims.Identity.Subject = ""
			},
			want: ErrMissingSubject,
		},
		{
			name: "missing issued at",
			mutate: func(claims *TokenClaims) {
				claims.IssuedAt = 0
			},
			want: ErrInvalidToken,
		},
		{
			name: "future issued at beyond skew",
			mutate: func(claims *TokenClaims) {
				claims.IssuedAt = now.Add(cfg.ClockSkew + time.Second).Unix()
			},
			want: ErrInvalidToken,
		},
		{
			name: "missing not before",
			mutate: func(claims *TokenClaims) {
				claims.NotBefore = 0
			},
			want: ErrInvalidToken,
		},
		{
			name: "missing expires at",
			mutate: func(claims *TokenClaims) {
				claims.ExpiresAt = 0
			},
			want: ErrInvalidToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := baseClaims
			tt.mutate(&claims)
			token := mustSignToken(t, mgr, claims)

			_, err := mgr.VerifyToken(t.Context(), token, TokenTypeAccess)
			if !errors.Is(err, tt.want) {
				t.Fatalf("VerifyToken error = %v, want %v", err, tt.want)
			}
		})
	}
}
