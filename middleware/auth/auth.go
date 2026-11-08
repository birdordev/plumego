package auth

import (
	"errors"
	"net/http"
	"strings"
	"unicode"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/security/authn"
)

// AuthErrorHandler handles authentication/authorization errors.
type AuthErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

var (
	// ErrNilAuthenticator is returned when authentication is configured without an authenticator.
	ErrNilAuthenticator = errors.New("auth: authenticator cannot be nil")
	// ErrNilAuthorizer is returned when authorization is configured without an authorizer.
	ErrNilAuthorizer = errors.New("auth: authorizer cannot be nil")
	// ErrNilAuthorizeResolver is returned when authorization is configured without an action/resource resolver.
	ErrNilAuthorizeResolver = errors.New("auth: authorize resolver cannot be nil")
)

type authOptions struct {
	errorHandler AuthErrorHandler
	realm        string
}

// AuthOption configures authentication middleware behavior.
type AuthOption func(*authOptions)

// WithAuthErrorHandler overrides the default error handling.
func WithAuthErrorHandler(handler AuthErrorHandler) AuthOption {
	return func(o *authOptions) {
		if handler != nil {
			o.errorHandler = handler
		}
	}
}

// WithAuthRealm sets the WWW-Authenticate realm header value for 401 responses.
func WithAuthRealm(realm string) AuthOption {
	return func(o *authOptions) {
		o.realm = realm
	}
}

// Authenticate validates the request and stores the principal in context.
func Authenticate(authenticator authn.Authenticator, opts ...AuthOption) (middleware.Middleware, error) {
	if authenticator == nil {
		return nil, ErrNilAuthenticator
	}
	cfg := applyAuthOptions(opts...)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := r
			var (
				principal *authn.Principal
				err       error
			)
			if enriched, ok := authenticator.(authn.RequestAuthenticator); ok {
				principal, req, err = enriched.AuthenticateRequest(r)
				if req == nil {
					req = r
				}
			} else {
				principal, err = authenticator.Authenticate(r)
			}
			if err != nil {
				cfg.errorHandler(w, r, err)
				return
			}
			if principal == nil {
				cfg.errorHandler(w, r, authn.ErrUnauthenticated)
				return
			}

			ctx := authn.WithPrincipal(req.Context(), principal)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}, nil
}

// Authorize enforces access for the provided action/resource pair.
func Authorize(authorizer authn.Authorizer, action, resource string, opts ...AuthOption) (middleware.Middleware, error) {
	return AuthorizeFunc(authorizer, func(*http.Request) (string, string) {
		return action, resource
	}, opts...)
}

// AuthorizeFunc enforces access using a resolver for action/resource values.
func AuthorizeFunc(authorizer authn.Authorizer, resolver func(*http.Request) (string, string), opts ...AuthOption) (middleware.Middleware, error) {
	if authorizer == nil {
		return nil, ErrNilAuthorizer
	}
	if resolver == nil {
		return nil, ErrNilAuthorizeResolver
	}
	cfg := applyAuthOptions(opts...)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := authn.PrincipalFromContext(r.Context())
			if principal == nil {
				cfg.errorHandler(w, r, authn.ErrUnauthenticated)
				return
			}

			action, resource := resolver(r)
			if err := authorizer.Authorize(principal, action, resource); err != nil {
				cfg.errorHandler(w, r, err)
				return
			}

			next.ServeHTTP(w, r)
		})
	}, nil
}

func applyAuthOptions(opts ...AuthOption) authOptions {
	cfg := authOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.errorHandler == nil {
		cfg.errorHandler = defaultAuthErrorHandler(cfg.realm)
	}
	return cfg
}

func defaultAuthErrorHandler(realm string) AuthErrorHandler {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if err == nil {
			err = authn.ErrUnauthenticated
		}

		var apiErr contract.APIError
		if errors.As(err, &apiErr) {
			_ = contract.WriteError(w, r, apiErr)
			return
		}

		apiErr = authErrorToAPIError(err)
		if apiErr.Status() == http.StatusUnauthorized && realm != "" {
			if challenge := bearerChallenge(realm); challenge != "" {
				w.Header().Set("WWW-Authenticate", challenge)
			}
		}
		_ = contract.WriteError(w, r, apiErr)
	}
}

func bearerChallenge(realm string) string {
	realm = sanitizeAuthRealm(realm)
	if realm == "" {
		return ""
	}
	return "Bearer realm=" + quoteAuthParam(realm)
}

func sanitizeAuthRealm(realm string) string {
	var b strings.Builder
	for _, r := range realm {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func quoteAuthParam(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

func authErrorToAPIError(err error) contract.APIError {
	unauthorized := func(msg string) contract.APIError {
		return contract.NewErrorBuilder().
			Type(contract.TypeUnauthorized).
			Message(msg).
			Build()
	}
	switch {
	case errors.Is(err, authn.ErrUnauthorized):
		return contract.NewErrorBuilder().
			Type(contract.TypeForbidden).
			Message("access forbidden").
			Build()
	case errors.Is(err, authn.ErrInvalidToken):
		return unauthorized("invalid token")
	case errors.Is(err, authn.ErrExpiredToken):
		return unauthorized("token expired")
	case errors.Is(err, authn.ErrUnauthenticated):
		fallthrough
	default:
		return unauthorized("authentication required")
	}
}
