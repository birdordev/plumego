package mq

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"
)

// startMemSampler launches a goroutine that periodically samples memory usage
// and caches the result in cachedMemUsage. Interval: 5s.
func (b *InProcBroker) startMemSampler() {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		// Sample once immediately so the cache is populated before first Publish.
		b.sampleMemory()
		for {
			select {
			case <-ticker.C:
				b.sampleMemory()
			case <-b.memSamplerDone:
				return
			}
		}
	}()
}

// sampleMemory reads current memory stats and stores them in cachedMemUsage.
func (b *InProcBroker) sampleMemory() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	b.cachedMemUsage.Store(ms.Alloc)
}

// executeWithObservability wraps an operation with observability logic.
func (b *InProcBroker) executeWithObservability(
	ctx context.Context,
	op Operation,
	topic string,
	fn func() error,
) (err error) {
	start := time.Now()
	panicked := false
	defer func() {
		if recovered := recover(); recovered != nil {
			panicked = true
			err = b.handlePanic(ctx, op, recovered)
		}
		b.observe(ctx, op, topic, start, err, panicked)
	}()
	return fn()
}

// checkMemoryLimit checks if cached memory usage exceeds the configured limit.
// The cache is updated by a background goroutine every 5 seconds to avoid
// runtime.ReadMemStats STW pauses on every Publish call.
func (b *InProcBroker) checkMemoryLimit() error {
	if b.config.MaxMemoryUsage == 0 {
		return nil // No limit configured
	}
	usage := b.cachedMemUsage.Load()
	if usage > b.config.MaxMemoryUsage {
		return fmt.Errorf("%w: memory usage %d bytes exceeds limit %d bytes",
			ErrMemoryLimitExceeded, usage, b.config.MaxMemoryUsage)
	}
	return nil
}

// CloseWithContext shuts down the broker and passes ctx to shutdown observers.
// It reports errors from both persistence and the underlying broker. Persistence
// is closed at most once, even if its Close returns an error.
func (b *InProcBroker) CloseWithContext(ctx context.Context) error {
	return b.executeWithObservability(ctx, OpClose, "", func() error {
		// Validate broker initialization
		if b == nil || b.ps == nil {
			return nil // Close is idempotent
		}

		// Stop background memory sampler if it was started.
		// sync.Once guarantees the channel is closed exactly once even if
		// Close() is called concurrently from multiple goroutines.
		if b.memSamplerDone != nil {
			b.memSamplerStop.Do(func() { close(b.memSamplerDone) })
		}

		b.closePriorityDispatchers()

		// Close ack tracker if it exists
		if b.ackTracker != nil {
			b.ackTracker.close()
		}

		// Close TTL tracker if it exists
		if b.ttlTracker != nil {
			b.ttlTracker.close()
		}

		// Close transaction manager if it exists
		if b.txManager != nil {
			b.txManager.close()
		}

		// Close dead letter manager if it exists
		if b.deadLetterManager != nil {
			b.deadLetterManager.close()
		}

		if b.persistenceManager != nil {
			if err := b.persistenceManager.close(); err != nil {
				persistenceErr := fmt.Errorf("failed to close persistence: %w", err)
				b.lastError = persistenceErr
				return errors.Join(persistenceErr, b.ps.Close())
			}
		}

		return b.ps.Close()
	})
}

// Close shuts down the broker using a background context and reports errors
// from persistence and the underlying broker.
func (b *InProcBroker) Close() error {
	return b.CloseWithContext(context.Background())
}

func (b *InProcBroker) handlePanic(ctx context.Context, op Operation, recovered any) error {
	if b != nil && b.panicHandler != nil {
		func() {
			defer func() {
				_ = recover()
			}()
			b.panicHandler(ctx, op, recovered)
		}()
	}
	return fmt.Errorf("%w: %s: %v", ErrRecoveredPanic, op, recovered)
}

func (b *InProcBroker) observe(ctx context.Context, op Operation, topic string, start time.Time, err error, panicked bool) {
	if b == nil || b.metrics == nil {
		return
	}

	duration := time.Since(start)

	func() {
		defer func() {
			if recovered := recover(); recovered != nil && b.panicHandler != nil {
				b.panicHandler(ctx, OpMetrics, recovered)
			}
		}()
		// Use the unified interface
		b.metrics.ObserveMQ(ctx, string(op), topic, duration, err, panicked)
	}()
}
