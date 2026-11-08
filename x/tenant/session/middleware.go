package session

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/security/authn"
)

// SessionErrorHandler handles session-related errors.
type SessionErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

type sessionOptions struct {
	errorHandler       SessionErrorHandler
	requireSessionID   bool
	now                func() time.Time
	sessionIDExtractor func(*authn.Principal) (string, bool)
	sessionLookup      SessionLookup
}

// SessionOption configures session validation behavior.
type SessionOption func(*sessionOptions)

// SessionLookup resolves a session for validation.
type SessionLookup func(ctx context.Context, r *http.Request, p *authn.Principal) (*Session, error)

// WithSessionErrorHandler overrides the default error handling.
func WithSessionErrorHandler(handler SessionErrorHandler) SessionOption {
	return func(o *sessionOptions) {
		if handler != nil {
			o.errorHandler = handler
		}
	}
}

// WithSessionRequired configures whether a missing session id is treated as an error.
func WithSessionRequired(required bool) SessionOption {
	return func(o *sessionOptions) {
		o.requireSessionID = required
	}
}

// WithSessionNow overrides the time source for session validation.
func WithSessionNow(now func() time.Time) SessionOption {
	return func(o *sessionOptions) {
		if now != nil {
			o.now = now
		}
	}
}

// WithSessionIDExtractor overrides how session ids are extracted from principals.
func WithSessionIDExtractor(extractor func(*authn.Principal) (string, bool)) SessionOption {
	return func(o *sessionOptions) {
		if extractor != nil {
			o.sessionIDExtractor = extractor
		}
	}
}

// WithSessionLookup overrides session resolution.
func WithSessionLookup(lookup SessionLookup) SessionOption {
	return func(o *sessionOptions) {
		if lookup != nil {
			o.sessionLookup = lookup
		}
	}
}

// SessionCheck validates session state (revocation, expiry, versioning).
// It reads the principal from context (set by auth.Authenticate) and validates
// the backing session via store and validator.
func SessionCheck(store SessionStore, validator SessionValidator, opts ...SessionOption) middleware.Middleware {
	cfg := applySessionOptions(opts...)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if validator == nil {
				writeSessionInternal(w, r, "session validator is nil")
				return
			}

			principal := authn.PrincipalFromContext(r.Context())
			if principal == nil {
				cfg.errorHandler(w, r, authn.ErrUnauthenticated)
				return
			}

			lookup := cfg.sessionLookup
			if lookup == nil {
				if store == nil {
					writeSessionInternal(w, r, "session store is nil")
					return
				}
				lookup = func(ctx context.Context, _ *http.Request, p *authn.Principal) (*Session, error) {
					sessionID, ok := cfg.sessionIDExtractor(p)
					if !ok || sessionID == "" {
						if cfg.requireSessionID {
							return nil, authn.ErrUnauthenticated
						}
						return nil, nil
					}
					return store.GetSession(ctx, sessionID)
				}
			}

			session, err := lookup(r.Context(), r, principal)
			if err != nil {
				cfg.errorHandler(w, r, err)
				return
			}
			if session == nil {
				if cfg.requireSessionID {
					cfg.errorHandler(w, r, authn.ErrUnauthenticated)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			if err := validator.ValidateSession(r.Context(), session, cfg.now()); err != nil {
				cfg.errorHandler(w, r, err)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func applySessionOptions(opts ...SessionOption) sessionOptions {
	cfg := sessionOptions{
		errorHandler:       defaultSessionErrorHandler(),
		requireSessionID:   true,
		now:                time.Now,
		sessionIDExtractor: sessionIDFromPrincipal,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.errorHandler == nil {
		cfg.errorHandler = defaultSessionErrorHandler()
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.sessionIDExtractor == nil {
		cfg.sessionIDExtractor = sessionIDFromPrincipal
	}
	return cfg
}

func sessionIDFromPrincipal(p *authn.Principal) (string, bool) {
	if p == nil || p.Claims == nil {
		return "", false
	}
	id, ok := p.Claims["session_id"]
	return id, ok
}

func writeSessionInternal(w http.ResponseWriter, r *http.Request, message string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeInternal).
		Message(message).
		Build())
}

func defaultSessionErrorHandler() SessionErrorHandler {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if err == nil {
			err = authn.ErrUnauthenticated
		}

		var apiErr contract.APIError
		if errors.As(err, &apiErr) {
			_ = contract.WriteError(w, r, apiErr)
			return
		}

		_ = contract.WriteError(w, r, sessionErrorToAPIError(err))
	}
}

func sessionErrorToAPIError(err error) contract.APIError {
	unauthorized := func(msg string) contract.APIError {
		return contract.NewErrorBuilder().
			Type(contract.TypeUnauthorized).
			Message(msg).
			Build()
	}
	switch {
	case errors.Is(err, ErrSessionRevoked):
		return unauthorized("session revoked")
	case errors.Is(err, ErrSessionExpired):
		return unauthorized("session expired")
	case errors.Is(err, ErrRefreshReused):
		return unauthorized("refresh token reuse detected")
	case errors.Is(err, ErrTokenVersionMismatch):
		return unauthorized("token version mismatch")
	case errors.Is(err, authn.ErrUnauthenticated):
		fallthrough
	default:
		return unauthorized("authentication required")
	}
}
