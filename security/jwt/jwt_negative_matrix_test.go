package jwt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
)

func TestAuthenticateNegativeMatrix(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	mgr, err := NewJWTManager(store, cfg)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	accessPair, err := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "neg-user"}, AuthorizationClaims{})
	if err != nil {
		t.Fatalf("failed to generate access pair: %v", err)
	}
	refreshPair, err := mgr.GenerateTokenPair(t.Context(), IdentityClaims{Subject: "neg-user"}, AuthorizationClaims{})
	if err != nil {
		t.Fatalf("failed to generate refresh pair: %v", err)
	}

	handlerCalled := false
	protected := mustAuthMiddleware(t, mgr.Authenticator(TokenTypeAccess))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name            string
		authzHeader     string
		expectedCode    string
		expectedMessage string
	}{
		{
			name:            "missing authorization header",
			authzHeader:     "",
			expectedCode:    contract.CodeUnauthorized,
			expectedMessage: "authentication required",
		},
		{
			name:            "empty bearer token",
			authzHeader:     "Bearer   ",
			expectedCode:    contract.CodeUnauthorized,
			expectedMessage: "authentication required",
		},
		{
			name:            "malformed token",
			authzHeader:     "Bearer invalid.token",
			expectedCode:    contract.CodeUnauthorized,
			expectedMessage: "invalid token",
		},
		{
			name:            "tampered signature",
			authzHeader:     "Bearer " + tamperJWT(accessPair.AccessToken),
			expectedCode:    contract.CodeUnauthorized,
			expectedMessage: "invalid token",
		},
		{
			name:            "wrong token type",
			authzHeader:     "Bearer " + refreshPair.RefreshToken,
			expectedCode:    contract.CodeUnauthorized,
			expectedMessage: "invalid token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlerCalled = false

			req := httptest.NewRequest(http.MethodGet, "/secure", nil)
			if tt.authzHeader != "" {
				req.Header.Set("Authorization", tt.authzHeader)
			}

			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rec.Code)
			}
			if handlerCalled {
				t.Fatalf("protected handler must not run for negative auth cases")
			}

			var payload struct {
				Error struct {
					Code     string                 `json:"code"`
					Message  string                 `json:"message"`
					Category contract.ErrorCategory `json:"category"`
					Type     contract.ErrorType     `json:"type,omitempty"`
					Details  map[string]any         `json:"details,omitempty"`
				} `json:"error"`
				RequestID string `json:"request_id,omitempty"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("failed to decode error payload: %v", err)
			}

			if payload.Error.Code != tt.expectedCode {
				t.Fatalf("error code = %q, want %q", payload.Error.Code, tt.expectedCode)
			}
			if payload.Error.Message != tt.expectedMessage {
				t.Fatalf("error message = %q, want %q", payload.Error.Message, tt.expectedMessage)
			}
		})
	}
}

func tamperJWT(token string) string {
	if token == "" {
		return token
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return token + ".tampered"
	}
	if parts[2] == "" {
		parts[2] = "tampered"
	} else if parts[2][0] == 'A' {
		parts[2] = "B" + parts[2][1:]
	} else {
		parts[2] = "A" + parts[2][1:]
	}
	return strings.Join(parts, ".")
}

func TestAuthenticateRejectsNonBearerSchemes(t *testing.T) {
	store := newTestStore(t)
	cfg := DefaultJWTConfig()
	mgr, err := NewJWTManager(store, cfg)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	protected := mustAuthMiddleware(t, mgr.Authenticator(TokenTypeAccess))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "authentication required") {
		t.Fatalf("expected authentication required response, got %q", rec.Body.String())
	}
}
