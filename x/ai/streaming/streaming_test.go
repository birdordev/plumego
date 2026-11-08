package streaming

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/ai/orchestration"
	"github.com/birdordev/plumego/x/ai/sse"
)

func TestStreamManager(t *testing.T) {
	t.Run("RegisterAndGet", func(t *testing.T) {
		sm := NewStreamManager()

		w := httptest.NewRecorder()
		stream, _ := sse.NewStream(t.Context(), w)

		if err := sm.Register("workflow-1", stream); err != nil {
			t.Fatal(err)
		}

		retrieved, ok := sm.Get("workflow-1")
		if !ok {
			t.Fatal("expected to find registered stream")
		}

		if retrieved != stream {
			t.Error("retrieved stream does not match registered stream")
		}
	})

	t.Run("Unregister", func(t *testing.T) {
		sm := NewStreamManager()

		w := httptest.NewRecorder()
		stream, _ := sse.NewStream(t.Context(), w)

		if err := sm.Register("workflow-1", stream); err != nil {
			t.Fatal(err)
		}
		sm.Unregister("workflow-1")

		_, ok := sm.Get("workflow-1")
		if ok {
			t.Error("stream should have been unregistered")
		}
	})

	t.Run("Count", func(t *testing.T) {
		sm := NewStreamManager()

		if sm.Count() != 0 {
			t.Error("expected 0 streams initially")
		}

		w1 := httptest.NewRecorder()
		w2 := httptest.NewRecorder()
		stream1, _ := sse.NewStream(t.Context(), w1)
		stream2, _ := sse.NewStream(t.Context(), w2)
		if err := sm.Register("workflow-1", stream1); err != nil {
			t.Fatal(err)
		}
		if err := sm.Register("workflow-2", stream2); err != nil {
			t.Fatal(err)
		}

		if sm.Count() != 2 {
			t.Errorf("expected 2 streams, got %d", sm.Count())
		}
	})

	t.Run("SendUpdate", func(t *testing.T) {
		sm := NewStreamManager()

		w := httptest.NewRecorder()
		stream, _ := sse.NewStream(t.Context(), w)

		if err := sm.Register("workflow-1", stream); err != nil {
			t.Fatal(err)
		}

		update := &ProgressUpdate{
			WorkflowID: "workflow-1",
			StepName:   "test-step",
			Status:     StatusStarted,
			Progress:   0.5,
			Timestamp:  time.Now(),
		}

		err := sm.SendUpdate("workflow-1", update)
		if err != nil {
			t.Fatalf("SendUpdate failed: %v", err)
		}

		// Check that data was written
		output := w.Body.String()
		if output == "" {
			t.Error("expected output, got empty string")
		}
	})

	t.Run("SendUpdateNotFound", func(t *testing.T) {
		sm := NewStreamManager()

		update := &ProgressUpdate{
			WorkflowID: "nonexistent",
			StepName:   "test",
			Status:     StatusStarted,
			Timestamp:  time.Now(),
		}

		err := sm.SendUpdate("nonexistent", update)
		if err == nil {
			t.Error("expected error for nonexistent stream")
		}
	})

	t.Run("SendUpdateClosedStream", func(t *testing.T) {
		sm := NewStreamManager()

		w := httptest.NewRecorder()
		stream, _ := sse.NewStream(t.Context(), w)
		if err := sm.Register("workflow-1", stream); err != nil {
			t.Fatal(err)
		}

		if err := stream.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}

		err := sm.SendUpdate("workflow-1", &ProgressUpdate{
			WorkflowID: "workflow-1",
			StepName:   "closed",
			Status:     StatusStarted,
			Timestamp:  time.Now(),
		})
		if err == nil {
			t.Fatal("SendUpdate() should fail for a closed stream")
		}
	})

	t.Run("Close", func(t *testing.T) {
		sm := NewStreamManager()

		w := httptest.NewRecorder()
		stream, _ := sse.NewStream(t.Context(), w)

		if err := sm.Register("workflow-1", stream); err != nil {
			t.Fatal(err)
		}

		err := sm.Close("workflow-1")
		if err != nil {
			t.Fatalf("Close failed: %v", err)
		}

		_, ok := sm.Get("workflow-1")
		if ok {
			t.Error("stream should have been closed and unregistered")
		}
	})
}

func TestStreamManager_Register(t *testing.T) {
	t.Run("empty workflow ID", func(t *testing.T) {
		sm := NewStreamManager()

		w := httptest.NewRecorder()
		stream, _ := sse.NewStream(t.Context(), w)

		err := sm.Register("", stream)
		if !errors.Is(err, ErrWorkflowIDRequired) {
			t.Fatalf("RegisterE() error = %v, want ErrWorkflowIDRequired", err)
		}
	})

	t.Run("nil stream", func(t *testing.T) {
		sm := NewStreamManager()

		err := sm.Register("workflow-1", nil)
		if !errors.Is(err, ErrStreamRequired) {
			t.Fatalf("RegisterE() error = %v, want ErrStreamRequired", err)
		}
	})

	t.Run("valid registration", func(t *testing.T) {
		sm := NewStreamManager()

		w := httptest.NewRecorder()
		stream, _ := sse.NewStream(t.Context(), w)

		if err := sm.Register("workflow-1", stream); err != nil {
			t.Fatalf("RegisterE() error = %v", err)
		}

		retrieved, ok := sm.Get("workflow-1")
		if !ok {
			t.Fatal("expected stream to be registered")
		}
		if retrieved != stream {
			t.Fatal("expected registered stream")
		}
	})
}

func TestProgressUpdate(t *testing.T) {
	t.Run("MarshalJSON", func(t *testing.T) {
		update := &ProgressUpdate{
			WorkflowID: "workflow-1",
			StepName:   "test-step",
			Status:     StatusCompleted,
			Progress:   1.0,
			Timestamp:  time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		}

		data, err := json.Marshal(update)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}

		var result struct {
			WorkflowID string         `json:"workflow_id"`
			StepName   string         `json:"step_name"`
			Status     ProgressStatus `json:"status"`
			Progress   float64        `json:"progress"`
			Timestamp  string         `json:"timestamp"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("Unmarshal failed: %v", err)
		}

		if result.WorkflowID != "workflow-1" || result.StepName != "test-step" || result.Status != StatusCompleted || result.Progress != 1.0 {
			t.Fatalf("unexpected progress update payload: %+v", result)
		}
		if result.Timestamp == "" {
			t.Error("timestamp should not be empty")
		}
	})
}

func TestStreamConfig(t *testing.T) {
	t.Run("DefaultConfig", func(t *testing.T) {
		config := DefaultStreamConfig()

		if !config.EnableProgress {
			t.Error("EnableProgress should be true by default")
		}

		if !config.EnableStepLogs {
			t.Error("EnableStepLogs should be true by default")
		}

		if config.KeepAlive != 15*time.Second {
			t.Errorf("expected KeepAlive 15s, got %v", config.KeepAlive)
		}

		if config.BufferSize != 100 {
			t.Errorf("expected BufferSize 100, got %d", config.BufferSize)
		}
	})
}

func TestStreamingEngine(t *testing.T) {
	t.Run("NewStreamingEngine", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)

		if streamEngine == nil {
			t.Fatal("expected non-nil streaming engine")
		}

		if streamEngine.StreamManager() == nil {
			t.Error("expected non-nil stream manager")
		}
	})

	t.Run("ExecuteStreamingEmptyWorkflow", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)

		workflow := &orchestration.Workflow{
			Name:  "test-workflow",
			Steps: []orchestration.Step{},
		}

		w := httptest.NewRecorder()
		ctx := t.Context()
		stream, _ := sse.NewStream(ctx, w)

		results, err := streamEngine.ExecuteStreaming(ctx, workflow, "workflow-1", stream)
		if err != nil {
			t.Fatalf("ExecuteStreaming failed: %v", err)
		}

		if len(results) != 0 {
			t.Error("expected empty results for empty workflow")
		}

		// Check that events were sent
		output := w.Body.String()
		if output == "" {
			t.Error("expected output events")
		}
	})

	t.Run("ExecuteStreamingWithSequentialStep", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)

		// Create a mock provider
		mockProvider := &mockProvider{
			output: "test output",
		}

		mockAgent := &orchestration.Agent{
			Name:         "test-agent",
			SystemPrompt: "You are a test agent",
			Provider:     mockProvider,
		}

		workflow := &orchestration.Workflow{
			Name:  "test-workflow",
			State: make(map[string]any),
			Steps: []orchestration.Step{
				&orchestration.SequentialStep{
					StepName:  "test-step",
					Agent:     mockAgent,
					InputFn:   func(state map[string]any) string { return "test input" },
					OutputKey: "result",
				},
			},
		}

		w := httptest.NewRecorder()
		ctx := t.Context()
		stream, _ := sse.NewStream(ctx, w)

		results, err := streamEngine.ExecuteStreaming(ctx, workflow, "workflow-1", stream)
		if err != nil {
			t.Fatalf("ExecuteStreaming failed: %v", err)
		}

		if len(results) == 0 {
			t.Error("expected at least one result")
		}

		// Parse events from output
		output := w.Body.String()
		if output == "" {
			t.Fatal("expected output events")
		}

		// Should contain started and completed events
		if !contains(output, "started") {
			t.Error("expected started event")
		}
		if !contains(output, "completed") {
			t.Error("expected completed event")
		}
	})

	t.Run("ExecuteStreamingWithError", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)

		// Create a mock provider that returns an error
		mockProvider := &mockProvider{
			err: fmt.Errorf("agent error"),
		}

		mockAgent := &orchestration.Agent{
			Name:     "failing-agent",
			Provider: mockProvider,
		}

		workflow := &orchestration.Workflow{
			Name:  "test-workflow",
			State: make(map[string]any),
			Steps: []orchestration.Step{
				&orchestration.SequentialStep{
					StepName:  "failing-step",
					Agent:     mockAgent,
					InputFn:   func(state map[string]any) string { return "test input" },
					OutputKey: "result",
				},
			},
		}

		w := httptest.NewRecorder()
		ctx := t.Context()
		stream, _ := sse.NewStream(ctx, w)

		_, err := streamEngine.ExecuteStreaming(ctx, workflow, "workflow-1", stream)
		if err == nil {
			t.Error("expected error from failing agent")
		}

		// Parse events from output
		output := w.Body.String()
		if !contains(output, "failed") {
			t.Error("expected failed event")
		}
	})
}

func TestGetStepName(t *testing.T) {
	tests := []struct {
		name     string
		step     orchestration.Step
		expected string
	}{
		{
			name: "SequentialStep",
			step: &orchestration.SequentialStep{
				StepName: "test-step",
				Agent:    &orchestration.Agent{Name: "test-agent"},
			},
			expected: "test-step",
		},
		{
			name:     "SequentialStep",
			step:     &orchestration.SequentialStep{},
			expected: "sequential",
		},
		{
			name:     "ParallelStep",
			step:     &orchestration.ParallelStep{},
			expected: "parallel",
		},
		{
			name:     "ConditionalStep",
			step:     &orchestration.ConditionalStep{},
			expected: "conditional",
		},
		{
			name:     "RetryStep",
			step:     &orchestration.RetryStep{},
			expected: "retry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStepName(tt.step)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestGetStepType(t *testing.T) {
	tests := []struct {
		name     string
		step     orchestration.Step
		expected string
	}{
		{
			name:     "SequentialStep",
			step:     &orchestration.SequentialStep{},
			expected: "sequential",
		},
		{
			name:     "SequentialStep",
			step:     &orchestration.SequentialStep{},
			expected: "sequential",
		},
		{
			name:     "ParallelStep",
			step:     &orchestration.ParallelStep{},
			expected: "parallel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStepType(tt.step)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}
