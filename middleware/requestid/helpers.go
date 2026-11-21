package requestid

import (
	"context"
	"net/http"

	"github.com/birdordev/plumego/contract"
)

// RequestIDFromRequest extracts the canonical request id from context or headers.
func RequestIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if id := contract.RequestIDFromContext(r.Context()); id != "" {
		return id
	}
	return acceptedRequestID(r.Header.Get(contract.RequestIDHeader))
}

// AttachRequestID stores an accepted request id in context and headers.
// If requested, it writes the header on a copy of the incoming header map.
func AttachRequestID(w http.ResponseWriter, r *http.Request, id string, includeInRequest bool) *http.Request {
	if r == nil {
		return r
	}
	id = acceptedRequestID(id)
	if id == "" {
		return r
	}
	ctx := contract.WithRequestID(r.Context(), id)
	r = r.WithContext(ctx)
	if includeInRequest {
		r.Header = r.Header.Clone()
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		r.Header.Set(contract.RequestIDHeader, id)
	}
	if w != nil {
		w.Header().Set(contract.RequestIDHeader, id)
	}
	return r
}

// EnsureRequestID returns an existing request id or generates one when missing.
func EnsureRequestID(r *http.Request, generate func() string) string {
	if id := RequestIDFromRequest(r); id != "" {
		return id
	}
	if generate != nil {
		if id := acceptedRequestID(generate()); id != "" {
			return id
		}
	}
	return NewRequestID()
}

func acceptedRequestID(id string) string {
	// Use an empty context so rejection cannot fall back to a previously attached ID.
	return contract.RequestIDFromContext(contract.WithRequestID(context.Background(), id))
}
