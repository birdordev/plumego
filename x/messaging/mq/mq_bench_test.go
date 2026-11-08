package mq

import (
	"fmt"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

func BenchmarkPublish(b *testing.B) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := b.Context()
	msg := Message{ID: "bench-1", Data: "benchmark data"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := broker.Publish(ctx, "bench-topic", msg); err != nil {
			b.Fatalf("publish error: %v", err)
		}
	}
}

func BenchmarkPublishBatch(b *testing.B) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := b.Context()
	msgs := make([]Message, 10)
	for i := 0; i < 10; i++ {
		msgs[i] = Message{ID: fmt.Sprintf("bench-%d", i), Data: "benchmark data"}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := broker.PublishBatch(ctx, "bench-topic", msgs); err != nil {
			b.Fatalf("publish batch error: %v", err)
		}
	}
}

func BenchmarkPublishPriority(b *testing.B) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := b.Context()
	msg := PriorityMessage{
		Message:  Message{ID: "bench-1", Data: "benchmark data"},
		Priority: PriorityNormal,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := broker.PublishPriority(ctx, "bench-topic", msg); err != nil {
			b.Fatalf("publish priority error: %v", err)
		}
	}
}

func BenchmarkSubscribe(b *testing.B) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := b.Context()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sub, err := broker.Subscribe(ctx, fmt.Sprintf("bench-topic-%d", i), SubOptions{BufferSize: 10})
		if err != nil {
			b.Fatalf("subscribe error: %v", err)
		}
		sub.Cancel()
	}
}

func BenchmarkPublishSubscribe(b *testing.B) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := b.Context()
	topic := "bench-pubsub"

	// Subscribe first
	sub, err := broker.Subscribe(ctx, topic, SubOptions{BufferSize: b.N})
	if err != nil {
		b.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	msg := Message{ID: "bench-1", Data: "benchmark data"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := broker.Publish(ctx, topic, msg); err != nil {
			b.Fatalf("publish error: %v", err)
		}
		<-sub.C() // Receive the message
	}
}

func BenchmarkConfigValidation(b *testing.B) {
	cfg := DefaultConfig()
	cfg.EnableCluster = true
	cfg.ClusterNodeID = "node-1"
	cfg.EnablePersistence = true
	cfg.PersistencePath = "/tmp/mq-data"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cfg.Validate(); err != nil {
			b.Fatalf("validate error: %v", err)
		}
	}
}

func BenchmarkValidateTopic(b *testing.B) {
	topic := "valid-topic-name"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validateTopic(topic); err != nil {
			b.Fatalf("validate topic error: %v", err)
		}
	}
}

func BenchmarkValidateMessage(b *testing.B) {
	msg := Message{ID: "bench-1", Data: "benchmark data"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validateMessage(msg); err != nil {
			b.Fatalf("validate message error: %v", err)
		}
	}
}

func BenchmarkHealthCheck(b *testing.B) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Subscribe to some topics to make health check more realistic
	ctx := b.Context()
	for i := 0; i < 5; i++ {
		sub, _ := broker.Subscribe(ctx, fmt.Sprintf("health-topic-%d", i), SubOptions{BufferSize: 10})
		defer sub.Cancel()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = broker.HealthCheck()
	}
}

func BenchmarkMemoryUsage(b *testing.B) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = broker.GetMemoryUsage()
	}
}

func BenchmarkCheckMemoryLimit(b *testing.B) {
	cfg := DefaultConfig()
	cfg.MaxMemoryUsage = 1 << 30 // 1GB limit
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := broker.checkMemoryLimit(); err != nil {
			b.Fatalf("check memory limit error: %v", err)
		}
	}
}

// Benchmark parallel publishing
func BenchmarkPublishParallel(b *testing.B) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := b.Context()
	msg := Message{ID: "bench-1", Data: "benchmark data"}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := broker.Publish(ctx, "bench-topic", msg); err != nil {
				b.Errorf("publish error: %v", err)
			}
		}
	})
}

// Benchmark parallel subscribing
func BenchmarkSubscribeParallel(b *testing.B) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := b.Context()
	counter := 0

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			counter++
			sub, err := broker.Subscribe(ctx, fmt.Sprintf("bench-topic-%d", counter), SubOptions{BufferSize: 10})
			if err != nil {
				b.Errorf("subscribe error: %v", err)
			}
			sub.Cancel()
		}
	})
}

// Benchmark TTL message publishing
func BenchmarkPublishTTL(b *testing.B) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 30 * time.Second
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := b.Context()
	ttlMsg := TTLMessage{
		Message:   Message{ID: "bench-ttl", Data: "benchmark ttl data"},
		ExpiresAt: time.Now().Add(30 * time.Second),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := broker.PublishTTL(ctx, "bench-topic", ttlMsg); err != nil {
			b.Fatalf("publish TTL error: %v", err)
		}
	}
}

// Benchmark priority + TTL message publishing
func BenchmarkPublishPriorityTTL(b *testing.B) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 30 * time.Second
	cfg.EnablePriorityQueue = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := b.Context()
	priorityTTLMsg := PriorityTTLMessage{
		Message:   Message{ID: "bench-priority-ttl", Data: "benchmark priority ttl data"},
		Priority:  PriorityNormal,
		ExpiresAt: time.Now().Add(30 * time.Second),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := broker.PublishPriorityTTL(ctx, "bench-topic", priorityTTLMsg); err != nil {
			b.Fatalf("publish priority TTL error: %v", err)
		}
	}
}

// Benchmark ACK + TTL message publishing
func BenchmarkPublishWithAckTTL(b *testing.B) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 30 * time.Second
	cfg.EnableAckSupport = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := b.Context()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ackTTLMsg := AckTTLMessage{
			Message:    Message{ID: fmt.Sprintf("bench-ack-ttl-%d", i), Data: "benchmark ack ttl data"},
			AckID:      fmt.Sprintf("bench-ack-%d", i),
			AckPolicy:  AckRequired,
			AckTimeout: 30 * time.Second,
			ExpiresAt:  time.Now().Add(30 * time.Second),
		}
		if err := broker.PublishWithAckTTL(ctx, "bench-topic", ackTTLMsg); err != nil {
			b.Fatalf("publish ACK TTL error: %v", err)
		}
	}
}

// Benchmark TTL validation
func BenchmarkValidateTTL(b *testing.B) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	expiresAt := time.Now().Add(30 * time.Second)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := broker.validateTTL(expiresAt); err != nil {
			b.Fatalf("validate TTL error: %v", err)
		}
	}
}

// Benchmark TTL tracker operations
func BenchmarkTTLTrackerTrack(b *testing.B) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 30 * time.Second
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	expiresAt := time.Now().Add(30 * time.Second)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msgID := fmt.Sprintf("msg-%d", i)
		if err := broker.ttlTracker.track(msgID, "bench-topic", expiresAt); err != nil {
			b.Fatalf("track error: %v", err)
		}
	}
}

// Benchmark TTL tracker stats
func BenchmarkTTLTrackerStats(b *testing.B) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 30 * time.Second
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Add some tracked messages
	expiresAt := time.Now().Add(30 * time.Second)
	for i := 0; i < 100; i++ {
		_ = broker.ttlTracker.track(fmt.Sprintf("msg-%d", i), "bench-topic", expiresAt)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = broker.ttlTracker.stats()
	}
}

// Benchmark parallel TTL publishing
func BenchmarkPublishTTLParallel(b *testing.B) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 30 * time.Second
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := b.Context()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			ttlMsg := TTLMessage{
				Message:   Message{ID: fmt.Sprintf("bench-ttl-%d", i), Data: "benchmark ttl data"},
				ExpiresAt: time.Now().Add(30 * time.Second),
			}
			if err := broker.PublishTTL(ctx, "bench-topic", ttlMsg); err != nil {
				b.Errorf("publish TTL error: %v", err)
			}
			i++
		}
	})
}
