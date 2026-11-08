package distributed

import (
	"context"
	"fmt"
	"time"

	"github.com/birdordev/plumego/x/ai/orchestration"
)

// RemoteStep wraps a step for remote execution via task queue.
type RemoteStep struct {
	Step        orchestration.Step
	Queue       TaskQueue
	ExecutionID string
	WorkflowID  string
	Timeout     time.Duration
	RetryPolicy *RetryPolicy
}

// NewRemoteStep creates a new remote step wrapper.
func NewRemoteStep(
	step orchestration.Step,
	queue TaskQueue,
	executionID string,
	workflowID string,
) *RemoteStep {
	return &RemoteStep{
		Step:        step,
		Queue:       queue,
		ExecutionID: executionID,
		WorkflowID:  workflowID,
		Timeout:     5 * time.Minute,
		RetryPolicy: DefaultRetryPolicy(),
	}
}

// Execute executes the step remotely.
func (r *RemoteStep) Execute(ctx context.Context, wf *orchestration.Workflow) (*orchestration.AgentResult, error) {
	// Create task
	task := &WorkflowTask{
		ID:            fmt.Sprintf("%s-%s-%d", r.ExecutionID, r.Step.Name(), time.Now().UnixNano()),
		ExecutionID:   r.ExecutionID,
		WorkflowID:    r.WorkflowID,
		StepName:      r.Step.Name(),
		WorkflowState: wf.State,
		Priority:      5,
		CreatedAt:     time.Now(),
		Timeout:       r.Timeout,
	}

	// Enqueue task
	if err := r.Queue.Enqueue(ctx, task); err != nil {
		return nil, fmt.Errorf("failed to enqueue remote task: %w", err)
	}

	// Subscribe to results
	resultChan, err := r.Queue.SubscribeResults(ctx, r.ExecutionID)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to results: %w", err)
	}

	// Wait for result with timeout
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultChan:
		if result == nil {
			return nil, fmt.Errorf("received nil result")
		}
		if result.Error != "" {
			return result.AgentResult, fmt.Errorf("remote execution failed: %s", result.Error)
		}

		// Update workflow state with result state
		if result.UpdatedState != nil {
			for k, v := range result.UpdatedState {
				wf.State[k] = v
			}
		}

		return result.AgentResult, nil
	case <-timer.C:
		return nil, fmt.Errorf("remote execution timeout after %v", timeout)
	}
}

// Name returns the step name.
func (r *RemoteStep) Name() string {
	return r.Step.Name()
}

// DistributedParallelStep executes multiple agents concurrently across workers.
type DistributedParallelStep struct {
	StepName    string
	Agents      []*orchestration.Agent
	InputFn     func(state map[string]any) []string
	OutputKeys  []string
	Queue       TaskQueue
	ExecutionID string
	WorkflowID  string
	MaxWorkers  int
	Timeout     time.Duration
}

// NewDistributedParallelStep creates a new distributed parallel step.
func NewDistributedParallelStep(
	stepName string,
	agents []*orchestration.Agent,
	inputFn func(state map[string]any) []string,
	outputKeys []string,
	queue TaskQueue,
	executionID string,
	workflowID string,
) *DistributedParallelStep {
	return &DistributedParallelStep{
		StepName:    stepName,
		Agents:      agents,
		InputFn:     inputFn,
		OutputKeys:  outputKeys,
		Queue:       queue,
		ExecutionID: executionID,
		WorkflowID:  workflowID,
		MaxWorkers:  len(agents), // Default: one worker per agent
		Timeout:     5 * time.Minute,
	}
}

// Execute executes agents in parallel across distributed workers.
func (d *DistributedParallelStep) Execute(ctx context.Context, wf *orchestration.Workflow) (*orchestration.AgentResult, error) {
	// Get inputs
	inputs := d.InputFn(wf.State)

	if len(inputs) != len(d.Agents) {
		return nil, fmt.Errorf("input count (%d) does not match agent count (%d)", len(inputs), len(d.Agents))
	}

	// Create tasks for each agent
	taskIDs := make([]string, len(d.Agents))
	for i, agent := range d.Agents {
		taskID := fmt.Sprintf("%s-%s-%d-%d", d.ExecutionID, d.StepName, i, time.Now().UnixNano())
		taskIDs[i] = taskID

		task := &WorkflowTask{
			ID:            taskID,
			ExecutionID:   d.ExecutionID,
			WorkflowID:    d.WorkflowID,
			StepIndex:     i,
			StepName:      fmt.Sprintf("%s-agent-%d", d.StepName, i),
			WorkflowState: map[string]any{"input": inputs[i], "agent": agent.Name},
			Priority:      5,
			CreatedAt:     time.Now(),
			Timeout:       d.Timeout,
		}

		if err := d.Queue.Enqueue(ctx, task); err != nil {
			return nil, fmt.Errorf("failed to enqueue task %d: %w", i, err)
		}
	}

	// Subscribe to results
	resultChan, err := d.Queue.SubscribeResults(ctx, d.ExecutionID)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to results: %w", err)
	}

	// Collect results
	results := make([]*orchestration.AgentResult, len(d.Agents))
	completedCount := 0

	timeout := d.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for completedCount < len(d.Agents) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-resultChan:
			if result == nil {
				continue
			}

			// Find matching task
			for i, taskID := range taskIDs {
				if result.TaskID == taskID {
					if result.Error != "" {
						return nil, fmt.Errorf("agent %d failed: %s", i, result.Error)
					}
					results[i] = result.AgentResult
					completedCount++
					break
				}
			}
		case <-timer.C:
			return nil, fmt.Errorf("distributed parallel execution timeout after %v", timeout)
		}
	}

	// Update workflow state with all outputs
	for i, result := range results {
		if i < len(d.OutputKeys) {
			wf.State[d.OutputKeys[i]] = result.Output
		}
	}

	// Return first result as representative
	return results[0], nil
}

// Name returns the step name.
func (d *DistributedParallelStep) Name() string {
	return d.StepName
}
