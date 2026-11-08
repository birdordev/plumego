// Package orchestration provides agent workflow orchestration.
//
// Stability: experimental — API may change between minor releases without a deprecation period.
// Step types and Engine configuration are still evolving.
package orchestration

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
)

// Agent represents a single AI agent with a specific role.
type Agent struct {
	ID           string
	Name         string
	Description  string
	Provider     provider.Provider
	Model        string
	SystemPrompt string
	Temperature  float64
	MaxTokens    int
	Tools        []provider.Tool
}

// AgentResult represents the result of an agent execution.
type AgentResult struct {
	AgentID    string
	Input      string
	Output     string
	Error      error
	StartTime  time.Time
	EndTime    time.Time
	Duration   time.Duration
	TokenUsage tokenizer.TokenUsage
}

// Workflow represents an orchestrated agent workflow.
type Workflow struct {
	ID          string
	Name        string
	Description string
	Steps       []Step
	State       map[string]any
	mu          sync.RWMutex
}

// Step represents a single step in the workflow.
type Step interface {
	Execute(ctx context.Context, wf *Workflow) (*AgentResult, error)
	Name() string
}

// SequentialStep executes an agent and waits for completion.
type SequentialStep struct {
	StepName  string
	Agent     *Agent
	InputFn   func(state map[string]any) string
	OutputKey string
}

// Execute implements Step.
func (s *SequentialStep) Execute(ctx context.Context, wf *Workflow) (*AgentResult, error) {
	wf.mu.RLock()
	input := s.InputFn(wf.State)
	wf.mu.RUnlock()

	result, err := executeAgent(ctx, s.Agent, input)
	if err != nil {
		return result, err
	}

	// Store output in workflow state
	wf.mu.Lock()
	wf.State[s.OutputKey] = result.Output
	wf.mu.Unlock()

	return result, nil
}

// Name implements Step.
func (s *SequentialStep) Name() string {
	return s.StepName
}

// ParallelStep executes multiple agents concurrently.
type ParallelStep struct {
	StepName   string
	Agents     []*Agent
	InputFn    func(state map[string]any) []string
	OutputKeys []string
}

// Execute implements Step.
func (p *ParallelStep) Execute(ctx context.Context, wf *Workflow) (*AgentResult, error) {
	wf.mu.RLock()
	inputs := p.InputFn(wf.State)
	wf.mu.RUnlock()

	if len(inputs) != len(p.Agents) {
		return nil, fmt.Errorf("input count (%d) does not match agent count (%d)", len(inputs), len(p.Agents))
	}

	var wg sync.WaitGroup
	results := make([]*AgentResult, len(p.Agents))
	errors := make([]error, len(p.Agents))

	for i, agent := range p.Agents {
		wg.Add(1)
		go func(idx int, ag *Agent, input string) {
			defer wg.Done()
			result, err := executeAgent(ctx, ag, input)
			results[idx] = result
			errors[idx] = err
		}(i, agent, inputs[i])
	}

	wg.Wait()

	// Check for errors
	for i, err := range errors {
		if err != nil {
			return results[i], fmt.Errorf("agent %s failed: %w", p.Agents[i].Name, err)
		}
	}

	// Store all outputs in workflow state
	wf.mu.Lock()
	for i, result := range results {
		if i < len(p.OutputKeys) {
			wf.State[p.OutputKeys[i]] = result.Output
		}
	}
	wf.mu.Unlock()

	// Return first result (combined result)
	return results[0], nil
}

// Name implements Step.
func (p *ParallelStep) Name() string {
	return p.StepName
}

// ConditionalStep executes one of two branches based on a condition.
type ConditionalStep struct {
	StepName  string
	Condition func(state map[string]any) bool
	TrueStep  Step
	FalseStep Step
}

// Execute implements Step.
func (c *ConditionalStep) Execute(ctx context.Context, wf *Workflow) (*AgentResult, error) {
	wf.mu.RLock()
	condition := c.Condition(wf.State)
	wf.mu.RUnlock()

	if condition {
		return c.TrueStep.Execute(ctx, wf)
	}
	if c.FalseStep != nil {
		return c.FalseStep.Execute(ctx, wf)
	}
	return nil, nil
}

// Name implements Step.
func (c *ConditionalStep) Name() string {
	return c.StepName
}

// RetryStep retries a step on failure.
type RetryStep struct {
	StepName   string
	Step       Step
	MaxRetries int
	Delay      time.Duration
}

// Execute implements Step.
func (r *RetryStep) Execute(ctx context.Context, wf *Workflow) (*AgentResult, error) {
	var lastResult *AgentResult
	var lastErr error

	for i := 0; i <= r.MaxRetries; i++ {
		lastResult, lastErr = r.Step.Execute(ctx, wf)
		if lastErr == nil {
			return lastResult, nil
		}

		if i < r.MaxRetries {
			timer := time.NewTimer(r.Delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return lastResult, ctx.Err()
			case <-timer.C:
				// Continue to next retry
			}
		}
	}

	return lastResult, fmt.Errorf("step %s failed after %d retries: %w", r.StepName, r.MaxRetries, lastErr)
}

// Name implements Step.
func (r *RetryStep) Name() string {
	return r.StepName
}

// executeAgent executes a single agent.
func executeAgent(ctx context.Context, agent *Agent, input string) (*AgentResult, error) {
	result := &AgentResult{
		AgentID:   agent.ID,
		Input:     input,
		StartTime: time.Now(),
	}

	// Build request
	req := &provider.CompletionRequest{
		Model: agent.Model,
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, input),
		},
		Temperature: agent.Temperature,
		MaxTokens:   agent.MaxTokens,
	}

	// Add system prompt if provided
	if agent.SystemPrompt != "" {
		req.System = agent.SystemPrompt
	}

	// Add tools if provided
	if len(agent.Tools) > 0 {
		req.Tools = agent.Tools
	}

	// Execute
	resp, err := agent.Provider.Complete(ctx, req)
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	if err != nil {
		result.Error = err
		return result, err
	}

	result.Output = resp.GetText()
	result.TokenUsage = resp.Usage

	return result, nil
}

// Engine orchestrates agent workflows.
type Engine struct {
	workflows map[string]*Workflow
	mu        sync.RWMutex
}

// NewEngine creates a new orchestration engine.
func NewEngine() *Engine {
	return &Engine{
		workflows: make(map[string]*Workflow),
	}
}

// RegisterWorkflow registers a workflow.
func (e *Engine) RegisterWorkflow(wf *Workflow) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.workflows[wf.ID] = wf
}

// GetWorkflow retrieves a workflow by ID.
func (e *Engine) GetWorkflow(id string) (*Workflow, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	wf, ok := e.workflows[id]
	if !ok {
		return nil, fmt.Errorf("workflow not found: %s", id)
	}
	return wf, nil
}

// Execute executes a workflow.
func (e *Engine) Execute(ctx context.Context, workflowID string, initialState map[string]any) ([]*AgentResult, error) {
	wf, err := e.GetWorkflow(workflowID)
	if err != nil {
		return nil, err
	}

	// Initialize state
	wf.mu.Lock()
	wf.State = initialState
	if wf.State == nil {
		wf.State = make(map[string]any)
	}
	wf.mu.Unlock()

	// Execute steps
	results := make([]*AgentResult, 0, len(wf.Steps))
	for _, step := range wf.Steps {
		result, err := step.Execute(ctx, wf)
		if err != nil {
			return results, fmt.Errorf("step %s failed: %w", step.Name(), err)
		}
		if result != nil {
			results = append(results, result)
		}
	}

	return results, nil
}

// NewWorkflow creates a new workflow.
func NewWorkflow(id, name, description string) *Workflow {
	return &Workflow{
		ID:          id,
		Name:        name,
		Description: description,
		Steps:       make([]Step, 0),
		State:       make(map[string]any),
	}
}

// AddStep adds a step to the workflow.
func (wf *Workflow) AddStep(step Step) *Workflow {
	wf.Steps = append(wf.Steps, step)
	return wf
}

// GetState retrieves a value from workflow state.
func (wf *Workflow) GetState(key string) (any, bool) {
	wf.mu.RLock()
	defer wf.mu.RUnlock()
	val, ok := wf.State[key]
	return val, ok
}

// SetState sets a value in workflow state.
func (wf *Workflow) SetState(key string, value any) {
	wf.mu.Lock()
	defer wf.mu.Unlock()
	wf.State[key] = value
}
