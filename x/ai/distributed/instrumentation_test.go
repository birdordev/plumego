package distributed

import (
	"path/filepath"
	"testing"
	"time"

	kv "github.com/spcent/plumego/store/kv"
	"github.com/spcent/plumego/x/ai/orchestration"
	"github.com/spcent/plumego/x/messaging/mq"
	"github.com/spcent/plumego/x/messaging/pubsub"
)

func newTestInstrumentedEngine(t *testing.T) (*InstrumentedDistributedEngine, func()) {
	t.Helper()

	broker, err := mq.NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	store, err := kv.NewKVStore(kv.Config{DataDir: filepath.Join(tmpDir, "inst")})
	if err != nil {
		t.Fatalf("NewKVStore: %v", err)
	}

	persistence := NewKVPersistence(store)
	queue := NewMQTaskQueue(broker, persistence, DefaultMQTaskQueueConfig())
	localEngine := orchestration.NewEngine()
	engine := NewDistributedEngine(localEngine, queue, persistence, DefaultEngineConfig())

	collector := NoopMetricRecorder()
	ie := NewInstrumentedDistributedEngine(engine, collector)

	cleanup := func() {
		ie.Close()
		store.Close()
		broker.Close()
	}
	return ie, cleanup
}

func TestInstrumentedEngine_ExecuteAsync_NonexistentWorkflow(t *testing.T) {
	ie, cleanup := newTestInstrumentedEngine(t)
	defer cleanup()

	_, err := ie.ExecuteAsync(t.Context(), "no-such-wf", nil, DefaultExecutionOptions())
	if err == nil {
		t.Error("expected error for nonexistent workflow")
	}
}

func TestInstrumentedEngine_ExecuteSync_NonexistentWorkflow(t *testing.T) {
	ie, cleanup := newTestInstrumentedEngine(t)
	defer cleanup()

	_, err := ie.ExecuteSync(t.Context(), "no-such-wf", nil, DefaultExecutionOptions())
	if err == nil {
		t.Error("expected error for nonexistent workflow")
	}
}

func TestInstrumentedEngine_ExecuteAsync_RegisteredWorkflow(t *testing.T) {
	broker, err := mq.NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	tmpDir := t.TempDir()
	store, _ := kv.NewKVStore(kv.Config{DataDir: filepath.Join(tmpDir, "inst2")})
	defer store.Close()

	persistence := NewKVPersistence(store)
	queue := NewMQTaskQueue(broker, persistence, DefaultMQTaskQueueConfig())
	localEngine := orchestration.NewEngine()

	wf := orchestration.NewWorkflow("wf-inst", "Instrumented Test", "")
	localEngine.RegisterWorkflow(wf)

	engine := NewDistributedEngine(localEngine, queue, persistence, DefaultEngineConfig())
	ie := NewInstrumentedDistributedEngine(engine, NoopMetricRecorder())
	defer ie.Close()

	id, err := ie.ExecuteAsync(t.Context(), "wf-inst", map[string]any{}, DefaultExecutionOptions())
	if err != nil {
		t.Fatalf("ExecuteAsync: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty execution ID")
	}
}

func TestInstrumentedEngine_GetExecutionStatus(t *testing.T) {
	broker, err := mq.NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	tmpDir := t.TempDir()
	store, _ := kv.NewKVStore(kv.Config{DataDir: filepath.Join(tmpDir, "inst3")})
	defer store.Close()

	persistence := NewKVPersistence(store)
	queue := NewMQTaskQueue(broker, persistence, DefaultMQTaskQueueConfig())
	localEngine := orchestration.NewEngine()

	wf := orchestration.NewWorkflow("wf-status", "Status Test", "")
	localEngine.RegisterWorkflow(wf)

	engine := NewDistributedEngine(localEngine, queue, persistence, DefaultEngineConfig())
	ie := NewInstrumentedDistributedEngine(engine, NoopMetricRecorder())
	defer ie.Close()

	id, err := ie.ExecuteAsync(t.Context(), "wf-status", nil, DefaultExecutionOptions())
	if err != nil {
		t.Fatalf("ExecuteAsync: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	status, err := ie.GetExecutionStatus(t.Context(), id)
	if err != nil {
		t.Fatalf("GetExecutionStatus: %v", err)
	}
	if status == nil {
		t.Error("expected non-nil status")
	}
}

func TestInstrumentedEngine_Pause_UnknownExecution(t *testing.T) {
	ie, cleanup := newTestInstrumentedEngine(t)
	defer cleanup()

	err := ie.Pause(t.Context(), "nonexistent-exec")
	if err == nil {
		t.Error("expected error pausing nonexistent execution")
	}
}

func TestInstrumentedEngine_Resume_UnknownExecution(t *testing.T) {
	ie, cleanup := newTestInstrumentedEngine(t)
	defer cleanup()

	err := ie.Resume(t.Context(), "nonexistent-exec")
	if err == nil {
		t.Error("expected error resuming nonexistent execution")
	}
}

func TestInstrumentedEngine_Close(t *testing.T) {
	ie, cleanup := newTestInstrumentedEngine(t)
	defer cleanup()

	if err := ie.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
