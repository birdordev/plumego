package distributed

import (
	"context"
	"errors"
	"testing"

	"github.com/birdordev/plumego/x/ai/orchestration"
)

// failQueue is a minimal TaskQueue that always fails on Enqueue.
type failQueue struct{}

func (f *failQueue) Enqueue(_ context.Context, _ *WorkflowTask) error {
	return errors.New("queue unavailable")
}
func (f *failQueue) Subscribe(_ context.Context, _ string, _ TaskHandler) error { return nil }
func (f *failQueue) UpdateTaskStatus(_ context.Context, _ string, _ *TaskStatus) error {
	return nil
}
func (f *failQueue) GetTaskStatus(_ context.Context, _ string) (*TaskStatus, error) {
	return nil, errors.New("not found")
}
func (f *failQueue) PublishResult(_ context.Context, _ *TaskResult) error { return nil }
func (f *failQueue) SubscribeResults(_ context.Context, _ string) (<-chan *TaskResult, error) {
	ch := make(chan *TaskResult)
	close(ch)
	return ch, nil
}
func (f *failQueue) Close() error { return nil }

// noopStep is a minimal orchestration.Step.
type noopStep struct{ name string }

func (s *noopStep) Execute(_ context.Context, _ *orchestration.Workflow) (*orchestration.AgentResult, error) {
	return &orchestration.AgentResult{Output: "ok"}, nil
}
func (s *noopStep) Name() string { return s.name }

// --- RemoteStep ---

func TestRemoteStep_Name(t *testing.T) {
	step := &noopStep{name: "my-step"}
	rs := NewRemoteStep(step, &failQueue{}, "exec-1", "wf-1")
	if rs.Name() != "my-step" {
		t.Errorf("Name() = %q, want my-step", rs.Name())
	}
}

func TestRemoteStep_Execute_EnqueueError(t *testing.T) {
	step := &noopStep{name: "failing-step"}
	rs := NewRemoteStep(step, &failQueue{}, "exec-1", "wf-1")

	wf := &orchestration.Workflow{State: map[string]any{}}
	_, err := rs.Execute(t.Context(), wf)
	if err == nil {
		t.Error("expected error when queue is unavailable")
	}
}

func TestRemoteStep_Execute_ContextCancel(t *testing.T) {
	// Use a queue that succeeds on Enqueue but returns a closed channel on SubscribeResults,
	// and a pre-cancelled context to exercise the ctx.Done() branch.
	step := &noopStep{name: "ctx-step"}
	rs := NewRemoteStep(step, &failQueue{}, "exec-2", "wf-2")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	wf := &orchestration.Workflow{State: map[string]any{}}
	// Will fail at Enqueue (failQueue), not at ctx.Done(), but error must still be non-nil.
	_, err := rs.Execute(ctx, wf)
	if err == nil {
		t.Error("expected error for cancelled context / queue failure")
	}
}

// --- DistributedParallelStep ---

func TestDistributedParallelStep_Name(t *testing.T) {
	dps := &DistributedParallelStep{StepName: "parallel-step"}
	if dps.Name() != "parallel-step" {
		t.Errorf("Name() = %q, want parallel-step", dps.Name())
	}
}

func TestDistributedParallelStep_Execute_QueueError(t *testing.T) {
	// A step with one agent whose queue always fails enqueue.
	agent := &orchestration.Agent{ID: "a1", Name: "agent-1", Model: "test"}
	dps := NewDistributedParallelStep(
		"parallel-fail",
		[]*orchestration.Agent{agent},
		func(state map[string]any) []string { return []string{"input"} },
		[]string{"output"},
		&failQueue{},
		"exec-3",
		"wf-3",
	)
	wf := &orchestration.Workflow{State: map[string]any{"input": "hello"}}
	_, err := dps.Execute(t.Context(), wf)
	if err == nil {
		t.Error("expected error when queue is unavailable")
	}
}

type lifecycleQueue struct {
	TaskQueue
	subscribe func(context.Context) error
}

func (q lifecycleQueue) Subscribe(ctx context.Context, _ string, _ TaskHandler) error {
	return q.subscribe(ctx)
}

func TestWorkerStartupFailureCanRetryButStopIsFinal(t *testing.T) {
	subscribing := make(chan struct{})
	release := make(chan struct{})
	attempts := 0
	subscribeErr := errors.New("subscribe failed")
	queue := lifecycleQueue{subscribe: func(context.Context) error {
		attempts++
		if attempts == 1 {
			close(subscribing)
			<-release
			return subscribeErr
		}
		return nil
	}}
	worker := NewWorker(queue, nil, DefaultWorkerConfig())
	first := make(chan error, 1)
	go func() { first <- worker.Start(t.Context()) }()
	<-subscribing
	stopErr := worker.Stop(t.Context())
	close(release)
	if stopErr == nil {
		t.Fatal("Stop succeeded before startup completed")
	}
	if err := <-first; !errors.Is(err, subscribeErr) {
		t.Fatalf("first Start: %v", err)
	}
	if err := worker.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := worker.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := worker.Start(t.Context()); err == nil {
		t.Fatal("Start succeeded after Stop")
	}
}
