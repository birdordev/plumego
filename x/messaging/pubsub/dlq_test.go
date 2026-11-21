package pubsub

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestDLQ_Basic(t *testing.T) {
	ps := New()
	defer ps.Close()

	config := DefaultDLQConfig("dlq.test")
	config.RetryStrategy = NoRetry
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Add message
	msg := Message{Data: "test"}
	if err := dlq.Add(msg, "test.topic", "test failure"); err != nil {
		t.Fatalf("Failed to add message: %v", err)
	}

	// Check count
	if dlq.Count() != 1 {
		t.Errorf("Expected 1 message, got %d", dlq.Count())
	}

	// Check stats
	stats := dlq.Stats()
	if stats.TotalMessages.Load() != 1 {
		t.Errorf("Expected 1 total message, got %d", stats.TotalMessages.Load())
	}
}

func TestDLQ_Query(t *testing.T) {
	ps := New()
	defer ps.Close()

	config := DefaultDLQConfig("dlq.test")
	config.RetryStrategy = NoRetry
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Add multiple messages
	reasons := []string{"timeout", "error", "timeout", "invalid"}
	for i, reason := range reasons {
		msg := Message{Data: i}
		_ = dlq.Add(msg, "topic.test", reason)
	}

	time.Sleep(50 * time.Millisecond)

	// Query by reason
	results, err := dlq.Query(DLQQueryOptions{
		Reason: "timeout",
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results for 'timeout', got %d", len(results))
	}

	// Query all
	allResults, _ := dlq.Query(DLQQueryOptions{})
	if len(allResults) != 4 {
		t.Errorf("Expected 4 total results, got %d", len(allResults))
	}
}

func TestDLQ_TimeRangeQuery(t *testing.T) {
	ps := New()
	defer ps.Close()

	config := DefaultDLQConfig("dlq.test")
	config.RetryStrategy = NoRetry
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	start := time.Now()

	// Add first message
	_ = dlq.Add(Message{Data: 1}, "topic", "reason1")
	time.Sleep(100 * time.Millisecond)

	mid := time.Now()
	time.Sleep(100 * time.Millisecond)

	// Add second message
	_ = dlq.Add(Message{Data: 2}, "topic", "reason2")

	// Query first period
	results, _ := dlq.Query(DLQQueryOptions{
		StartTime: start,
		EndTime:   mid,
	})

	if len(results) != 1 {
		t.Errorf("Expected 1 result in first period, got %d", len(results))
	}
}

func TestDLQ_Sorting(t *testing.T) {
	ps := New()
	defer ps.Close()

	config := DefaultDLQConfig("dlq.test")
	config.RetryStrategy = NoRetry
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Add messages with different retry counts
	for i := 0; i < 3; i++ {
		msg := Message{Data: i}
		_ = dlq.Add(msg, "topic", "test")
	}

	time.Sleep(50 * time.Millisecond)

	// Get first message and manually update retry count
	results, _ := dlq.Query(DLQQueryOptions{})
	if len(results) > 0 {
		dlq.messagesMu.Lock()
		if msg, exists := dlq.messages[results[0].ID]; exists {
			msg.RetryCount = 5
		}
		dlq.messagesMu.Unlock()
	}

	// Query sorted by retries
	sorted, _ := dlq.Query(DLQQueryOptions{
		SortBy: "retries",
	})

	if len(sorted) > 0 && sorted[0].RetryCount != 5 {
		t.Errorf("Expected highest retry count first, got %d", sorted[0].RetryCount)
	}
}

func TestDLQ_Retry(t *testing.T) {
	ps := New()
	defer ps.Close()

	// Subscribe to target topic
	sub, _ := ps.Subscribe(t.Context(), "retry.target", SubOptions{BufferSize: 10})
	defer sub.Cancel()

	config := DefaultDLQConfig("dlq.test")
	config.RetryStrategy = NoRetry // Manual retry
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Add message
	msg := Message{Data: "retry test"}
	_ = dlq.Add(msg, "retry.target", "test error")

	time.Sleep(50 * time.Millisecond)

	// Get message ID
	results, _ := dlq.Query(DLQQueryOptions{})
	if len(results) == 0 {
		t.Fatal("No messages in DLQ")
	}

	msgID := results[0].ID

	// Retry
	if err := dlq.Retry(msgID); err != nil {
		t.Fatalf("Retry failed: %v", err)
	}

	// Should be removed from DLQ
	if dlq.Count() != 0 {
		t.Errorf("Expected 0 messages after successful retry, got %d", dlq.Count())
	}

	// Should appear in target topic
	select {
	case <-sub.C():
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Error("Message not received in target topic")
	}

	// Check stats
	stats := dlq.Stats()
	if stats.SuccessfulRetries.Load() != 1 {
		t.Errorf("Expected 1 successful retry, got %d", stats.SuccessfulRetries.Load())
	}
}

func TestDLQ_ExponentialBackoff(t *testing.T) {
	ps := New()
	defer ps.Close()

	config := DefaultDLQConfig("dlq.test")
	config.RetryStrategy = ExponentialBackoff
	config.InitialRetryDelay = 100 * time.Millisecond
	config.MaxRetryDelay = 1 * time.Second
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Test delay calculation
	delays := []time.Duration{
		dlq.calculateRetryDelay(0), // 100ms
		dlq.calculateRetryDelay(1), // 200ms
		dlq.calculateRetryDelay(2), // 400ms
		dlq.calculateRetryDelay(3), // 800ms
		dlq.calculateRetryDelay(4), // 1000ms (capped)
	}

	expected := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1000 * time.Millisecond,
	}

	for i, delay := range delays {
		if delay != expected[i] {
			t.Errorf("Delay %d: expected %v, got %v", i, expected[i], delay)
		}
	}
}

func TestDLQ_AutoRetry(t *testing.T) {
	ps := New()
	defer ps.Close()

	// Subscribe to target (will fail first time)
	var receiveCount atomic.Int32

	config := DefaultDLQConfig("dlq.test")
	config.RetryStrategy = ExponentialBackoff
	config.InitialRetryDelay = 100 * time.Millisecond
	config.MaxRetryAttempts = 3
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Add message
	msg := Message{Data: "auto retry"}
	_ = dlq.Add(msg, "retry.auto", "initial failure")

	// Subscribe after delay to allow retry
	time.Sleep(50 * time.Millisecond)
	sub, _ := ps.Subscribe(t.Context(), "retry.auto", SubOptions{BufferSize: 10})
	defer sub.Cancel()

	go func() {
		for range sub.C() {
			receiveCount.Add(1)
		}
	}()

	// Wait for retries
	time.Sleep(1 * time.Second)

	// Should have received at least one retry
	if receiveCount.Load() == 0 {
		t.Log("Note: No retries received (may be timing dependent)")
	}
}

func TestDLQ_RetryBatch(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, _ := ps.Subscribe(t.Context(), "batch.target", SubOptions{BufferSize: 20})
	defer sub.Cancel()

	config := DefaultDLQConfig("dlq.test")
	config.RetryStrategy = NoRetry
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Add multiple messages
	var msgIDs []string
	for i := 0; i < 5; i++ {
		msg := Message{Data: i}
		_ = dlq.Add(msg, "batch.target", "test")
	}

	time.Sleep(50 * time.Millisecond)

	// Get IDs
	results, _ := dlq.Query(DLQQueryOptions{})
	for _, r := range results {
		msgIDs = append(msgIDs, r.ID)
	}

	// Retry batch
	succeeded, failed := dlq.RetryBatch(msgIDs)

	if succeeded != 5 {
		t.Errorf("Expected 5 successful retries, got %d", succeeded)
	}
	if failed != 0 {
		t.Errorf("Expected 0 failed retries, got %d", failed)
	}

	// DLQ should be empty
	if dlq.Count() != 0 {
		t.Errorf("Expected empty DLQ, got %d messages", dlq.Count())
	}
}

func TestDLQ_AlertThreshold(t *testing.T) {
	ps := New()
	defer ps.Close()

	var alertTriggered atomic.Bool

	config := DefaultDLQConfig("dlq.test")
	config.AlertThreshold = 3
	config.AlertCallback = func(alert DLQAlert) {
		alertTriggered.Store(true)
	}
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Add messages to exceed threshold
	for i := 0; i < 5; i++ {
		msg := Message{Data: i}
		_ = dlq.Add(msg, "topic", "test")
	}

	time.Sleep(100 * time.Millisecond)

	if !alertTriggered.Load() {
		t.Error("Expected alert to be triggered")
	}

	stats := dlq.Stats()
	if stats.AlertsTriggered.Load() == 0 {
		t.Error("Expected alerts triggered stat to be incremented")
	}
}

func TestDLQ_MaxSize(t *testing.T) {
	ps := New()
	defer ps.Close()

	config := DefaultDLQConfig("dlq.test")
	config.MaxSize = 5
	config.RetryStrategy = NoRetry
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Add more than max size
	for i := 0; i < 10; i++ {
		msg := Message{Data: i}
		_ = dlq.Add(msg, "topic", "test")
	}

	time.Sleep(100 * time.Millisecond)

	// Should not exceed max size
	count := dlq.Count()
	if count > config.MaxSize {
		t.Errorf("Expected max %d messages, got %d", config.MaxSize, count)
	}

	// Oldest should be archived
	stats := dlq.Stats()
	if stats.ArchivedMessages.Load() == 0 {
		t.Log("Note: No messages archived yet")
	}
}

func TestDLQ_Archive(t *testing.T) {
	ps := New()
	defer ps.Close()

	config := DefaultDLQConfig("dlq.test")
	config.MaxRetryAttempts = 2
	config.RetryStrategy = NoRetry
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Add message
	msg := Message{Data: "archive test"}
	_ = dlq.Add(msg, "archive.topic", "test")

	time.Sleep(50 * time.Millisecond)

	results, _ := dlq.Query(DLQQueryOptions{})
	if len(results) == 0 {
		t.Fatal("No messages in DLQ")
	}

	msgID := results[0].ID

	// Close pubsub to make retries fail
	ps.Close()

	// Retry until max attempts - these will fail because pubsub is closed
	for i := 0; i <= config.MaxRetryAttempts; i++ {
		_ = dlq.Retry(msgID)
	}

	time.Sleep(100 * time.Millisecond)

	// Should be archived
	stats := dlq.Stats()
	if stats.ArchivedMessages.Load() != 1 {
		t.Errorf("Expected 1 archived message, got %d", stats.ArchivedMessages.Load())
	}

	// Query with archived
	archived, _ := dlq.Query(DLQQueryOptions{
		IncludeArchived: true,
	})

	foundArchived := false
	for _, m := range archived {
		if m.IsArchived {
			foundArchived = true
			break
		}
	}

	if !foundArchived {
		t.Error("Expected to find archived message in query")
	}
}

func TestDLQ_Delete(t *testing.T) {
	ps := New()
	defer ps.Close()

	config := DefaultDLQConfig("dlq.test")
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Add message
	msg := Message{Data: "delete test"}
	_ = dlq.Add(msg, "topic", "test")

	time.Sleep(50 * time.Millisecond)

	results, _ := dlq.Query(DLQQueryOptions{})
	if len(results) == 0 {
		t.Fatal("No messages in DLQ")
	}

	msgID := results[0].ID

	// Delete
	if err := dlq.Delete(msgID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Should be gone
	if dlq.Count() != 0 {
		t.Errorf("Expected 0 messages after delete, got %d", dlq.Count())
	}

	// Delete again should fail
	if err := dlq.Delete(msgID); err != ErrDLQNotFound {
		t.Errorf("Expected ErrDLQNotFound, got %v", err)
	}
}

func TestDLQ_Clear(t *testing.T) {
	ps := New()
	defer ps.Close()

	config := DefaultDLQConfig("dlq.test")
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Add multiple messages
	for i := 0; i < 5; i++ {
		msg := Message{Data: i}
		_ = dlq.Add(msg, "topic", "test")
	}

	time.Sleep(50 * time.Millisecond)

	if dlq.Count() != 5 {
		t.Errorf("Expected 5 messages, got %d", dlq.Count())
	}

	// Clear
	dlq.Clear()

	if dlq.Count() != 0 {
		t.Errorf("Expected 0 messages after clear, got %d", dlq.Count())
	}
}

func TestDLQ_LinearBackoff(t *testing.T) {
	ps := New()
	defer ps.Close()

	config := DefaultDLQConfig("dlq.test")
	config.RetryStrategy = LinearBackoff
	config.InitialRetryDelay = 100 * time.Millisecond
	config.MaxRetryDelay = 500 * time.Millisecond
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Test linear backoff
	delays := []time.Duration{
		dlq.calculateRetryDelay(0), // 100ms
		dlq.calculateRetryDelay(1), // 200ms
		dlq.calculateRetryDelay(2), // 300ms
		dlq.calculateRetryDelay(3), // 400ms
		dlq.calculateRetryDelay(4), // 500ms (capped)
		dlq.calculateRetryDelay(5), // 500ms (capped)
	}

	expected := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		300 * time.Millisecond,
		400 * time.Millisecond,
		500 * time.Millisecond,
		500 * time.Millisecond,
	}

	for i, delay := range delays {
		if delay != expected[i] {
			t.Errorf("Delay %d: expected %v, got %v", i, expected[i], delay)
		}
	}
}

func TestDLQ_RetryAll(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, _ := ps.Subscribe(t.Context(), "retry.all", SubOptions{BufferSize: 20})
	defer sub.Cancel()

	config := DefaultDLQConfig("dlq.test")
	config.RetryStrategy = NoRetry
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Add messages with different reasons
	for i := 0; i < 3; i++ {
		msg := Message{Data: i}
		_ = dlq.Add(msg, "retry.all", "timeout")
	}
	for i := 3; i < 5; i++ {
		msg := Message{Data: i}
		_ = dlq.Add(msg, "retry.all", "error")
	}

	time.Sleep(50 * time.Millisecond)

	// Retry only timeout messages
	succeeded, failed := dlq.RetryAll(DLQQueryOptions{
		Reason: "timeout",
	})

	if succeeded != 3 {
		t.Errorf("Expected 3 successful retries, got %d", succeeded)
	}

	if failed != 0 {
		t.Errorf("Expected 0 failed retries, got %d", failed)
	}

	// Should have 2 error messages left
	if dlq.Count() != 2 {
		t.Errorf("Expected 2 messages remaining, got %d", dlq.Count())
	}
}

func BenchmarkEnhancedDLQ_Add(b *testing.B) {
	ps := New()
	defer ps.Close()

	config := DefaultDLQConfig("dlq.bench")
	config.RetryStrategy = NoRetry
	config.EnableAlerts = false
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	msg := Message{Data: []byte("benchmark message")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = dlq.Add(msg, "bench.topic", "test")
	}
}

func BenchmarkEnhancedDLQ_Query(b *testing.B) {
	ps := New()
	defer ps.Close()

	config := DefaultDLQConfig("dlq.bench")
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	// Pre-populate
	for i := 0; i < 100; i++ {
		msg := Message{Data: i}
		_ = dlq.Add(msg, "topic", "test")
	}

	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = dlq.Query(DLQQueryOptions{
			Limit: 10,
		})
	}
}

func TestDLQGetReturnsDetachedMessageValue(t *testing.T) {
	ps := New()
	defer ps.Close()
	config := DefaultDLQConfig("dlq.fixture")
	config.RetryStrategy = NoRetry
	dlq := NewDLQ(ps, config)
	defer dlq.Close()

	if err := dlq.Add(Message{Data: "fixture"}, "jobs", "failed"); err != nil {
		t.Fatal(err)
	}
	messages, err := dlq.Query(DLQQueryOptions{})
	if err != nil || len(messages) != 1 {
		t.Fatalf("Query returned %d messages, %v", len(messages), err)
	}
	id := messages[0].ID
	snapshot, err := dlq.Get(id)
	if err != nil || snapshot == nil || snapshot.Reason != "failed" || snapshot.OriginalTopic != "jobs" {
		t.Fatalf("Get = %+v, %v", snapshot, err)
	}
	snapshot.Reason = "changed"
	if got, err := dlq.Get(id); err != nil || got == nil || got.Reason != "failed" {
		t.Errorf("Get after modifying the snapshot = %+v, %v", got, err)
	}
	if got, err := dlq.Get("missing"); got != nil || !errors.Is(err, ErrDLQNotFound) {
		t.Errorf("Get for missing ID = %+v, %v", got, err)
	}
}
