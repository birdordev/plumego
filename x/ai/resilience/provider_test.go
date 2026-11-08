package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
	sharedcircuitbreaker "github.com/birdordev/plumego/x/resilience/circuitbreaker"
	sharedratelimit "github.com/birdordev/plumego/x/resilience/ratelimit"
)

// Mock provider for testing
type mockProvider struct {
	name      string
	err       error
	callCount int
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	return &provider.CompletionResponse{
		ID:    "test-id",
		Model: req.Model,
		Content: []provider.ContentBlock{
			{Type: provider.ContentTypeText, Text: "test response"},
		},
		Usage: tokenizer.TokenUsage{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
		},
	}, nil
}

func (m *mockProvider) CompleteStream(ctx context.Context, req *provider.CompletionRequest) (*provider.StreamReader, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	return nil, nil
}

func (m *mockProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "test-model", Name: "Test Model", Provider: m.name}}, nil
}

func (m *mockProvider) GetModel(ctx context.Context, modelID string) (*provider.Model, error) {
	return &provider.Model{ID: modelID, Name: "Test Model", Provider: m.name}, nil
}

func (m *mockProvider) CountTokens(text string) (int, error) {
	return len(text) / 4, nil
}

func TestResilientProvider_RateLimit(t *testing.T) {
	mockProv := &mockProvider{name: "test-provider"}
	limiter := sharedratelimit.NewKeyed(0, 2) // 2 requests, no refill
	cb := sharedcircuitbreaker.New(sharedcircuitbreaker.Config{
		Name:             "test",
		FailureThreshold: 1.0,
		MinRequests:      5,
		Timeout:          time.Second,
	})

	resilient, err := NewResilientProvider(Config{
		Provider:       mockProv,
		RateLimiter:    limiter,
		CircuitBreaker: cb,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	// First 2 requests should succeed
	for i := 0; i < 2; i++ {
		_, err := resilient.Complete(ctx, req)
		if err != nil {
			t.Errorf("Request %d should succeed, got error: %v", i, err)
		}
	}

	// 3rd request should be rate limited
	_, err = resilient.Complete(ctx, req)
	if !errors.Is(err, ErrRateLimitExceeded) {
		t.Errorf("Error = %v, want ErrRateLimitExceeded", err)
	}

	// Provider should have been called only 2 times
	if mockProv.callCount != 2 {
		t.Errorf("Provider call count = %d, want 2", mockProv.callCount)
	}
}

func TestNewResilientProviderRejectsNilProvider(t *testing.T) {
	resilient, err := NewResilientProvider(Config{})
	if !errors.Is(err, ErrNilProvider) {
		t.Fatalf("NewResilientProvider error = %v, want ErrNilProvider", err)
	}
	if resilient != nil {
		t.Fatalf("NewResilientProvider returned provider = %#v, want nil", resilient)
	}
}

func TestResilientProviderRejectsNilCompletionRequest(t *testing.T) {
	resilient, err := NewResilientProvider(Config{
		Provider: &mockProvider{name: "test-provider"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := resilient.Complete(t.Context(), nil); !errors.Is(err, ErrNilRequest) {
		t.Fatalf("Complete nil request error = %v, want ErrNilRequest", err)
	}
	if _, err := resilient.CompleteStream(t.Context(), nil); !errors.Is(err, ErrNilRequest) {
		t.Fatalf("CompleteStream nil request error = %v, want ErrNilRequest", err)
	}
}

func TestResilientProvider_CircuitBreaker(t *testing.T) {
	mockProv := &mockProvider{
		name: "test-provider",
		err:  errors.New("provider error"),
	}
	cb := sharedcircuitbreaker.New(sharedcircuitbreaker.Config{
		Name:             "test",
		FailureThreshold: 1.0,
		MinRequests:      2,
		Timeout:          time.Second,
	})

	resilient, err := NewResilientProvider(Config{
		Provider:       mockProv,
		CircuitBreaker: cb,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	// First 2 requests trigger circuit breaker
	for i := 0; i < 2; i++ {
		_, err := resilient.Complete(ctx, req)
		if err == nil {
			t.Error("Should return error")
		}
	}

	// Circuit should be open
	if resilient.CircuitBreakerState() != sharedcircuitbreaker.StateOpen {
		t.Errorf("Circuit state = %v, want StateOpen", resilient.CircuitBreakerState())
	}

	// Next request should fail fast
	_, err = resilient.Complete(ctx, req)
	if !errors.Is(err, sharedcircuitbreaker.ErrCircuitOpen) {
		t.Errorf("Error = %v, want ErrCircuitOpen", err)
	}

	// Provider should have been called only 2 times
	if mockProv.callCount != 2 {
		t.Errorf("Provider call count = %d, want 2", mockProv.callCount)
	}
}

func TestResilientProvider_Combined(t *testing.T) {
	mockProv := &mockProvider{name: "test-provider"}
	limiter := sharedratelimit.NewKeyed(1, 5)
	cb := sharedcircuitbreaker.New(sharedcircuitbreaker.Config{
		Name:             "test",
		FailureThreshold: 1.0,
		MinRequests:      3,
		Timeout:          time.Second,
	})

	resilient, err := NewResilientProvider(Config{
		Provider:       mockProv,
		RateLimiter:    limiter,
		CircuitBreaker: cb,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	// Multiple successful requests
	for i := 0; i < 3; i++ {
		_, err := resilient.Complete(ctx, req)
		if err != nil {
			t.Errorf("Request %d should succeed, got error: %v", i, err)
		}
	}

	// Check stats
	stats := resilient.CircuitBreakerStats()
	if stats.State != sharedcircuitbreaker.StateClosed {
		t.Errorf("Circuit state = %v, want StateClosed", stats.State)
	}

	// Check rate limit remaining
	remaining, err := resilient.RateLimitRemaining(ctx, "test-model")
	if err != nil {
		t.Errorf("RateLimitRemaining() error = %v", err)
	}
	if remaining < 0 {
		t.Errorf("Remaining = %d, should be >= 0", remaining)
	}
}

func TestResilientProvider_NoRateLimiter(t *testing.T) {
	mockProv := &mockProvider{name: "test-provider"}
	cb := sharedcircuitbreaker.New(sharedcircuitbreaker.Config{
		Name:             "test",
		FailureThreshold: 1.0,
		MinRequests:      5,
		Timeout:          time.Second,
	})

	resilient, err := NewResilientProvider(Config{
		Provider:       mockProv,
		CircuitBreaker: cb,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	// Should allow many requests without rate limiting
	for i := 0; i < 10; i++ {
		_, err := resilient.Complete(ctx, req)
		if err != nil {
			t.Errorf("Request %d should succeed, got error: %v", i, err)
		}
	}
}

func TestResilientProvider_NoCircuitBreaker(t *testing.T) {
	mockProv := &mockProvider{name: "test-provider"}
	limiter := sharedratelimit.NewKeyed(0, 5)

	resilient, err := NewResilientProvider(Config{
		Provider:    mockProv,
		RateLimiter: limiter,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	// Rate limiting still applies
	for i := 0; i < 5; i++ {
		_, err := resilient.Complete(ctx, req)
		if err != nil {
			t.Errorf("Request %d should succeed, got error: %v", i, err)
		}
	}

	_, err = resilient.Complete(ctx, req)
	if !errors.Is(err, ErrRateLimitExceeded) {
		t.Errorf("Error = %v, want ErrRateLimitExceeded", err)
	}
}

func TestResilientProvider_CompleteStream(t *testing.T) {
	mockProv := &mockProvider{name: "test-provider"}
	limiter := sharedratelimit.NewKeyed(0, 2)
	cb := sharedcircuitbreaker.New(sharedcircuitbreaker.Config{
		Name:             "test",
		FailureThreshold: 1.0,
		MinRequests:      5,
		Timeout:          time.Second,
	})

	resilient, err := NewResilientProvider(Config{
		Provider:       mockProv,
		RateLimiter:    limiter,
		CircuitBreaker: cb,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	// First 2 stream requests should succeed
	for i := 0; i < 2; i++ {
		_, err := resilient.CompleteStream(ctx, req)
		if err != nil {
			t.Errorf("Stream request %d should succeed, got error: %v", i, err)
		}
	}

	// 3rd should be rate limited
	_, err = resilient.CompleteStream(ctx, req)
	if !errors.Is(err, ErrRateLimitExceeded) {
		t.Errorf("Error = %v, want ErrRateLimitExceeded", err)
	}
}

func TestResilientProvider_ListModels(t *testing.T) {
	mockProv := &mockProvider{name: "test-provider"}
	cb := sharedcircuitbreaker.New(sharedcircuitbreaker.Config{
		Name:             "test",
		FailureThreshold: 1.0,
		MinRequests:      5,
		Timeout:          time.Second,
	})

	resilient, err := NewResilientProvider(Config{
		Provider:       mockProv,
		CircuitBreaker: cb,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	models, err := resilient.ListModels(ctx)

	if err != nil {
		t.Errorf("ListModels() error = %v", err)
	}

	if len(models) != 1 {
		t.Errorf("Models count = %d, want 1", len(models))
	}
}

func TestResilientProvider_CountTokens(t *testing.T) {
	mockProv := &mockProvider{name: "test-provider"}
	limiter := sharedratelimit.NewKeyed(0, 1)
	cb := sharedcircuitbreaker.New(sharedcircuitbreaker.Config{
		Name:             "test",
		FailureThreshold: 1.0,
		MinRequests:      1,
		Timeout:          time.Second,
	})

	resilient, err := NewResilientProvider(Config{
		Provider:       mockProv,
		RateLimiter:    limiter,
		CircuitBreaker: cb,
	})
	if err != nil {
		t.Fatal(err)
	}

	// CountTokens should not be rate limited or circuit broken
	count, err := resilient.CountTokens("test text")
	if err != nil {
		t.Errorf("CountTokens() error = %v", err)
	}

	if count <= 0 {
		t.Errorf("Count = %d, should be > 0", count)
	}
}

func TestNewResilientProvider_UsesSharedRateLimiterAsCanonicalInput(t *testing.T) {
	mockProv := &mockProvider{name: "test-provider"}
	limiter := sharedratelimit.NewKeyed(0, 2)

	resilient, err := NewResilientProvider(Config{
		Provider:    mockProv,
		RateLimiter: limiter,
	})
	if err != nil {
		t.Fatalf("NewResilientProvider() error = %v", err)
	}

	ctx := t.Context()
	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	for i := 0; i < 2; i++ {
		if _, err := resilient.Complete(ctx, req); err != nil {
			t.Fatalf("request %d should succeed, got %v", i, err)
		}
	}

	if _, err := resilient.Complete(ctx, req); !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("third request error = %v, want ErrRateLimitExceeded", err)
	}

	remaining, err := resilient.RateLimitRemaining(ctx, "test-model")
	if err != nil {
		t.Fatalf("RateLimitRemaining() error = %v", err)
	}
	if remaining != 0 {
		t.Fatalf("RateLimitRemaining() = %d, want 0", remaining)
	}
}

func TestNewResilientProvider_UsesSharedCircuitBreakerAsCanonicalInput(t *testing.T) {
	mockProv := &mockProvider{
		name: "test-provider",
		err:  errors.New("provider error"),
	}
	breaker := sharedcircuitbreaker.New(sharedcircuitbreaker.Config{
		Name:             "shared-breaker",
		FailureThreshold: 0.5,
		MinRequests:      2,
		Timeout:          time.Second,
	})

	resilient, err := NewResilientProvider(Config{
		Provider:       mockProv,
		CircuitBreaker: breaker,
	})
	if err != nil {
		t.Fatalf("NewResilientProvider() error = %v", err)
	}

	ctx := t.Context()
	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	for i := 0; i < 2; i++ {
		if _, err := resilient.Complete(ctx, req); err == nil {
			t.Fatalf("request %d should fail", i)
		}
	}

	if resilient.CircuitBreakerState() != sharedcircuitbreaker.StateOpen {
		t.Fatalf("CircuitBreakerState() = %v, want StateOpen", resilient.CircuitBreakerState())
	}

	if _, err := resilient.Complete(ctx, req); !errors.Is(err, sharedcircuitbreaker.ErrCircuitOpen) {
		t.Fatalf("third request error = %v, want ErrCircuitOpen", err)
	}

	stats := resilient.CircuitBreakerStats()
	if stats.Name != "shared-breaker" {
		t.Fatalf("CircuitBreakerStats().Name = %q, want shared-breaker", stats.Name)
	}
	if stats.State != sharedcircuitbreaker.StateOpen {
		t.Fatalf("CircuitBreakerStats().State = %v, want StateOpen", stats.State)
	}
}
