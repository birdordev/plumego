package provider

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

var (
	// ErrProviderRequired is returned when registering a nil provider.
	ErrProviderRequired = errors.New("provider: provider is required")

	// ErrProviderNameRequired is returned when a provider has no registry name.
	ErrProviderNameRequired = errors.New("provider: provider name is required")
)

// Manager manages multiple providers and routes requests.
type Manager struct {
	providers map[string]Provider
	router    Router
	mu        sync.RWMutex
}

// NewManager creates a new provider manager.
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{
		providers: make(map[string]Provider),
		router:    &DefaultRouter{},
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// ManagerOption configures the manager.
type ManagerOption func(*Manager)

// WithRouter sets the routing strategy.
func WithRouter(router Router) ManagerOption {
	return func(m *Manager) {
		m.router = router
	}
}

// Register registers a provider by name. A nil provider, including a nil
// dynamic value in a non-nil interface, returns ErrProviderRequired.
func (m *Manager) Register(provider Provider) error {
	if providerIsNil(provider) {
		return ErrProviderRequired
	}
	name := provider.Name()
	if name == "" {
		return ErrProviderNameRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[name] = provider
	return nil
}

func providerIsNil(provider Provider) bool {
	if provider == nil {
		return true
	}
	value := reflect.ValueOf(provider)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// Get returns a provider by name.
func (m *Manager) Get(name string) (Provider, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	provider, ok := m.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", name)
	}

	return provider, nil
}

// Route routes a request to the appropriate provider.
func (m *Manager) Route(ctx context.Context, req *CompletionRequest) (Provider, error) {
	m.mu.RLock()
	providers := make([]Provider, 0, len(m.providers))
	for _, p := range m.providers {
		providers = append(providers, p)
	}
	m.mu.RUnlock()

	return m.router.Route(ctx, req, providers)
}

// Complete sends a completion request using routing.
func (m *Manager) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	provider, err := m.Route(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("route: %w", err)
	}

	return provider.Complete(ctx, req)
}

// CompleteStream sends a streaming completion request using routing.
func (m *Manager) CompleteStream(ctx context.Context, req *CompletionRequest) (*StreamReader, error) {
	provider, err := m.Route(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("route: %w", err)
	}

	return provider.CompleteStream(ctx, req)
}

// Router defines routing strategy.
type Router interface {
	Route(ctx context.Context, req *CompletionRequest, providers []Provider) (Provider, error)
}

// DefaultRouter implements default routing logic.
type DefaultRouter struct{}

// Route implements Router.
func (r *DefaultRouter) Route(ctx context.Context, req *CompletionRequest, providers []Provider) (Provider, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("no providers available")
	}

	// Try to match by model prefix
	for _, p := range providers {
		if matchesProvider(req.Model, p.Name()) {
			return p, nil
		}
	}

	// Use first provider as fallback
	return providers[0], nil
}

// matchesProvider checks if a model belongs to a provider.
func matchesProvider(model, providerName string) bool {
	switch providerName {
	case "claude":
		return len(model) >= 6 && model[:6] == "claude"
	case "openai":
		return len(model) >= 3 && (model[:3] == "gpt" || model[:4] == "text")
	}
	return false
}

// LoadBalancerRouter implements load balancing across providers.
type LoadBalancerRouter struct {
	index uint64
	mu    sync.Mutex
}

// Route implements Router with round-robin load balancing.
func (r *LoadBalancerRouter) Route(ctx context.Context, req *CompletionRequest, providers []Provider) (Provider, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("no providers available")
	}

	r.mu.Lock()
	idx := r.index % uint64(len(providers))
	r.index++
	r.mu.Unlock()

	return providers[idx], nil
}

// CostOptimizedRouter chooses the cheapest provider for a model.
type CostOptimizedRouter struct {
	// Model -> Provider mapping
	modelProviders map[string]string
}

// NewCostOptimizedRouter creates a cost-optimized router.
func NewCostOptimizedRouter() *CostOptimizedRouter {
	return &CostOptimizedRouter{
		modelProviders: map[string]string{
			// Map models to preferred providers based on cost
			"claude-3-haiku":  "claude",
			"claude-3-sonnet": "claude",
			"claude-3-opus":   "claude",
			"gpt-3.5-turbo":   "openai",
			"gpt-4":           "openai",
		},
	}
}

// Route implements Router.
func (r *CostOptimizedRouter) Route(ctx context.Context, req *CompletionRequest, providers []Provider) (Provider, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("no providers available")
	}

	// Check if we have a preferred provider for this model
	if preferredProvider, ok := r.modelProviders[req.Model]; ok {
		for _, p := range providers {
			if p.Name() == preferredProvider {
				return p, nil
			}
		}
	}

	// Fallback to default routing
	return (&DefaultRouter{}).Route(ctx, req, providers)
}

// TaskType represents the type of task.
type TaskType string

const (
	TaskTypeCoding        TaskType = "coding"
	TaskTypeAnalysis      TaskType = "analysis"
	TaskTypeConversation  TaskType = "conversation"
	TaskTypeSummarization TaskType = "summarization"
	TaskTypeTranslation   TaskType = "translation"
)

// TaskBasedRouter routes based on task complexity and type.
type TaskBasedRouter struct {
	// Task type -> Model preferences
	taskModels map[TaskType][]string
}

// NewTaskBasedRouter creates a task-based router.
func NewTaskBasedRouter() *TaskBasedRouter {
	return &TaskBasedRouter{
		taskModels: map[TaskType][]string{
			TaskTypeCoding:        {"claude-3-opus", "gpt-4"},
			TaskTypeAnalysis:      {"claude-3-opus", "claude-3-sonnet"},
			TaskTypeConversation:  {"claude-3-sonnet", "gpt-3.5-turbo"},
			TaskTypeSummarization: {"claude-3-haiku", "gpt-3.5-turbo"},
			TaskTypeTranslation:   {"claude-3-sonnet", "gpt-4"},
		},
	}
}

// Route implements Router.
func (r *TaskBasedRouter) Route(ctx context.Context, req *CompletionRequest, providers []Provider) (Provider, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("no providers available")
	}

	// Infer task type from request
	taskType := r.inferTaskType(req)

	// Get preferred models for this task
	preferredModels, ok := r.taskModels[taskType]
	if ok {
		// Try to find a provider that supports one of the preferred models
		for _, model := range preferredModels {
			for _, p := range providers {
				if matchesProvider(model, p.Name()) {
					// Update request model if not specified
					if req.Model == "" {
						req.Model = model
					}
					return p, nil
				}
			}
		}
	}

	// Fallback to default routing
	return (&DefaultRouter{}).Route(ctx, req, providers)
}

// inferTaskType infers the task type from the request.
func (r *TaskBasedRouter) inferTaskType(req *CompletionRequest) TaskType {
	// Simple heuristic based on prompt keywords
	var prompt string
	for _, msg := range req.Messages {
		prompt += msg.GetText() + " "
	}
	prompt = prompt[:min(len(prompt), 500)] // Check first 500 chars

	// Check for coding keywords
	codingKeywords := []string{"code", "function", "class", "implement", "debug", "fix", "algorithm"}
	for _, keyword := range codingKeywords {
		if contains(prompt, keyword) {
			return TaskTypeCoding
		}
	}

	// Check for analysis keywords
	analysisKeywords := []string{"analyze", "review", "evaluate", "assess", "critique"}
	for _, keyword := range analysisKeywords {
		if contains(prompt, keyword) {
			return TaskTypeAnalysis
		}
	}

	// Check for summarization keywords
	summarizeKeywords := []string{"summarize", "summary", "tldr", "brief"}
	for _, keyword := range summarizeKeywords {
		if contains(prompt, keyword) {
			return TaskTypeSummarization
		}
	}

	// Check for translation keywords
	translateKeywords := []string{"translate", "translation", "convert to"}
	for _, keyword := range translateKeywords {
		if contains(prompt, keyword) {
			return TaskTypeTranslation
		}
	}

	// Default to conversation
	return TaskTypeConversation
}

// FallbackRouter provides automatic failover to backup providers.
type FallbackRouter struct {
	primary  Router
	fallback Router
}

// NewFallbackRouter creates a fallback router.
func NewFallbackRouter(primary, fallback Router) *FallbackRouter {
	return &FallbackRouter{
		primary:  primary,
		fallback: fallback,
	}
}

// Route implements Router with fallback support.
func (r *FallbackRouter) Route(ctx context.Context, req *CompletionRequest, providers []Provider) (Provider, error) {
	// Try primary router
	provider, err := r.primary.Route(ctx, req, providers)
	if err == nil {
		return provider, nil
	}

	// Fallback to secondary router
	return r.fallback.Route(ctx, req, providers)
}

// SmartRouter combines multiple routing strategies.
type SmartRouter struct {
	strategies []Router
}

// NewSmartRouter creates a smart router.
func NewSmartRouter(strategies ...Router) *SmartRouter {
	if len(strategies) == 0 {
		strategies = []Router{
			NewTaskBasedRouter(),
			NewCostOptimizedRouter(),
			&DefaultRouter{},
		}
	}
	return &SmartRouter{
		strategies: strategies,
	}
}

// Route implements Router using multiple strategies.
func (r *SmartRouter) Route(ctx context.Context, req *CompletionRequest, providers []Provider) (Provider, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("no providers available")
	}

	// Try each strategy in order
	for _, strategy := range r.strategies {
		provider, err := strategy.Route(ctx, req, providers)
		if err == nil {
			return provider, nil
		}
	}

	// Should never reach here if DefaultRouter is last
	return providers[0], nil
}

// Helper functions

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	sLower := toLower(s)
	substrLower := toLower(substr)

	for i := 0; i <= len(sLower)-len(substrLower); i++ {
		if sLower[i:i+len(substrLower)] == substrLower {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}
	return string(result)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
