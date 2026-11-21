package pubsub

import (
	"time"
)

// HealthStatus represents the health status of the pubsub system.
type HealthStatus struct {
	// Healthy indicates if the system is operational
	Healthy bool `json:"healthy"`

	// Status is a human-readable status message
	Status string `json:"status"`

	// Timestamp is when this health check was performed
	Timestamp time.Time `json:"timestamp"`

	// Uptime is how long the system has been running
	Uptime time.Duration `json:"uptime,omitempty"`

	// Metrics contains key health metrics
	Metrics HealthMetrics `json:"metrics"`

	// Details contains additional diagnostic information
	Details map[string]any `json:"details,omitempty"`
}

// HealthMetrics contains key metrics for health assessment.
type HealthMetrics struct {
	// TopicCount is the number of active topics
	TopicCount int `json:"topic_count"`

	// PatternCount is the number of active pattern subscriptions
	PatternCount int `json:"pattern_count"`

	// SubscriberCount is the total number of subscribers
	SubscriberCount int `json:"subscriber_count"`

	// PendingAsyncOps is the number of pending async operations
	PendingAsyncOps int64 `json:"pending_async_ops"`

	// WorkerPoolSize is the current worker pool usage
	WorkerPoolSize int `json:"worker_pool_size"`

	// WorkerPoolCapacity is the worker pool capacity
	WorkerPoolCapacity int `json:"worker_pool_capacity"`

	// WorkerPoolUsage is the percentage of worker pool in use
	WorkerPoolUsage float64 `json:"worker_pool_usage"`

	// TotalPublished is the total messages published
	TotalPublished uint64 `json:"total_published"`

	// TotalDelivered is the total messages delivered
	TotalDelivered uint64 `json:"total_delivered"`

	// TotalDropped is the total messages dropped
	TotalDropped uint64 `json:"total_dropped"`

	// DelayedMessages is the number of pending delayed messages
	DelayedMessages int `json:"delayed_messages,omitempty"`

	// HistorySize is the total messages in history
	HistorySize int `json:"history_size,omitempty"`
}

// Health returns the current health status of the pubsub system.
func (ps *InProcBroker) Health() HealthStatus {
	status := HealthStatus{
		Timestamp: time.Now(),
		Details:   make(map[string]any),
	}

	// Check if closed
	if ps.closed.Load() {
		status.Healthy = false
		status.Status = "closed"
		return status
	}

	// Check if draining
	if ps.draining.Load() {
		status.Healthy = true
		status.Status = "draining"
	} else {
		status.Healthy = true
		status.Status = "healthy"
	}

	// Collect metrics
	status.Metrics = ps.collectHealthMetrics()

	// Add details
	status.Details["shards"] = ps.shards.shardCount

	return status
}

// collectHealthMetrics gathers all health metrics.
func (ps *InProcBroker) collectHealthMetrics() HealthMetrics {
	metrics := HealthMetrics{}

	// Topic and pattern counts
	topics := ps.shards.listTopics()
	patterns := ps.shards.listPatterns()
	metrics.TopicCount = len(topics)
	metrics.PatternCount = len(patterns)

	// Subscriber counts
	for _, topic := range topics {
		metrics.SubscriberCount += ps.shards.getTopicSubscriberCount(topic)
	}
	for _, pattern := range patterns {
		metrics.SubscriberCount += ps.shards.getPatternSubscriberCount(pattern)
	}

	// Pending async operations (active worker pool slots in use)
	if ps.workerPool != nil {
		metrics.PendingAsyncOps = int64(ps.workerPool.Size())
	}

	// Worker pool
	if ps.workerPool != nil {
		metrics.WorkerPoolSize = ps.workerPool.Size()
		metrics.WorkerPoolCapacity = ps.workerPool.MaxSize()
		if metrics.WorkerPoolCapacity > 0 {
			metrics.WorkerPoolUsage = float64(metrics.WorkerPoolSize) / float64(metrics.WorkerPoolCapacity) * 100
		}
	}

	// Aggregate metrics from snapshot
	snapshot := ps.metrics.Snapshot()
	for _, tm := range snapshot.Topics {
		metrics.TotalPublished += tm.PublishTotal
		metrics.TotalDelivered += tm.DeliveredTotal
		for _, dropped := range tm.DroppedByPolicy {
			metrics.TotalDropped += dropped
		}
	}

	return metrics
}

// IsHealthy returns true if the pubsub system is healthy.
func (ps *InProcBroker) IsHealthy() bool {
	return !ps.closed.Load()
}

// ReadinessCheck performs a readiness check.
// Returns nil if ready, error otherwise.
func (ps *InProcBroker) ReadinessCheck() error {
	if ps.closed.Load() {
		return newErr(ErrCodeClosed, "readiness", "", "broker is closed", nil)
	}
	return nil
}

// LivenessCheck performs a liveness check.
// Returns nil if alive, error otherwise.
func (ps *InProcBroker) LivenessCheck() error {
	if ps.closed.Load() {
		return newErr(ErrCodeClosed, "liveness", "", "broker is closed", nil)
	}

	// Check if worker pool is responsive
	if ps.workerPool != nil {
		// If pool is at capacity, the system may be under backpressure.
		if ps.workerPool.Available() == 0 {
			return newErr(ErrCodeBackpressure, "liveness", "", "worker pool at capacity", nil)
		}
	}

	return nil
}

// DiagnosticInfo returns detailed diagnostic information.
func (ps *InProcBroker) DiagnosticInfo() map[string]any {
	info := make(map[string]any)

	info["closed"] = ps.closed.Load()
	info["draining"] = ps.draining.Load()
	if ps.workerPool != nil {
		info["pending_ops"] = ps.workerPool.Size()
	} else {
		info["pending_ops"] = 0
	}

	// Config info
	info["config"] = map[string]any{
		"shard_count":         ps.config.ShardCount,
		"default_buffer_size": ps.config.DefaultBufferSize,
		"default_policy":      ps.config.DefaultPolicy.String(),
		"worker_pool_size":    ps.config.WorkerPoolSize,
		"panic_recovery":      ps.config.EnablePanicRecovery,
	}

	// Shard info
	info["shards"] = ps.shards.shardStats()

	// Topic-to-shard mapping
	info["topic_shard_mapping"] = ps.shards.topicShardMapping()

	// Worker pool info
	if ps.workerPool != nil {
		info["worker_pool"] = map[string]any{
			"size":      ps.workerPool.Size(),
			"max_size":  ps.workerPool.MaxSize(),
			"available": ps.workerPool.Available(),
		}
	}

	// Metrics snapshot
	info["metrics"] = ps.metrics.Snapshot()

	return info
}
