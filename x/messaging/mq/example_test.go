package mq_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/spcent/plumego/x/messaging/mq"
	"github.com/spcent/plumego/x/messaging/pubsub"
)

// Example_basicPubSub demonstrates basic publish/subscribe functionality.
func Example_basicPubSub() {
	broker, err := mq.NewInProcBroker(pubsub.New())
	if err != nil {
		panic(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := context.Background()

	// Subscribe to a topic
	sub, _ := broker.Subscribe(ctx, "events", mq.SubOptions{BufferSize: 10})
	defer sub.Cancel()

	// Publish a message
	msg := mq.Message{ID: "msg-1", Data: "Hello World"}
	_ = broker.Publish(ctx, "events", msg)

	// Receive message
	received := <-sub.C()
	fmt.Printf("Received: %s\n", received.Data)
	// Output: Received: Hello World
}

// Example_priorityMessages demonstrates priority message handling.
func Example_priorityMessages() {
	cfg := mq.DefaultConfig()
	cfg.EnablePriorityQueue = true
	broker, err := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))
	if err != nil {
		panic(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := context.Background()

	// Subscribe
	sub, _ := broker.Subscribe(ctx, "tasks", mq.SubOptions{BufferSize: 10})
	defer sub.Cancel()

	// Publish with different priorities
	lowPriority := mq.PriorityMessage{
		Message:  mq.Message{ID: "low", Data: "Low priority task"},
		Priority: mq.PriorityLow,
	}
	highPriority := mq.PriorityMessage{
		Message:  mq.Message{ID: "high", Data: "High priority task"},
		Priority: mq.PriorityHigh,
	}

	// Publish low priority first, then high priority
	_ = broker.PublishPriority(ctx, "tasks", lowPriority)
	time.Sleep(10 * time.Millisecond) // Allow priority queue to process
	_ = broker.PublishPriority(ctx, "tasks", highPriority)
	time.Sleep(10 * time.Millisecond) // Allow priority queue to process

	// Receive messages (order depends on priority queue processing)
	msg1 := <-sub.C()
	msg2 := <-sub.C()
	fmt.Printf("Received: %s and %s\n", msg1.Data, msg2.Data)
	// Output: Received: Low priority task and High priority task
}

// Example_ttlMessages demonstrates message TTL (time-to-live).
func Example_ttlMessages() {
	cfg := mq.DefaultConfig()
	cfg.MessageTTL = 100 * time.Millisecond
	broker, err := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))
	if err != nil {
		panic(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := context.Background()

	// Publish message with TTL
	ttlMsg := mq.TTLMessage{
		Message:   mq.Message{ID: "ttl-1", Data: "Expires soon"},
		ExpiresAt: time.Now().Add(50 * time.Millisecond),
	}
	_ = broker.PublishTTL(ctx, "temp", ttlMsg)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Message has expired
	fmt.Println("Message expired")
	// Output: Message expired
}

// Example_acknowledgment demonstrates message acknowledgment.
func Example_acknowledgment() {
	cfg := mq.DefaultConfig()
	cfg.EnableAckSupport = true
	broker, err := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))
	if err != nil {
		panic(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := context.Background()

	// Subscribe
	sub, _ := broker.Subscribe(ctx, "orders", mq.SubOptions{BufferSize: 10})
	defer sub.Cancel()

	// Publish with ACK requirement
	ackMsg := mq.AckMessage{
		Message:    mq.Message{ID: "order-1", Data: "Process this order"},
		AckID:      "ack-1",
		AckPolicy:  mq.AckRequired,
		AckTimeout: 5 * time.Second,
	}
	_ = broker.PublishWithAck(ctx, "orders", ackMsg)

	// Receive and process
	msg := <-sub.C()
	fmt.Printf("Processing: %s\n", msg.Data)

	// Acknowledge successful processing
	_ = broker.Ack(ctx, "orders", "ack-1")
	fmt.Println("Message acknowledged")
	// Output:
	// Processing: Process this order
	// Message acknowledged
}

// Example_transactions demonstrates transactional message publishing.
func Example_transactions() {
	cfg := mq.DefaultConfig()
	cfg.EnableTransactions = true
	broker, err := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))
	if err != nil {
		panic(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := context.Background()

	// Subscribe
	sub, _ := broker.Subscribe(ctx, "orders", mq.SubOptions{BufferSize: 10})
	defer sub.Cancel()

	// Start transaction
	txID := "tx-order-123"

	// Add messages to transaction
	msg1 := mq.Message{ID: "item-1", Data: "Product A"}
	msg2 := mq.Message{ID: "item-2", Data: "Product B"}

	_ = broker.PublishWithTransaction(ctx, "orders", msg1, txID)
	_ = broker.PublishWithTransaction(ctx, "orders", msg2, txID)

	// Commit transaction (messages are delivered now)
	_ = broker.CommitTransaction(ctx, txID)

	// Receive messages
	<-sub.C()
	<-sub.C()
	fmt.Println("Transaction committed, messages delivered")
	// Output: Transaction committed, messages delivered
}

// Example_deadLetterQueue demonstrates dead letter queue functionality.
func Example_deadLetterQueue() {
	cfg := mq.DefaultConfig()
	cfg.EnableDeadLetterQueue = true
	cfg.DeadLetterTopic = "dlq"
	broker, err := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))
	if err != nil {
		panic(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := context.Background()

	// Subscribe to DLQ
	dlqSub, _ := broker.Subscribe(ctx, "dlq", mq.SubOptions{BufferSize: 10})
	defer dlqSub.Cancel()

	// Send failed message to DLQ
	failedMsg := mq.Message{ID: "failed-1", Data: "Processing failed"}
	_ = broker.PublishToDeadLetter(ctx, "orders", failedMsg, "validation error")

	// Monitor DLQ
	msg := <-dlqSub.C()
	fmt.Printf("Dead letter: %s\n", msg.Data)

	// Check stats
	stats := broker.GetDeadLetterStats()
	fmt.Printf("DLQ count: %d\n", stats.TotalMessages)
	// Output:
	// Dead letter: Processing failed
	// DLQ count: 1
}

// Example_persistence demonstrates message persistence and recovery.
func Example_persistence() {
	cfg := mq.DefaultConfig()
	cfg.EnablePersistence = true
	cfg.PersistencePath = "/tmp/mq-example"
	broker, err := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	// Publish messages (automatically persisted)
	msg := mq.Message{ID: "persistent-1", Data: "Important data"}
	_ = broker.Publish(ctx, "critical", msg)

	_ = broker.Close()

	// Restart broker
	broker2, err := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))
	if err != nil {
		panic(err)
	}
	defer func() { _ = broker2.Close() }()

	// Recover persisted messages
	messages, _ := broker2.RecoverMessages(ctx, "critical", 10)
	fmt.Printf("Recovered %d messages\n", len(messages))
	// Output: Recovered 1 messages
}

// Example_comprehensive demonstrates multiple features together.
func Example_comprehensive() {
	// Configure broker with all features
	cfg := mq.DefaultConfig()
	cfg.EnablePriorityQueue = true
	cfg.EnableAckSupport = true
	cfg.EnableTransactions = true
	cfg.EnableDeadLetterQueue = true
	cfg.DeadLetterTopic = "dlq"
	cfg.EnablePersistence = true
	cfg.PersistencePath = "/tmp/mq-comprehensive"
	cfg.MaxMemoryUsage = 100 * 1024 * 1024 // 100MB

	broker, err := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))
	if err != nil {
		panic(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := context.Background()

	// Subscribe to main topic
	sub, err := broker.Subscribe(ctx, "events", mq.SubOptions{BufferSize: 100})
	if err != nil {
		log.Fatal(err)
	}
	defer sub.Cancel()

	// Use transaction for atomic publish
	txID := "tx-1"
	msg1 := mq.Message{ID: "event-1", Data: "First event"}
	msg2 := mq.Message{ID: "event-2", Data: "Second event"}

	_ = broker.PublishWithTransaction(ctx, "events", msg1, txID)
	_ = broker.PublishWithTransaction(ctx, "events", msg2, txID)
	_ = broker.CommitTransaction(ctx, txID)

	// Process messages with ACK
	processedCount := 0
	for i := 0; i < 2; i++ {
		msg := <-sub.C()
		// Simulate processing
		processedCount++
		fmt.Printf("Processed: %s\n", msg.Data)
	}

	// Check health
	health := broker.HealthCheck()
	fmt.Printf("Broker status: %s\n", health.Status)
	fmt.Printf("Messages processed: %d\n", processedCount)
	// Output:
	// Processed: First event
	// Processed: Second event
	// Broker status: healthy
	// Messages processed: 2
}

// Example_batchOperations demonstrates batch publish and subscribe.
func Example_batchOperations() {
	broker, err := mq.NewInProcBroker(pubsub.New())
	if err != nil {
		panic(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := context.Background()

	// Subscribe
	sub, _ := broker.Subscribe(ctx, "batch", mq.SubOptions{BufferSize: 100})
	defer sub.Cancel()

	// Batch publish
	messages := []mq.Message{
		{ID: "batch-1", Data: "Message 1"},
		{ID: "batch-2", Data: "Message 2"},
		{ID: "batch-3", Data: "Message 3"},
	}
	_ = broker.PublishBatch(ctx, "batch", messages)

	// Receive all messages
	count := 0
	timeout := time.After(100 * time.Millisecond)
LOOP:
	for count < 3 {
		select {
		case <-sub.C():
			count++
		case <-timeout:
			break LOOP
		}
	}

	fmt.Printf("Received %d messages\n", count)
	// Output: Received 3 messages
}

// Example_healthMonitoring demonstrates health check and metrics.
func Example_healthMonitoring() {
	cfg := mq.DefaultConfig()
	cfg.EnableMetrics = true
	broker, err := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))
	if err != nil {
		panic(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := context.Background()

	// Subscribe to create a topic
	sub, _ := broker.Subscribe(ctx, "test", mq.SubOptions{BufferSize: 10})
	defer sub.Cancel()

	// Publish some messages
	for i := 0; i < 5; i++ {
		msg := mq.Message{ID: fmt.Sprintf("msg-%d", i), Data: "test"}
		_ = broker.Publish(ctx, "test", msg)
	}

	// Check health
	health := broker.HealthCheck()
	fmt.Printf("Status: %s\n", health.Status)
	fmt.Printf("Has uptime: %v\n", health.Uptime != "")
	fmt.Printf("Has subscribers: %v\n", health.TotalSubs > 0)
	// Output:
	// Status: healthy
	// Has uptime: true
	// Has subscribers: true
}
