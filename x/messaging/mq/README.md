# net/mq - In-Process Message Broker

A high-performance, feature-rich in-process message broker for Go applications.

## Features

### ✅ Core Features (Production-Ready)

- **Basic Pub/Sub**: Topic-based message routing with multiple subscribers
- **Priority Queue**: Message ordering based on priority levels (Lowest to Highest)
- **Message TTL**: Automatic expiration with time-to-live support
- **ACK/NACK**: Message acknowledgment with timeout and automatic retry
- **Batch Operations**: Efficient batch publishing and subscribing
- **Memory Management**: Configurable memory limits with automatic enforcement
- **Metrics & Observability**: Built-in metrics collection and health checks

### ✅ Advanced Features (Stable)

- **Transactions**: Atomic message publishing with commit/rollback support
- **Dead Letter Queue**: Failed message handling with reason tracking
- **Persistence**: Message persistence with automatic recovery using store/kv backend
- **Message Recovery**: Replay persisted messages after broker restart

### ✅ Durable Task Queue (SQL-backed)

- **Task leasing**: Reserve/ack with lease timeouts for competitive consumers
- **Retry & DLQ**: Release with backoff, move to dead letter on exhaustion
- **Pluggable storage**: `TaskStore` interface with SQL and in-memory stores
- **Schema**: See `docs/MQ_TASK_QUEUE_SCHEMA.md` for recommended SQL tables
- **Attempt audit**: Optional attempt logging via `SQLConfig.EnableAttemptLog`
- **Metrics**: Queue operations emit `ObserveMQ` with `queue_*` operations
- **Graceful shutdown**: Stop drains in-flight tasks; timed-out shutdown releases leases
- **Duplicate protection**: Optional `TaskDeduper` hook to skip tasks already processed

### Durable Task Queue (Quick Start)

```go
// import mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
db, _ := sql.Open("postgres", os.Getenv("DATABASE_URL"))
store := mqstore.NewSQL(db, mqstore.DefaultSQLConfig())

queue := mq.NewTaskQueue(store)
worker := mq.NewWorker(queue, mq.WorkerConfig{
    ConsumerID:  "worker-1",
    Concurrency: 8,
})

worker.Register("sms.send", func(ctx context.Context, task mq.Task) error {
    // process task.Payload
    return nil
})

go worker.Start(context.Background())

_ = queue.Enqueue(context.Background(), mq.Task{
    ID:       "task-1",
    Topic:    "sms.send",
    TenantID: "tenant-a",
    Payload:  []byte(`{\"to\":\"13800000000\"}`),
}, mq.EnqueueOptions{})
```

### Duplicate Protection (Optional)

Provide a `TaskDeduper` to skip tasks already completed (helpful after retries or restarts):

```go
type memoryDeduper struct{ seen sync.Map }
func (d *memoryDeduper) IsCompleted(ctx context.Context, key string) (bool, error) {
    _, ok := d.seen.Load(key)
    return ok, nil
}
func (d *memoryDeduper) MarkCompleted(ctx context.Context, key string, ttl time.Duration) error {
    d.seen.Store(key, struct{}{})
    return nil
}

worker := mq.NewWorker(queue, mq.WorkerConfig{
    ConsumerID: "worker-1",
    Deduper:    &memoryDeduper{},
})
```

Use the built-in KV adapter for persistence with TTL:

```go
kv, _ := kvstore.NewKVStore(kvstore.Options{DataDir: "/tmp/mq-dedupe"})
deduper := mq.NewKVDeduper(kv, mq.KVDeduperConfig{
    Prefix:     "mq-dedupe",
    DefaultTTL: 24 * time.Hour,
})

worker := mq.NewWorker(queue, mq.WorkerConfig{
    ConsumerID: "worker-1",
    Deduper:    deduper,
    DedupeTTL:  24 * time.Hour,
})
```

Or use the SQL-backed deduper (requires an `idempotency_keys` migration and `github.com/birdordev/plumego/x/data/idempotency` for dialect constants):

```go
db, _ := sql.Open("postgres", os.Getenv("DATABASE_URL"))
deduper := mq.NewSQLDeduper(db, mq.SQLDeduperConfig{
    Dialect:    idempotency.DialectPostgres,
    Prefix:     "mq-dedupe",
    DefaultTTL: 24 * time.Hour,
})

worker := mq.NewWorker(queue, mq.WorkerConfig{
    ConsumerID: "worker-1",
    Deduper:    deduper,
    DedupeTTL:  24 * time.Hour,
})
```

### Queue Metrics

```go
collector := metrics.NewDevCollector(metrics.DefaultDevCollectorConfig())
queue := mq.NewTaskQueue(store, mq.WithQueueMetricsObserver(collector))

worker := mq.NewWorker(queue, mq.WorkerConfig{
    ConsumerID:       "worker-1",
    Concurrency:      8,
    MetricsObserver: collector,
})
```

### Attempt Log (Optional)

```go
cfg := mqstore.DefaultSQLConfig()
cfg.EnableAttemptLog = true
cfg.AttemptLogHook = func(ctx context.Context, info mqstore.AttemptLogError) {
    log.Printf("attempt log failed op=%s task=%s attempt=%d err=%v", info.Op, info.TaskID, info.Attempt, info.Err)
}

store := mqstore.NewSQL(db, cfg)
```

### ⚠️ Not Implemented

- **MQTT Protocol**: Interface defined, implementation pending
- **AMQP Protocol**: Interface defined, implementation pending
- **Cluster Mode**: Interface defined, implementation pending (requires distributed infrastructure)

## Installation

```bash
go get github.com/birdordev/plumego
```

## Quick Start

### Basic Pub/Sub

```go
broker := mq.NewInProcBroker(pubsub.New())
defer broker.Close()

ctx := context.Background()

// Subscribe
sub, _ := broker.Subscribe(ctx, "events", mq.SubOptions{BufferSize: 10})
defer sub.Cancel()

// Publish
msg := mq.Message{ID: "msg-1", Data: "Hello World"}
broker.Publish(ctx, "events", msg)

// Receive
received := <-sub.C()
fmt.Println(received.Data)
```

### With All Features

```go
// Configure broker
cfg := mq.DefaultConfig()
cfg.EnablePriorityQueue = true
cfg.EnableAckSupport = true
cfg.EnableTransactions = true
cfg.EnableDeadLetterQueue = true
cfg.DeadLetterTopic = "dlq"
cfg.EnablePersistence = true
cfg.PersistencePath = "/data/mq"

broker := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))
defer broker.Close()
```

## Usage Examples

### Priority Messages

```go
cfg := mq.DefaultConfig()
cfg.EnablePriorityQueue = true
broker := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))

highPriority := mq.PriorityMessage{
    Message:  mq.Message{ID: "urgent", Data: "Important task"},
    Priority: mq.PriorityHigh,
}
broker.PublishPriority(ctx, "tasks", highPriority)
```

### Message TTL

```go
ttlMsg := mq.TTLMessage{
    Message:   mq.Message{ID: "temp-1", Data: "Temporary data"},
    ExpiresAt: time.Now().Add(5 * time.Minute),
}
broker.PublishTTL(ctx, "temp", ttlMsg)
```

### Message Acknowledgment

```go
cfg := mq.DefaultConfig()
cfg.EnableAckSupport = true
broker := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))

// Publish with ACK requirement
ackMsg := mq.AckMessage{
    Message:    mq.Message{ID: "order-1", Data: "Process order"},
    AckID:      "ack-1",
    AckPolicy:  mq.AckRequired,
    AckTimeout: 30 * time.Second,
}
broker.PublishWithAck(ctx, "orders", ackMsg)

// Process and acknowledge
msg := <-sub.C()
// ... process message ...
broker.Ack(ctx, "orders", "ack-1")

// Or NACK for redelivery
broker.Nack(ctx, "orders", "ack-1")
```

### Transactions

```go
cfg := mq.DefaultConfig()
cfg.EnableTransactions = true
broker := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))

// Start transaction
txID := "tx-123"

// Add messages to transaction
broker.PublishWithTransaction(ctx, "orders", msg1, txID)
broker.PublishWithTransaction(ctx, "orders", msg2, txID)

// Commit (messages are delivered now)
broker.CommitTransaction(ctx, txID)

// Or rollback (messages are discarded)
broker.RollbackTransaction(ctx, txID)
```

### Dead Letter Queue

```go
cfg := mq.DefaultConfig()
cfg.EnableDeadLetterQueue = true
cfg.DeadLetterTopic = "dlq"
broker := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))

// Send failed message to DLQ
failedMsg := mq.Message{ID: "failed-1", Data: "Processing failed"}
broker.PublishToDeadLetter(ctx, "orders", failedMsg, "validation error")

// Monitor DLQ
dlqSub, _ := broker.Subscribe(ctx, "dlq", mq.SubOptions{BufferSize: 10})
for msg := range dlqSub.C() {
    log.Printf("Dead letter: %s", msg.Data)
}

// Check stats
stats := broker.GetDeadLetterStats()
fmt.Printf("DLQ count: %d\n", stats.TotalMessages)
```

### Persistence and Recovery

```go
cfg := mq.DefaultConfig()
cfg.EnablePersistence = true
cfg.PersistencePath = "/data/mq"
broker := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))

// Publish (automatically persisted)
msg := mq.Message{ID: "important-1", Data: "Critical data"}
broker.Publish(ctx, "critical", msg)

broker.Close()

// Restart broker
broker2 := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))

// Recover persisted messages
messages, _ := broker2.RecoverMessages(ctx, "critical", 100)

// Replay to subscribers
broker2.ReplayMessages(ctx, "critical", 100)
```

## Configuration

### Default Configuration

```go
cfg := mq.DefaultConfig()
// Returns:
// - MaxTopics: 0 (no limit)
// - MaxSubscribers: 0 (no limit)
// - DefaultBufferSize: 16
// - EnableMetrics: true
// - HealthCheckInterval: 30s
// - EnablePriorityQueue: true
// - EnableAckSupport: false
// - DefaultAckTimeout: 30s
// - MaxMemoryUsage: 0 (no limit)
// - EnableTransactions: false
// - TransactionTimeout: 30s
// - EnableDeadLetterQueue: false
// - EnablePersistence: false
```

### Custom Configuration

```go
cfg := mq.Config{
    EnableHealthCheck:        true,
    MaxTopics:                1000,
    MaxSubscribers:           100,
    DefaultBufferSize:        32,
    EnableMetrics:            true,
    HealthCheckInterval:      30 * time.Second,
    MessageTTL:               24 * time.Hour,
    EnablePriorityQueue:      true,
    EnableAckSupport:         true,
    DefaultAckTimeout:        30 * time.Second,
    MaxMemoryUsage:           500 * 1024 * 1024, // 500MB
    EnableTransactions:       true,
    TransactionTimeout:       60 * time.Second,
    EnableDeadLetterQueue:    true,
    DeadLetterTopic:          "dlq",
    EnablePersistence:        true,
    PersistencePath:          "/data/mq",
}

broker := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))
```

## Message Types

### Basic Message

```go
type Message struct {
    ID   string
    Data any
}
```

### Priority Message

```go
type PriorityMessage struct {
    Message
    Priority MessagePriority // PriorityLowest to PriorityHighest
}
```

### TTL Message

```go
type TTLMessage struct {
    Message
    ExpiresAt time.Time
}
```

### ACK Message

```go
type AckMessage struct {
    Message
    AckID      string
    AckPolicy  AckPolicy  // AckNone, AckRequired, AckTimeout
    AckTimeout time.Duration
}
```

### Combined Messages

```go
// Priority + TTL
type PriorityTTLMessage struct {
    Message
    Priority  MessagePriority
    ExpiresAt time.Time
}

// ACK + TTL
type AckTTLMessage struct {
    Message
    AckID      string
    AckPolicy  AckPolicy
    AckTimeout time.Duration
    ExpiresAt  time.Time
}
```

## Health Monitoring

```go
// Check broker health
health := broker.HealthCheck()
fmt.Printf("Status: %s\n", health.Status)
fmt.Printf("Uptime: %s\n", health.Uptime)
fmt.Printf("Topics: %d\n", health.TotalTopics)
fmt.Printf("Subscribers: %d\n", health.TotalSubs)
fmt.Printf("Memory: %d bytes\n", health.MemoryUsage)

// Get metrics snapshot
if health.Metrics.TotalPublished > 0 {
    fmt.Printf("Published: %d\n", health.Metrics.TotalPublished)
    fmt.Printf("Delivered: %d\n", health.Metrics.TotalDelivered)
    fmt.Printf("Dropped: %d\n", health.Metrics.TotalDropped)
}
```

## Performance

- **Sharded storage**: 16 shards for concurrent access
- **Binary serialization**: Optimal performance for persistence
- **Compression**: gzip compression for stored messages
- **LRU eviction**: Automatic memory management
- **WAL**: Write-ahead logging for durability
- **Lock-free operations**: Where possible for high throughput

## Architecture

```
InProcBroker
├── pubsub.PubSub (core pub/sub)
├── priorityDispatcher (priority queue)
├── ackTracker (acknowledgment tracking)
├── ttlTracker (TTL management)
├── txManager (transaction management)
├── deadLetterManager (DLQ management)
└── persistenceManager (persistence backend)
    └── KVPersistence (store/kv integration)
```

## Testing

```bash
# Run all tests
go test ./net/mq/...

# Run with race detector
go test -race ./net/mq/...

# Run examples
go test -v -run Example ./net/mq/...

# Run benchmarks
go test -bench=. ./net/mq/...
```

## Thread Safety

All broker operations are thread-safe and can be called concurrently from multiple goroutines.

## Error Handling

Common errors:
- `ErrNotInitialized`: Broker not properly initialized
- `ErrBrokerClosed`: Broker has been closed
- `ErrInvalidTopic`: Invalid topic name
- `ErrMessageExpired`: Message has expired (TTL)
- `ErrTransactionNotFound`: Transaction ID not found
- `ErrTransactionTimeout`: Transaction exceeded timeout
- `ErrMemoryLimitExceeded`: Memory usage exceeded limit
- `ErrDeadLetterNotSupported`: DLQ not enabled

## Best Practices

1. **Always close the broker**: Use `defer broker.Close()`
2. **Cancel subscriptions**: Use `defer sub.Cancel()`
3. **Handle context cancellation**: Check `ctx.Err()` in long-running operations
4. **Set appropriate buffer sizes**: Based on expected message volume
5. **Use transactions for atomicity**: When multiple messages must be delivered together
6. **Enable persistence for critical data**: Messages survive broker restarts
7. **Monitor dead letter queue**: Track and investigate failed messages
8. **Set memory limits**: Prevent unbounded growth in production

## Examples

See `example_test.go` for comprehensive examples of all features.

## License

See LICENSE file in the repository root.

## Contributing

Contributions are welcome! Please ensure:
- All tests pass
- Code is formatted with `gofmt`
- New features include tests and documentation
- Breaking changes are clearly documented
