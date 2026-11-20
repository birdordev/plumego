package distributed

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	kv "github.com/birdordev/plumego/store/kv"
	"github.com/birdordev/plumego/x/ai/orchestration"
)

func TestKVPersistence_Workflow(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()
	store, err := kv.NewKVStore(kv.Config{
		DataDir: filepath.Join(tmpDir, "test"),
	})
	if err != nil {
		t.Fatalf("Failed to create KVStore: %v", err)
	}
	defer store.Close()

	persistence := NewKVPersistence(store)

	t.Run("SaveAndLoad", func(t *testing.T) {
		wf := &orchestration.Workflow{
			ID:          "wf-1",
			Name:        "Test Workflow",
			Description: "Test Description",
			Steps:       []orchestration.Step{},
		}

		err := persistence.SaveWorkflow(ctx, wf)
		if err != nil {
			t.Fatalf("SaveWorkflow failed: %v", err)
		}

		loaded, err := persistence.LoadWorkflow(ctx, "wf-1")
		if err != nil {
			t.Fatalf("LoadWorkflow failed: %v", err)
		}

		if loaded.ID != wf.ID {
			t.Errorf("expected ID %s, got %s", wf.ID, loaded.ID)
		}
		if loaded.Name != wf.Name {
			t.Errorf("expected name %s, got %s", wf.Name, loaded.Name)
		}
		if loaded.Description != wf.Description {
			t.Errorf("expected description %s, got %s", wf.Description, loaded.Description)
		}
	})

	t.Run("SaveNilWorkflow", func(t *testing.T) {
		err := persistence.SaveWorkflow(ctx, nil)
		if err == nil {
			t.Error("expected error for nil workflow")
		}
	})

	t.Run("LoadNonexistent", func(t *testing.T) {
		_, err := persistence.LoadWorkflow(ctx, "nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent workflow")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		wf := &orchestration.Workflow{
			ID:   "wf-delete",
			Name: "Delete Test",
		}

		err := persistence.SaveWorkflow(ctx, wf)
		if err != nil {
			t.Fatalf("SaveWorkflow failed: %v", err)
		}

		err = persistence.DeleteWorkflow(ctx, "wf-delete")
		if err != nil {
			t.Fatalf("DeleteWorkflow failed: %v", err)
		}

		_, err = persistence.LoadWorkflow(ctx, "wf-delete")
		if err == nil {
			t.Error("expected error after deletion")
		}
	})
}

func TestKVPersistence_Snapshot(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()
	store, _ := kv.NewKVStore(kv.Config{DataDir: filepath.Join(tmpDir, "test")})
	defer store.Close()

	persistence := NewKVPersistence(store)

	t.Run("SaveAndLoad", func(t *testing.T) {
		snapshot := &ExecutionSnapshot{
			ExecutionID:    "exec-1",
			WorkflowID:     "wf-1",
			State:          map[string]any{"key": "value"},
			CompletedSteps: []int{0, 1},
			CurrentStep:    2,
			Results:        []*orchestration.AgentResult{},
			Status:         StatusRunning,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			TTL:            time.Hour,
		}

		err := persistence.SaveSnapshot(ctx, snapshot)
		if err != nil {
			t.Fatalf("SaveSnapshot failed: %v", err)
		}

		loaded, err := persistence.LoadSnapshot(ctx, "exec-1")
		if err != nil {
			t.Fatalf("LoadSnapshot failed: %v", err)
		}

		if loaded.ExecutionID != snapshot.ExecutionID {
			t.Errorf("expected execution ID %s, got %s", snapshot.ExecutionID, loaded.ExecutionID)
		}
		if loaded.WorkflowID != snapshot.WorkflowID {
			t.Errorf("expected workflow ID %s, got %s", snapshot.WorkflowID, loaded.WorkflowID)
		}
		if loaded.Status != snapshot.Status {
			t.Errorf("expected status %s, got %s", snapshot.Status, loaded.Status)
		}
		if len(loaded.CompletedSteps) != len(snapshot.CompletedSteps) {
			t.Errorf("expected %d completed steps, got %d", len(snapshot.CompletedSteps), len(loaded.CompletedSteps))
		}
	})

	t.Run("SaveNilSnapshot", func(t *testing.T) {
		err := persistence.SaveSnapshot(ctx, nil)
		if err == nil {
			t.Error("expected error for nil snapshot")
		}
	})

	t.Run("UpdateSnapshot", func(t *testing.T) {
		snapshot := &ExecutionSnapshot{
			ExecutionID: "exec-update",
			WorkflowID:  "wf-1",
			State:       map[string]any{},
			Status:      StatusRunning,
			CreatedAt:   time.Now(),
			TTL:         time.Hour,
		}

		err := persistence.SaveSnapshot(ctx, snapshot)
		if err != nil {
			t.Fatalf("SaveSnapshot failed: %v", err)
		}

		// Update status
		snapshot.Status = StatusCompleted
		snapshot.CompletedSteps = []int{0, 1, 2}

		err = persistence.SaveSnapshot(ctx, snapshot)
		if err != nil {
			t.Fatalf("SaveSnapshot update failed: %v", err)
		}

		loaded, err := persistence.LoadSnapshot(ctx, "exec-update")
		if err != nil {
			t.Fatalf("LoadSnapshot failed: %v", err)
		}

		if loaded.Status != StatusCompleted {
			t.Errorf("expected status %s, got %s", StatusCompleted, loaded.Status)
		}
		if len(loaded.CompletedSteps) != 3 {
			t.Errorf("expected 3 completed steps, got %d", len(loaded.CompletedSteps))
		}
	})

	t.Run("Delete", func(t *testing.T) {
		snapshot := &ExecutionSnapshot{
			ExecutionID: "exec-delete",
			WorkflowID:  "wf-1",
			State:       map[string]any{},
			Status:      StatusCompleted,
			CreatedAt:   time.Now(),
			TTL:         time.Hour,
		}

		err := persistence.SaveSnapshot(ctx, snapshot)
		if err != nil {
			t.Fatalf("SaveSnapshot failed: %v", err)
		}

		err = persistence.DeleteSnapshot(ctx, "exec-delete")
		if err != nil {
			t.Fatalf("DeleteSnapshot failed: %v", err)
		}

		_, err = persistence.LoadSnapshot(ctx, "exec-delete")
		if err == nil {
			t.Error("expected error after deletion")
		}
	})
}

func TestKVPersistence_StepResults(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()
	store, _ := kv.NewKVStore(kv.Config{DataDir: filepath.Join(tmpDir, "test")})
	defer store.Close()

	persistence := NewKVPersistence(store)

	t.Run("SaveAndLoad", func(t *testing.T) {
		result := &orchestration.AgentResult{
			AgentID:   "agent-1",
			Input:     "test input",
			Output:    "test output",
			StartTime: time.Now(),
			EndTime:   time.Now(),
			Duration:  time.Second,
		}

		err := persistence.SaveStepResult(ctx, "exec-1", 0, result)
		if err != nil {
			t.Fatalf("SaveStepResult failed: %v", err)
		}

		// Save another result
		result2 := &orchestration.AgentResult{
			AgentID:   "agent-2",
			Input:     "test input 2",
			Output:    "test output 2",
			StartTime: time.Now(),
			EndTime:   time.Now(),
			Duration:  2 * time.Second,
		}

		err = persistence.SaveStepResult(ctx, "exec-1", 1, result2)
		if err != nil {
			t.Fatalf("SaveStepResult failed: %v", err)
		}

		results, err := persistence.LoadStepResults(ctx, "exec-1")
		if err != nil || len(results) != 2 ||
			results[0].AgentID != "agent-1" || results[1].AgentID != "agent-2" {
			t.Fatalf("ordered results = %+v, %v", results, err)
		}
	})

	t.Run("SaveNilResult", func(t *testing.T) {
		err := persistence.SaveStepResult(ctx, "exec-1", 0, nil)
		if err == nil {
			t.Error("expected error for nil result")
		}
	})
}

func TestExecutionStatus(t *testing.T) {
	tests := []struct {
		status   ExecutionStatus
		expected string
	}{
		{StatusPending, "pending"},
		{StatusRunning, "running"},
		{StatusPaused, "paused"},
		{StatusCompleted, "completed"},
		{StatusFailed, "failed"},
		{StatusCancelled, "cancelled"},
	}

	for _, tt := range tests {
		if string(tt.status) != tt.expected {
			t.Errorf("expected status %s, got %s", tt.expected, string(tt.status))
		}
	}
}

func TestKVPersistence_Close(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := kv.NewKVStore(kv.Config{DataDir: filepath.Join(tmpDir, "test")})
	persistence := NewKVPersistence(store)

	err := persistence.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestKVPersistenceLoadWorkflowRejectsInvalidFields(t *testing.T) {
	store, err := kv.NewKVStore(kv.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	p := NewKVPersistence(store)
	for _, tt := range []struct{ name, raw string }{
		{"wrong type", "{\"id\":12,\"name\":\"Example\"}"},
		{"missing ID", "{\"name\":\"Example\"}"},
		{"malformed JSON", "{"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const id = "example"
			if err := store.Set(prefixWorkflow+id, []byte(tt.raw), 0); err != nil {
				t.Fatal(err)
			}
			wf, err := p.LoadWorkflow(t.Context(), id)
			if wf != nil || err == nil || !strings.Contains(err.Error(), id) {
				t.Fatalf("LoadWorkflow = %v, %v; want error with ID context", wf, err)
			}
			switch tt.name {
			case "wrong type":
				var decodeErr *json.UnmarshalTypeError
				if !errors.As(err, &decodeErr) || decodeErr.Field != "id" {
					t.Errorf("expected an ID field error, got %v", err)
				}
			case "malformed JSON":
				var syntaxErr *json.SyntaxError
				if !errors.As(err, &syntaxErr) {
					t.Errorf("expected a JSON syntax error, got %v", err)
				}
			}
		})
	}
	if wf, err := p.LoadWorkflow(t.Context(), "missing"); err == nil || wf != nil || errors.Unwrap(err) == nil {
		t.Errorf("missing record = %v, %v; want wrapped storage error", wf, err)
	}
}

type scanTestBackend struct {
	kvBackend
	keys   func(context.Context) ([]string, error)
	read   func(string) ([]byte, error)
	remove func(string) error
	write  func(string, []byte, time.Duration) error
}

func (s scanTestBackend) KeysContext(ctx context.Context) ([]string, error) { return s.keys(ctx) }
func (s scanTestBackend) Get(key string) ([]byte, error) {
	if s.read != nil {
		return s.read(key)
	}
	return s.kvBackend.Get(key)
}
func (s scanTestBackend) Set(key string, value []byte, ttl time.Duration) error {
	if s.write != nil {
		return s.write(key, value, ttl)
	}
	return s.kvBackend.Set(key, value, ttl)
}
func (s scanTestBackend) Delete(key string) error {
	if s.remove != nil {
		return s.remove(key)
	}
	return s.kvBackend.Delete(key)
}

func TestKVPersistenceLoadsOrderedStepResults(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, err := kv.NewKVStore(kv.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	saved := NewKVPersistence(store)
	for _, item := range []struct {
		id     string
		index  int
		output string
	}{{"run", 10, "ten"}, {"run", 2, "two"}, {"run:child", 0, "other"}} {
		if err := saved.SaveStepResult(ctx, item.id, item.index, &orchestration.AgentResult{Output: item.output}); err != nil {
			t.Fatal(err)
		}
	}
	if err := saved.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := kv.NewKVStore(kv.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	results, err := NewKVPersistence(reopened).LoadStepResults(ctx, "run")
	if err != nil || len(results) != 2 || results[0].Output != "two" || results[1].Output != "ten" {
		t.Fatalf("ordered results = %+v, %v", results, err)
	}

	wrapped := NewKVPersistence(struct{ kvBackend }{reopened})
	if _, err := wrapped.LoadStepResults(ctx, "run"); err == nil || !strings.Contains(err.Error(), "key enumeration") {
		t.Fatalf("non-enumerable backend error = %v", err)
	}
	sentinel := errors.New("storage error")
	for _, scanFails := range []bool{false, true} {
		backend := scanTestBackend{
			keys: func(received context.Context) ([]string, error) {
				if received != ctx {
					t.Error("scan received a different context")
				}
				if scanFails {
					return nil, sentinel
				}
				return []string{prefixStepResult + "run:2"}, nil
			},
			read: func(string) ([]byte, error) { return nil, sentinel },
		}
		if _, err := NewKVPersistence(backend).LoadStepResults(ctx, "run"); !errors.Is(err, sentinel) {
			t.Fatalf("scanFails=%t error = %v", scanFails, err)
		}
	}
}

func TestKVPersistenceListsOnlyLiveWorkflowSnapshots(t *testing.T) {
	store, err := kv.NewKVStore(kv.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	p := NewKVPersistence(store)
	ctx := t.Context()
	legacy := ExecutionSnapshot{ExecutionID: "a:old", WorkflowID: "project:workflow"}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(prefixSnapshot+legacy.ExecutionID, raw, 0); err != nil {
		t.Fatal(err)
	}
	const staleIndex = "distributed:execution:project:workflow:missing"
	if err := store.Set(staleIndex, []byte("missing"), 0); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []*ExecutionSnapshot{
		{ExecutionID: "z:new", WorkflowID: "project:workflow"},
		{ExecutionID: "other-run", WorkflowID: "project"},
	} {
		if err := p.SaveSnapshot(ctx, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	checkIDs := func(want ...string) {
		t.Helper()
		got, err := p.ListExecutions(ctx, "project:workflow")
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("ListExecutions=%v,%v want %v", got, err, want)
		}
	}
	checkIDs("a:old", "z:new")
	if err := p.DeleteSnapshot(ctx, "z:new"); err != nil {
		t.Fatal(err)
	}
	checkIDs("a:old")
	keys, err := store.KeysContext(ctx)
	if err != nil || !slices.Equal(keys, []string{staleIndex, prefixSnapshot + "a:old", prefixSnapshot + "other-run"}) {
		t.Fatalf("storage keys = %v, %v", keys, err)
	}
}

func TestKVPersistenceCleanupPreservesRefreshedSnapshots(t *testing.T) {
	store, err := kv.NewKVStore(kv.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := t.Context()
	cutoff := time.Now().Add(-time.Hour)
	for _, fixture := range []struct {
		id      string
		updated time.Time
	}{
		{"expired", cutoff.Add(-time.Second)},
		{"zz-failing", cutoff.Add(-time.Second)},
		{"refreshed", cutoff.Add(-time.Second)},
		{"boundary", cutoff},
	} {
		raw, err := json.Marshal(ExecutionSnapshot{ExecutionID: fixture.id, UpdatedAt: fixture.updated})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Set(prefixSnapshot+fixture.id, raw, 0); err != nil {
			t.Fatal(err)
		}
	}
	scanned := make(chan struct{})
	resume := make(chan struct{})
	blockScan := true
	injected := errors.New("delete failure")
	failDelete := true
	backend := scanTestBackend{
		kvBackend: store,
		keys: func(ctx context.Context) ([]string, error) {
			keys, err := store.KeysContext(ctx)
			if blockScan {
				close(scanned)
				<-resume
			}
			return keys, err
		},
		remove: func(key string) error {
			if failDelete && key == prefixSnapshot+"zz-failing" {
				return injected
			}
			return store.Delete(key)
		},
	}
	p := NewKVPersistence(backend)
	type cleanupResult struct {
		count int
		err   error
	}
	done := make(chan cleanupResult, 1)
	go func() {
		n, err := p.CleanupExpired(ctx, cutoff)
		done <- cleanupResult{n, err}
	}()
	<-scanned
	refreshErr := p.SaveSnapshot(ctx, &ExecutionSnapshot{ExecutionID: "refreshed"})
	close(resume)
	result := <-done
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	if result.count != 1 || !errors.Is(result.err, injected) {
		t.Fatalf("first cleanup = %+v", result)
	}
	blockScan = false
	failDelete = false
	if count, err := p.CleanupExpired(ctx, cutoff); count != 1 || err != nil {
		t.Fatalf("retry cleanup = %d, %v", count, err)
	}
	for _, id := range []string{"boundary", "refreshed"} {
		if _, err := p.LoadSnapshot(ctx, id); err != nil {
			t.Errorf("retained %s: %v", id, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if count, err := p.CleanupExpired(cancelled, time.Now()); count != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cleanup = %d, %v", count, err)
	}
}

func TestKVPersistenceSerializesSnapshotCleanupAndRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		cutoff := time.Now()
		key := prefixSnapshot + "run"
		raw, err := json.Marshal(ExecutionSnapshot{ExecutionID: "run", UpdatedAt: cutoff.Add(-time.Minute)})
		if err != nil {
			t.Fatal(err)
		}

		reading := make(chan struct{})
		resume := make(chan struct{})
		firstRead := true
		var changes []string
		p := NewKVPersistence(scanTestBackend{
			keys: func(context.Context) ([]string, error) { return []string{key}, nil },
			read: func(string) ([]byte, error) {
				if firstRead {
					firstRead = false
					close(reading)
					<-resume
				}
				return raw, nil
			},
			remove: func(string) error { changes = append(changes, "delete"); raw = nil; return nil },
			write: func(_ string, value []byte, _ time.Duration) error {
				changes = append(changes, "save")
				raw = value
				return nil
			},
		})

		type result struct {
			n   int
			err error
		}
		cleaned := make(chan result, 1)
		go func() { n, err := p.CleanupExpired(ctx, cutoff); cleaned <- result{n, err} }()
		<-reading

		canceledCtx, cancel := context.WithCancel(ctx)
		canceledRefresh := make(chan error, 1)
		go func() { canceledRefresh <- p.SaveSnapshot(canceledCtx, &ExecutionSnapshot{ExecutionID: "run"}) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case err := <-canceledRefresh:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("waiting refresh = %v", err)
			}
		default:
			t.Error("refresh did not respond to cancellation while waiting")
		}

		refreshed := make(chan error, 1)
		go func() {
			refreshed <- p.SaveSnapshot(ctx, &ExecutionSnapshot{ExecutionID: "run", Status: StatusCompleted})
		}()
		synctest.Wait()
		if len(changes) != 0 {
			t.Errorf("snapshot changed while cleanup was reading: %v", changes)
		}
		close(resume)

		if got := <-cleaned; got.n != 1 || got.err != nil {
			t.Fatalf("cleanup = %+v", got)
		}
		if err := <-refreshed; err != nil {
			t.Fatal(err)
		}
		got, err := p.LoadSnapshot(ctx, "run")
		if err != nil || got.Status != StatusCompleted || !slices.Equal(changes, []string{"delete", "save"}) {
			t.Fatalf("snapshot=%+v error=%v operations=%v", got, err, changes)
		}
	})
}
