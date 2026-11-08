// Package tool provides function calling framework for AI agents.
//
// Stability: stable — this package follows semantic versioning guarantees.
// Breaking changes require a major version bump.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/birdordev/plumego/x/ai/provider"
)

// Tool represents a callable function.
type Tool interface {
	// Name returns the tool name.
	Name() string

	// Description returns the tool description.
	Description() string

	// InputSchema returns the JSON schema for inputs.
	InputSchema() map[string]any

	// Execute executes the tool with given input.
	Execute(ctx context.Context, input map[string]any) (*Result, error)
}

// Result represents a tool execution result.
type Result struct {
	// Output data
	Output any

	// Error if execution failed
	Error error

	// Execution metrics
	Metrics Metrics

	// Whether the result is an error
	IsError bool
}

// Metrics tracks tool execution metrics.
type Metrics struct {
	// Execution duration
	Duration time.Duration

	// Retry count
	RetryCount int

	// Timestamp
	Timestamp time.Time
}

// Registry manages available tools.
type Registry struct {
	tools  map[string]Tool
	policy Policy
	mu     sync.RWMutex
}

// NewRegistry creates a new tool registry.
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{
		tools:  make(map[string]Tool),
		policy: &AllowAllPolicy{},
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// RegistryOption configures the registry.
type RegistryOption func(*Registry)

// WithPolicy sets the access policy.
func WithPolicy(policy Policy) RegistryOption {
	return func(r *Registry) {
		r.policy = policy
	}
}

// Register registers a tool.
func (r *Registry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := tool.Name()
	if name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}

	r.tools[name] = tool
	return nil
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}

	return tool, nil
}

// List returns all registered tools.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}

	return tools
}

// ListForContext returns tools allowed for a context.
func (r *Registry) ListForContext(ctx context.Context) []Tool {
	allTools := r.List()
	allowed := make([]Tool, 0, len(allTools))

	for _, tool := range allTools {
		if r.policy.CanExecute(ctx, tool.Name()) {
			allowed = append(allowed, tool)
		}
	}

	return allowed
}

// Execute executes a tool.
func (r *Registry) Execute(ctx context.Context, name string, input map[string]any) (*Result, error) {
	// Check policy
	if !r.policy.CanExecute(ctx, name) {
		return nil, fmt.Errorf("tool execution not allowed: %s", name)
	}

	// Get tool
	tool, err := r.Get(name)
	if err != nil {
		return nil, err
	}

	// Execute
	start := time.Now()
	result, err := tool.Execute(ctx, input)
	duration := time.Since(start)

	if err != nil {
		return &Result{
			Error:   err,
			IsError: true,
			Metrics: Metrics{
				Duration:  duration,
				Timestamp: time.Now(),
			},
		}, err
	}

	if result == nil {
		result = &Result{}
	}

	result.Metrics.Duration = duration
	result.Metrics.Timestamp = time.Now()

	return result, nil
}

// ExecuteToolUse executes a tool use from provider.
func (r *Registry) ExecuteToolUse(ctx context.Context, toolUse *provider.ToolUse) (*Result, error) {
	return r.Execute(ctx, toolUse.Name, toolUse.Input)
}

// ToProviderTools converts tools to provider format.
func (r *Registry) ToProviderTools(ctx context.Context) []provider.Tool {
	tools := r.ListForContext(ctx)
	providerTools := make([]provider.Tool, 0, len(tools))

	for _, tool := range tools {
		providerTools = append(providerTools, provider.Tool{
			Type: "function",
			Function: provider.FunctionDef{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.InputSchema(),
			},
		})
	}

	return providerTools
}

// Policy defines tool access control.
type Policy interface {
	// CanExecute checks if a tool can be executed.
	CanExecute(ctx context.Context, toolName string) bool

	// GetAllowedTools returns allowed tool names.
	GetAllowedTools(ctx context.Context) []string
}

// AllowAllPolicy allows all tools.
type AllowAllPolicy struct{}

// CanExecute implements Policy.
func (p *AllowAllPolicy) CanExecute(ctx context.Context, toolName string) bool {
	return true
}

// GetAllowedTools implements Policy.
func (p *AllowAllPolicy) GetAllowedTools(ctx context.Context) []string {
	return nil // All tools allowed
}

// AllowListPolicy allows only specified tools.
type AllowListPolicy struct {
	allowedTools map[string]bool
	mu           sync.RWMutex
}

// NewAllowListPolicy creates a new allow list policy.
func NewAllowListPolicy(tools []string) *AllowListPolicy {
	p := &AllowListPolicy{
		allowedTools: make(map[string]bool),
	}

	for _, tool := range tools {
		p.allowedTools[tool] = true
	}

	return p
}

// CanExecute implements Policy.
func (p *AllowListPolicy) CanExecute(ctx context.Context, toolName string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.allowedTools[toolName]
}

// GetAllowedTools implements Policy.
func (p *AllowListPolicy) GetAllowedTools(ctx context.Context) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tools := make([]string, 0, len(p.allowedTools))
	for tool := range p.allowedTools {
		tools = append(tools, tool)
	}

	return tools
}

// Add adds a tool to the allow list.
func (p *AllowListPolicy) Add(toolName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.allowedTools[toolName] = true
}

// Remove removes a tool from the allow list.
func (p *AllowListPolicy) Remove(toolName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.allowedTools, toolName)
}

// FuncTool wraps a function as a Tool.
type FuncTool struct {
	name        string
	description string
	inputSchema map[string]any
	fn          func(context.Context, map[string]any) (any, error)
}

// NewFuncTool creates a new function tool.
func NewFuncTool(name, description string, inputSchema map[string]any, fn func(context.Context, map[string]any) (any, error)) *FuncTool {
	return &FuncTool{
		name:        name,
		description: description,
		inputSchema: inputSchema,
		fn:          fn,
	}
}

// Name implements Tool.
func (t *FuncTool) Name() string {
	return t.name
}

// Description implements Tool.
func (t *FuncTool) Description() string {
	return t.description
}

// InputSchema implements Tool.
func (t *FuncTool) InputSchema() map[string]any {
	return t.inputSchema
}

// Execute implements Tool.
func (t *FuncTool) Execute(ctx context.Context, input map[string]any) (*Result, error) {
	output, err := t.fn(ctx, input)
	if err != nil {
		return &Result{
			Error:   err,
			IsError: true,
		}, err
	}

	return &Result{
		Output: output,
	}, nil
}

// FormatResultAsString formats a tool result as string for provider.
func FormatResultAsString(result *Result) string {
	if result.IsError {
		return fmt.Sprintf("Error: %v", result.Error)
	}

	// Try to format output as JSON
	if result.Output != nil {
		if data, err := json.Marshal(result.Output); err == nil {
			return string(data)
		}
		return fmt.Sprintf("%v", result.Output)
	}

	return "Success"
}
