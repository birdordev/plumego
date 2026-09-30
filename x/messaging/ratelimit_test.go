package messaging

import (
	"context"
	"testing"
	"time"
)

func TestRateLimiter_AllowsBurst(t *testing.T) {
	rl := NewRateLimiter(5, time.Second)
	ctx := t.Context()

	for i := 0; i < 5; i++ {
		if err := rl.Wait(ctx); err != nil {
			t.Fatalf("token %d: %v", i, err)
		}
	}
}

func TestRateLimiter_ContextCancel(t *testing.T) {
	rl := NewRateLimiter(1, time.Second)
	ctx := t.Context()
	// Drain the bucket.
	_ = rl.Wait(ctx)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	err := rl.Wait(ctx)
	if err == nil {
		t.Fatal("expected context error when bucket empty")
	}
}

func TestRateLimitedSMSProvider(t *testing.T) {
	inner := &mockSMS{}
	provider := NewRateLimitedSMSProvider(inner, 100)

	ctx := t.Context()
	result, err := provider.Send(ctx, SMSMessage{To: "+1234567890", Body: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderID == "" {
		t.Fatal("expected provider ID")
	}
	if provider.Name() != "mock-sms" {
		t.Fatalf("name=%s, want mock-sms", provider.Name())
	}
}

func TestRateLimitedEmailProvider(t *testing.T) {
	inner := &mockEmail{}
	provider := NewRateLimitedEmailProvider(inner, 100)

	ctx := t.Context()
	result, err := provider.Send(ctx, EmailMessage{To: "a@b.com", Subject: "s", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID == "" {
		t.Fatal("expected message ID")
	}
	if provider.Name() != "mock-email" {
		t.Fatalf("name=%s, want mock-email", provider.Name())
	}
}
