package pubsub

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/metrics"
)

// stubCollector embeds NoopCollector for default implementations
// and only overrides Record, ObservePubSub, and GetStats to track PubSub-specific metrics.
type stubCollector struct {
	*metrics.NoopCollector
	mu      sync.Mutex
	ops     []string
	topics  []string
	errs    []error
	records []metrics.MetricRecord
	stats   metrics.CollectorStats
}

func (s *stubCollector) Record(_ context.Context, record metrics.MetricRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
}

func (s *stubCollector) ObservePubSub(_ context.Context, operation, topic string, _ time.Duration, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops = append(s.ops, operation)
	s.topics = append(s.topics, topic)
	s.errs = append(s.errs, err)
}

func (s *stubCollector) GetStats() metrics.CollectorStats { return s.stats }

func TestPubSubMetricsObserver(t *testing.T) {
	ps := New()
	defer ps.Close()

	collector := &stubCollector{
		NoopCollector: metrics.NewNoopCollector(),
	}
	ps.SetMetricsObserver(collector)

	if _, err := ps.Subscribe(t.Context(), "metrics", DefaultSubOptions()); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := ps.Publish("metrics", Message{ID: "m1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	collector.mu.Lock()
	defer collector.mu.Unlock()

	if len(collector.ops) < 2 {
		t.Fatalf("expected at least 2 pubsub metrics, got %d", len(collector.ops))
	}

	hasSubscribe := false
	hasPublish := false
	for _, op := range collector.ops {
		if op == "subscribe" {
			hasSubscribe = true
		}
		if op == "publish" {
			hasPublish = true
		}
	}

	if !hasSubscribe || !hasPublish {
		t.Fatalf("expected publish and subscribe metrics, got %v", collector.ops)
	}
}
