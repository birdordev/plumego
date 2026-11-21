package pubsub

import (
	"context"
	"sync"
	"sync/atomic"
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

type switchingMetricsObserver func(context.Context, string, string, time.Duration, error)

func (f switchingMetricsObserver) ObservePubSub(ctx context.Context, op, topic string, duration time.Duration, err error) {
	f(ctx, op, topic, duration, err)
}

func TestPubSubMetricsObserverConcurrentReplacement(t *testing.T) {
	var b *InProcBroker
	var firstCalls, secondCalls atomic.Int64
	first := switchingMetricsObserver(func(context.Context, string, string, time.Duration, error) {
		firstCalls.Add(1)
		_ = b.Config()
	})
	second := switchingMetricsObserver(func(context.Context, string, string, time.Duration, error) {
		secondCalls.Add(1)
		_ = b.Config()
	})
	b = New(WithMetricsObserver(first))
	t.Cleanup(func() { _ = b.Close() })
	publish := func() {
		t.Helper()
		if err := b.Publish("fixture", Message{ID: "one"}); err != nil {
			t.Error(err)
		}
	}
	publish()
	b.SetMetricsObserver(second)
	publish()
	b.SetMetricsObserver(nil)
	publish()
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 || b.Config().MetricsObserver != nil {
		t.Fatalf("first=%d second=%d config=%v", firstCalls.Load(), secondCalls.Load(), b.Config().MetricsObserver)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		for range 200 {
			b.SetMetricsObserver(first)
			b.SetMetricsObserver(second)
		}
	})
	wg.Go(func() {
		<-start
		for range 200 {
			publish()
			_ = b.Config()
		}
	})
	close(start)
	wg.Wait()
}
