package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
)

// Mock provider for testing
type mockProvider struct {
	response string
	err      error
}

func (m *mockProvider) Name() string {
	return "mock"
}

func (m *mockProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &provider.CompletionResponse{
		ID:    "test-id",
		Model: req.Model,
		Content: []provider.ContentBlock{
			{Type: provider.ContentTypeText, Text: m.response},
		},
		Usage: tokenizer.TokenUsage{TotalTokens: 10},
	}, nil
}

func (m *mockProvider) CompleteStream(ctx context.Context, req *provider.CompletionRequest) (*provider.StreamReader, error) {
	return nil, nil
}

func (m *mockProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return []provider.Model{}, nil
}

func (m *mockProvider) GetModel(ctx context.Context, modelID string) (*provider.Model, error) {
	return &provider.Model{ID: modelID}, nil
}

func (m *mockProvider) CountTokens(text string) (int, error) {
	return len(text) / 4, nil
}

func TestSequentialStep_Execute(t *testing.T) {
	agent := &Agent{
		ID:       "agent-1",
		Name:     "Test Agent",
		Provider: &mockProvider{response: "test response"},
		Model:    "test-model",
	}

	step := &SequentialStep{
		StepName: "test-step",
		Agent:    agent,
		InputFn: func(state map[string]any) string {
			return "test input"
		},
		OutputKey: "result",
	}

	wf := NewWorkflow("wf-1", "Test Workflow", "Test")
	result, err := step.Execute(t.Context(), wf)

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Output != "test response" {
		t.Errorf("Output = %v, want 'test response'", result.Output)
	}

	// Check workflow state
	val, ok := wf.GetState("result")
	if !ok {
		t.Error("Output should be stored in workflow state")
	}
	if val != "test response" {
		t.Errorf("State value = %v, want 'test response'", val)
	}
}

func TestParallelStep_Execute(t *testing.T) {
	agent1 := &Agent{
		ID:       "agent-1",
		Name:     "Agent 1",
		Provider: &mockProvider{response: "response 1"},
		Model:    "test-model",
	}

	agent2 := &Agent{
		ID:       "agent-2",
		Name:     "Agent 2",
		Provider: &mockProvider{response: "response 2"},
		Model:    "test-model",
	}

	step := &ParallelStep{
		StepName: "parallel-step",
		Agents:   []*Agent{agent1, agent2},
		InputFn: func(state map[string]any) []string {
			return []string{"input 1", "input 2"}
		},
		OutputKeys: []string{"result1", "result2"},
	}

	wf := NewWorkflow("wf-1", "Test Workflow", "Test")
	_, err := step.Execute(t.Context(), wf)

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Check both results are stored
	val1, ok := wf.GetState("result1")
	if !ok || val1 != "response 1" {
		t.Errorf("result1 = %v, want 'response 1'", val1)
	}

	val2, ok := wf.GetState("result2")
	if !ok || val2 != "response 2" {
		t.Errorf("result2 = %v, want 'response 2'", val2)
	}
}

func TestParallelStep_InputMismatch(t *testing.T) {
	agent1 := &Agent{
		ID:       "agent-1",
		Name:     "Agent 1",
		Provider: &mockProvider{response: "response 1"},
		Model:    "test-model",
	}

	step := &ParallelStep{
		StepName: "parallel-step",
		Agents:   []*Agent{agent1},
		InputFn: func(state map[string]any) []string {
			return []string{"input 1", "input 2"} // More inputs than agents
		},
		OutputKeys: []string{"result1"},
	}

	wf := NewWorkflow("wf-1", "Test Workflow", "Test")
	_, err := step.Execute(t.Context(), wf)

	if err == nil {
		t.Error("Execute() should fail with input count mismatch")
	}
}

func TestConditionalStep_Execute(t *testing.T) {
	agentTrue := &Agent{
		ID:       "agent-true",
		Name:     "True Agent",
		Provider: &mockProvider{response: "true branch"},
		Model:    "test-model",
	}

	agentFalse := &Agent{
		ID:       "agent-false",
		Name:     "False Agent",
		Provider: &mockProvider{response: "false branch"},
		Model:    "test-model",
	}

	trueStep := &SequentialStep{
		StepName:  "true-step",
		Agent:     agentTrue,
		InputFn:   func(state map[string]any) string { return "test" },
		OutputKey: "result",
	}

	falseStep := &SequentialStep{
		StepName:  "false-step",
		Agent:     agentFalse,
		InputFn:   func(state map[string]any) string { return "test" },
		OutputKey: "result",
	}

	t.Run("condition true", func(t *testing.T) {
		step := &ConditionalStep{
			StepName:  "conditional",
			Condition: func(state map[string]any) bool { return true },
			TrueStep:  trueStep,
			FalseStep: falseStep,
		}

		wf := NewWorkflow("wf-1", "Test", "Test")
		result, err := step.Execute(t.Context(), wf)

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if result.Output != "true branch" {
			t.Errorf("Output = %v, want 'true branch'", result.Output)
		}
	})

	t.Run("condition false", func(t *testing.T) {
		step := &ConditionalStep{
			StepName:  "conditional",
			Condition: func(state map[string]any) bool { return false },
			TrueStep:  trueStep,
			FalseStep: falseStep,
		}

		wf := NewWorkflow("wf-1", "Test", "Test")
		result, err := step.Execute(t.Context(), wf)

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if result.Output != "false branch" {
			t.Errorf("Output = %v, want 'false branch'", result.Output)
		}
	})
}

func TestRetryStep_Execute(t *testing.T) {
	t.Run("success on first try", func(t *testing.T) {
		agent := &Agent{
			ID:       "agent-1",
			Name:     "Test Agent",
			Provider: &mockProvider{response: "success"},
			Model:    "test-model",
		}

		baseStep := &SequentialStep{
			StepName:  "base",
			Agent:     agent,
			InputFn:   func(state map[string]any) string { return "test" },
			OutputKey: "result",
		}

		retryStep := &RetryStep{
			StepName:   "retry",
			Step:       baseStep,
			MaxRetries: 3,
			Delay:      10 * time.Millisecond,
		}

		wf := NewWorkflow("wf-1", "Test", "Test")
		result, err := retryStep.Execute(t.Context(), wf)

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if result.Output != "success" {
			t.Errorf("Output = %v, want 'success'", result.Output)
		}
	})

	t.Run("fails after max retries", func(t *testing.T) {
		agent := &Agent{
			ID:       "agent-1",
			Name:     "Test Agent",
			Provider: &mockProvider{err: errors.New("test error")},
			Model:    "test-model",
		}

		baseStep := &SequentialStep{
			StepName:  "base",
			Agent:     agent,
			InputFn:   func(state map[string]any) string { return "test" },
			OutputKey: "result",
		}

		retryStep := &RetryStep{
			StepName:   "retry",
			Step:       baseStep,
			MaxRetries: 2,
			Delay:      1 * time.Millisecond,
		}

		wf := NewWorkflow("wf-1", "Test", "Test")
		_, err := retryStep.Execute(t.Context(), wf)

		if err == nil {
			t.Error("Execute() should fail after max retries")
		}

		if !strings.Contains(err.Error(), "failed after 2 retries") {
			t.Errorf("Error message should mention retries, got: %v", err)
		}
	})
}

func TestEngine_RegisterWorkflow(t *testing.T) {
	engine := NewEngine()
	wf := NewWorkflow("wf-1", "Test Workflow", "Test")

	engine.RegisterWorkflow(wf)

	retrieved, err := engine.GetWorkflow("wf-1")
	if err != nil {
		t.Fatalf("GetWorkflow() error = %v", err)
	}

	if retrieved.ID != "wf-1" {
		t.Errorf("Workflow ID = %v, want wf-1", retrieved.ID)
	}
}

func TestEngine_GetWorkflow_NotFound(t *testing.T) {
	engine := NewEngine()

	_, err := engine.GetWorkflow("nonexistent")
	if err == nil {
		t.Error("GetWorkflow() should return error for nonexistent workflow")
	}
}

func TestEngine_Execute(t *testing.T) {
	engine := NewEngine()

	agent1 := &Agent{
		ID:       "agent-1",
		Name:     "Agent 1",
		Provider: &mockProvider{response: "step 1 result"},
		Model:    "test-model",
	}

	agent2 := &Agent{
		ID:       "agent-2",
		Name:     "Agent 2",
		Provider: &mockProvider{response: "step 2 result"},
		Model:    "test-model",
	}

	wf := NewWorkflow("wf-1", "Test Workflow", "Multi-step workflow")

	step1 := &SequentialStep{
		StepName:  "step1",
		Agent:     agent1,
		InputFn:   func(state map[string]any) string { return state["input"].(string) },
		OutputKey: "step1_output",
	}

	step2 := &SequentialStep{
		StepName: "step2",
		Agent:    agent2,
		InputFn: func(state map[string]any) string {
			return "Input: " + state["step1_output"].(string)
		},
		OutputKey: "step2_output",
	}

	wf.AddStep(step1).AddStep(step2)
	engine.RegisterWorkflow(wf)

	initialState := map[string]any{
		"input": "test input",
	}

	results, err := engine.Execute(t.Context(), "wf-1", initialState)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Results count = %d, want 2", len(results))
	}

	if results[0].Output != "step 1 result" {
		t.Errorf("Step 1 output = %v, want 'step 1 result'", results[0].Output)
	}

	if results[1].Output != "step 2 result" {
		t.Errorf("Step 2 output = %v, want 'step 2 result'", results[1].Output)
	}
}

func TestWorkflow_StateManagement(t *testing.T) {
	wf := NewWorkflow("wf-1", "Test", "Test")

	// Set state
	wf.SetState("key1", "value1")
	wf.SetState("key2", 42)

	// Get state
	val1, ok := wf.GetState("key1")
	if !ok || val1 != "value1" {
		t.Errorf("GetState(key1) = %v, want 'value1'", val1)
	}

	val2, ok := wf.GetState("key2")
	if !ok || val2 != 42 {
		t.Errorf("GetState(key2) = %v, want 42", val2)
	}

	// Non-existent key
	_, ok = wf.GetState("nonexistent")
	if ok {
		t.Error("GetState(nonexistent) should return false")
	}
}

func TestAgentResult_Fields(t *testing.T) {
	agent := &Agent{
		ID:       "agent-1",
		Name:     "Test Agent",
		Provider: &mockProvider{response: "test output"},
		Model:    "test-model",
	}

	result, err := executeAgent(t.Context(), agent, "test input")
	if err != nil {
		t.Fatalf("executeAgent() error = %v", err)
	}

	if result.AgentID != "agent-1" {
		t.Errorf("AgentID = %v, want agent-1", result.AgentID)
	}

	if result.Input != "test input" {
		t.Errorf("Input = %v, want 'test input'", result.Input)
	}

	if result.Output != "test output" {
		t.Errorf("Output = %v, want 'test output'", result.Output)
	}

	if result.Duration == 0 {
		t.Error("Duration should be > 0")
	}

	if result.TokenUsage.TotalTokens != 10 {
		t.Errorf("TokenUsage = %v, want 10", result.TokenUsage.TotalTokens)
	}
}

func TestStepName(t *testing.T) {
	tests := []struct {
		name string
		step Step
		want string
	}{
		{
			name: "SequentialStep",
			step: &SequentialStep{StepName: "seq-step"},
			want: "seq-step",
		},
		{
			name: "ParallelStep",
			step: &ParallelStep{StepName: "par-step"},
			want: "par-step",
		},
		{
			name: "ConditionalStep",
			step: &ConditionalStep{StepName: "cond-step"},
			want: "cond-step",
		},
		{
			name: "RetryStep",
			step: &RetryStep{StepName: "retry-step"},
			want: "retry-step",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.step.Name(); got != tt.want {
				t.Errorf("Name() = %v, want %v", got, tt.want)
			}
		})
	}
}
