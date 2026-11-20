// Package distributed provides distributed workflow execution capabilities.
package distributed

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
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
// Snapshot mutations are serialized within one instance. Backend operations
// called during a snapshot mutation must return before reentering one.
type KVPersistence struct {
	store          kvBackend
	snapshotWrites chan struct{}
}

// Storage key prefixes
const (
	prefixWorkflow   = "distributed:workflow:"
	prefixSnapshot   = "distributed:snapshot:"
	prefixStepResult = "distributed:step:"
)

// NewKVPersistence creates a new KV-based workflow persistence layer.
func NewKVPersistence(store kvBackend) *KVPersistence {
	return &KVPersistence{
		store:          store,
		snapshotWrites: make(chan struct{}, 1),
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

// LoadWorkflow loads a workflow definition by ID and reports invalid stored records as errors.
func (p *KVPersistence) LoadWorkflow(ctx context.Context, id string) (*orchestration.Workflow, error) {
	if id == "" {
		return nil, fmt.Errorf("workflow ID cannot be empty")
	}

	key := prefixWorkflow + id
	data, err := p.store.Get(key)
	if err != nil {
		return nil, fmt.Errorf("failed to load workflow %s: %w", id, err)
	}

	var wfData struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(data, &wfData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflow %s: %w", id, err)
	}
	if wfData.ID == "" {
		return nil, fmt.Errorf("stored workflow %s has an empty ID", id)
	}

	// Note: Steps are not persisted (they contain functions)
	// Workflow definitions should be registered in the engine separately
	wf := &orchestration.Workflow{
		ID:          wfData.ID,
		Name:        wfData.Name,
		Description: wfData.Description,
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
	if err := ctx.Err(); err != nil {
		return err
	}
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

	if err := p.lockSnapshots(ctx); err != nil {
		return err
	}
	defer p.unlockSnapshots()

	var saveErr error
	if store, ok := p.store.(interface {
		SetContext(context.Context, string, []byte, time.Duration) error
	}); ok {
		saveErr = store.SetContext(ctx, key, jsonData, ttl)
	} else {
		saveErr = p.store.Set(key, jsonData, ttl)
	}
	if saveErr != nil {
		return fmt.Errorf("failed to save snapshot: %w", saveErr)
	}
	return nil
}

// LoadSnapshot loads an execution state snapshot by execution ID.
func (p *KVPersistence) LoadSnapshot(ctx context.Context, executionID string) (*ExecutionSnapshot, error) {
	if executionID == "" {
		return nil, fmt.Errorf("execution ID cannot be empty")
	}
	return p.readSnapshot(ctx, prefixSnapshot+executionID)
}

func (p *KVPersistence) readSnapshot(ctx context.Context, key string) (*ExecutionSnapshot, error) {
	executionID := strings.TrimPrefix(key, prefixSnapshot)
	data, err := p.readValue(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to load snapshot %s: %w", executionID, err)
	}

	var snapshot *ExecutionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to unmarshal snapshot: %w", err)
	}
	if snapshot == nil || executionID == "" || snapshot.ExecutionID != executionID {
		return nil, fmt.Errorf("persisted snapshot does not match execution %s", executionID)
	}
	return snapshot, nil
}

// DeleteSnapshot removes an execution snapshot.
func (p *KVPersistence) DeleteSnapshot(ctx context.Context, executionID string) error {
	if executionID == "" {
		return fmt.Errorf("execution ID cannot be empty")
	}

	if err := p.lockSnapshots(ctx); err != nil {
		return err
	}
	defer p.unlockSnapshots()
	return p.deleteValue(ctx, prefixSnapshot+executionID)
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

// LoadStepResults loads stored results by ascending step index. The backend
// must support KeysContext. Errors reading an enumerated record are returned
// to the caller, including when a record expires during the scan.
func (p *KVPersistence) LoadStepResults(ctx context.Context, executionID string) ([]*orchestration.AgentResult, error) {
	if executionID == "" {
		return nil, fmt.Errorf("execution ID cannot be empty")
	}
	prefix := prefixStepResult + executionID + ":"
	keys, err := p.scanKeys(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to scan step results: %w", err)
	}

	type stepKey struct {
		index int
		key   string
	}
	steps := make([]stepKey, 0, len(keys))
	for _, key := range keys {
		// Another execution ID can extend this prefix with a colon.
		index, err := strconv.Atoi(strings.TrimPrefix(key, prefix))
		if err == nil {
			steps = append(steps, stepKey{index: index, key: key})
		}
	}
	slices.SortFunc(steps, func(a, b stepKey) int {
		return cmp.Compare(a.index, b.index)
	})
	results := make([]*orchestration.AgentResult, 0, len(steps))
	for _, step := range steps {
		data, err := p.readValue(ctx, step.key)
		if err != nil {
			return nil, fmt.Errorf("read step result %d: %w", step.index, err)
		}
		var result *orchestration.AgentResult
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("decode step result %d: %w", step.index, err)
		}
		if result == nil {
			return nil, fmt.Errorf("step result %d is null", step.index)
		}
		results = append(results, result)
	}
	return results, nil
}

// ListExecutions returns sorted execution IDs from live snapshots belonging to
// workflowID. The backend must support KeysContext. Legacy execution index keys
// are left to expire and no longer affect visibility.
func (p *KVPersistence) ListExecutions(ctx context.Context, workflowID string) ([]string, error) {
	if workflowID == "" {
		return nil, fmt.Errorf("workflow ID cannot be empty")
	}
	keys, err := p.scanKeys(ctx, prefixSnapshot)
	if err != nil {
		return nil, fmt.Errorf("failed to list executions: %w", err)
	}

	executionIDs := make([]string, 0, len(keys))
	for _, key := range keys {
		snapshot, err := p.readSnapshot(ctx, key)
		if err != nil {
			return nil, err
		}
		if snapshot.WorkflowID == workflowID {
			executionIDs = append(executionIDs, snapshot.ExecutionID)
		}
	}
	slices.Sort(executionIDs)
	return executionIDs, nil
}

// CleanupExpired removes snapshots updated strictly before the cutoff. The
// backend must support KeysContext. Each read and conditional delete is
// serialized with snapshot writes through this instance. Independent writers
// sharing the backend must coordinate with cleanup. On error the count covers
// the deletions already completed.
func (p *KVPersistence) CleanupExpired(ctx context.Context, before time.Time) (int, error) {
	keys, err := p.scanKeys(ctx, prefixSnapshot)
	if err != nil {
		return 0, fmt.Errorf("failed to scan snapshots: %w", err)
	}

	count := 0
	for _, key := range keys {
		deleted, err := p.cleanupSnapshot(ctx, key, before)
		if err != nil {
			return count, err
		}
		if deleted {
			count++
		}
	}
	return count, nil
}

func (p *KVPersistence) cleanupSnapshot(ctx context.Context, key string, before time.Time) (bool, error) {
	if err := p.lockSnapshots(ctx); err != nil {
		return false, err
	}
	defer p.unlockSnapshots()

	snapshot, err := p.readSnapshot(ctx, key)
	if err != nil {
		return false, err
	}
	if !snapshot.UpdatedAt.Before(before) {
		return false, nil
	}
	if err := p.deleteValue(ctx, key); err != nil {
		return false, fmt.Errorf("delete expired snapshot %s: %w", snapshot.ExecutionID, err)
	}
	return true, nil
}

// lockSnapshots waits for exclusive access without delaying cancellation.
func (p *KVPersistence) lockSnapshots(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case p.snapshotWrites <- struct{}{}:
		if err := ctx.Err(); err != nil {
			p.unlockSnapshots()
			return err
		}
		return nil
	}
}

func (p *KVPersistence) unlockSnapshots() { <-p.snapshotWrites }

// Close closes the persistence backend.
func (p *KVPersistence) Close() error {
	if p.store != nil {
		return p.store.Close()
	}
	return nil
}

func (p *KVPersistence) scanKeys(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store, ok := p.store.(interface {
		KeysContext(context.Context) ([]string, error)
	})
	if !ok {
		return nil, fmt.Errorf("persistence backend does not support key enumeration")
	}
	keys, err := store.KeysContext(ctx)
	if err != nil {
		return nil, err
	}
	matches := make([]string, 0)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.HasPrefix(key, prefix) {
			matches = append(matches, key)
		}
	}
	return matches, ctx.Err()
}

func (p *KVPersistence) readValue(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store, ok := p.store.(interface {
		GetContext(context.Context, string) ([]byte, error)
	}); ok {
		return store.GetContext(ctx, key)
	}
	return p.store.Get(key)
}

func (p *KVPersistence) deleteValue(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store, ok := p.store.(interface {
		DeleteContext(context.Context, string) error
	}); ok {
		return store.DeleteContext(ctx, key)
	}
	return p.store.Delete(key)
}
