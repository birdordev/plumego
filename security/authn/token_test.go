package authn

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticToken(t *testing.T) {
	tests := []struct {
		name          string
		configured    string
		authorization string
		xToken        string
		wantErr       error
	}{
		{
			name:       "valid x-token header",
			configured: "secret123",
			xToken:     "secret123",
		},
		{
			name:          "valid bearer token",
			configured:    "secret123",
			authorization: "Bearer secret123",
		},
		{
			name:    "missing configured token fails closed",
			wantErr: ErrUnauthenticated,
		},
		{
			name:       "missing credentials",
			configured: "secret123",
			wantErr:    ErrUnauthenticated,
		},
		{
			name:          "malformed authorization header",
			configured:    "secret123",
			authorization: "Bearer",
			wantErr:       ErrUnauthenticated,
		},
		{
			name:          "bearer prefix without delimiter rejected",
			configured:    "X secret123",
			authorization: "BearerX secret123",
			wantErr:       ErrUnauthenticated,
		},
		{
			name:          "bearer token with embedded whitespace rejected",
			configured:    "secret123",
			authorization: "Bearer secret123 extra",
			wantErr:       ErrUnauthenticated,
		},
		{
			name:       "query token ignored",
			configured: "secret123",
			wantErr:    ErrUnauthenticated,
		},
		{
			name:       "invalid token",
			configured: "secret123",
			xToken:     "wrong-token",
			wantErr:    ErrUnauthenticated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test?token=secret123", nil)
			if tt.authorization != "" {
				req.Header.Set(HeaderAuthorization, tt.authorization)
			}
			if tt.xToken != "" {
				req.Header.Set(HeaderXToken, tt.xToken)
			}

			principal, err := StaticToken(tt.configured).Authenticate(req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
			if tt.wantErr != nil {
				if principal != nil {
					t.Fatal("expected nil principal on auth failure")
				}
				if strings.Contains(err.Error(), "secret123") || strings.Contains(err.Error(), "wrong-token") {
					t.Fatal("failure diagnostics included credential input")
				}
				return
			}
			if principal == nil || principal.Subject != staticTokenSubject {
				t.Fatalf("expected static-token principal, got %#v", principal)
			}
		})
	}
}

func TestStaticTokenNilRequestFailsClosed(t *testing.T) {
	principal, err := StaticToken("secret123").Authenticate(nil)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
	if principal != nil {
		t.Fatalf("expected nil principal, got %#v", principal)
	}
}

func TestExtractBearerTokenStrictScheme(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{name: "single space", header: "Bearer token123", want: "token123"},
		{name: "multiple spaces", header: "Bearer   token123", want: "token123"},
		{name: "tab delimiter", header: "Bearer\ttoken123", want: "token123"},
		{name: "case insensitive scheme", header: "BEARER token123", want: "token123"},
		{name: "missing delimiter", header: "Bearertoken123", want: ""},
		{name: "wrong scheme prefix", header: "BearerX token123", want: ""},
		{name: "empty token", header: "Bearer   ", want: ""},
		{name: "embedded whitespace", header: "Bearer token 123", want: ""},
		{name: "non bearer", header: "Basic token123", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set(HeaderAuthorization, tt.header)
			if got := ExtractBearerToken(req); got != tt.want {
				t.Fatalf("ExtractBearerToken() = %q, want %q", got, tt.want)
			}
		})
	}
}
