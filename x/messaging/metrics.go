package messaging

import (
	"context"
	"time"

	"github.com/birdordev/plumego/metrics"
	"github.com/birdordev/plumego/x/messaging/mq"
)

type messagingMetrics interface {
	mq.MetricsObserver
	metrics.Recorder
}

// metricsWrapper wraps the configured metrics observer/recorder to provide messaging-specific
// helper methods for recording send latency, errors, and queue depth.
type metricsWrapper struct {
	collector messagingMetrics
}

func newMetrics(collector messagingMetrics) *metricsWrapper {
	return &metricsWrapper{collector: collector}
}

func (m *metricsWrapper) enabled() bool {
	return m != nil && m.collector != nil
}

// ObserveSend records a send operation (success or failure).
func (m *metricsWrapper) ObserveSend(ctx context.Context, channel Channel, provider string, duration time.Duration, err error) {
	if !m.enabled() {
		return
	}
	m.collector.ObserveMQ(ctx, "messaging_send", string(channel), duration, err, false)
	m.collector.Record(ctx, metrics.MetricRecord{
		Name:      "messaging.send." + string(channel),
		Value:     duration.Seconds(),
		Labels:    metrics.MetricLabels{"channel": string(channel), "provider": provider},
		Duration:  duration,
		Error:     err,
		Timestamp: time.Now(),
	})
}

// ObserveEnqueue records an enqueue operation.
func (m *metricsWrapper) ObserveEnqueue(ctx context.Context, channel Channel, duration time.Duration, err error) {
	if !m.enabled() {
		return
	}
	m.collector.ObserveMQ(ctx, "messaging_enqueue", string(channel), duration, err, false)
}

// ObserveValidation records a validation failure.
func (m *metricsWrapper) ObserveValidation(ctx context.Context, channel Channel) {
	if !m.enabled() {
		return
	}
	m.collector.Record(ctx, metrics.MetricRecord{
		Name:      "messaging.validation." + string(channel),
		Value:     1,
		Labels:    metrics.MetricLabels{"channel": string(channel)},
		Timestamp: time.Now(),
	})
}
