// Package streaming provides real-time progress updates for AI workflow orchestration.
//
// Stability: stable — this package follows semantic versioning guarantees.
// Breaking changes require a major version bump.
package streaming

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/birdordev/plumego/x/ai/orchestration"
	"github.com/birdordev/plumego/x/ai/sse"
)

// ProgressStatus represents the status of a workflow step.
type ProgressStatus string

const (
	StatusStarted   ProgressStatus = "started"
	StatusCompleted ProgressStatus = "completed"
	StatusFailed    ProgressStatus = "failed"
	StatusCancelled ProgressStatus = "cancelled"
)

var (
	// ErrWorkflowIDRequired is returned when registering a stream without a workflow ID.
	ErrWorkflowIDRequired = errors.New("streaming: workflow ID is required")

	// ErrStreamRequired is returned when registering a nil SSE stream.
	ErrStreamRequired = errors.New("streaming: stream is required")
)

// ProgressUpdate represents a real-time progress update for a workflow step.
type ProgressUpdate struct {
	// Workflow identification
	WorkflowID string `json:"workflow_id"`
	SessionID  string `json:"session_id,omitempty"`

	// Step information
	StepName  string `json:"step_name"`
	StepIndex int    `json:"step_index"`
	StepType  string `json:"step_type"` // "sequential", "parallel", "conditional", "retry"

	// Progress details
	Status    ProgressStatus `json:"status"`
	Progress  float64        `json:"progress"` // 0.0 to 1.0
	Message   string         `json:"message,omitempty"`
	Timestamp time.Time      `json:"timestamp"`

	// Results (only for completed status)
	Result *orchestration.AgentResult `json:"result,omitempty"`
	Error  string                     `json:"error,omitempty"`

	// Parallel step details (when applicable)
	ParallelIndex int `json:"parallel_index,omitempty"` // Index within parallel step
	ParallelTotal int `json:"parallel_total,omitempty"` // Total parallel agents
}

// StreamConfig configures streaming behavior.
type StreamConfig struct {
	// Enable progress updates
	EnableProgress bool

	// Enable detailed step logging
	EnableStepLogs bool

	// Send keep-alive events
	KeepAlive time.Duration

	// Buffer size for progress channel
	BufferSize int
}

// DefaultStreamConfig returns default streaming configuration.
func DefaultStreamConfig() *StreamConfig {
	return &StreamConfig{
		EnableProgress: true,
		EnableStepLogs: true,
		KeepAlive:      15 * time.Second,
		BufferSize:     100,
	}
}

// StreamManager manages active SSE streams for workflows.
type StreamManager struct {
	streams map[string]*sse.Stream
	mu      sync.RWMutex
}

// NewStreamManager creates a new stream manager.
func NewStreamManager() *StreamManager {
	return &StreamManager{
		streams: make(map[string]*sse.Stream),
	}
}

// RegisterE registers a new stream for a workflow and reports invalid input.
func (sm *StreamManager) Register(workflowID string, stream *sse.Stream) error {
	if workflowID == "" {
		return ErrWorkflowIDRequired
	}
	if stream == nil {
		return ErrStreamRequired
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.streams[workflowID] = stream
	return nil
}

// Unregister removes a stream.
func (sm *StreamManager) Unregister(workflowID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	delete(sm.streams, workflowID)
}

// Get retrieves a stream by workflow ID.
func (sm *StreamManager) Get(workflowID string) (*sse.Stream, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	stream, ok := sm.streams[workflowID]
	return stream, ok
}

// SendUpdate sends a progress update to the stream.
func (sm *StreamManager) SendUpdate(workflowID string, update *ProgressUpdate) error {
	stream, ok := sm.Get(workflowID)
	if !ok {
		return fmt.Errorf("stream not found: %s", workflowID)
	}

	// Marshal update to JSON
	jsonData, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("failed to marshal update: %w", err)
	}

	// SendJSON expects event type and JSON string
	return stream.SendJSON("progress", string(jsonData))
}

// Close closes a stream and unregisters it.
func (sm *StreamManager) Close(workflowID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	stream, ok := sm.streams[workflowID]
	if !ok {
		return nil
	}

	delete(sm.streams, workflowID)
	return stream.Close()
}

// Count returns the number of active streams.
func (sm *StreamManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return len(sm.streams)
}

// StreamingEngine wraps the orchestration engine with streaming capabilities.
type StreamingEngine struct {
	engine    *orchestration.Engine
	streamMgr *StreamManager
	config    *StreamConfig
}

// NewStreamingEngine creates a new streaming engine.
func NewStreamingEngine(engine *orchestration.Engine, config *StreamConfig) *StreamingEngine {
	if config == nil {
		config = DefaultStreamConfig()
	}

	return &StreamingEngine{
		engine:    engine,
		streamMgr: NewStreamManager(),
		config:    config,
	}
}

// StreamManager returns the stream manager.
func (se *StreamingEngine) StreamManager() *StreamManager {
	return se.streamMgr
}

// ExecuteStreaming executes a workflow with real-time progress updates.
func (se *StreamingEngine) ExecuteStreaming(
	ctx context.Context,
	workflow *orchestration.Workflow,
	workflowID string,
	stream *sse.Stream,
) ([]*orchestration.AgentResult, error) {
	// Register stream
	if err := se.streamMgr.Register(workflowID, stream); err != nil {
		return nil, err
	}
	defer se.streamMgr.Unregister(workflowID)

	// Send initial event
	se.sendUpdate(workflowID, &ProgressUpdate{
		WorkflowID: workflowID,
		StepName:   "workflow",
		Status:     StatusStarted,
		Progress:   0.0,
		Message:    fmt.Sprintf("Starting workflow: %s", workflow.Name),
		Timestamp:  time.Now(),
	})

	// Execute workflow with progress tracking
	result, err := se.executeWithProgress(ctx, workflow, workflowID)

	// Send final event
	if err != nil {
		se.sendUpdate(workflowID, &ProgressUpdate{
			WorkflowID: workflowID,
			StepName:   "workflow",
			Status:     StatusFailed,
			Progress:   1.0,
			Error:      err.Error(),
			Timestamp:  time.Now(),
		})
		return nil, err
	}

	se.sendUpdate(workflowID, &ProgressUpdate{
		WorkflowID: workflowID,
		StepName:   "workflow",
		Status:     StatusCompleted,
		Progress:   1.0,
		Message:    "Workflow completed successfully",
		Timestamp:  time.Now(),
	})

	return result, nil
}

// executeWithProgress executes the workflow with progress updates.
func (se *StreamingEngine) executeWithProgress(
	ctx context.Context,
	workflow *orchestration.Workflow,
	workflowID string,
) ([]*orchestration.AgentResult, error) {
	totalSteps := len(workflow.Steps)
	if totalSteps == 0 {
		return []*orchestration.AgentResult{}, nil
	}

	startTime := time.Now()
	results := make([]*orchestration.AgentResult, 0, totalSteps)

	for idx, step := range workflow.Steps {
		// Send step started event
		se.sendStepUpdate(workflowID, step, idx, totalSteps, StatusStarted, nil, nil)

		// Execute step
		result, err := step.Execute(ctx, workflow)

		// Calculate progress
		progress := float64(idx+1) / float64(totalSteps)

		// Send step completed/failed event
		if err != nil {
			se.sendStepUpdate(workflowID, step, idx, totalSteps, StatusFailed, result, err)
			return results, err
		}

		// Store result
		if result != nil {
			results = append(results, result)
		}

		se.sendStepUpdate(workflowID, step, idx, totalSteps, StatusCompleted, result, nil)

		// Update overall progress
		se.sendUpdate(workflowID, &ProgressUpdate{
			WorkflowID: workflowID,
			StepName:   "workflow",
			Status:     StatusStarted,
			Progress:   progress,
			Message:    fmt.Sprintf("Completed step %d of %d", idx+1, totalSteps),
			Timestamp:  time.Now(),
			Result:     result,
		})
	}

	_ = time.Since(startTime) // Track duration if needed

	return results, nil
}

// sendStepUpdate sends a progress update for a specific step.
func (se *StreamingEngine) sendStepUpdate(
	workflowID string,
	step orchestration.Step,
	stepIndex int,
	totalSteps int,
	status ProgressStatus,
	result *orchestration.AgentResult,
	err error,
) {
	stepName := getStepName(step)
	stepType := getStepType(step)

	progress := float64(stepIndex) / float64(totalSteps)
	if status == StatusCompleted {
		progress = float64(stepIndex+1) / float64(totalSteps)
	}

	update := &ProgressUpdate{
		WorkflowID: workflowID,
		StepName:   stepName,
		StepIndex:  stepIndex,
		StepType:   stepType,
		Status:     status,
		Progress:   progress,
		Timestamp:  time.Now(),
		Result:     result,
	}

	if err != nil {
		update.Error = err.Error()
	}

	se.sendUpdate(workflowID, update)
}

// sendUpdate sends a progress update.
func (se *StreamingEngine) sendUpdate(workflowID string, update *ProgressUpdate) {
	if !se.config.EnableProgress {
		return
	}

	if err := se.streamMgr.SendUpdate(workflowID, update); err != nil {
		// Log error but don't fail the workflow
		// In production, use proper logging
		_ = err
	}
}

// getStepName extracts the step name from a step.
func getStepName(step orchestration.Step) string {
	switch s := step.(type) {
	case *orchestration.SequentialStep:
		if s.StepName != "" {
			return s.StepName
		}
		return "sequential"
	case *orchestration.ParallelStep:
		if s.StepName != "" {
			return s.StepName
		}
		return "parallel"
	case *orchestration.ConditionalStep:
		if s.StepName != "" {
			return s.StepName
		}
		return "conditional"
	case *orchestration.RetryStep:
		return "retry"
	default:
		return "unknown"
	}
}

// getStepType returns the step type as a string.
func getStepType(step orchestration.Step) string {
	switch step.(type) {
	case *orchestration.SequentialStep:
		return "sequential"
	case *orchestration.ParallelStep:
		return "parallel"
	case *orchestration.ConditionalStep:
		return "conditional"
	case *orchestration.RetryStep:
		return "retry"
	default:
		return "unknown"
	}
}

// MarshalJSON implements json.Marshaler for ProgressUpdate.
func (pu *ProgressUpdate) MarshalJSON() ([]byte, error) {
	type Alias ProgressUpdate
	return json.Marshal(&struct {
		*Alias
		Timestamp string `json:"timestamp"`
	}{
		Alias:     (*Alias)(pu),
		Timestamp: pu.Timestamp.Format(time.RFC3339),
	})
}
