package transport_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/tenant/transport"
)

func TestHeaderOrDefault(t *testing.T) {
	tests := []struct {
		value    string
		fallback string
		want     string
	}{
		{"tenant-abc", "default", "tenant-abc"},
		{"", "default", "default"},
		{"  ", "default", "  "}, // non-empty spaces are returned as-is
	}
	for _, tt := range tests {
		got := transport.HeaderOrDefault(tt.value, tt.fallback)
		if got != tt.want {
			t.Errorf("HeaderOrDefault(%q, %q) = %q, want %q", tt.value, tt.fallback, got, tt.want)
		}
	}
}

func TestSetRetryAfterHeader(t *testing.T) {
	t.Run("positive duration", func(t *testing.T) {
		w := httptest.NewRecorder()
		transport.SetRetryAfterHeader(w, 30*time.Second)
		got := w.Header().Get("Retry-After")
		if got != "30" {
			t.Errorf("Retry-After = %q, want 30", got)
		}
	})

	t.Run("zero duration skipped", func(t *testing.T) {
		w := httptest.NewRecorder()
		transport.SetRetryAfterHeader(w, 0)
		if got := w.Header().Get("Retry-After"); got != "" {
			t.Errorf("expected no Retry-After header for zero duration, got %q", got)
		}
	})

	t.Run("negative duration skipped", func(t *testing.T) {
		w := httptest.NewRecorder()
		transport.SetRetryAfterHeader(w, -5*time.Second)
		if got := w.Header().Get("Retry-After"); got != "" {
			t.Errorf("expected no Retry-After header for negative duration, got %q", got)
		}
	})

	t.Run("fractional seconds truncated", func(t *testing.T) {
		w := httptest.NewRecorder()
		transport.SetRetryAfterHeader(w, 90*time.Second+500*time.Millisecond)
		got := w.Header().Get("Retry-After")
		if got != "90" {
			t.Errorf("Retry-After = %q, want 90", got)
		}
	})
}

func TestTransportQuotaRejectionHeadersCompose(t *testing.T) {
	w := httptest.NewRecorder()
	transport.SetQuotaHeaders(w, 1, 2)
	transport.SetRetryAfterHeader(w, 45*time.Second)

	if got := w.Header().Get("X-Quota-Remaining-Requests"); got != "1" {
		t.Errorf("X-Quota-Remaining-Requests = %q, want 1", got)
	}
	if got := w.Header().Get("X-Quota-Remaining-Tokens"); got != "2" {
		t.Errorf("X-Quota-Remaining-Tokens = %q, want 2", got)
	}
	if got := w.Header().Get("Retry-After"); got != "45" {
		t.Errorf("Retry-After = %q, want 45", got)
	}
}

func TestSetRateLimitHeaders(t *testing.T) {
	t.Run("limit and remaining", func(t *testing.T) {
		w := httptest.NewRecorder()
		transport.SetRateLimitHeaders(w, 100, 42)
		if got := w.Header().Get("X-RateLimit-Limit"); got != "100" {
			t.Errorf("X-RateLimit-Limit = %q, want 100", got)
		}
		if got := w.Header().Get("X-RateLimit-Remaining"); got != "42" {
			t.Errorf("X-RateLimit-Remaining = %q, want 42", got)
		}
	})

	t.Run("zero limit omitted", func(t *testing.T) {
		w := httptest.NewRecorder()
		transport.SetRateLimitHeaders(w, 0, 5)
		if got := w.Header().Get("X-RateLimit-Limit"); got != "" {
			t.Errorf("X-RateLimit-Limit should be omitted for limit=0, got %q", got)
		}
		if got := w.Header().Get("X-RateLimit-Remaining"); got != "5" {
			t.Errorf("X-RateLimit-Remaining = %q, want 5", got)
		}
	})

	t.Run("zero remaining included", func(t *testing.T) {
		w := httptest.NewRecorder()
		transport.SetRateLimitHeaders(w, 10, 0)
		if got := w.Header().Get("X-RateLimit-Remaining"); got != "0" {
			t.Errorf("X-RateLimit-Remaining = %q, want 0", got)
		}
	})
}

func TestSetQuotaHeaders(t *testing.T) {
	t.Run("both values set", func(t *testing.T) {
		w := httptest.NewRecorder()
		transport.SetQuotaHeaders(w, 50, 1000)
		if got := w.Header().Get("X-Quota-Remaining-Requests"); got != "50" {
			t.Errorf("X-Quota-Remaining-Requests = %q, want 50", got)
		}
		if got := w.Header().Get("X-Quota-Remaining-Tokens"); got != "1000" {
			t.Errorf("X-Quota-Remaining-Tokens = %q, want 1000", got)
		}
	})

	t.Run("negative values omitted (unlimited)", func(t *testing.T) {
		w := httptest.NewRecorder()
		transport.SetQuotaHeaders(w, -1, -1)
		if got := w.Header().Get("X-Quota-Remaining-Requests"); got != "" {
			t.Errorf("X-Quota-Remaining-Requests should be omitted for -1, got %q", got)
		}
		if got := w.Header().Get("X-Quota-Remaining-Tokens"); got != "" {
			t.Errorf("X-Quota-Remaining-Tokens should be omitted for -1, got %q", got)
		}
	})

	t.Run("zero values included", func(t *testing.T) {
		w := httptest.NewRecorder()
		transport.SetQuotaHeaders(w, 0, 0)
		if got := w.Header().Get("X-Quota-Remaining-Requests"); got != "0" {
			t.Errorf("X-Quota-Remaining-Requests = %q, want 0", got)
		}
		if got := w.Header().Get("X-Quota-Remaining-Tokens"); got != "0" {
			t.Errorf("X-Quota-Remaining-Tokens = %q, want 0", got)
		}
	})

	t.Run("partial: only requests set", func(t *testing.T) {
		w := httptest.NewRecorder()
		transport.SetQuotaHeaders(w, 10, -1)
		if got := w.Header().Get("X-Quota-Remaining-Requests"); got != "10" {
			t.Errorf("X-Quota-Remaining-Requests = %q, want 10", got)
		}
		if got := w.Header().Get("X-Quota-Remaining-Tokens"); got != "" {
			t.Errorf("X-Quota-Remaining-Tokens should be omitted, got %q", got)
		}
	})
}

func TestErrorCodes_AreNonEmpty(t *testing.T) {
	codes := []string{
		transport.CodeRequired,
		transport.CodeInvalidID,
		transport.CodeRateLimited,
		transport.CodePolicyDenied,
		transport.CodeQuotaExceeded,
	}
	for _, c := range codes {
		if c == "" {
			t.Errorf("expected non-empty error code, got empty string")
		}
	}
}
