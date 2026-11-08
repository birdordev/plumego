package jwt

import (
	"testing"

	"github.com/birdordev/plumego/middleware"
	authmw "github.com/birdordev/plumego/middleware/auth"
	"github.com/birdordev/plumego/security/authn"
)

func mustAuthMiddleware(t *testing.T, authenticator authn.Authenticator, opts ...authmw.AuthOption) middleware.Middleware {
	t.Helper()
	mw, err := authmw.Authenticate(authenticator, opts...)
	if err != nil {
		t.Fatalf("auth middleware: %v", err)
	}
	return mw
}

func mustAuthorizeMiddleware(t *testing.T, authorizer authn.Authorizer, action, resource string, opts ...authmw.AuthOption) middleware.Middleware {
	t.Helper()
	mw, err := authmw.Authorize(authorizer, action, resource, opts...)
	if err != nil {
		t.Fatalf("authorize middleware: %v", err)
	}
	return mw
}
