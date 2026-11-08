package jwt

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerificationExpiryBoundary(t *testing.T) {
	for _, algorithm := range []Algorithm{AlgorithmHS256, AlgorithmEdDSA} {
		for _, skew := range []time.Duration{0, 5 * time.Second} {
			t.Run(fmt.Sprintf("%s/skew=%s", algorithm, skew), func(t *testing.T) {
				cfg := DefaultJWTConfig()
				cfg.Algorithm, cfg.ClockSkew = algorithm, skew
				mgr, err := NewJWTManager(NewInMemoryKeyStore(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				issued := time.Unix(1800000000, 0)
				mgr.now = func() time.Time { return issued }
				pair, err := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "boundary-user"}, AuthorizationClaims{})
				if err != nil {
					t.Fatal(err)
				}
				boundary := issued.Add(cfg.AccessExpiration + skew)
				mgr.now = func() time.Time { return boundary.Add(-time.Second) }
				if _, err := mgr.VerifyToken(t.Context(), pair.AccessToken, TokenTypeAccess); err != nil {
					t.Fatalf("before boundary: %v", err)
				}
				mgr.now = func() time.Time { return boundary }
				claims, err := mgr.VerifyToken(t.Context(), pair.AccessToken, TokenTypeAccess)
				if claims != nil || !errors.Is(err, ErrTokenExpired) {
					t.Fatalf("at boundary: claims present=%v, error=%v", claims != nil, err)
				}
				claims = &TokenClaims{
					TokenType: TokenTypeAccess, Identity: IdentityClaims{Subject: "boundary-user"},
					Issuer: cfg.Issuer, Audience: cfg.Audience,
					IssuedAt: issued.Unix(), NotBefore: issued.Unix(), ExpiresAt: math.MaxInt64,
				}
				token := mustSignToken(t, mgr, *claims)
				if _, err := mgr.VerifyToken(t.Context(), token, TokenTypeAccess); err != nil {
					t.Fatalf("large expiration must not overflow with skew: %v", err)
				}
			})
		}
	}
}

func TestPersistedSigningKeyErrorsDoNotExposeValues(t *testing.T) {
	const sensitive = "credential-canary-do-not-log"
	cases := map[string]string{
		"invalid time":     `{"id":"key","created_at":"` + sensitive + `"}`,
		"wrong field type": `{"id":"key","secret":{"` + sensitive + `":true}}`,
		"invalid material": `{"id":"key","alg":"HS256","secret":"c2hvcnQ="}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			store := NewInMemoryKeyStore()
			if err := store.SetContext(t.Context(), keyPrefix+sensitive, []byte(raw), 0); err != nil {
				t.Fatal(err)
			}
			mgr, err := NewJWTManager(store, DefaultJWTConfig())
			if mgr != nil || err == nil {
				t.Fatal("invalid persisted key must fail startup")
			}
			for current := err; current != nil; current = errors.Unwrap(current) {
				if strings.Contains(fmt.Sprintf("%+v", current), sensitive) {
					t.Fatal("signing key error exposes a persisted value")
				}
			}
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("error = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestPersistedEdDSAKeyMustBeConsistent(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mismatched public key", "corrupt private key"} {
		t.Run(name, func(t *testing.T) {
			key := JWTSigningKey{ID: "key", Algorithm: AlgorithmEdDSA, Public: append([]byte(nil), public...), Secret: append([]byte(nil), private...), CreatedAt: time.Now()}
			if name == "mismatched public key" {
				key.Public = otherPublic
			} else {
				key.Secret[len(key.Secret)-1] ^= 1
			}
			raw, err := json.Marshal(key)
			if err != nil {
				t.Fatal(err)
			}
			store := NewInMemoryKeyStore()
			if err := store.SetContext(t.Context(), keyPrefix+key.ID, raw, 0); err != nil {
				t.Fatal(err)
			}
			mgr, err := NewJWTManager(store, DefaultJWTConfig())
			if mgr != nil || !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("inconsistent key accepted: manager present=%v, error=%v", mgr != nil, err)
			}
		})
	}
}

func TestCredentialDenialsDoNotExposeValues(t *testing.T) {
	for _, algorithm := range []Algorithm{AlgorithmHS256, AlgorithmEdDSA} {
		t.Run(string(algorithm), func(t *testing.T) {
			cfg := DefaultJWTConfig()
			cfg.Algorithm, cfg.ClockSkew = algorithm, 0
			mgr, err := NewJWTManager(NewInMemoryKeyStore(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			issued := time.Unix(1800000000, 0)
			mgr.now = func() time.Time { return issued }
			pair, err := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "secret-subject-canary"}, AuthorizationClaims{})
			if err != nil {
				t.Fatal(err)
			}
			key := mgr.keyCache[mgr.active]
			unknown := key
			unknown.ID = "unknown-key-canary"
			unknownToken, err := mgr.buildToken(unknown, TokenTypeAccess, IdentityClaims{Subject: "secret-subject-canary"}, AuthorizationClaims{}, issued, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name, token string
				now         time.Time
			}{
				{"malformed", "sensitive.invalid.credential", issued},
				{"signature", tamperJWT(pair.AccessToken), issued},
				{"unknown key", unknownToken, issued},
				{"wrong type", pair.RefreshToken, issued},
				{"expired", pair.AccessToken, issued.Add(cfg.AccessExpiration + time.Second)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					mgr.now = func() time.Time { return tc.now }
					req := httptest.NewRequest(http.MethodGet, "/protected", nil)
					req.Header.Set("Authorization", "Bearer "+tc.token)
					principal, enriched, err := mgr.Authenticator(TokenTypeAccess).AuthenticateRequest(req)
					if principal != nil || enriched != req || err == nil {
						t.Fatal("failed authentication must not enrich the request or return a principal")
					}
					rec := httptest.NewRecorder()
					protected := mustAuthMiddleware(t, mgr.Authenticator(TokenTypeAccess))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid credential reached downstream") }))
					protected.ServeHTTP(rec, req)
					if rec.Code != http.StatusUnauthorized {
						t.Fatalf("status = %d, want 401", rec.Code)
					}
					text := fmt.Sprintf("%+v %s %v", err, rec.Body.String(), rec.Header())
					parts := strings.Split(tc.token, ".")
					for _, value := range []string{tc.token, "secret-subject-canary", "unknown-key-canary", string(key.Secret), base64.StdEncoding.EncodeToString(key.Secret), parts[len(parts)-1]} {
						if value != "" && strings.Contains(text, value) {
							t.Fatal("credential denial exposes sensitive values")
						}
					}
				})
			}
		})
	}
}
