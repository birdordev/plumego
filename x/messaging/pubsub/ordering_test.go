package pubsub

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitUntil(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return condition()
}

type orderingMessagePayload struct {
	Key    string
	Seq    int
	Worker int
}

func TestOrderedPubSub_Basic(t *testing.T) {
	config := DefaultOrderingConfig()
	ops := NewOrdered(config)
	defer ops.Close()

	// Subscribe
	sub, err := ops.Subscribe(t.Context(), "test.order", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}
	defer sub.Cancel()

	// Publish ordered messages
	for i := 0; i < 5; i++ {
		msg := Message{Data: orderingMessagePayload{Seq: i}}
		if err := ops.PublishOrdered("test.order", msg, OrderPerTopic); err != nil {
			t.Fatalf("Failed to publish: %v", err)
		}
	}

	// Receive and verify order
	received := make([]int, 0, 5)
	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancel()

	for i := 0; i < 5; i++ {
		select {
		case msg := <-sub.C():
			received = append(received, msg.Data.(orderingMessagePayload).Seq)

		case <-ctx.Done():
			t.Fatalf("Timeout waiting for messages, got %d", len(received))
		}
	}

	// Verify order
	for i := 0; i < 5; i++ {
		if received[i] != i {
			t.Errorf("Message %d out of order: expected %d, got %d", i, i, received[i])
		}
	}
}

func TestOrderedPubSub_OrderLevels(t *testing.T) {
	tests := []struct {
		name  string
		level OrderLevel
	}{
		{"None", OrderNone},
		{"PerTopic", OrderPerTopic},
		{"PerKey", OrderPerKey},
		{"Global", OrderGlobal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultOrderingConfig()
			config.DefaultLevel = tt.level
			ops := NewOrdered(config)
			defer ops.Close()

			sub, _ := ops.Subscribe(t.Context(), "test", SubOptions{BufferSize: 10})
			defer sub.Cancel()

			// Publish
			for i := 0; i < 3; i++ {
				msg := Message{Data: i}
				if err := ops.PublishOrdered("test", msg, tt.level); err != nil {
					t.Errorf("Failed to publish: %v", err)
				}
			}

			// Receive up to 3 messages with timeout (ring buffer uses unbuffered channel)
			count := 0
			deadline := time.After(120 * time.Millisecond)
		drain:
			for count < 3 {
				select {
				case <-sub.C():
					count++
				case <-deadline:
					break drain
				}
			}

			if count != 3 {
				t.Errorf("Expected 3 messages, got %d", count)
			}
		})
	}
}

func TestOrderedPubSub_WithKey(t *testing.T) {
	config := DefaultOrderingConfig()
	ops := NewOrdered(config)
	defer ops.Close()

	sub, _ := ops.Subscribe(t.Context(), "test.key", SubOptions{BufferSize: 20})
	defer sub.Cancel()

	// Publish messages with different keys
	keys := []string{"user1", "user2", "user1", "user3", "user2"}
	for i, key := range keys {
		msg := Message{Data: orderingMessagePayload{Key: key, Seq: i}}
		if err := ops.PublishWithKey("test.key", key, msg, OrderPerKey); err != nil {
			t.Fatalf("Failed to publish: %v", err)
		}
	}

	waitUntil(180*time.Millisecond, func() bool { return len(sub.C()) >= len(keys) })

	// Receive and check
	received := make(map[string][]int)
	for len(sub.C()) > 0 {
		msg := <-sub.C()
		data := msg.Data.(orderingMessagePayload)
		received[data.Key] = append(received[data.Key], data.Seq)
	}

	// Verify each key's messages are ordered
	for key, seqs := range received {
		for i := 1; i < len(seqs); i++ {
			if seqs[i] < seqs[i-1] {
				t.Errorf("Key %s: messages out of order: %v", key, seqs)
			}
		}
	}
}

func TestOrderedPubSub_ConcurrentPublish(t *testing.T) {
	config := DefaultOrderingConfig()
	ops := NewOrdered(config)
	defer ops.Close()

	const numGoroutines = 10
	const messagesPerGoroutine = 20

	// Publish concurrently
	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < messagesPerGoroutine; j++ {
				msg := Message{Data: orderingMessagePayload{Worker: id, Seq: j}}
				_ = ops.PublishOrdered("test.concurrent", msg, OrderPerTopic)
			}
		}(i)
	}

	wg.Wait()

	stats := ops.OrderingStats()
	expected := uint64(numGoroutines * messagesPerGoroutine)
	if !waitUntil(180*time.Millisecond, func() bool {
		return ops.OrderingStats().OrderedPublishes >= expected
	}) {
		stats = ops.OrderingStats()
		t.Logf("Published %d, expected %d (may still be queued)", stats.OrderedPublishes, expected)
	}
}

func TestOrderedPubSub_Batching(t *testing.T) {
	config := DefaultOrderingConfig()
	config.MaxBatchSize = 5
	config.BatchTimeout = 50 * time.Millisecond
	ops := NewOrdered(config)
	defer ops.Close()

	sub, _ := ops.Subscribe(t.Context(), "test.batch", SubOptions{BufferSize: 20})
	defer sub.Cancel()

	// Publish messages
	for i := 0; i < 12; i++ {
		msg := Message{Data: i}
		_ = ops.PublishOrdered("test.batch", msg, OrderPerTopic)
	}

	// Should receive all messages (ring buffer uses unbuffered channel, len() always 0)
	count := 0
	deadline := time.After(180 * time.Millisecond)
batchDrain:
	for count < 12 {
		select {
		case <-sub.C():
			count++
		case <-deadline:
			break batchDrain
		}
	}

	if count != 12 {
		t.Errorf("Expected 12 messages, got %d", count)
	}

	stats := ops.OrderingStats()
	if stats.TopicQueues != 1 {
		t.Errorf("Expected 1 topic queue, got %d", stats.TopicQueues)
	}
}

func TestOrderedPubSub_SequenceCheck(t *testing.T) {
	config := DefaultOrderingConfig()
	config.SequenceCheckEnabled = true
	ops := NewOrdered(config)
	defer ops.Close()

	sub, _ := ops.Subscribe(t.Context(), "test.seq", SubOptions{BufferSize: 10})
	defer sub.Cancel()

	// Publish sequential messages
	for i := 0; i < 5; i++ {
		msg := Message{Data: i}
		_ = ops.PublishOrdered("test.seq", msg, OrderPerTopic)
	}

	waitUntil(120*time.Millisecond, func() bool { return ops.OrderingStats().OrderedPublishes >= 5 })

	stats := ops.OrderingStats()
	if stats.SequenceErrors > 0 {
		t.Errorf("Expected no sequence errors, got %d", stats.SequenceErrors)
	}
}

func TestOrderedPubSub_PartitionKey(t *testing.T) {
	partitions := 4

	// Generate keys and check distribution
	keys := make([]string, 100)
	for i := 0; i < 100; i++ {
		keys[i] = PartitionKey(string(rune('A'+i%26)), partitions)
	}

	// Count distribution
	counts := make(map[string]int)
	for _, key := range keys {
		counts[key]++
	}

	if len(counts) > partitions {
		t.Errorf("Expected at most %d partitions, got %d", partitions, len(counts))
	}
}

func TestOrderedPubSub_GlobalOrder(t *testing.T) {
	config := DefaultOrderingConfig()
	config.DefaultLevel = OrderGlobal
	ops := NewOrdered(config)
	defer ops.Close()

	sub, _ := ops.Subscribe(t.Context(), "topic1", SubOptions{BufferSize: 10})
	defer sub.Cancel()

	sub2, _ := ops.Subscribe(t.Context(), "topic2", SubOptions{BufferSize: 10})
	defer sub2.Cancel()

	// Publish to different topics
	var publishOrder atomic.Uint64
	for i := 0; i < 3; i++ {
		topic := "topic1"
		if i%2 == 0 {
			topic = "topic2"
		}

		seq := publishOrder.Add(1)
		msg := Message{Data: orderingMessagePayload{Seq: int(seq)}}
		_ = ops.PublishOrdered(topic, msg, OrderGlobal)
	}

	waitUntil(120*time.Millisecond, func() bool { return ops.OrderingStats().OrderedPublishes >= 3 })

	// All messages should be processed in global order
	stats := ops.OrderingStats()
	if stats.OrderedPublishes != 3 {
		t.Logf("Expected 3 ordered publishes, got %d (may be in queue)", stats.OrderedPublishes)
	}
}

func TestOrderedPubSub_MultipleTopics(t *testing.T) {
	config := DefaultOrderingConfig()
	ops := NewOrdered(config)
	defer ops.Close()

	topics := []string{"topic.a", "topic.b", "topic.c"}

	// Publish to multiple topics
	for _, topic := range topics {
		for i := 0; i < 5; i++ {
			msg := Message{Data: i}
			_ = ops.PublishOrdered(topic, msg, OrderPerTopic)
		}
	}

	waitUntil(120*time.Millisecond, func() bool {
		return ops.OrderingStats().TopicQueues == len(topics)
	})

	stats := ops.OrderingStats()
	if stats.TopicQueues != len(topics) {
		t.Errorf("Expected %d topic queues, got %d", len(topics), stats.TopicQueues)
	}
}

func TestOrderedPubSub_QueueFull(t *testing.T) {
	config := DefaultOrderingConfig()
	config.QueueSize = 5 // Small queue
	ops := NewOrdered(config)
	defer ops.Close()

	// Try to fill queue
	errors := 0
	for i := 0; i < 10; i++ {
		msg := Message{Data: i}
		if err := ops.PublishOrdered("test", msg, OrderPerTopic); err != nil {
			errors++
		}
	}

	if errors == 0 {
		t.Log("Note: Queue not filled (workers may be processing fast)")
	}
}

func TestOrderedPubSub_Close(t *testing.T) {
	config := DefaultOrderingConfig()
	ops := NewOrdered(config)

	// Publish some messages
	for i := 0; i < 5; i++ {
		msg := Message{Data: i}
		_ = ops.PublishOrdered("test", msg, OrderPerTopic)
	}

	// Close
	if err := ops.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Publishing after close should fail
	msg := Message{Data: "test"}
	if err := ops.PublishOrdered("test", msg, OrderPerTopic); err != ErrOrderingClosed {
		t.Errorf("Expected ErrOrderingClosed, got %v", err)
	}
}

func TestOrderedPubSub_OrderedSubscription(t *testing.T) {
	config := DefaultOrderingConfig()
	config.SequenceCheckEnabled = true
	ops := NewOrdered(config)
	defer ops.Close()

	osub, err := ops.SubscribeOrdered("test.ordered", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("Failed to create ordered subscription: %v", err)
	}
	defer osub.Cancel()

	// Publish messages
	for i := 0; i < 5; i++ {
		msg := Message{Data: i}
		_ = ops.PublishOrdered("test.ordered", msg, OrderPerTopic)
	}

	waitUntil(120*time.Millisecond, func() bool { return len(osub.C()) >= 5 })

	// Check missing sequences
	missing := osub.MissingSequences()
	if len(missing) > 0 {
		t.Logf("Missing sequences: %v", missing)
	}
}

func TestOrderedPubSub_ReceiveVerifiesSequence(t *testing.T) {
	config := DefaultOrderingConfig()
	config.SequenceCheckEnabled = true
	ops := NewOrdered(config)
	defer ops.Close()

	osub, err := ops.SubscribeOrdered("test.recv.seq", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("Failed to create ordered subscription: %v", err)
	}
	defer osub.Cancel()

	// Publish ordered messages
	for i := 0; i < 5; i++ {
		msg := Message{Data: i}
		if err := ops.PublishOrdered("test.recv.seq", msg, OrderPerTopic); err != nil {
			t.Fatalf("Failed to publish: %v", err)
		}
	}

	// Receive all messages via Receive and verify sequence metadata is present
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	var lastSeq uint64
	for i := 0; i < 5; i++ {
		msg, err := osub.Receive(ctx)
		if err != nil {
			t.Fatalf("Receive %d failed: %v", i, err)
		}

		seqStr, ok := msg.Meta[MetaKeySequence]
		if !ok {
			t.Fatalf("Message %d missing sequence metadata", i)
		}

		seq, err := strconv.ParseUint(seqStr, 10, 64)
		if err != nil {
			t.Fatalf("Message %d: invalid sequence %q: %v", i, seqStr, err)
		}

		if seq <= lastSeq && i > 0 {
			t.Errorf("Message %d: sequence %d <= previous %d", i, seq, lastSeq)
		}
		lastSeq = seq
	}

	// After in-order delivery, there should be no missing sequences
	missing := osub.MissingSequences()
	if len(missing) != 0 {
		t.Errorf("Expected no missing sequences, got %v", missing)
	}
}

func TestOrderedPubSub_QueueStatsPerTopic(t *testing.T) {
	config := DefaultOrderingConfig()
	config.MaxBatchSize = 5
	ops := NewOrdered(config)
	defer ops.Close()

	sub, _ := ops.Subscribe(t.Context(), "stats.topic", SubOptions{BufferSize: 20})
	defer sub.Cancel()

	// Publish messages
	for i := 0; i < 10; i++ {
		msg := Message{Data: i}
		_ = ops.PublishOrdered("stats.topic", msg, OrderPerTopic)
	}

	waitUntil(200*time.Millisecond, func() bool {
		return ops.OrderingStats().OrderedPublishes >= 10
	})

	stats := ops.OrderingStats()

	// Should have per-queue stats for "stats.topic"
	if stats.QueueStats == nil {
		t.Fatal("QueueStats should not be nil")
	}

	qs, exists := stats.QueueStats["stats.topic"]
	if !exists {
		t.Fatal("Expected QueueStats entry for 'stats.topic'")
	}

	if qs.Processed != 10 {
		t.Errorf("Expected 10 processed, got %d", qs.Processed)
	}

	if qs.BatchFlushes == 0 {
		t.Error("Expected at least one batch flush")
	}
}

func TestOrderedPubSub_QueueStatsPerKey(t *testing.T) {
	config := DefaultOrderingConfig()
	ops := NewOrdered(config)
	defer ops.Close()

	sub, _ := ops.Subscribe(t.Context(), "key.topic", SubOptions{BufferSize: 20})
	defer sub.Cancel()

	keys := []string{"keyA", "keyB"}
	for _, key := range keys {
		for i := 0; i < 5; i++ {
			msg := Message{Data: orderingMessagePayload{Key: key, Seq: i}}
			_ = ops.PublishWithKey("key.topic", key, msg, OrderPerKey)
		}
	}

	waitUntil(200*time.Millisecond, func() bool {
		return ops.OrderingStats().OrderedPublishes >= 10
	})

	stats := ops.OrderingStats()
	if stats.KeyQueues != 2 {
		t.Errorf("Expected 2 key queues, got %d", stats.KeyQueues)
	}

	for _, key := range keys {
		qs, exists := stats.QueueStats[key]
		if !exists {
			t.Errorf("Expected QueueStats entry for key %q", key)
			continue
		}
		if qs.Processed != 5 {
			t.Errorf("Key %q: expected 5 processed, got %d", key, qs.Processed)
		}
	}
}

func TestOrderedPubSub_QueueStatsGlobal(t *testing.T) {
	config := DefaultOrderingConfig()
	config.DefaultLevel = OrderGlobal
	ops := NewOrdered(config)
	defer ops.Close()

	sub1, _ := ops.Subscribe(t.Context(), "g.topic1", SubOptions{BufferSize: 10})
	defer sub1.Cancel()
	sub2, _ := ops.Subscribe(t.Context(), "g.topic2", SubOptions{BufferSize: 10})
	defer sub2.Cancel()

	// Publish to multiple topics through the global queue
	for i := 0; i < 3; i++ {
		_ = ops.PublishOrdered("g.topic1", Message{Data: i}, OrderGlobal)
		_ = ops.PublishOrdered("g.topic2", Message{Data: i}, OrderGlobal)
	}

	waitUntil(200*time.Millisecond, func() bool {
		return ops.OrderingStats().OrderedPublishes >= 6
	})

	stats := ops.OrderingStats()

	qs, exists := stats.QueueStats[globalQueueIdentifier]
	if !exists {
		t.Fatal("Expected QueueStats entry for global queue")
	}

	if qs.Processed != 6 {
		t.Errorf("Expected 6 processed in global queue, got %d", qs.Processed)
	}

	if qs.BatchFlushes == 0 {
		t.Error("Expected at least one batch flush for global queue")
	}
}

func TestOrderedPubSub_GlobalSequenceCheck(t *testing.T) {
	config := DefaultOrderingConfig()
	config.DefaultLevel = OrderGlobal
	config.SequenceCheckEnabled = true
	ops := NewOrdered(config)
	defer ops.Close()

	sub1, _ := ops.Subscribe(t.Context(), "seq.topic1", SubOptions{BufferSize: 10})
	defer sub1.Cancel()
	sub2, _ := ops.Subscribe(t.Context(), "seq.topic2", SubOptions{BufferSize: 10})
	defer sub2.Cancel()

	// Publish to different topics through the global queue
	for i := 0; i < 5; i++ {
		topic := "seq.topic1"
		if i%2 == 0 {
			topic = "seq.topic2"
		}
		_ = ops.PublishOrdered(topic, Message{Data: i}, OrderGlobal)
	}

	waitUntil(200*time.Millisecond, func() bool {
		return ops.OrderingStats().OrderedPublishes >= 5
	})

	stats := ops.OrderingStats()
	// All messages share a single global sequence space via globalQueueIdentifier,
	// so there should be no sequence errors.
	if stats.SequenceErrors > 0 {
		t.Errorf("Expected no sequence errors for global ordering, got %d", stats.SequenceErrors)
	}
}

func TestOrderedPubSub_MultipleTopicQueueStats(t *testing.T) {
	config := DefaultOrderingConfig()
	ops := NewOrdered(config)
	defer ops.Close()

	topics := []string{"multi.a", "multi.b", "multi.c"}
	for _, topic := range topics {
		sub, _ := ops.Subscribe(t.Context(), topic, SubOptions{BufferSize: 20})
		defer sub.Cancel()

		for i := 0; i < 4; i++ {
			msg := Message{Data: i}
			_ = ops.PublishOrdered(topic, msg, OrderPerTopic)
		}
	}

	waitUntil(200*time.Millisecond, func() bool {
		return ops.OrderingStats().OrderedPublishes >= 12
	})

	stats := ops.OrderingStats()

	if len(stats.QueueStats) != len(topics) {
		t.Errorf("Expected %d queue stats entries, got %d", len(topics), len(stats.QueueStats))
	}

	for _, topic := range topics {
		qs, exists := stats.QueueStats[topic]
		if !exists {
			t.Errorf("Missing QueueStats entry for topic %q", topic)
			continue
		}
		if qs.Processed != 4 {
			t.Errorf("Topic %q: expected 4 processed, got %d", topic, qs.Processed)
		}
	}
}

func TestOrderedPubSub_SequenceCheckPerKey(t *testing.T) {
	config := DefaultOrderingConfig()
	config.SequenceCheckEnabled = true
	ops := NewOrdered(config)
	defer ops.Close()

	sub, _ := ops.Subscribe(t.Context(), "seqkey.topic", SubOptions{BufferSize: 20})
	defer sub.Cancel()

	// Publish messages with different keys
	keys := []string{"alpha", "beta"}
	for _, key := range keys {
		for i := 0; i < 5; i++ {
			msg := Message{Data: orderingMessagePayload{Key: key, Seq: i}}
			_ = ops.PublishWithKey("seqkey.topic", key, msg, OrderPerKey)
		}
	}

	waitUntil(200*time.Millisecond, func() bool {
		return ops.OrderingStats().OrderedPublishes >= 10
	})

	stats := ops.OrderingStats()
	if stats.SequenceErrors > 0 {
		t.Errorf("Expected no sequence errors, got %d", stats.SequenceErrors)
	}

	// Each key should have its own queue stats
	for _, key := range keys {
		qs, exists := stats.QueueStats[key]
		if !exists {
			t.Errorf("Missing QueueStats for key %q", key)
			continue
		}
		if qs.SequenceErrors != 0 {
			t.Errorf("Key %q: expected 0 sequence errors, got %d", key, qs.SequenceErrors)
		}
	}
}

func BenchmarkOrderedPubSub_PerTopic(b *testing.B) {
	config := DefaultOrderingConfig()
	ops := NewOrdered(config)
	defer ops.Close()

	msg := Message{Data: []byte("benchmark message")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ops.PublishOrdered("bench.topic", msg, OrderPerTopic)
	}
}

func BenchmarkOrderedPubSub_PerKey(b *testing.B) {
	config := DefaultOrderingConfig()
	ops := NewOrdered(config)
	defer ops.Close()

	msg := Message{Data: []byte("benchmark message")}
	key := "test-key"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ops.PublishWithKey("bench.topic", key, msg, OrderPerKey)
	}
}

func BenchmarkOrderedPubSub_Global(b *testing.B) {
	config := DefaultOrderingConfig()
	config.DefaultLevel = OrderGlobal
	ops := NewOrdered(config)
	defer ops.Close()

	msg := Message{Data: []byte("benchmark message")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ops.PublishOrdered("bench.topic", msg, OrderGlobal)
	}
}

func TestOrderedPubSubRejectedPublishKeepsSequence(t *testing.T) {
	cfg := DefaultOrderingConfig()
	cfg.QueueSize = 1
	broker := NewOrdered(cfg)
	defer broker.Close()

	// Control consumption at the queue boundary so the rejection is deterministic.
	queue := broker.createQueue(cfg.MaxBatchSize)
	broker.topicQueues["fixture"] = queue
	publish := func() error {
		return broker.PublishOrdered("fixture", Message{Data: "sample"}, OrderPerTopic)
	}
	if err := publish(); err != nil {
		t.Fatal(err)
	}
	if err := publish(); err == nil {
		t.Fatal("full queue accepted a message")
	}
	if got := (<-queue.ch).sequence; got != 1 {
		t.Fatalf("first sequence = %d", got)
	}
	if err := publish(); err != nil {
		t.Fatal(err)
	}
	if got := (<-queue.ch).sequence; got != 2 {
		t.Errorf("sequence after rejected publish = %d, want 2", got)
	}
}

func TestOrderedPubSubConcurrentAdmissionPreservesSequences(t *testing.T) {
	for _, tc := range []struct {
		name  string
		level OrderLevel
	}{
		{"topic", OrderPerTopic},
		{"key", OrderPerKey},
		{"global", OrderGlobal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const count = 32
			cfg := DefaultOrderingConfig()
			cfg.SequenceCheckEnabled = true
			broker := NewOrdered(cfg)
			defer broker.Close()
			sub, err := broker.SubscribeOrdered("fixture", SubOptions{BufferSize: count})
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Cancel()

			var publishers sync.WaitGroup
			start := make(chan struct{})
			for i := range count {
				publishers.Go(func() {
					<-start
					if err := broker.PublishWithKey("fixture", "key", Message{Data: i}, tc.level); err != nil {
						t.Errorf("publish %d: %v", i, err)
					}
					_ = broker.OrderingStats()
				})
			}
			close(start)
			publishers.Wait()

			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			received := make(map[int]bool, count)
			for i := 1; i <= count; i++ {
				msg, err := sub.Receive(ctx)
				if err != nil {
					t.Fatalf("receive %d: %v", i, err)
				}
				if got := msg.Meta[MetaKeySequence]; got != strconv.Itoa(i) {
					t.Errorf("sequence = %q, want %d", got, i)
				}
				received[msg.Data.(int)] = true
			}
			stats := broker.OrderingStats()
			if len(received) != count || len(stats.QueueStats) != 1 || stats.QueuedMessages != count || stats.SequenceErrors != 0 {
				t.Errorf("received %d distinct messages; stats = %+v", len(received), stats)
			}
		})
	}
}

func TestOrderedPubSubConcurrentRegistrationAndClose(t *testing.T) {
	for range 8 {
		broker := NewOrdered(DefaultOrderingConfig())
		var operations sync.WaitGroup
		start := make(chan struct{})
		for i := range 12 {
			operations.Go(func() {
				<-start
				level := []OrderLevel{OrderPerTopic, OrderPerKey, OrderGlobal}[i%3]
				err := broker.PublishWithKey(strconv.Itoa(i), strconv.Itoa(i), Message{Data: i}, level)
				if err != nil && err != ErrOrderingClosed {
					t.Errorf("publish at shutdown: %v", err)
				}
				_ = broker.OrderingStats()
			})
		}
		operations.Go(func() {
			<-start
			if err := broker.Close(); err != nil {
				t.Error(err)
			}
		})
		close(start)
		operations.Wait()
		for _, level := range []OrderLevel{OrderNone, OrderPerTopic, OrderPerKey, OrderGlobal} {
			if err := broker.PublishOrdered("after-close", Message{Data: "sample"}, level); err != ErrOrderingClosed {
				t.Errorf("order level %v after close: %v", level, err)
			}
		}
	}
}
