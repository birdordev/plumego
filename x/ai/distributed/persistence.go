// Package distributed provides distributed workflow execution capabilities.
package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/birdordev/plumego/x/ai/orchestration"
)

// ExecutionStatus represents the status of a workflow execution.
type ExecutionStatus string

const (
	// StatusPending indicates the execution is queued but not yet started.
	StatusPending ExecutionStatus = "pending"
	// StatusRunning indicates the execution is currently in progress.
	StatusRunning ExecutionStatus = "running"
	// StatusPaused indicates the execution has been paused.
	StatusPaused ExecutionStatus = "paused"
	// StatusCompleted indicates the execution completed successfully.
	StatusCompleted ExecutionStatus = "completed"
	// StatusFailed indicates the execution failed.
	StatusFailed ExecutionStatus = "failed"
	// StatusCancelled indicates the execution was cancelled.
	StatusCancelled ExecutionStatus = "cancelled"
)

// ExecutionSnapshot represents a point-in-time snapshot of workflow execution state.
type ExecutionSnapshot struct {
	ExecutionID    string                       `json:"execution_id"`
	WorkflowID     string                       `json:"workflow_id"`
	State          map[string]any               `json:"state"`
	CompletedSteps []int                        `json:"completed_steps"` // Indices of completed steps
	CurrentStep    int                          `json:"current_step"`    // Index of current step
	Results        []*orchestration.AgentResult `json:"results"`
	Status         ExecutionStatus              `json:"status"`
	Error          string                       `json:"error,omitempty"`
	CreatedAt      time.Time                    `json:"created_at"`
	UpdatedAt      time.Time                    `json:"updated_at"`
	TTL            time.Duration                `json:"ttl,omitempty"`
}

// WorkflowPersistence defines the interface for persisting workflow definitions and execution state.
type WorkflowPersistence interface {
	// SaveWorkflow persists a workflow definition.
	SaveWorkflow(ctx context.Context, wf *orchestration.Workflow) error

	// LoadWorkflow loads a workflow definition by ID.
	LoadWorkflow(ctx context.Context, id string) (*orchestration.Workflow, error)

	// DeleteWorkflow removes a workflow definition.
	DeleteWorkflow(ctx context.Context, id string) error

	// SaveSnapshot persists an execution state snapshot.
	SaveSnapshot(ctx context.Context, snapshot *ExecutionSnapshot) error

	// LoadSnapshot loads an execution state snapshot by execution ID.
	LoadSnapshot(ctx context.Context, executionID string) (*ExecutionSnapshot, error)

	// DeleteSnapshot removes an execution snapshot.
	DeleteSnapshot(ctx context.Context, executionID string) error

	// SaveStepResult persists a step execution result.
	SaveStepResult(ctx context.Context, executionID string, stepIndex int, result *orchestration.AgentResult) error

	// LoadStepResults loads all step results for an execution.
	LoadStepResults(ctx context.Context, executionID string) ([]*orchestration.AgentResult, error)

	// ListExecutions returns all execution IDs for a workflow.
	ListExecutions(ctx context.Context, workflowID string) ([]string, error)

	// CleanupExpired removes expired execution snapshots.
	CleanupExpired(ctx context.Context, before time.Time) (int, error)

	// Close closes the persistence backend.
	Close() error
}

// kvBackend is the narrow storage interface used by KVPersistence.
// *store/kv.KVStore satisfies this interface at the call site.
type kvBackend interface {
	Set(key string, value []byte, ttl time.Duration) error
	Get(key string) ([]byte, error)
	Delete(key string) error
	Close() error
}

// KVPersistence implements WorkflowPersistence using a key-value store.
type KVPersistence struct {
	store kvBackend
}

// Storage key prefixes
const (
	prefixWorkflow   = "distributed:workflow:"
	prefixSnapshot   = "distributed:snapshot:"
	prefixStepResult = "distributed:step:"
	prefixExecution  = "distributed:execution:"
)

// NewKVPersistence creates a new KV-based workflow persistence layer.
func NewKVPersistence(store kvBackend) *KVPersistence {
	return &KVPersistence{
		store: store,
	}
}

// SaveWorkflow persists a workflow definition.
func (p *KVPersistence) SaveWorkflow(ctx context.Context, wf *orchestration.Workflow) error {
	if wf == nil || wf.ID == "" {
		return fmt.Errorf("invalid workflow: nil or empty ID")
	}

	// Serialize workflow (excluding Steps as they may contain functions)
	data := map[string]any{
		"id":          wf.ID,
		"name":        wf.Name,
		"description": wf.Description,
		"step_count":  len(wf.Steps),
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow: %w", err)
	}

	key := prefixWorkflow + wf.ID
	return p.store.Set(key, jsonData, 0) // No TTL for workflow definitions
}

// LoadWorkflow loads a workflow definition by ID.
func (p *KVPersistence) LoadWorkflow(ctx context.Context, id string) (*orchestration.Workflow, error) {
	if id == "" {
		return nil, fmt.Errorf("workflow ID cannot be empty")
	}

	key := prefixWorkflow + id
	data, err := p.store.Get(key)
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow %s: %w", id, err)
	}

	var wfData map[string]any
	if err := json.Unmarshal(data, &wfData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflow: %w", err)
	}

	// Note: Steps are not persisted (they contain functions)
	// Workflow definitions should be registered in the engine separately
	wf := &orchestration.Workflow{
		ID:          wfData["id"].(string),
		Name:        wfData["name"].(string),
		Description: wfData["description"].(string),
		Steps:       []orchestration.Step{}, // Will be populated by engine
		State:       make(map[string]any),
	}

	return wf, nil
}

// DeleteWorkflow removes a workflow definition.
func (p *KVPersistence) DeleteWorkflow(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("workflow ID cannot be empty")
	}

	key := prefixWorkflow + id
	return p.store.Delete(key)
}

// SaveSnapshot persists an execution state snapshot.
func (p *KVPersistence) SaveSnapshot(ctx context.Context, snapshot *ExecutionSnapshot) error {
	if snapshot == nil || snapshot.ExecutionID == "" {
		return fmt.Errorf("invalid snapshot: nil or empty execution ID")
	}

	snapshot.UpdatedAt = time.Now()

	jsonData, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot: %w", err)
	}

	key := prefixSnapshot + snapshot.ExecutionID

	// Set TTL if specified
	ttl := snapshot.TTL
	if ttl == 0 {
		ttl = 24 * time.Hour // Default 24 hour retention
	}

	if err := p.store.Set(key, jsonData, ttl); err != nil {
		return fmt.Errorf("failed to save snapshot: %w", err)
	}

	// Also index by workflow ID for listing
	indexKey := prefixExecution + snapshot.WorkflowID + ":" + snapshot.ExecutionID
	return p.store.Set(indexKey, []byte(snapshot.ExecutionID), ttl)
}

// LoadSnapshot loads an execution state snapshot by execution ID.
func (p *KVPersistence) LoadSnapshot(ctx context.Context, executionID string) (*ExecutionSnapshot, error) {
	if executionID == "" {
		return nil, fmt.Errorf("execution ID cannot be empty")
	}

	key := prefixSnapshot + executionID
	data, err := p.store.Get(key)
	if err != nil {
		return nil, fmt.Errorf("failed to load snapshot %s: %w", executionID, err)
	}

	var snapshot ExecutionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to unmarshal snapshot: %w", err)
	}

	return &snapshot, nil
}

// DeleteSnapshot removes an execution snapshot.
func (p *KVPersistence) DeleteSnapshot(ctx context.Context, executionID string) error {
	if executionID == "" {
		return fmt.Errorf("execution ID cannot be empty")
	}

	key := prefixSnapshot + executionID
	return p.store.Delete(key)
}

// SaveStepResult persists a step execution result.
func (p *KVPersistence) SaveStepResult(ctx context.Context, executionID string, stepIndex int, result *orchestration.AgentResult) error {
	if executionID == "" {
		return fmt.Errorf("execution ID cannot be empty")
	}
	if result == nil {
		return fmt.Errorf("result cannot be nil")
	}

	jsonData, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	key := fmt.Sprintf("%s%s:%d", prefixStepResult, executionID, stepIndex)
	return p.store.Set(key, jsonData, 24*time.Hour) // 24 hour retention
}

// LoadStepResults loads all step results for an execution.
func (p *KVPersistence) LoadStepResults(ctx context.Context, executionID string) ([]*orchestration.AgentResult, error) {
	if executionID == "" {
		return nil, fmt.Errorf("execution ID cannot be empty")
	}

	// Scan for all step results with prefix
	prefix := prefixStepResult + executionID + ":"
	keys, err := p.scanKeys(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to scan step results: %w", err)
	}

	results := make([]*orchestration.AgentResult, 0, len(keys))
	for _, key := range keys {
		data, err := p.store.Get(key)
		if err != nil {
			continue // Skip missing keys
		}

		var result orchestration.AgentResult
		if err := json.Unmarshal(data, &result); err != nil {
			continue // Skip invalid data
		}

		results = append(results, &result)
	}

	return results, nil
}

// ListExecutions returns all execution IDs for a workflow.
func (p *KVPersistence) ListExecutions(ctx context.Context, workflowID string) ([]string, error) {
	if workflowID == "" {
		return nil, fmt.Errorf("workflow ID cannot be empty")
	}

	prefix := prefixExecution + workflowID + ":"
	keys, err := p.scanKeys(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to list executions: %w", err)
	}

	executionIDs := make([]string, 0, len(keys))
	for _, key := range keys {
		// Extract execution ID from key
		parts := strings.Split(key, ":")
		if len(parts) >= 3 {
			executionIDs = append(executionIDs, parts[len(parts)-1])
		}
	}

	return executionIDs, nil
}

// CleanupExpired removes expired execution snapshots.
func (p *KVPersistence) CleanupExpired(ctx context.Context, before time.Time) (int, error) {
	// Scan all snapshots
	keys, err := p.scanKeys(ctx, prefixSnapshot)
	if err != nil {
		return 0, fmt.Errorf("failed to scan snapshots: %w", err)
	}

	count := 0
	for _, key := range keys {
		data, err := p.store.Get(key)
		if err != nil {
			continue
		}

		var snapshot ExecutionSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			continue
		}

		// Delete if updated before the cutoff time
		if snapshot.UpdatedAt.Before(before) {
			if err := p.store.Delete(key); err == nil {
				count++
			}
		}
	}

	return count, nil
}

// Close closes the persistence backend.
func (p *KVPersistence) Close() error {
	if p.store != nil {
		return p.store.Close()
	}
	return nil
}

// scanKeys scans for all keys with a given prefix.
func (p *KVPersistence) scanKeys(ctx context.Context, prefix string) ([]string, error) {
	// Note: This is a simplified implementation
	// In production, use proper key scanning or indexing
	keys := make([]string, 0)

	// The kv.Store interface doesn't have a Scan method, so we'll need to
	// maintain an index or use a different approach in production
	// For now, we'll return empty list - this should be implemented properly
	// based on the actual kv.Store implementation

	return keys, nil
}
