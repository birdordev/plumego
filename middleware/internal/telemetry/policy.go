package telemetry

import (
	"net/http"
	"strings"
	"time"

	"github.com/birdordev/plumego/middleware/requestid"
)

const sensitiveFieldMask = "***"

// SpanIDHeader is the canonical response header for span correlation.
const SpanIDHeader = "X-Span-ID"

func MiddlewareLogFields(r *http.Request, status int, duration time.Duration) map[string]any {
	fields := map[string]any{
		"method":     "",
		"path":       "",
		"status":     status,
		"duration":   duration.String(),
		"request_id": "",
	}
	if r == nil {
		return fields
	}
	fields["method"] = r.Method
	if r.URL != nil {
		fields["path"] = r.URL.Path
	}
	fields["request_id"] = requestid.RequestIDFromRequest(r)
	return fields
}

func RedactFields(fields map[string]any, extraSensitiveKeys ...string) map[string]any {
	if fields == nil {
		return nil
	}

	sensitiveKeys := map[string]struct{}{
		"token":     {},
		"secret":    {},
		"signature": {},
		"password":  {},
	}
	for _, key := range extraSensitiveKeys {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		sensitiveKeys[key] = struct{}{}
	}

	out := make(map[string]any, len(fields))
	for k, v := range fields {
		if isSensitiveKey(k, sensitiveKeys) {
			out[k] = sensitiveFieldMask
			continue
		}
		out[k] = redactValue(v, sensitiveKeys)
	}
	return out
}

func redactValue(v any, sensitiveKeys map[string]struct{}) any {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, nested := range value {
			if isSensitiveKey(k, sensitiveKeys) {
				out[k] = sensitiveFieldMask
				continue
			}
			out[k] = redactValue(nested, sensitiveKeys)
		}
		return out
	case []any:
		items := make([]any, len(value))
		for i, item := range value {
			items[i] = redactValue(item, sensitiveKeys)
		}
		return items
	default:
		return value
	}
}

func isSensitiveKey(key string, sensitiveKeys map[string]struct{}) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	for sensitive := range sensitiveKeys {
		if strings.Contains(normalized, sensitive) {
			return true
		}
	}
	return false
}
