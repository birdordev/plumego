package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/birdordev/plumego/x/ai/orchestration"
	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/sse"
)

func TestStreamingParallelStep(t *testing.T) {
	t.Run("ExecuteEmpty", func(t *testing.T) {
		streamMgr := NewStreamManager()
		w := httptest.NewRecorder()
		ctx := t.Context()
		stream, _ := sse.NewStream(ctx, w)
		if err := streamMgr.Register("workflow-1", stream); err != nil {
			t.Fatal(err)
		}

		sps := NewStreamingParallelStep(
			[]*orchestration.Agent{},
			"workflow-1",
			"parallel-step",
			0,
			streamMgr,
			nil,
		)

		workflow := &orchestration.Workflow{Name: "test"}
		_, err := sps.Execute(ctx, workflow)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	})

	t.Run("ExecuteSingleAgent", func(t *testing.T) {
		streamMgr := NewStreamManager()
		w := httptest.NewRecorder()
		ctx := t.Context()
		stream, _ := sse.NewStream(ctx, w)
		if err := streamMgr.Register("workflow-1", stream); err != nil {
			t.Fatal(err)
		}

		mockProvider := &mockProvider{
			output: "result-1",
		}

		mockAgent := &orchestration.Agent{
			Name:     "agent-1",
			Provider: mockProvider,
		}

		sps := NewStreamingParallelStep(
			[]*orchestration.Agent{mockAgent},
			"workflow-1",
			"parallel-step",
			0,
			streamMgr,
			nil,
		)

		workflow := &orchestration.Workflow{Name: "test"}
		_, err := sps.Execute(ctx, workflow)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		output := w.Body.String()
		if !contains(output, "agent-1") {
			t.Error("expected agent-1 in output")
		}
	})

	t.Run("ExecuteMultipleAgents", func(t *testing.T) {
		streamMgr := NewStreamManager()
		w := httptest.NewRecorder()
		ctx := t.Context()
		stream, _ := sse.NewStream(ctx, w)
		if err := streamMgr.Register("workflow-1", stream); err != nil {
			t.Fatal(err)
		}

		agents := []*orchestration.Agent{
			{
				Name:     "agent-1",
				Provider: &mockProvider{output: "result-1"},
			},
			{
				Name:     "agent-2",
				Provider: &mockProvider{output: "result-2"},
			},
			{
				Name:     "agent-3",
				Provider: &mockProvider{output: "result-3"},
			},
		}

		sps := NewStreamingParallelStep(
			agents,
			"workflow-1",
			"parallel-step",
			0,
			streamMgr,
			nil,
		)

		workflow := &orchestration.Workflow{Name: "test"}
		_, err := sps.Execute(ctx, workflow)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		output := w.Body.String()

		// All agents should be in output
		for _, agent := range agents {
			if !contains(output, agent.Name) {
				t.Errorf("expected %s in output", agent.Name)
			}
		}

		// Should contain started and completed events
		if !contains(output, "started") {
			t.Error("expected started events")
		}
		if !contains(output, "completed") {
			t.Error("expected completed events")
		}
	})

	t.Run("ExecuteWithError", func(t *testing.T) {
		streamMgr := NewStreamManager()
		w := httptest.NewRecorder()
		ctx := t.Context()
		stream, _ := sse.NewStream(ctx, w)
		if err := streamMgr.Register("workflow-1", stream); err != nil {
			t.Fatal(err)
		}

		agents := []*orchestration.Agent{
			{
				Name:     "agent-1",
				Provider: &mockProvider{output: "result-1"},
			},
			{
				Name:     "failing-agent",
				Provider: &mockProvider{err: fmt.Errorf("agent failed")},
			},
			{
				Name:     "agent-3",
				Provider: &mockProvider{output: "result-3"},
			},
		}

		sps := NewStreamingParallelStep(
			agents,
			"workflow-1",
			"parallel-step",
			0,
			streamMgr,
			nil,
		)

		workflow := &orchestration.Workflow{Name: "test"}
		_, err := sps.Execute(ctx, workflow)
		if err == nil {
			t.Error("expected error from failing agent")
		}

		output := w.Body.String()
		if !contains(output, "failed") {
			t.Error("expected failed event in output")
		}
	})

	t.Run("ProgressTracking", func(t *testing.T) {
		streamMgr := NewStreamManager()
		w := httptest.NewRecorder()
		ctx := t.Context()
		stream, _ := sse.NewStream(ctx, w)
		if err := streamMgr.Register("workflow-1", stream); err != nil {
			t.Fatal(err)
		}

		agentCount := 5
		agents := make([]*orchestration.Agent, agentCount)
		for i := 0; i < agentCount; i++ {
			agents[i] = &orchestration.Agent{
				Name:     fmt.Sprintf("agent-%d", i),
				Provider: &mockProvider{output: fmt.Sprintf("result-%d", i)},
			}
		}

		sps := NewStreamingParallelStep(
			agents,
			"workflow-1",
			"parallel-step",
			0,
			streamMgr,
			nil,
		)

		workflow := &orchestration.Workflow{Name: "test"}
		_, err := sps.Execute(ctx, workflow)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		output := w.Body.String()

		// Should have parallel_index and parallel_total in events
		if !contains(output, "parallel_index") {
			t.Error("expected parallel_index in output")
		}
		if !contains(output, "parallel_total") {
			t.Error("expected parallel_total in output")
		}
	})
}

func TestStreamingWorkflow(t *testing.T) {
	t.Run("NewStreamingWorkflow", func(t *testing.T) {
		streamMgr := NewStreamManager()
		sw := NewStreamingWorkflow("test-workflow", "workflow-1", streamMgr, nil)

		if sw == nil {
			t.Fatal("expected non-nil streaming workflow")
		}

		if sw.Name != "test-workflow" {
			t.Errorf("expected name 'test-workflow', got %s", sw.Name)
		}

		if sw.WorkflowID != "workflow-1" {
			t.Errorf("expected workflow ID 'workflow-1', got %s", sw.WorkflowID)
		}
	})

	t.Run("AddAgentStep", func(t *testing.T) {
		streamMgr := NewStreamManager()
		sw := NewStreamingWorkflow("test-workflow", "workflow-1", streamMgr, nil)

		agent := &orchestration.Agent{Name: "test-agent"}
		sw.AddAgentStep(agent)

		if len(sw.Steps) != 1 {
			t.Errorf("expected 1 step, got %d", len(sw.Steps))
		}
	})

	t.Run("AddParallelStep", func(t *testing.T) {
		streamMgr := NewStreamManager()
		sw := NewStreamingWorkflow("test-workflow", "workflow-1", streamMgr, nil)

		agents := []*orchestration.Agent{
			{Name: "agent-1"},
			{Name: "agent-2"},
		}

		sw.AddParallelStep(agents)

		if len(sw.Steps) != 1 {
			t.Errorf("expected 1 step, got %d", len(sw.Steps))
		}

		// Check that it's a StreamingParallelStep
		_, ok := sw.Steps[0].(*StreamingParallelStep)
		if !ok {
			t.Error("expected StreamingParallelStep")
		}
	})

	t.Run("MixedSteps", func(t *testing.T) {
		streamMgr := NewStreamManager()
		sw := NewStreamingWorkflow("test-workflow", "workflow-1", streamMgr, nil)

		sw.AddAgentStep(&orchestration.Agent{Name: "agent-1"})
		sw.AddParallelStep([]*orchestration.Agent{
			{Name: "parallel-1"},
			{Name: "parallel-2"},
		})
		sw.AddAgentStep(&orchestration.Agent{Name: "agent-2"})

		if len(sw.Steps) != 3 {
			t.Errorf("expected 3 steps, got %d", len(sw.Steps))
		}
	})
}

func TestConcurrentStreamingParallelStep(t *testing.T) {
	t.Run("HighConcurrency", func(t *testing.T) {
		streamMgr := NewStreamManager()
		w := httptest.NewRecorder()
		ctx := t.Context()
		stream, _ := sse.NewStream(ctx, w)
		if err := streamMgr.Register("workflow-1", stream); err != nil {
			t.Fatal(err)
		}

		// Create many agents
		agentCount := 20
		agents := make([]*orchestration.Agent, agentCount)
		for i := 0; i < agentCount; i++ {
			agents[i] = &orchestration.Agent{
				Name:     fmt.Sprintf("agent-%d", i),
				Provider: &mockProvider{output: fmt.Sprintf("result-%d", i)},
			}
		}

		sps := NewStreamingParallelStep(
			agents,
			"workflow-1",
			"parallel-step",
			0,
			streamMgr,
			nil,
		)

		workflow := &orchestration.Workflow{Name: "test"}
		_, err := sps.Execute(ctx, workflow)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		// All agents should complete
		output := w.Body.String()
		completedCount := 0
		for i := 0; i < agentCount; i++ {
			if contains(output, fmt.Sprintf("agent-%d", i)) {
				completedCount++
			}
		}

		if completedCount != agentCount {
			t.Errorf("expected %d completed agents, got %d", agentCount, completedCount)
		}
	})
}

type waitingProvider struct {
	mockProvider
	release <-chan struct{}
}

func (p *waitingProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	<-p.release
	return p.mockProvider.Complete(ctx, req)
}

func TestParallelProgressTracksCompletedAgents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := httptest.NewRecorder()
		stream, err := sse.NewStream(t.Context(), recorder)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		manager := NewStreamManager()
		if err := manager.Register("parallel", stream); err != nil {
			t.Fatal(err)
		}

		release := make(chan struct{})
		wantErr := errors.New("slow agent failed")
		step := NewStreamingParallelStep([]*orchestration.Agent{
			{Name: "slow", Provider: &waitingProvider{mockProvider: mockProvider{err: wantErr}, release: release}},
			{Name: "fast", Provider: &mockProvider{output: "done"}},
		}, "parallel", "step", 0, manager, nil)

		done := make(chan error, 1)
		go func() {
			_, err := step.Execute(t.Context(), &orchestration.Workflow{Name: "test"})
			done <- err
		}()
		synctest.Wait()
		close(release)
		if err := <-done; !errors.Is(err, wantErr) {
			t.Fatalf("Execute error = %v; want %v", err, wantErr)
		}

		completed := 0
		started := 0
		for line := range strings.SplitSeq(recorder.Body.String(), "\n") {
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}
			var update ProgressUpdate
			if err := json.Unmarshal([]byte(data), &update); err != nil {
				t.Fatal(err)
			}
			if update.StepName == "step" {
				continue
			}
			if update.Status == StatusStarted {
				started++
			} else {
				completed++
				if completed == 1 && (update.StepName != "fast" || update.ParallelIndex != 1 || update.Status != StatusCompleted) {
					t.Errorf("first terminal event = %+v", update)
				}
				if completed == 2 && (update.StepName != "slow" || update.ParallelIndex != 0 || update.Status != StatusFailed || update.Error != wantErr.Error()) {
					t.Errorf("second terminal event = %+v", update)
				}
			}
			if want := float64(completed) / 2; update.Progress != want {
				t.Errorf("%s %s progress = %v; want %v", update.StepName, update.Status, update.Progress, want)
			}
		}
		if started != 2 || completed != 2 {
			t.Errorf("got %d started, %d terminal events; want two of each", started, completed)
		}
	})
}
