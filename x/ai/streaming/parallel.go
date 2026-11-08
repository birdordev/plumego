package streaming

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/birdordev/plumego/x/ai/orchestration"
	"github.com/birdordev/plumego/x/ai/provider"
)

// StreamingParallelStep wraps a ParallelStep to stream results as agents complete.
type StreamingParallelStep struct {
	// Base parallel step
	Agents []*orchestration.Agent

	// Streaming configuration
	WorkflowID string
	StepName   string
	StepIndex  int
	StreamMgr  *StreamManager
	Config     *StreamConfig
}

// NewStreamingParallelStep creates a new streaming parallel step.
func NewStreamingParallelStep(
	agents []*orchestration.Agent,
	workflowID string,
	stepName string,
	stepIndex int,
	streamMgr *StreamManager,
	config *StreamConfig,
) *StreamingParallelStep {
	if config == nil {
		config = DefaultStreamConfig()
	}

	return &StreamingParallelStep{
		Agents:     agents,
		WorkflowID: workflowID,
		StepName:   stepName,
		StepIndex:  stepIndex,
		StreamMgr:  streamMgr,
		Config:     config,
	}
}

// Execute executes all agents in parallel and streams results as they complete.
func (sps *StreamingParallelStep) Execute(ctx context.Context, workflow *orchestration.Workflow) (*orchestration.AgentResult, error) {
	if len(sps.Agents) == 0 {
		return nil, nil
	}

	// Send step started event
	sps.sendUpdate(&ProgressUpdate{
		WorkflowID:    sps.WorkflowID,
		StepName:      sps.StepName,
		StepIndex:     sps.StepIndex,
		StepType:      "parallel",
		Status:        StatusStarted,
		Progress:      0.0,
		ParallelTotal: len(sps.Agents),
		Timestamp:     time.Now(),
	})

	// Execute agents concurrently
	results := make([]*orchestration.AgentResult, len(sps.Agents))
	errors := make([]error, len(sps.Agents))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, agent := range sps.Agents {
		wg.Add(1)
		go func(idx int, ag *orchestration.Agent) {
			defer wg.Done()

			// Send agent started event
			sps.sendAgentUpdate(idx, StatusStarted, nil, nil)

			// Execute agent using provider
			result, err := executeAgent(ctx, ag)

			// Update results
			mu.Lock()
			results[idx] = result
			errors[idx] = err
			mu.Unlock()

			// Send agent completed/failed event
			if err != nil {
				sps.sendAgentUpdate(idx, StatusFailed, nil, err)
			} else {
				sps.sendAgentUpdate(idx, StatusCompleted, result, nil)
			}
		}(i, agent)
	}

	// Wait for all agents to complete
	wg.Wait()

	// Check for errors
	var firstError error
	for _, err := range errors {
		if err != nil {
			firstError = err
			break
		}
	}

	// Send final step update
	if firstError != nil {
		sps.sendUpdate(&ProgressUpdate{
			WorkflowID:    sps.WorkflowID,
			StepName:      sps.StepName,
			StepIndex:     sps.StepIndex,
			StepType:      "parallel",
			Status:        StatusFailed,
			Progress:      1.0,
			Error:         firstError.Error(),
			ParallelTotal: len(sps.Agents),
			Timestamp:     time.Now(),
		})
		return nil, firstError
	}

	// Get last non-nil result to return
	var lastResult *orchestration.AgentResult
	for i := len(results) - 1; i >= 0; i-- {
		if results[i] != nil {
			lastResult = results[i]
			break
		}
	}

	sps.sendUpdate(&ProgressUpdate{
		WorkflowID:    sps.WorkflowID,
		StepName:      sps.StepName,
		StepIndex:     sps.StepIndex,
		StepType:      "parallel",
		Status:        StatusCompleted,
		Progress:      1.0,
		ParallelTotal: len(sps.Agents),
		Timestamp:     time.Now(),
	})

	return lastResult, nil
}

// sendAgentUpdate sends an update for a specific parallel agent.
func (sps *StreamingParallelStep) sendAgentUpdate(
	agentIndex int,
	status ProgressStatus,
	result *orchestration.AgentResult,
	err error,
) {
	agentName := "agent"
	if agentIndex < len(sps.Agents) && sps.Agents[agentIndex] != nil {
		agentName = sps.Agents[agentIndex].Name
	}

	// Calculate progress based on completed agents
	completedCount := agentIndex + 1
	if status == StatusStarted {
		completedCount = agentIndex
	}
	progress := float64(completedCount) / float64(len(sps.Agents))

	update := &ProgressUpdate{
		WorkflowID:    sps.WorkflowID,
		StepName:      agentName,
		StepIndex:     sps.StepIndex,
		StepType:      "parallel",
		Status:        status,
		Progress:      progress,
		ParallelIndex: agentIndex,
		ParallelTotal: len(sps.Agents),
		Result:        result,
		Timestamp:     time.Now(),
	}

	if err != nil {
		update.Error = err.Error()
	}

	sps.sendUpdate(update)
}

// sendUpdate sends a progress update to the stream.
func (sps *StreamingParallelStep) sendUpdate(update *ProgressUpdate) {
	if !sps.Config.EnableProgress {
		return
	}

	if err := sps.StreamMgr.SendUpdate(sps.WorkflowID, update); err != nil {
		// Log error but don't fail execution
		_ = err
	}
}

// StreamingWorkflow represents a workflow with streaming support.
type StreamingWorkflow struct {
	*orchestration.Workflow
	WorkflowID string
	StreamMgr  *StreamManager
	Config     *StreamConfig
}

// NewStreamingWorkflow creates a new streaming workflow.
func NewStreamingWorkflow(
	name string,
	workflowID string,
	streamMgr *StreamManager,
	config *StreamConfig,
) *StreamingWorkflow {
	if config == nil {
		config = DefaultStreamConfig()
	}

	return &StreamingWorkflow{
		Workflow: &orchestration.Workflow{
			Name:  name,
			Steps: []orchestration.Step{},
		},
		WorkflowID: workflowID,
		StreamMgr:  streamMgr,
		Config:     config,
	}
}

// AddParallelStep adds a streaming parallel step to the workflow.
func (sw *StreamingWorkflow) AddParallelStep(agents []*orchestration.Agent) *StreamingWorkflow {
	stepIndex := len(sw.Steps)
	stepName := fmt.Sprintf("parallel-%d", stepIndex)

	streamingStep := NewStreamingParallelStep(
		agents,
		sw.WorkflowID,
		stepName,
		stepIndex,
		sw.StreamMgr,
		sw.Config,
	)

	sw.Steps = append(sw.Steps, streamingStep)
	return sw
}

// AddAgentStep adds a regular agent step to the workflow.
func (sw *StreamingWorkflow) AddAgentStep(agent *orchestration.Agent) *StreamingWorkflow {
	sw.Steps = append(sw.Steps, &orchestration.SequentialStep{
		StepName:  agent.Name,
		Agent:     agent,
		InputFn:   func(state map[string]any) string { return "" },
		OutputKey: agent.Name + "_output",
	})
	return sw
}

// Name implements the Step interface for StreamingParallelStep.
func (sps *StreamingParallelStep) Name() string {
	return sps.StepName
}

// executeAgent executes a single agent and returns the result.
func executeAgent(ctx context.Context, agent *orchestration.Agent) (*orchestration.AgentResult, error) {
	result := &orchestration.AgentResult{
		AgentID:   agent.ID,
		StartTime: time.Now(),
	}

	// If provider is nil, return error
	if agent.Provider == nil {
		result.Error = fmt.Errorf("agent provider is nil")
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(result.StartTime)
		return result, result.Error
	}

	// Build request
	req := &provider.CompletionRequest{
		Model:       agent.Model,
		Messages:    []provider.Message{},
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
