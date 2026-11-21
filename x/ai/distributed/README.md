# Distributed Workflows

Distributed workflow execution system for AI agent orchestration across multiple worker nodes.

## Features

### Core (P0) ✅
- **WorkflowPersistence**: Durable workflow state with KV store backend
- **TaskQueue**: Message queue-based task distribution
- **Worker**: Distributed worker nodes with dynamic scaling
- **DistributedEngine**: Async/sync workflow execution with checkpointing
- **RemoteStep**: Remote step execution wrapper

### Enhanced (P1) ✅
- **RecoveryManager**: Fault tolerance and automatic retry
- **InstrumentedEngine**: Metrics collection and observability
- **WorkerPool**: Multi-worker management with scaling

## Quick Start

Supply application-owned implementations of `TaskQueue` and
`WorkflowPersistence`, and start a worker that can execute the workflow's
steps before dispatching. The caller owns the returned engine and should close
it when finished. Use `GetExecutionStatus(ctx, executionID)` to read progress.

```go
import (
    "context"
    "errors"

    "github.com/birdordev/plumego/x/ai/distributed"
    "github.com/birdordev/plumego/x/ai/orchestration"
)

func startWorkflow(
    ctx context.Context,
    queue distributed.TaskQueue,
    persistence distributed.WorkflowPersistence,
    workflow *orchestration.Workflow,
    initialState map[string]any,
) (*distributed.DistributedEngine, string, error) {
    local := orchestration.NewEngine()
    local.RegisterWorkflow(workflow)
    engine := distributed.NewDistributedEngine(local, queue, persistence, distributed.DefaultEngineConfig())

    options := distributed.DefaultExecutionOptions()
    options.Distributed = true
    options.Checkpoints = true
    id, err := engine.ExecuteAsync(ctx, workflow.ID, initialState, options)
    if err != nil {
        return nil, "", errors.Join(err, engine.Close())
    }
    return engine, id, nil
}
```

## Execution and recovery

For distributed execution, the engine subscribes to queue results before
enqueuing the first incomplete step. It waits for the result matching the
current task and releases the subscription when the execution ends.

Checkpoints advance the recovery cursor only after their snapshot has been
saved. When a step result exists, the engine writes it before saving the
snapshot. A result record can therefore exist even when the checkpoint fails.
Execution IDs are opaque. Each run uses a single execution timeout budget for
local steps, queue operations, and remote results. A shorter caller deadline
still applies. Terminal snapshot writes use the engine's separate persistence
budget.

New snapshots record the execution mode so local runs resume locally. Older
snapshots without a mode retain the default distributed behavior.

Execution stops on either failure. Resume uses the last readable snapshot
and skips steps recorded as complete, including in older snapshots. Steps
with external side effects should be prepared for retries.

Pause saves the paused state before requesting cancellation. If saving fails,
Pause returns the error and leaves the execution active. The paused state
remains visible when the canceled step exits. Resume can start once the
previous run has exited.

## Worker Setup

Register the steps that the queue can dispatch. The caller owns the returned
pool and should handle the error from `pool.Stop(ctx)` at shutdown.

```go
import (
    "context"
    "errors"

    "github.com/birdordev/plumego/x/ai/distributed"
    "github.com/birdordev/plumego/x/ai/orchestration"
)

func startWorkerPool(
    ctx context.Context,
    queue distributed.TaskQueue,
    engine *orchestration.Engine,
    steps []orchestration.Step,
) (*distributed.WorkerPool, error) {
    executor := distributed.NewStepExecutor(engine)
    for _, step := range steps {
        executor.RegisterStep(step.Name(), step)
    }
    pool := distributed.NewWorkerPool(queue, executor, distributed.DefaultPoolConfig())
    if err := pool.Start(ctx); err != nil {
        return nil, err
    }
    if err := pool.Scale(ctx, 8); err != nil {
        return nil, errors.Join(err, pool.Stop(ctx))
    }
    return pool, nil
}
```

## Distributed Parallel Execution

```go
// Execute agents across multiple workers
parallelStep := distributed.NewDistributedParallelStep(
    "parallel-analysis",
    []*orchestration.Agent{sentimentAgent, entityAgent, summaryAgent},
    func(state map[string]any) []string {
        text := state["data"].(string)
        return []string{text, text, text}
    },
    []string{"sentiment", "entities", "summary"},
    queue,
    executionID,
    workflowID,
)

workflow.AddStep(parallelStep)
```

## Fault Tolerance

`RecoverFailedExecution` resumes a specific failed execution. The
`RecoverStaleTasks` placeholder returns zero without scanning or retrying tasks.
`MonitorTimeouts` waits for context cancellation and performs no timeout checks.

```go
// Create a recovery manager
recovery := distributed.NewRecoveryManager(persistence, queue, engine)

// Recover failed execution
err := recovery.RecoverFailedExecution(ctx, executionID)

// Retry with policy
err = distributed.RetryWithPolicy(ctx, distributed.DefaultRetryPolicy(), func() error {
    return someOperation()
})
```

## Monitoring

Adapt the workflow records to the application's metrics recorder. The wrapper
emits `workflow_execution_started`, `workflow_execution_completed`,
`workflow_resume`, and `workflow_pause` through the corresponding methods.

```go
import (
    "context"

    "github.com/birdordev/plumego/metrics"
    "github.com/birdordev/plumego/x/ai/distributed"
)

type workflowMetricRecorder struct {
    recorder metrics.Recorder
}

func (r workflowMetricRecorder) Record(ctx context.Context, record distributed.MetricRecord) {
    r.recorder.Record(ctx, metrics.MetricRecord{
        Name: record.Name,
        Value: record.Value,
        Labels: metrics.MetricLabels(record.Labels),
        Duration: record.Duration,
        Error: record.Error,
    })
}

func instrumentWorkflowEngine(
    engine *distributed.DistributedEngine,
    recorder metrics.Recorder,
) *distributed.InstrumentedDistributedEngine {
    return distributed.NewInstrumentedDistributedEngine(engine, workflowMetricRecorder{recorder: recorder})
}
```

## Architecture

```
                    ┌─────────────────┐
                    │ Distributed     │
                    │ Engine          │
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
        ┌─────▼─────┐  ┌─────▼─────┐  ┌────▼────┐
        │Persistence│  │Task Queue │  │Recovery │
        │(KV Store) │  │(net/mq)   │  │Manager  │
        └───────────┘  └─────┬─────┘  └─────────┘
                             │
                    ┌────────┴────────┐
                    │                 │
               ┌────▼────┐       ┌────▼────┐
               │Worker 1 │       │Worker 2 │
               │Pool     │       │Pool     │
               └─────────┘       └─────────┘
```

## Testing

```bash
go test ./x/ai/distributed -v
```

**Test Coverage**: 48+ tests, ~85% coverage

## Production Considerations

1. **Persistence**: Use production KV store with proper backup
2. **Message Queue**: Consider dedicated message broker for scale
3. **Worker Scaling**: Monitor queue depth for auto-scaling
4. **Checkpoints**: Balance frequency vs. performance
5. **Timeouts**: Set appropriate timeouts per workflow type
6. **Monitoring**: Enable metrics collection and alerting

## License

MIT
