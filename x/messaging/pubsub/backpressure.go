package pubsub

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Backpressure errors

// BackpressurePropagationPolicy defines how to handle backpressure feedback
type BackpressurePropagationPolicy int

const (
	// BackpressureNone disables backpressure
	BackpressureNone BackpressurePropagationPolicy = iota

	// BackpressureWarn returns warnings but allows publishing
	BackpressureWarn

	// BackpressureThrottle slows down publishing
	BackpressureThrottle

	// BackpressureBlock blocks publishing until pressure reduces
	BackpressureBlock
)

// BackpressureConfig configures backpressure behavior
type BackpressureConfig struct {
	// Enabled enables backpressure propagation
	Enabled bool

	// Policy determines backpressure behavior
	Policy BackpressurePropagationPolicy

	// HighWaterMark is the buffer fill percentage to trigger backpressure (0.0-1.0)
	HighWaterMark float64

	// LowWaterMark is the buffer fill percentage to release backpressure (0.0-1.0)
	LowWaterMark float64

	// CheckInterval is how often to check subscriber health
	CheckInterval time.Duration

	// SlowStartInitialRate is the initial publish rate when starting slow
	SlowStartInitialRate int

	// SlowStartMultiplier increases rate gradually
	SlowStartMultiplier float64

	// SlowStartMaxRate is the maximum rate during slow start
	SlowStartMaxRate int

	// BlockTimeout is max time to wait when policy is Block
	BlockTimeout time.Duration

	// Callback is called when backpressure state changes
	Callback func(topic string, active bool, pressure float64)
}

// DefaultBackpressureConfig returns default configuration
func DefaultBackpressureConfig() BackpressureConfig {
	return BackpressureConfig{
		Enabled:              true,
		Policy:               BackpressureThrottle,
		HighWaterMark:        0.8,
		LowWaterMark:         0.5,
		CheckInterval:        100 * time.Millisecond,
		SlowStartInitialRate: 10,
		SlowStartMultiplier:  1.5,
		SlowStartMaxRate:     1000,
		BlockTimeout:         5 * time.Second,
		Callback:             nil,
	}
}

// BackpressureSignal represents a backpressure event
type BackpressureSignal struct {
	Topic        string
	SubscriberID string
	BufferUsage  float64
	DroppedCount uint64
	Timestamp    time.Time
}

// BackpressureStats provides backpressure statistics
type BackpressureStats struct {
	ActivePressure   atomic.Bool
	SignalCount      atomic.Uint64
	ThrottledPublish atomic.Uint64
	BlockedPublish   atomic.Uint64
	CurrentRate      atomic.Int64
	TargetRate       atomic.Int64
	PressureLevel    atomic.Uint64 // 0-100
}

// BackpressureController manages backpressure feedback
type BackpressureController struct {
	ps     *InProcBroker
	config BackpressureConfig

	// Pressure tracking per topic
	topicPressure   map[string]*topicPressure
	topicPressureMu sync.RWMutex

	// Statistics
	stats BackpressureStats

	// Slow start state
	slowStart   bool
	currentRate int
	slowStartMu sync.Mutex

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed atomic.Bool
}

// topicPressure tracks backpressure for a topic
type topicPressure struct {
	topic           string
	pressureSignals atomic.Uint64
	avgBufferUsage  atomic.Uint64 // Stored as int (percentage * 100)
	lastUpdate      time.Time
	lastUpdateMu    sync.RWMutex
	active          atomic.Bool
}

// NewBackpressureController creates a new backpressure controller
func NewBackpressureController(ps *InProcBroker, config BackpressureConfig) *BackpressureController {
	if !config.Enabled {
		return nil
	}

	// Clamp watermarks to valid range (0.0, 1.0].
	if config.HighWaterMark <= 0 || config.HighWaterMark > 1.0 {
		config.HighWaterMark = 0.8
	}
	if config.LowWaterMark <= 0 || config.LowWaterMark >= config.HighWaterMark {
		config.LowWaterMark = config.HighWaterMark / 2
	}

	ctx, cancel := context.WithCancel(context.Background())

	bpc := &BackpressureController{
		ps:            ps,
		config:        config,
		topicPressure: make(map[string]*topicPressure),
		slowStart:     true,
		currentRate:   config.SlowStartInitialRate,
		ctx:           ctx,
		cancel:        cancel,
	}

	bpc.stats.CurrentRate.Store(int64(config.SlowStartInitialRate))
	bpc.stats.TargetRate.Store(int64(config.SlowStartMaxRate))

	// Start background workers
	bpc.wg.Add(2)
	go bpc.monitorPressure()
	go bpc.slowStartWorker()

	return bpc
}

// ReportPressure reports backpressure from a subscriber
func (bpc *BackpressureController) ReportPressure(signal BackpressureSignal) {
	if bpc == nil || bpc.closed.Load() {
		return
	}

	bpc.topicPressureMu.Lock()
	tp, exists := bpc.topicPressure[signal.Topic]
	if !exists {
		tp = &topicPressure{
			topic:      signal.Topic,
			lastUpdate: time.Now(),
		}
		bpc.topicPressure[signal.Topic] = tp
	}
	bpc.topicPressureMu.Unlock()

	// Update pressure metrics
	tp.pressureSignals.Add(1)
	tp.avgBufferUsage.Store(uint64(signal.BufferUsage * 10000))
	tp.lastUpdateMu.Lock()
	tp.lastUpdate = signal.Timestamp
	tp.lastUpdateMu.Unlock()

	bpc.stats.SignalCount.Add(1)

	// Check if pressure is high
	if signal.BufferUsage >= bpc.config.HighWaterMark {
		if !tp.active.Load() {
			tp.active.Store(true)
			bpc.stats.ActivePressure.Store(true)
			bpc.stats.PressureLevel.Store(uint64(signal.BufferUsage * 100))

			// Callback
			if bpc.config.Callback != nil {
				bpc.config.Callback(signal.Topic, true, signal.BufferUsage)
			}
		}
	} else if signal.BufferUsage <= bpc.config.LowWaterMark {
		if tp.active.Load() {
			tp.active.Store(false)
			bpc.updateGlobalPressure()

			// Callback
			if bpc.config.Callback != nil {
				bpc.config.Callback(signal.Topic, false, signal.BufferUsage)
			}
		}
	}
}

// CheckPublish checks if publishing should proceed
func (bpc *BackpressureController) CheckPublish(topic string) error {
	if bpc == nil || !bpc.config.Enabled {
		return nil
	}

	if bpc.closed.Load() {
		return ErrBackpressureClosed
	}

	// Check topic pressure
	bpc.topicPressureMu.RLock()
	tp, exists := bpc.topicPressure[topic]
	bpc.topicPressureMu.RUnlock()

	if !exists || !tp.active.Load() {
		return nil
	}

	// Handle based on policy
	switch bpc.config.Policy {
	case BackpressureNone:
		return nil

	case BackpressureWarn:
		return ErrBackpressureActive

	case BackpressureThrottle:
		bpc.stats.ThrottledPublish.Add(1)
		// Apply rate limiting
		if bpc.slowStart {
			// Wait based on current rate
			delay := time.Second / time.Duration(bpc.currentRate)
			time.Sleep(delay)
		}
		return ErrBackpressureActive

	case BackpressureBlock:
		bpc.stats.BlockedPublish.Add(1)
		// Poll until pressure clears or the deadline expires.
		deadline := time.Now().Add(bpc.config.BlockTimeout)
		for time.Now().Before(deadline) {
			if !tp.active.Load() {
				return nil
			}
			time.Sleep(50 * time.Millisecond)
		}

		// Still under pressure after timeout
		return ErrBackpressureActive
	}

	return nil
}

// GetPressureLevel returns current pressure level for a topic (0-100)
func (bpc *BackpressureController) GetPressureLevel(topic string) int {
	if bpc == nil {
		return 0
	}

	bpc.topicPressureMu.RLock()
	tp, exists := bpc.topicPressure[topic]
	bpc.topicPressureMu.RUnlock()

	if !exists {
		return 0
	}

	usage := tp.avgBufferUsage.Load()
	return int(usage / 100)
}

// IsUnderPressure checks if a topic is under backpressure
func (bpc *BackpressureController) IsUnderPressure(topic string) bool {
	if bpc == nil {
		return false
	}

	bpc.topicPressureMu.RLock()
	tp, exists := bpc.topicPressure[topic]
	bpc.topicPressureMu.RUnlock()

	if !exists {
		return false
	}

	return tp.active.Load()
}

// updateGlobalPressure updates global pressure state
func (bpc *BackpressureController) updateGlobalPressure() {
	bpc.topicPressureMu.RLock()
	defer bpc.topicPressureMu.RUnlock()

	anyActive := false
	maxPressure := uint64(0)

	for _, tp := range bpc.topicPressure {
		if tp.active.Load() {
			anyActive = true
			usage := tp.avgBufferUsage.Load()
			if usage > maxPressure {
				maxPressure = usage
			}
		}
	}

	bpc.stats.ActivePressure.Store(anyActive)
	bpc.stats.PressureLevel.Store(maxPressure / 100)
}

// monitorPressure periodically checks and updates pressure state
func (bpc *BackpressureController) monitorPressure() {
	defer bpc.wg.Done()

	ticker := time.NewTicker(bpc.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bpc.updateGlobalPressure()

			// Clean up stale topic pressure data
			bpc.cleanupStalePressure()

		case <-bpc.ctx.Done():
			return
		}
	}
}

// cleanupStalePressure removes old pressure tracking data
func (bpc *BackpressureController) cleanupStalePressure() {
	bpc.topicPressureMu.Lock()
	defer bpc.topicPressureMu.Unlock()

	staleThreshold := time.Now().Add(-10 * time.Minute)

	for topic, tp := range bpc.topicPressure {
		tp.lastUpdateMu.RLock()
		lastUpdate := tp.lastUpdate
		tp.lastUpdateMu.RUnlock()

		if lastUpdate.Before(staleThreshold) {
			delete(bpc.topicPressure, topic)
		}
	}
}

// slowStartWorker gradually increases publish rate
func (bpc *BackpressureController) slowStartWorker() {
	defer bpc.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bpc.slowStartMu.Lock()
			// Re-check under the lock to avoid a data race with ResetSlowStart.
			if !bpc.slowStart {
				bpc.slowStartMu.Unlock()
				continue
			}

			// Check if we're under pressure
			if bpc.stats.ActivePressure.Load() {
				// Reduce rate
				bpc.currentRate = int(float64(bpc.currentRate) / bpc.config.SlowStartMultiplier)
				if bpc.currentRate < bpc.config.SlowStartInitialRate {
					bpc.currentRate = bpc.config.SlowStartInitialRate
				}
			} else {
				// Increase rate
				bpc.currentRate = int(float64(bpc.currentRate) * bpc.config.SlowStartMultiplier)
				if bpc.currentRate >= bpc.config.SlowStartMaxRate {
					bpc.currentRate = bpc.config.SlowStartMaxRate
					bpc.slowStart = false
				}
			}

			bpc.stats.CurrentRate.Store(int64(bpc.currentRate))
			bpc.slowStartMu.Unlock()

		case <-bpc.ctx.Done():
			return
		}
	}
}

// ResetSlowStart resets slow start to initial state
func (bpc *BackpressureController) ResetSlowStart() {
	if bpc == nil {
		return
	}

	bpc.slowStartMu.Lock()
	defer bpc.slowStartMu.Unlock()

	bpc.slowStart = true
	bpc.currentRate = bpc.config.SlowStartInitialRate
	bpc.stats.CurrentRate.Store(int64(bpc.currentRate))
}

// Stats returns backpressure statistics.
func (bpc *BackpressureController) Stats() *BackpressureStats {
	if bpc == nil {
		return &BackpressureStats{}
	}

	stats := &BackpressureStats{}
	stats.ActivePressure.Store(bpc.stats.ActivePressure.Load())
	stats.SignalCount.Store(bpc.stats.SignalCount.Load())
	stats.ThrottledPublish.Store(bpc.stats.ThrottledPublish.Load())
	stats.BlockedPublish.Store(bpc.stats.BlockedPublish.Load())
	stats.CurrentRate.Store(bpc.stats.CurrentRate.Load())
	stats.TargetRate.Store(bpc.stats.TargetRate.Load())
	stats.PressureLevel.Store(bpc.stats.PressureLevel.Load())

	return stats
}

// Close stops the backpressure controller
func (bpc *BackpressureController) Close() error {
	if bpc == nil {
		return nil
	}

	if !bpc.closed.CompareAndSwap(false, true) {
		return ErrBackpressureClosed
	}

	bpc.cancel()
	bpc.wg.Wait()

	return nil
}

// PublishWithBackpressure publishes with automatic backpressure handling
func (bpc *BackpressureController) PublishWithBackpressure(topic string, msg Message) error {
	// Note: caller must ensure ps is not nil when bpc is nil
	if bpc == nil {
		return nil // Backpressure disabled, publish handled by caller
	}

	// Check backpressure
	if err := bpc.CheckPublish(topic); err != nil && bpc.config.Policy == BackpressureBlock {
		// For block policy, retry after pressure reduces
		return err
	}

	// Publish
	return bpc.ps.Publish(topic, msg)
}
