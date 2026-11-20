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

// Execute subscribes before dispatch and waits for the result matching the task ID.
// The subscription is canceled when Execute returns.
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

	subscriptionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resultChan, err := r.Queue.SubscribeResults(subscriptionCtx, r.ExecutionID)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to results: %w", err)
	}

	if err := r.Queue.Enqueue(ctx, task); err != nil {
		return nil, fmt.Errorf("failed to enqueue remote task: %w", err)
	}

	// Wait for result with timeout
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result, ok := <-resultChan:
			if !ok {
				return nil, fmt.Errorf("task result subscription closed")
			}
			if result == nil || result.TaskID != task.ID {
				continue
			}
			if result.Error != "" {
				return result.AgentResult, fmt.Errorf("remote execution failed: %s", result.Error)
			}
			for key, value := range result.UpdatedState {
				wf.SetState(key, value)
			}
			return result.AgentResult, nil
		case <-timer.C:
			return nil, fmt.Errorf("remote execution timeout after %v", timeout)
		}
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

// Execute subscribes before dispatch and collects one result per task. It fails
// if the subscription closes before all results arrive or a successful task
// has no agent result. Outputs are stored in agent order on success, and the
// first agent's result is returned. The subscription ends when Execute returns.
func (d *DistributedParallelStep) Execute(ctx context.Context, wf *orchestration.Workflow) (*orchestration.AgentResult, error) {
	if len(d.Agents) == 0 {
		return nil, fmt.Errorf("distributed parallel step requires at least one agent")
	}
	inputs := d.InputFn(wf.State)

	if len(inputs) != len(d.Agents) {
		return nil, fmt.Errorf("input count (%d) does not match agent count (%d)", len(inputs), len(d.Agents))
	}

	subscriptionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resultChan, err := d.Queue.SubscribeResults(subscriptionCtx, d.ExecutionID)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to results: %w", err)
	}

	pending := make(map[string]int, len(d.Agents))
	for i, agent := range d.Agents {
		taskID := fmt.Sprintf("%s-%s-%d-%d", d.ExecutionID, d.StepName, i, time.Now().UnixNano())
		pending[taskID] = i

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

	results := make([]*orchestration.AgentResult, len(d.Agents))

	timeout := d.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for len(pending) > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result, ok := <-resultChan:
			if !ok {
				return nil, fmt.Errorf("task result subscription closed")
			}
			if result == nil {
				continue
			}
			i, waiting := pending[result.TaskID]
			if !waiting {
				continue
			}
			if result.Error != "" {
				return nil, fmt.Errorf("agent %d failed: %s", i, result.Error)
			}
			if result.AgentResult == nil {
				return nil, fmt.Errorf("agent %d returned no result", i)
			}
			results[i] = result.AgentResult
			delete(pending, result.TaskID)
		case <-timer.C:
			return nil, fmt.Errorf("distributed parallel execution timeout after %v", timeout)
		}
	}

	// Update workflow state with all outputs
	for i, result := range results {
		if i < len(d.OutputKeys) {
			wf.SetState(d.OutputKeys[i], result.Output)
		}
	}

	// Return first result as representative
	return results[0], nil
}

// Name returns the step name.
func (d *DistributedParallelStep) Name() string {
	return d.StepName
}
