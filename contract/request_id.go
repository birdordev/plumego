package contract

import (
	"context"
	"strings"
	"unicode/utf8"
)

type requestIDContextKey struct{}

const (
	// RequestIDHeader is the canonical request id header.
	RequestIDHeader = "X-Request-ID"
)

// maxRequestIDLength is the documented stable acceptance cap, in bytes after
// trimming surrounding whitespace.
const maxRequestIDLength = 128

// WithRequestID stores the canonical request correlation id in ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	id, ok := normalizeRequestID(id)
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, requestIDContextKey{}, id)
}

// RequestIDFromContext returns the canonical request correlation id from ctx.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(requestIDContextKey{}).(string); ok {
		return strings.TrimSpace(id)
	}
	return ""
}

func normalizeRequestID(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > maxRequestIDLength || !utf8.ValidString(id) || containsControlChar(id) {
		return "", false
	}
	return id, true
}

func containsControlChar(id string) bool {
	for _, ch := range id {
		if ch < 0x20 || ch == 0x7f {
			return true
		}
	}
	return false
}
