package distributed

import (
	"context"
	"errors"
	"strings"
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

type remoteStepTestQueue struct {
	failQueue
	ctx          context.Context
	results      chan *TaskResult
	subscribeErr error
	enqueue      func(*WorkflowTask) error
}

func (q *remoteStepTestQueue) SubscribeResults(ctx context.Context, _ string) (<-chan *TaskResult, error) {
	q.ctx = ctx
	return q.results, q.subscribeErr
}

func (q *remoteStepTestQueue) Enqueue(_ context.Context, task *WorkflowTask) error {
	return q.enqueue(task)
}

func testDistributedParallelStep(queue TaskQueue, agents ...*orchestration.Agent) *DistributedParallelStep {
	return NewDistributedParallelStep("parallel", agents, func(map[string]any) []string {
		return make([]string, len(agents))
	}, []string{"first", "second"}, queue, "exec", "wf")
}

func TestRemoteStepsSubscribeBeforeEnqueue(t *testing.T) {
	sentinel := errors.New("queue operation failed")
	for _, step := range []struct {
		name    string
		newStep func(TaskQueue) orchestration.Step
	}{
		{"single", func(q TaskQueue) orchestration.Step { return NewRemoteStep(&noopStep{name: "remote"}, q, "exec", "wf") }},
		{"parallel", func(q TaskQueue) orchestration.Step { return testDistributedParallelStep(q, &orchestration.Agent{}) }},
	} {
		for _, mode := range []string{"success", "subscribe failure", "enqueue failure", "cancel wait"} {
			t.Run(step.name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				queue := &remoteStepTestQueue{results: make(chan *TaskResult, 1)}
				var wantErr error
				switch mode {
				case "subscribe failure":
					queue.subscribeErr = sentinel
					wantErr = sentinel
				case "enqueue failure":
					wantErr = sentinel
				case "cancel wait":
					wantErr = context.Canceled
				}
				calls := 0
				queue.enqueue = func(task *WorkflowTask) error {
					calls++
					if queue.ctx == nil || queue.ctx.Err() != nil {
						t.Fatal("task dispatched without a live result subscription")
					}
					switch mode {
					case "success":
						queue.results <- &TaskResult{TaskID: task.ID, AgentResult: &orchestration.AgentResult{Output: "immediate"}}
					case "enqueue failure":
						return sentinel
					case "cancel wait":
						cancel()
					}
					return nil
				}
				got, err := step.newStep(queue).Execute(ctx, orchestration.NewWorkflow("wf", "workflow", ""))
				if !errors.Is(err, wantErr) {
					t.Fatalf("Execute error = %v, want %v", err, wantErr)
				}
				if mode == "success" && (got == nil || got.Output != "immediate") {
					t.Fatalf("immediate result = %+v", got)
				}
				wantCalls := 1
				if mode == "subscribe failure" {
					wantCalls = 0
				}
				if calls != wantCalls || queue.ctx == nil || !errors.Is(queue.ctx.Err(), context.Canceled) {
					t.Errorf("enqueue calls = %d, want %d; subscription ctx = %v", calls, wantCalls, queue.ctx)
				}
			})
		}
	}
}

func TestRemoteStepMatchesTaskResult(t *testing.T) {
	for _, mode := range []string{"success", "task failure", "closed"} {
		t.Run(mode, func(t *testing.T) {
			queue := &remoteStepTestQueue{results: make(chan *TaskResult, 3)}
			expected := &orchestration.AgentResult{Output: "matching"}
			queue.enqueue = func(task *WorkflowTask) error {
				queue.results <- nil
				queue.results <- &TaskResult{TaskID: "unrelated", UpdatedState: map[string]any{"wrong": true}}
				if mode == "closed" {
					close(queue.results)
					return nil
				}
				reply := &TaskResult{TaskID: task.ID, AgentResult: expected, UpdatedState: map[string]any{"correct": true}}
				if mode == "task failure" {
					reply.Error = "task failed"
				}
				queue.results <- reply
				return nil
			}
			wf := orchestration.NewWorkflow("wf", "workflow", "")
			got, err := NewRemoteStep(&noopStep{name: "remote"}, queue, "exec", "wf").Execute(t.Context(), wf)
			switch mode {
			case "closed":
				if got != nil || err == nil || !strings.Contains(err.Error(), "subscription closed") {
					t.Fatalf("Execute = %v, %v", got, err)
				}
			case "task failure":
				if got != expected || err == nil || !strings.Contains(err.Error(), "task failed") {
					t.Fatalf("Execute = %v, %v", got, err)
				}
			case "success":
				if got != expected || err != nil {
					t.Fatalf("Execute = %v, %v", got, err)
				}
			}
			if _, ok := wf.GetState("wrong"); ok {
				t.Error("applied unrelated state")
			}
			if _, ok := wf.GetState("correct"); ok != (mode == "success") {
				t.Errorf("applied matching state = %t", ok)
			}
		})
	}
}

func TestDistributedParallelStepCollectsDistinctResults(t *testing.T) {
	for _, mode := range []string{"success", "closed", "missing payload", "empty"} {
		t.Run(mode, func(t *testing.T) {
			queue := &remoteStepTestQueue{results: make(chan *TaskResult, 5)}
			var agents []*orchestration.Agent
			if mode != "empty" {
				agents = []*orchestration.Agent{{Name: "first"}, {Name: "second"}}
			}
			var firstID string
			queue.enqueue = func(task *WorkflowTask) error {
				if task.StepIndex == 0 {
					firstID = task.ID
					return nil
				}
				switch mode {
				case "closed":
					close(queue.results)
				case "missing payload":
					queue.results <- &TaskResult{TaskID: task.ID}
				case "success":
					second := &TaskResult{TaskID: task.ID, AgentResult: &orchestration.AgentResult{Output: "two"}}
					queue.results <- second
					queue.results <- &TaskResult{TaskID: "unrelated", Error: "ignore"}
					queue.results <- second
					queue.results <- &TaskResult{TaskID: firstID, AgentResult: &orchestration.AgentResult{Output: "one"}}
				}
				return nil
			}
			wf := orchestration.NewWorkflow("wf", "workflow", "")
			got, err := testDistributedParallelStep(queue, agents...).Execute(t.Context(), wf)
			if mode == "success" {
				if err != nil || got == nil || got.Output != "one" || wf.State["first"] != "one" || wf.State["second"] != "two" {
					t.Fatalf("Execute = %+v, %v, state = %v", got, err, wf.State)
				}
				return
			}
			want := map[string]string{"closed": "subscription closed", "missing payload": "returned no result", "empty": "at least one agent"}[mode]
			if err == nil || !strings.Contains(err.Error(), want) || got != nil || len(wf.State) != 0 {
				t.Fatalf("Execute = %+v, %v, state = %v", got, err, wf.State)
			}
			if mode == "empty" && queue.ctx != nil {
				t.Error("empty step subscribed for results")
			}
		})
	}
}

type stateSnapshotQueue struct {
	failQueue
	enqueue func(*WorkflowTask) error
	results chan *TaskResult
}

func (q stateSnapshotQueue) Enqueue(_ context.Context, task *WorkflowTask) error {
	return q.enqueue(task)
}

func (q stateSnapshotQueue) SubscribeResults(context.Context, string) (<-chan *TaskResult, error) {
	return q.results, nil
}

func TestRemoteStepStateSnapshot(t *testing.T) {
	wf := &orchestration.Workflow{State: map[string]any{"input": "original"}}
	failure := errors.New("queue failure")
	q := &stateSnapshotQueue{results: make(chan *TaskResult, 1), enqueue: func(task *WorkflowTask) error {
		task.WorkflowState["input"] = "adapter change"
		return failure
	}}
	step := NewRemoteStep(&noopStep{name: "step"}, q, "ex", "wf")
	if _, err := step.Execute(t.Context(), wf); !errors.Is(err, failure) {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := wf.GetState("input"); got != "original" {
		t.Fatalf("state after failed enqueue: %v", got)
	}
	q.enqueue = func(task *WorkflowTask) error {
		q.results <- &TaskResult{TaskID: task.ID, AgentResult: &orchestration.AgentResult{Output: "success"}, UpdatedState: map[string]any{"saved": true}}
		return nil
	}
	step.Queue = q
	if _, err := step.Execute(t.Context(), wf); err != nil {
		t.Fatal(err)
	}
	if got, _ := wf.GetState("saved"); got != true {
		t.Fatalf("result state: %v", got)
	}
	parallel := NewDistributedParallelStep("parallel", []*orchestration.Agent{{Name: "agent"}}, func(state map[string]any) []string {
		input := state["input"].(string)
		state["input"] = "callback change"
		return []string{input}
	}, []string{"output"}, q, "ex", "wf")
	if _, err := parallel.Execute(t.Context(), wf); err != nil {
		t.Fatal(err)
	}
	if got, _ := wf.GetState("input"); got != "original" {
		t.Fatalf("state after parallel callback: %v", got)
	}
	if got, _ := wf.GetState("output"); got != "success" {
		t.Fatalf("parallel output: %v", got)
	}
}
