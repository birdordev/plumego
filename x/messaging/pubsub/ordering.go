package pubsub

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Ordering errors

// maxMissingSequences caps the number of sequence gaps tracked per queue to
// prevent unbounded memory growth when messages are permanently lost.
const maxMissingSequences = 10_000

// MetaKeySequence is the message metadata key for the ordering sequence number.
const MetaKeySequence = "X-Message-Sequence"

// OrderLevel defines the level of ordering guarantee
type OrderLevel int

const (
	// OrderNone - no ordering guarantee (default, highest throughput)
	OrderNone OrderLevel = iota

	// OrderPerTopic - messages within the same topic are ordered
	OrderPerTopic

	// OrderPerKey - messages with the same partition key are ordered
	OrderPerKey

	// OrderGlobal - all messages are globally ordered (lowest throughput)
	OrderGlobal
)

// OrderingConfig configures the ordering system
type OrderingConfig struct {
	// DefaultLevel is the default ordering level
	DefaultLevel OrderLevel

	// QueueSize for ordered message queues (default: 1000)
	QueueSize int

	// WorkerCount for processing ordered messages (default: 4)
	WorkerCount int

	// MaxBatchSize for batching ordered messages (default: 10)
	MaxBatchSize int

	// BatchTimeout for flushing batched messages (default: 10ms)
	BatchTimeout time.Duration

	// SequenceCheckEnabled enables sequence number verification
	SequenceCheckEnabled bool
}

// DefaultOrderingConfig returns default ordering configuration
func DefaultOrderingConfig() OrderingConfig {
	return OrderingConfig{
		DefaultLevel:         OrderNone,
		QueueSize:            1000,
		WorkerCount:          4,
		MaxBatchSize:         10,
		BatchTimeout:         10 * time.Millisecond,
		SequenceCheckEnabled: false,
	}
}

// OrderedPubSub wraps InProcBroker with ordering guarantees
type OrderedPubSub struct {
	*InProcBroker

	config OrderingConfig

	// Per-topic queues
	topicQueues   map[string]*orderedQueue
	topicQueuesMu sync.RWMutex

	// Per-key queues (for OrderPerKey)
	keyQueues   map[string]*orderedQueue
	keyQueuesMu sync.RWMutex

	// Global queue (for OrderGlobal)
	globalQueue *orderedQueue

	// Sequence tracking
	sequences   map[string]*sequenceTracker
	sequencesMu sync.RWMutex

	// Background workers
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed atomic.Bool

	// Metrics
	orderedPublishes atomic.Uint64
	queuedMessages   atomic.Uint64
	sequenceErrors   atomic.Uint64
}

// globalQueueIdentifier is the identifier used for the global ordering queue.
const globalQueueIdentifier = "__global__"

// orderedQueue represents a FIFO queue for ordered messages
type orderedQueue struct {
	ch        chan orderedMessage
	mu        sync.Mutex
	sequence  atomic.Uint64
	batch     []orderedMessage
	batchSize int
	closed    atomic.Bool

	// Per-queue statistics
	processed      atomic.Uint64
	batchFlushes   atomic.Uint64
	sequenceErrors atomic.Uint64
}

// orderedMessage wraps a message with ordering metadata
type orderedMessage struct {
	topic    string
	key      string
	message  Message
	sequence uint64
	enqueued time.Time
}

// sequenceTracker tracks message sequences for verification
type sequenceTracker struct {
	mu           sync.Mutex
	lastSequence uint64
	missing      map[uint64]bool
}

// NewOrdered creates a new ordered pubsub instance
func NewOrdered(config OrderingConfig, opts ...Option) *OrderedPubSub {
	// Apply defaults
	if config.QueueSize == 0 {
		config.QueueSize = 1000
	}
	if config.WorkerCount == 0 {
		config.WorkerCount = 4
	}
	if config.MaxBatchSize == 0 {
		config.MaxBatchSize = 10
	}
	if config.BatchTimeout == 0 {
		config.BatchTimeout = 10 * time.Millisecond
	}

	// Create base pubsub
	ps := New(opts...)

	ctx, cancel := context.WithCancel(context.Background())

	ops := &OrderedPubSub{
		InProcBroker: ps,
		config:       config,
		topicQueues:  make(map[string]*orderedQueue),
		keyQueues:    make(map[string]*orderedQueue),
		sequences:    make(map[string]*sequenceTracker),
		ctx:          ctx,
		cancel:       cancel,
	}

	// Create global queue if needed
	if config.DefaultLevel == OrderGlobal {
		ops.globalQueue = ops.createQueue(config.MaxBatchSize)
		ops.startQueueWorker(ops.globalQueue, globalQueueIdentifier)
	}

	return ops
}

// PublishOrdered publishes a message with ordering guarantee
func (ops *OrderedPubSub) PublishOrdered(topic string, msg Message, level OrderLevel) error {
	if ops.closed.Load() {
		return ErrOrderingClosed
	}

	return ops.PublishWithKey(topic, "", msg, level)
}

// PublishWithKey publishes a message with a partition key
func (ops *OrderedPubSub) PublishWithKey(topic, key string, msg Message, level OrderLevel) error {
	if ops.closed.Load() {
		return ErrOrderingClosed
	}

	// Generate sequence number
	var queue *orderedQueue

	switch level {
	case OrderNone:
		// Fast path: no ordering
		return ops.Publish(topic, msg)

	case OrderPerTopic:
		queue = ops.getOrCreateTopicQueue(topic)

	case OrderPerKey:
		if key == "" {
			key = topic // Default to topic if no key
		}
		queue = ops.getOrCreateKeyQueue(key)

	case OrderGlobal:
		if ops.globalQueue == nil {
			ops.globalQueue = ops.createQueue(ops.config.MaxBatchSize)
			ops.startQueueWorker(ops.globalQueue, globalQueueIdentifier)
		}
		queue = ops.globalQueue

	default:
		return ErrInvalidOrderLevel
	}

	// Create ordered message
	om := orderedMessage{
		topic:    topic,
		key:      key,
		message:  msg,
		sequence: queue.sequence.Add(1),
		enqueued: time.Now(),
	}

	// Enqueue
	select {
	case queue.ch <- om:
		ops.queuedMessages.Add(1)
		return nil

	case <-ops.ctx.Done():
		return ErrOrderingClosed

	default:
		// Queue full - caller should retry with backoff or use a different OrderLevel.
		return newErr(ErrCodeBufferFull, "enqueue", "", "ordered queue is full", nil)
	}
}

// startQueueWorker starts a worker to process an ordered queue
func (ops *OrderedPubSub) startQueueWorker(queue *orderedQueue, identifier string) {
	ops.wg.Add(1)
	go func() {
		defer ops.wg.Done()
		ops.processQueue(queue, identifier)
	}()
}

// processQueue processes messages from an ordered queue.
// The identifier parameter represents the ordering key for this queue:
//   - For OrderPerTopic: the topic name
//   - For OrderPerKey: the partition key
//   - For OrderGlobal: globalQueueIdentifier
//
// It is used for sequence verification and per-queue statistics tracking.
func (ops *OrderedPubSub) processQueue(queue *orderedQueue, identifier string) {
	ticker := time.NewTicker(ops.config.BatchTimeout)
	defer ticker.Stop()

	for {
		select {
		case om, ok := <-queue.ch:
			if !ok {
				// queue.ch is never explicitly closed (closing a channel that has a
				// concurrent non-blocking sender would panic). Workers exit via
				// ctx.Done() instead. This branch is here for safety completeness
				// but is unreachable in normal operation.
				ops.flushBatch(queue, identifier)
				return
			}

			// Add to batch
			queue.mu.Lock()
			queue.batch = append(queue.batch, om)
			shouldFlush := len(queue.batch) >= queue.batchSize
			queue.mu.Unlock()

			if shouldFlush {
				ops.flushBatch(queue, identifier)
			}

		case <-ticker.C:
			// Periodic flush
			ops.flushBatch(queue, identifier)

		case <-ops.ctx.Done():
			ops.flushBatch(queue, identifier)
			return
		}
	}
}

// flushBatch flushes a batch of messages for the queue identified by identifier.
// The identifier is used as the sequence tracking key, ensuring that:
//   - Per-topic queues track sequences by topic name
//   - Per-key queues track sequences by partition key
//   - Global queue uses a single shared sequence space
func (ops *OrderedPubSub) flushBatch(queue *orderedQueue, identifier string) {
	queue.mu.Lock()
	if len(queue.batch) == 0 {
		queue.mu.Unlock()
		return
	}

	batch := queue.batch
	queue.batch = make([]orderedMessage, 0, queue.batchSize)
	queue.mu.Unlock()

	queue.batchFlushes.Add(1)

	// Publish in order
	for _, om := range batch {
		// Verify sequence if enabled
		if ops.config.SequenceCheckEnabled {
			if err := ops.verifySequence(om, identifier); err != nil {
				ops.sequenceErrors.Add(1)
				queue.sequenceErrors.Add(1)
				continue
			}
		}

		// Stamp sequence into message metadata so subscribers can verify
		if om.message.Meta == nil {
			om.message.Meta = make(map[string]string, 1)
		}
		om.message.Meta[MetaKeySequence] = strconv.FormatUint(om.sequence, 10)

		// Publish
		_ = ops.Publish(om.topic, om.message)
		ops.orderedPublishes.Add(1)
		queue.processed.Add(1)
	}
}

// PartitionKey generates a deterministic partition key from a value.
// partitions must be > 0.  The returned string is the decimal representation
// of the partition index (0 to partitions-1), so it is always valid ASCII.
func PartitionKey(value string, partitions int) string {
	if partitions <= 0 {
		partitions = 1
	}
	partition := int(fnv32a(value)) % partitions
	if partition < 0 {
		partition = -partition
	}
	return strconv.Itoa(partition)
}

// Close closes the ordered pubsub
func (ops *OrderedPubSub) Close() error {
	if ops.closed.Swap(true) {
		return nil
	}

	// Cancel the context first so workers exit via ctx.Done().
	// We intentionally do NOT close individual queue channels here: PublishWithKey
	// uses a non-blocking select with case queue.ch <- om, and sending to a closed
	// channel panics in Go even when a default case is present.  Workers drain
	// their in-flight batch on ctx.Done() and exit cleanly without a channel close.
	ops.cancel()

	// Mark queues closed so PublishWithKey returns ErrOrderingClosed for any
	// concurrent callers that already passed the ops.closed check.
	ops.topicQueuesMu.Lock()
	for _, queue := range ops.topicQueues {
		queue.closed.Store(true)
	}
	ops.topicQueuesMu.Unlock()

	ops.keyQueuesMu.Lock()
	for _, queue := range ops.keyQueues {
		queue.closed.Store(true)
	}
	ops.keyQueuesMu.Unlock()

	if ops.globalQueue != nil {
		ops.globalQueue.closed.Store(true)
	}

	// Wait for workers
	ops.wg.Wait()

	// Close base pubsub
	return ops.InProcBroker.Close()
}

// OrderingStats holds ordering metrics
type OrderingStats struct {
	OrderedPublishes uint64
	QueuedMessages   uint64
	SequenceErrors   uint64
	TopicQueues      int
	KeyQueues        int

	// QueueStats contains per-queue statistics keyed by identifier.
	// For topic queues the key is the topic name, for key queues
	// it is the partition key, and for the global queue it is
	// globalQueueIdentifier ("__global__").
	QueueStats map[string]QueueStat
}

// QueueStat holds statistics for a single ordered queue.
type QueueStat struct {
	// Processed is the number of messages successfully published from this queue.
	Processed uint64

	// BatchFlushes is the number of batch flush operations performed.
	BatchFlushes uint64

	// SequenceErrors is the number of sequence verification failures.
	SequenceErrors uint64

	// Pending is the current number of messages waiting in the queue channel.
	Pending int
}

// OrderedSubscription wraps a subscription with sequence verification
type OrderedSubscription struct {
	Subscription
	tracker *sequenceTracker
	topic   string
}

// SubscribeOrdered creates a subscription with sequence verification.
// Each ordered subscription gets its own sequence tracker so that
// subscriber-side verification is independent of publisher-side checks.
func (ops *OrderedPubSub) SubscribeOrdered(topic string, opts SubOptions) (*OrderedSubscription, error) {
	sub, err := ops.Subscribe(context.Background(), topic, opts)
	if err != nil {
		return nil, err
	}

	return &OrderedSubscription{
		Subscription: sub,
		tracker: &sequenceTracker{
			missing: make(map[uint64]bool),
		},
		topic: topic,
	}, nil
}

// Receive receives a message and verifies sequence
func (os *OrderedSubscription) Receive(ctx context.Context) (Message, error) {
	select {
	case msg, ok := <-os.C():
		if !ok {
			return Message{}, newErr(ErrCodeClosed, "consume", "", "subscription is closed", nil)
		}

		// Verify sequence from message metadata
		if seqStr, ok := msg.Meta[MetaKeySequence]; ok {
			seq, err := strconv.ParseUint(seqStr, 10, 64)
			if err == nil {
				os.tracker.mu.Lock()
				expected := os.tracker.lastSequence + 1
				if seq < expected {
					os.tracker.mu.Unlock()
					return msg, ErrOutOfOrderMessage
				}
				if seq > expected {
					// Gap detected — record missing sequence numbers
					for s := expected; s < seq; s++ {
						os.tracker.missing[s] = true
					}
				}
				delete(os.tracker.missing, seq)
				os.tracker.lastSequence = seq
				os.tracker.mu.Unlock()
			}
		}

		return msg, nil

	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}
