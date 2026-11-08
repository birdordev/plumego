package mq

import (
	"time"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

// HealthStatus represents the health status of the broker.
type HealthStatus struct {
	Status      string          `json:"status"`
	Timestamp   time.Time       `json:"timestamp"`
	Uptime      string          `json:"uptime"`
	TotalTopics int             `json:"total_topics"`
	TotalSubs   int             `json:"total_subscribers"`
	MemoryUsage uint64          `json:"memory_usage,omitempty"`
	MemoryLimit uint64          `json:"memory_limit,omitempty"`
	Metrics     MetricsSnapshot `json:"metrics,omitempty"`
}

// MetricsSnapshot represents a snapshot of broker metrics.
type MetricsSnapshot struct {
	TotalPublished uint64        `json:"total_published"`
	TotalDelivered uint64        `json:"total_delivered"`
	TotalDropped   uint64        `json:"total_dropped"`
	ActiveTopics   int           `json:"active_topics"`
	ActiveSubs     int           `json:"active_subscribers"`
	AverageLatency time.Duration `json:"average_latency"`
	LastError      string        `json:"last_error,omitempty"`
	LastPanic      string        `json:"last_panic,omitempty"`
	LastPanicTime  time.Time     `json:"last_panic_time,omitempty"`
}

// ClusterStatus represents the status of the cluster.
type ClusterStatus struct {
	Status            string        `json:"status"`
	NodeID            string        `json:"node_id,omitempty"`
	Peers             []string      `json:"peers,omitempty"`
	ReplicationFactor int           `json:"replication_factor,omitempty"`
	SyncInterval      time.Duration `json:"sync_interval,omitempty"`
	TotalNodes        int           `json:"total_nodes,omitempty"`
	HealthyNodes      int           `json:"healthy_nodes,omitempty"`
	LastSyncTime      time.Time     `json:"last_sync_time,omitempty"`
}

// DeadLetterStats represents statistics about the dead letter queue.
type DeadLetterStats struct {
	Enabled         bool      `json:"enabled"`
	Topic           string    `json:"topic,omitempty"`
	TotalMessages   uint64    `json:"total_messages,omitempty"`
	CurrentCount    int       `json:"current_count,omitempty"`
	LastMessageTime time.Time `json:"last_message_time,omitempty"`
}

// HealthCheck returns the current health status of the broker.
func (b *InProcBroker) HealthCheck() HealthStatus {
	if b == nil || b.ps == nil {
		return HealthStatus{
			Status:    "unhealthy",
			Timestamp: time.Now(),
		}
	}

	status := HealthStatus{
		Status:    "healthy",
		Timestamp: time.Now(),
		Uptime:    time.Since(b.startTime).String(),
	}

	// Get topic and subscriber counts
	if snapper, ok := b.ps.(interface {
		ListTopics() []string
		GetSubscriberCount(topic string) int
	}); ok {
		topics := snapper.ListTopics()
		status.TotalTopics = len(topics)
		for _, topic := range topics {
			status.TotalSubs += snapper.GetSubscriberCount(topic)
		}
	}

	// Get memory usage
	status.MemoryUsage = b.GetMemoryUsage()
	status.MemoryLimit = b.config.MaxMemoryUsage

	// Get metrics snapshot
	if b.config.EnableMetrics {
		status.Metrics = b.getMetricsSnapshot()
	}

	return status
}

// UpdateConfig dynamically updates the broker configuration.
func (b *InProcBroker) UpdateConfig(cfg Config) error {
	if b == nil {
		return ErrNotInitialized
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	b.config = cfg
	return nil
}

// GetConfig returns the current broker configuration.
func (b *InProcBroker) GetConfig() Config {
	if b == nil {
		return Config{}
	}
	return b.config
}

// getMetricsSnapshot creates a snapshot of current metrics.
func (b *InProcBroker) getMetricsSnapshot() MetricsSnapshot {
	snapshot := MetricsSnapshot{}

	// Get pubsub metrics if available
	if snapper, ok := b.ps.(interface{ Snapshot() pubsub.MetricsSnapshot }); ok {
		pubsubSnap := snapper.Snapshot()

		// Aggregate metrics from all topics
		var totalPublished, totalDelivered, totalDropped uint64
		var activeSubs int

		for _, topicMetrics := range pubsubSnap.Topics {
			totalPublished += topicMetrics.PublishTotal
			totalDelivered += topicMetrics.DeliveredTotal

			// Sum all dropped counts
			for _, dropped := range topicMetrics.DroppedByPolicy {
				totalDropped += dropped
			}

			activeSubs += topicMetrics.SubscribersGauge
		}

		snapshot.TotalPublished = totalPublished
		snapshot.TotalDelivered = totalDelivered
		snapshot.TotalDropped = totalDropped
		snapshot.ActiveTopics = len(pubsubSnap.Topics)
		snapshot.ActiveSubs = activeSubs
	}

	// Add error and panic information
	if b.lastError != nil {
		snapshot.LastError = b.lastError.Error()
	}
	if b.lastPanic != nil {
		snapshot.LastPanic = b.lastPanic.Error()
		snapshot.LastPanicTime = b.lastPanicTime
	}

	return snapshot
}

// Snapshot exposes in-process pubsub metrics when supported.
func (b *InProcBroker) Snapshot() pubsub.MetricsSnapshot {
	if b == nil || b.ps == nil {
		return pubsub.MetricsSnapshot{}
	}
	if snapper, ok := b.ps.(interface{ Snapshot() pubsub.MetricsSnapshot }); ok {
		return snapper.Snapshot()
	}
	return pubsub.MetricsSnapshot{}
}

// GetClusterStatus returns the current cluster status.
func (b *InProcBroker) GetClusterStatus() ClusterStatus {
	if b == nil || !b.config.EnableCluster {
		return ClusterStatus{
			Status: "disabled",
		}
	}

	return ClusterStatus{
		Status:            "active",
		NodeID:            b.config.ClusterNodeID,
		Peers:             b.config.ClusterNodes,
		ReplicationFactor: b.config.ClusterReplicationFactor,
		SyncInterval:      b.config.ClusterSyncInterval,
	}
}
