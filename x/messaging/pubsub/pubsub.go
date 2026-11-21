package pubsub

import (
	"context"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// InProcBroker is the in-memory Broker implementation.
//
// It implements Broker, PatternSubscriber, MQTTSubscriber, BatchPublisher,
// and Drainable. Callers should declare variables using the narrowest
// interface they depend on.
//
// Example:
//
//	b := pubsub.New(
//		pubsub.WithShardCount(32),
//		pubsub.WithWorkerPoolSize(512),
//	)
//	defer b.Close()
//
//	sub, err := b.Subscribe(ctx, "user.created", pubsub.DefaultSubOptions())
//	if err != nil {
//		// handle
//	}
//	defer sub.Cancel()
//
//	err = b.Publish("user.created", pubsub.Message{Data: "hello"})
type InProcBroker struct {
	shards     *shardedMap
	config     Config
	observerMu sync.RWMutex

	closed atomic.Bool
	nextID atomic.Uint64

	metrics    metricsPubSub
	workerPool *workerPool

	// draining state
	draining atomic.Bool
	drainWg  sync.WaitGroup
}

// Compile-time interface assertions.
var (
	_ Broker            = (*InProcBroker)(nil)
	_ PatternSubscriber = (*InProcBroker)(nil)
	_ MQTTSubscriber    = (*InProcBroker)(nil)
	_ BatchPublisher    = (*InProcBroker)(nil)
	_ Drainable         = (*InProcBroker)(nil)
)

// New creates a new InProcBroker with the given options.
//
// Capability features (history, scheduling, request-reply, etc.) are provided
// by wrapping the returned broker with sub-package constructors — not by
// passing Enable* flags here.
//
// Example:
//
//	b := pubsub.New(
//		pubsub.WithShardCount(32),
//		pubsub.WithDefaultBufferSize(64),
//		pubsub.WithWorkerPoolSize(512),
//	)
//	defer b.Close()
func New(opts ...Option) *InProcBroker {
	config := DefaultConfig()
	for _, opt := range opts {
		opt(&config)
	}

	b := &InProcBroker{
		shards:     newShardedMap(config.ShardCount),
		config:     config,
		workerPool: newWorkerPool(config.WorkerPoolSize),
	}
	b.nextID.Store(0)

	return b
}

func newBackgroundLifecycle() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func stopBackground(cancel context.CancelFunc, wg *sync.WaitGroup) {
	if cancel != nil {
		cancel()
	}
	if wg != nil {
		wg.Wait()
	}
}

// Close shuts down the broker and cancels all active subscriptions.
func (b *InProcBroker) Close() error {
	if b.closed.Swap(true) {
		return nil
	}

	// Wait for pending async operations
	b.workerPool.Close()

	// Cancel all subscribers
	for _, s := range b.shards.collectAllSubscribers() {
		s.Cancel()
	}

	return nil
}

// Drain blocks until all in-flight messages are delivered or ctx is cancelled.
func (b *InProcBroker) Drain(ctx context.Context) error {
	if b.closed.Load() {
		return newErr(ErrCodeClosed, "drain", "", "broker is closed", nil)
	}

	b.draining.Store(true)
	defer b.draining.Store(false)

	done := make(chan struct{})
	go func() {
		b.drainWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Subscribe creates a new subscription to an exact topic.
// When ctx is cancelled the subscription is automatically closed.
func (b *InProcBroker) Subscribe(ctx context.Context, topic string, opts SubOptions) (Subscription, error) {
	return b.subscribeInternal(ctx, topic, opts, false)
}

// SubscribePattern creates a subscription matching a glob pattern.
func (b *InProcBroker) SubscribePattern(pattern string, opts SubOptions) (Subscription, error) {
	return b.subscribeInternal(context.Background(), pattern, opts, true)
}

// SubscribePatternWithContext creates a pattern subscription cancelled when ctx is done.
func (b *InProcBroker) SubscribePatternWithContext(ctx context.Context, pattern string, opts SubOptions) (Subscription, error) {
	return b.subscribeInternal(ctx, pattern, opts, true)
}

// SubscribeMQTT creates a subscription using MQTT-style topic patterns.
func (b *InProcBroker) SubscribeMQTT(pattern string, opts SubOptions) (Subscription, error) {
	return b.subscribeMQTTInternal(context.Background(), pattern, opts)
}

// SubscribeMQTTWithContext creates an MQTT subscription cancelled when ctx is done.
func (b *InProcBroker) SubscribeMQTTWithContext(ctx context.Context, pattern string, opts SubOptions) (Subscription, error) {
	return b.subscribeMQTTInternal(ctx, pattern, opts)
}

// subscribeInternal handles both exact-topic and glob-pattern subscriptions.
func (b *InProcBroker) subscribeInternal(ctx context.Context, topic string, opts SubOptions, isPattern bool) (Subscription, error) {
	start := time.Now()
	var err error
	defer func() {
		b.recordMetrics(ctx, "subscribe", topic, time.Since(start), err)
	}()

	if b.closed.Load() {
		err = newErr(ErrCodeClosed, "subscribe", topic, "broker is closed", nil)
		return nil, err
	}

	topic = strings.TrimSpace(topic)
	if topic == "" {
		if isPattern {
			err = newErr(ErrCodeInvalidPattern, "subscribe", "", "pattern must not be empty", nil)
		} else {
			err = newErr(ErrCodeInvalidTopic, "subscribe", "", "topic must not be empty", nil)
		}
		return nil, err
	}

	if isPattern {
		if _, matchErr := path.Match(topic, "probe"); matchErr != nil {
			err = newErr(ErrCodeInvalidPattern, "subscribe", topic, "invalid glob pattern", matchErr)
			return nil, err
		}
	}

	opts = b.normalizeSubOptions(opts)
	if opts.BufferSize < 1 {
		err = newErr(ErrCodeInvalidOptions, "subscribe", topic, "buffer size must be at least 1", nil)
		return nil, err
	}

	id := b.nextID.Add(1)

	needsCtxWatch := ctx != nil && ctx.Done() != nil

	var cancel context.CancelFunc
	if needsCtxWatch {
		ctx, cancel = context.WithCancel(ctx)
	}

	kind := subKindTopic
	if isPattern {
		kind = subKindPattern
	}

	sub := &subscriber{
		id:     id,
		topic:  topic,
		opts:   opts,
		ps:     b,
		kind:   kind,
		done:   make(chan struct{}),
		cancel: cancel,
	}

	initSubscriberChannel(sub, opts)

	if isPattern {
		b.shards.addPattern(topic, id, sub)
	} else {
		b.shards.addTopic(topic, id, sub)
	}
	b.metrics.addSubs(topic, 1)

	b.notifySubscribe(topic, id)

	if needsCtxWatch {
		go func() {
			select {
			case <-ctx.Done():
				sub.Cancel()
			case <-sub.done:
			}
		}()
	}

	return sub, nil
}

// subscribeMQTTInternal handles MQTT-pattern subscriptions.
func (b *InProcBroker) subscribeMQTTInternal(ctx context.Context, pattern string, opts SubOptions) (Subscription, error) {
	start := time.Now()
	var err error
	defer func() {
		b.recordMetrics(ctx, "subscribe_mqtt", pattern, time.Since(start), err)
	}()

	if b.closed.Load() {
		err = newErr(ErrCodeClosed, "subscribe_mqtt", pattern, "broker is closed", nil)
		return nil, err
	}

	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		err = newErr(ErrCodeInvalidPattern, "subscribe_mqtt", "", "pattern must not be empty", nil)
		return nil, err
	}

	if validErr := ValidateMQTTPattern(pattern); validErr != nil {
		err = newErr(ErrCodeInvalidPattern, "subscribe_mqtt", pattern, validErr.Error(), validErr)
		return nil, err
	}

	opts = b.normalizeSubOptions(opts)
	if opts.BufferSize < 1 {
		err = newErr(ErrCodeInvalidOptions, "subscribe_mqtt", pattern, "buffer size must be at least 1", nil)
		return nil, err
	}

	id := b.nextID.Add(1)

	needsCtxWatch := ctx != nil && ctx.Done() != nil

	var cancel context.CancelFunc
	if needsCtxWatch {
		ctx, cancel = context.WithCancel(ctx)
	}

	sub := &subscriber{
		id:     id,
		topic:  pattern,
		opts:   opts,
		ps:     b,
		kind:   subKindMQTT,
		done:   make(chan struct{}),
		cancel: cancel,
	}

	initSubscriberChannel(sub, opts)

	b.shards.addMQTTPattern(pattern, id, sub)
	b.metrics.addSubs(pattern, 1)

	b.notifySubscribe(pattern, id)

	if needsCtxWatch {
		go func() {
			select {
			case <-ctx.Done():
				sub.Cancel()
			case <-sub.done:
			}
		}()
	}

	return sub, nil
}
