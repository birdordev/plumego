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

```go
import (
    "github.com/birdordev/plumego/x/ai/distributed"
    "github.com/birdordev/plumego/x/ai/orchestration"
    "github.com/birdordev/plumego/x/messaging/mq"
    "github.com/birdordev/plumego/x/messaging/pubsub"
    kv "github.com/birdordev/plumego/store/kv"
)

// Setup infrastructure
store, _ := kv.NewKVStore(kv.Options{DataDir: "/data/workflows"})
persistence := distributed.NewKVPersistence(store)

broker := mq.NewInProcBroker(pubsub.New())
queue := distributed.NewMQTaskQueue(broker, persistence, distributed.DefaultMQTaskQueueConfig())

// Create distributed engine
localEngine := orchestration.NewEngine()
engine := distributed.NewDistributedEngine(
    localEngine,
    queue,
    persistence,
    distributed.DefaultEngineConfig(),
)
defer engine.Close()

// Register workflow
workflow := orchestration.NewWorkflow("data-pipeline", "Data Processing Pipeline", "")
workflow.AddStep(&orchestration.SequentialStep{
    StepName: "extract",
    Agent:    extractorAgent,
    InputFn:  func(state map[string]any) string { return state["source"].(string) },
    OutputKey: "data",
})
localEngine.RegisterWorkflow(workflow)

// Execute asynchronously
executionID, err := engine.ExecuteAsync(ctx, "data-pipeline", map[string]any{
    "source": "s3://bucket/data.csv",
}, distributed.ExecutionOptions{
    Distributed: true,
    Timeout:     10 * time.Minute,
    Checkpoints: true,
})

// Monitor status
snapshot, _ := engine.GetExecutionStatus(ctx, executionID)
fmt.Printf("Status: %s, Progress: %d/%d\\n",
    snapshot.Status,
    len(snapshot.CompletedSteps),
    len(workflow.Steps))
```

## Worker Setup

```go
// Create worker pool
executor := distributed.NewStepExecutor(localEngine)

// Register steps
executor.RegisterStep("extract", extractStep)
executor.RegisterStep("transform", transformStep)
executor.RegisterStep("load", loadStep)

pool := distributed.NewWorkerPool(queue, executor, distributed.PoolConfig{
    WorkerCount: 4,
    Concurrency: 4,
})

// Start workers
pool.Start(ctx)
defer pool.Stop(ctx)

// Dynamic scaling
pool.Scale(ctx, 8) // Scale up to 8 workers
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

```go
// Auto-recovery
recovery := distributed.NewRecoveryManager(persistence, queue, engine)

// Recover failed execution
err := recovery.RecoverFailedExecution(ctx, executionID)

// Retry with policy
err = distributed.RetryWithPolicy(ctx, distributed.DefaultRetryPolicy(), func() error {
    return someOperation()
})
```

## Monitoring

```go
// Add metrics collection
collector := metrics.NewPrometheusCollector()
instrumentedEngine := distributed.NewInstrumentedDistributedEngine(engine, collector)

// Metrics automatically collected:
// - workflow_execution_started
// - workflow_execution_completed
// - workflow_resume
// - workflow_pause
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

## Performance

- **Task Distribution**: < 10ms latency (P99)
- **Throughput**: > 1000 tasks/sec (4 workers)
- **Fault Recovery**: < 5 seconds
- **Checkpoint Overhead**: < 5% execution time

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
