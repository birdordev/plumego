package pubsub

import (
	"context"
	"maps"
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

// OrderedPubSub wraps InProcBroker with ordering guarantees.
// Topic, partition key, and global queues have independent sequence spaces.
type OrderedPubSub struct {
	*InProcBroker

	config OrderingConfig

	// queuesMu serializes queue registration and admission with shutdown.
	queuesMu    sync.RWMutex
	topicQueues map[string]*orderedQueue
	keyQueues   map[string]*orderedQueue
	globalQueue *orderedQueue

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
	enqueueMu sync.Mutex
	sequence  uint64
	batch     []orderedMessage
	batchSize int
	verified  sequenceTracker // Owned by the queue worker.

	// Per-queue statistics
	processed      atomic.Uint64
	batchFlushes   atomic.Uint64
	sequenceErrors atomic.Uint64
}

// orderedMessage wraps a message with ordering metadata
type orderedMessage struct {
	topic    string
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
		ctx:          ctx,
		cancel:       cancel,
	}

	// Create global queue if needed
	if config.DefaultLevel == OrderGlobal {
		ops.createOrderedQueue(globalQueueIdentifier, OrderGlobal)
	}

	return ops
}

// PublishOrdered publishes a message with ordering guarantee
func (ops *OrderedPubSub) PublishOrdered(topic string, msg Message, level OrderLevel) error {
	return ops.PublishWithKey(topic, "", msg, level)
}

// PublishWithKey publishes a message with a partition key.
// Ordered queues copy Meta before returning and stamp the sequence on that copy.
func (ops *OrderedPubSub) PublishWithKey(topic, key string, msg Message, level OrderLevel) error {
	if ops.closed.Load() {
		return ErrOrderingClosed
	}

	var identifier string
	switch level {
	case OrderNone:
		return ops.Publish(topic, msg)
	case OrderPerTopic:
		identifier = topic
	case OrderPerKey:
		identifier = key
		if identifier == "" {
			identifier = topic
		}
	case OrderGlobal:
		identifier = globalQueueIdentifier
	default:
		return ErrInvalidOrderLevel
	}

	ops.queuesMu.RLock()
	queue := ops.orderedQueue(identifier, level)
	if queue == nil {
		ops.queuesMu.RUnlock()
		ops.queuesMu.Lock()
		defer ops.queuesMu.Unlock()
		if ops.closed.Load() {
			return ErrOrderingClosed
		}
		queue = ops.orderedQueue(identifier, level)
		if queue == nil {
			queue = ops.createOrderedQueue(identifier, level)
		}
	} else {
		defer ops.queuesMu.RUnlock()
		if ops.closed.Load() {
			return ErrOrderingClosed
		}
	}

	return ops.enqueueOrdered(queue, topic, msg)
}

// enqueueOrdered assigns sequence numbers in admission order. Only a successful
// enqueue advances the sequence, so a rejected message leaves no gap.
func (ops *OrderedPubSub) enqueueOrdered(queue *orderedQueue, topic string, msg Message) error {
	queue.enqueueMu.Lock()
	defer queue.enqueueMu.Unlock()

	msg.Meta = maps.Clone(msg.Meta)
	om := orderedMessage{
		topic:    topic,
		message:  msg,
		sequence: queue.sequence + 1,
		enqueued: time.Now(),
	}
	select {
	case queue.ch <- om:
		queue.sequence = om.sequence
		ops.queuedMessages.Add(1)
		return nil
	default:
		return newErr(ErrCodeBufferFull, "enqueue", "", "ordered queue is full", nil)
	}
}

// startQueueWorker starts a worker to process an ordered queue
func (ops *OrderedPubSub) startQueueWorker(queue *orderedQueue) {
	ops.wg.Add(1)
	go func() {
		defer ops.wg.Done()
		ops.processQueue(queue)
	}()
}

// processQueue processes messages from an ordered queue.
func (ops *OrderedPubSub) processQueue(queue *orderedQueue) {
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
				ops.flushBatch(queue)
				return
			}

			// Add to batch
			queue.mu.Lock()
			queue.batch = append(queue.batch, om)
			shouldFlush := len(queue.batch) >= queue.batchSize
			queue.mu.Unlock()

			if shouldFlush {
				ops.flushBatch(queue)
			}

		case <-ticker.C:
			// Periodic flush
			ops.flushBatch(queue)

		case <-ops.ctx.Done():
			ops.flushBatch(queue)
			return
		}
	}
}

// flushBatch publishes a batch in its queue's admission order.
func (ops *OrderedPubSub) flushBatch(queue *orderedQueue) {
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
			if err := queue.verifySequence(om.sequence); err != nil {
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

// Close stops ordered admission and waits for workers to flush their in-flight
// batches before closing the underlying broker.
func (ops *OrderedPubSub) Close() error {
	ops.queuesMu.Lock()
	if ops.closed.Swap(true) {
		ops.queuesMu.Unlock()
		return nil
	}
	// Once cancellation begins, no publisher can enqueue or register a worker.
	ops.cancel()
	ops.queuesMu.Unlock()

	ops.wg.Wait()
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
	// For duplicate identifiers, key queue statistics take precedence over
	// topic queue statistics, and global statistics take precedence over both.
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
