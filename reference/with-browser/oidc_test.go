package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

func TestOIDCProviderVerifiesNonceAndMapsRoles(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issuer := "https://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		var payload any
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			payload = map[string]any{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
				"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			}
		case "/keys":
			payload = map[string]any{"keys": []map[string]string{{
				"kid": "fixture-key", "kty": "RSA", "alg": "RS256",
				"n": encode(key.N.Bytes()), "e": encode(big.NewInt(int64(key.E)).Bytes()),
			}}}
		case "/token":
			if err := r.ParseForm(); err != nil || r.PostForm.Get("code_verifier") != "fixture-verifier" {
				http.Error(w, "missing verifier", http.StatusBadRequest)
				return
			}
			claims, err := json.Marshal(map[string]any{
				"iss": issuer, "sub": "member-1", "aud": "fixture-client",
				"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
				"nonce": "fixture-nonce", "groups": []string{"admin-group"},
			})
			if err != nil {
				t.Error(err)
				return
			}
			signed := encode([]byte(`{"alg":"RS256","kid":"fixture-key"}`)) + "." + encode(claims)
			digest := sha256.Sum256([]byte(signed))
			signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
			if err != nil {
				t.Error(err)
				return
			}
			payload = map[string]any{
				"access_token": "fixture-access", "token_type": "Bearer", "id_token": signed + "." + encode(signature),
			}
		default:
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	ctx := oidc.ClientContext(t.Context(), server.Client())
	provider, err := newOIDC(ctx, server.URL, "fixture-client", "fixture-secret", "https://app.example/auth/callback", "admin-group")
	if err != nil {
		t.Fatal(err)
	}
	if authURL := provider.authorizationURL("fixture-state", "fixture-nonce", "fixture-verifier"); !strings.Contains(authURL, "code_challenge_method=S256") {
		t.Fatalf("authorization URL omits PKCE: %s", authURL)
	}
	if _, err := provider.identity(ctx, "code", "fixture-verifier", "wrong-nonce"); err == nil {
		t.Fatal("accepted the wrong nonce")
	}
	principal, err := provider.identity(ctx, "code", "fixture-verifier", "fixture-nonce")
	if err != nil || principal.Subject != server.URL+":member-1" || !slices.Contains(principal.Roles, "admin") {
		t.Fatalf("identity mapping: principal=%+v err=%v", principal, err)
	}
}
