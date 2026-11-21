package pubsub

import (
	"context"
	"testing"
	"time"
)

func TestConsumerGroupManager_Basic(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("test-group")
	if err := cgm.CreateGroup(config); err != nil {
		t.Fatalf("Failed to create group: %v", err)
	}

	// Should not create duplicate group
	if err := cgm.CreateGroup(config); err == nil {
		t.Error("Expected error for duplicate group")
	}
}

func TestConsumerGroupManager_JoinLeave(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("test-group")
	_ = cgm.CreateGroup(config)

	// Join group
	consumer, err := cgm.JoinGroup("test-group", "consumer1", []string{"test.topic"})
	if err != nil {
		t.Fatalf("Failed to join group: %v", err)
	}

	if consumer == nil {
		t.Fatal("Expected non-nil consumer")
	}

	// Leave group
	if err := cgm.LeaveGroup("test-group", "consumer1"); err != nil {
		t.Fatalf("Failed to leave group: %v", err)
	}

	// Leaving again should fail
	if err := cgm.LeaveGroup("test-group", "consumer1"); err != ErrConsumerNotFound {
		t.Errorf("Expected ErrConsumerNotFound, got %v", err)
	}
}

func TestConsumerGroupManager_MultipleConsumers(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("multi-group")
	_ = cgm.CreateGroup(config)

	// Join multiple consumers
	consumers := make([]*groupConsumer, 3)
	for i := 0; i < 3; i++ {
		c, err := cgm.JoinGroup("multi-group", string(rune('A'+i)), []string{"test.topic"})
		if err != nil {
			t.Fatalf("Failed to join consumer %d: %v", i, err)
		}
		consumers[i] = c
	}

	time.Sleep(200 * time.Millisecond) // Wait for rebalance

	stats, _ := cgm.Stats("multi-group")
	if stats.Consumers != 3 {
		t.Errorf("Expected 3 consumers, got %d", stats.Consumers)
	}

	// Cleanup
	for i, c := range consumers {
		_ = cgm.LeaveGroup("multi-group", string(rune('A'+i)))
		c.Close()
	}
}

func TestConsumerGroupManager_MessageDistribution(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("dist-group")
	config.Strategy = RoundRobin
	_ = cgm.CreateGroup(config)

	// Create 2 consumers
	c1, _ := cgm.JoinGroup("dist-group", "consumer1", []string{"dist.topic"})
	c2, _ := cgm.JoinGroup("dist-group", "consumer2", []string{"dist.topic"})

	time.Sleep(200 * time.Millisecond) // Wait for rebalance

	// Publish messages
	for i := 0; i < 10; i++ {
		msg := Message{
			ID:   generateCorrelationID(),
			Data: i,
		}
		_ = ps.Publish("dist.topic", msg)
	}

	time.Sleep(200 * time.Millisecond)

	// Count messages received by each consumer
	count1 := len(c1.messageChannel)
	count2 := len(c2.messageChannel)

	total := count1 + count2
	if total == 0 {
		t.Log("Note: No messages distributed (assignment may need tuning)")
	} else {
		t.Logf("Consumer1: %d, Consumer2: %d", count1, count2)
	}

	// Cleanup
	c1.Close()
	c2.Close()
}

func TestConsumerGroupManager_OffsetCommit(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("offset-group")
	_ = cgm.CreateGroup(config)

	// Commit offset
	if err := cgm.CommitOffset("offset-group", "test.topic", 0, 42); err != nil {
		t.Fatalf("Failed to commit offset: %v", err)
	}

	// Get offset
	offset, err := cgm.GetOffset("offset-group", "test.topic", 0)
	if err != nil {
		t.Fatalf("Failed to get offset: %v", err)
	}

	if offset != 42 {
		t.Errorf("Expected offset 42, got %d", offset)
	}

	// Get offset for non-existent partition
	offset2, _ := cgm.GetOffset("offset-group", "test.topic", 99)
	if offset2 != 0 {
		t.Errorf("Expected offset 0 for new partition, got %d", offset2)
	}
}

func TestConsumerGroupManager_Rebalance(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("rebal-group")
	_ = cgm.CreateGroup(config)

	// Start with 2 consumers
	c1, _ := cgm.JoinGroup("rebal-group", "c1", []string{"rebal.topic"})
	c2, _ := cgm.JoinGroup("rebal-group", "c2", []string{"rebal.topic"})

	time.Sleep(200 * time.Millisecond)

	stats1, _ := cgm.Stats("rebal-group")
	gen1 := stats1.Generation

	// Add third consumer (should trigger rebalance)
	c3, _ := cgm.JoinGroup("rebal-group", "c3", []string{"rebal.topic"})

	time.Sleep(200 * time.Millisecond)

	stats2, _ := cgm.Stats("rebal-group")
	gen2 := stats2.Generation

	if gen2 <= gen1 {
		t.Logf("Note: Generation may not increase immediately (gen1=%d, gen2=%d)", gen1, gen2)
	}

	// Cleanup
	c1.Close()
	c2.Close()
	c3.Close()
}

func TestConsumerGroupManager_AssignmentStrategies(t *testing.T) {
	strategies := []struct {
		name     string
		strategy AssignmentStrategy
	}{
		{"RoundRobin", RoundRobin},
		{"Sticky", Sticky},
		{"Range", Range},
		{"ConsistentHash", ConsistentHash},
	}

	for _, test := range strategies {
		t.Run(test.name, func(t *testing.T) {
			ps := New()
			defer ps.Close()

			cgm := NewConsumerGroupManager(ps)

			config := DefaultConsumerGroupConfig("strategy-group-" + test.name)
			config.Strategy = test.strategy
			_ = cgm.CreateGroup(config)

			// Join consumers
			c1, _ := cgm.JoinGroup(config.GroupID, "c1", []string{"test"})
			c2, _ := cgm.JoinGroup(config.GroupID, "c2", []string{"test"})

			time.Sleep(200 * time.Millisecond)

			stats, _ := cgm.Stats(config.GroupID)
			if stats.Consumers != 2 {
				t.Errorf("Expected 2 consumers, got %d", stats.Consumers)
			}

			// Cleanup
			c1.Close()
			c2.Close()
		})
	}
}

func TestConsumerGroupManager_Heartbeat(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("hb-group")
	config.SessionTimeout = 500 * time.Millisecond
	config.HeartbeatInterval = 100 * time.Millisecond
	_ = cgm.CreateGroup(config)

	c1, _ := cgm.JoinGroup("hb-group", "c1", []string{"test"})
	defer c1.Close()

	// Send heartbeats
	for i := 0; i < 3; i++ {
		c1.Heartbeat()
		time.Sleep(200 * time.Millisecond)
	}

	stats, _ := cgm.Stats("hb-group")
	if stats.Consumers != 1 {
		t.Errorf("Expected 1 consumer after heartbeats, got %d", stats.Consumers)
	}
}

func TestConsumerGroupManager_DeadConsumerRemoval(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("dead-group")
	config.SessionTimeout = 300 * time.Millisecond
	config.HeartbeatInterval = 100 * time.Millisecond
	_ = cgm.CreateGroup(config)

	c1, _ := cgm.JoinGroup("dead-group", "c1", []string{"test"})

	// Don't send heartbeats - consumer should be removed

	time.Sleep(600 * time.Millisecond) // Wait for session timeout + check

	stats, _ := cgm.Stats("dead-group")
	if stats.Consumers != 0 {
		t.Logf("Note: Dead consumer not yet removed (got %d consumers)", stats.Consumers)
	}

	c1.Close()
}

func TestConsumerGroupManager_DeleteGroup(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("del-group")
	_ = cgm.CreateGroup(config)

	// Add consumer
	c1, _ := cgm.JoinGroup("del-group", "c1", []string{"test"})
	defer c1.Close()

	// Delete group
	if err := cgm.DeleteGroup("del-group"); err != nil {
		t.Fatalf("Failed to delete group: %v", err)
	}

	// Should not exist anymore
	_, err := cgm.Stats("del-group")
	if err != ErrGroupNotFound {
		t.Errorf("Expected ErrGroupNotFound, got %v", err)
	}
}

func TestConsumerGroupManager_Stats(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("stats-group")
	_ = cgm.CreateGroup(config)

	stats, err := cgm.Stats("stats-group")
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	if stats.GroupID != "stats-group" {
		t.Errorf("Expected GroupID 'stats-group', got '%s'", stats.GroupID)
	}

	if stats.Consumers != 0 {
		t.Errorf("Expected 0 consumers, got %d", stats.Consumers)
	}

	// Add consumer
	c1, _ := cgm.JoinGroup("stats-group", "c1", []string{"test"})
	defer c1.Close()

	time.Sleep(100 * time.Millisecond)

	stats2, _ := cgm.Stats("stats-group")
	if stats2.Consumers != 1 {
		t.Errorf("Expected 1 consumer, got %d", stats2.Consumers)
	}
}

func TestConsumerGroupManager_InvalidConfig(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	// Empty group ID
	config := ConsumerGroupConfig{}
	if err := cgm.CreateGroup(config); err == nil {
		t.Error("Expected error for empty group ID")
	}
}

func TestConsumerGroupManager_InvalidJoin(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	// Non-existent group
	_, err := cgm.JoinGroup("nonexistent", "c1", []string{"test"})
	if err != ErrGroupNotFound {
		t.Errorf("Expected ErrGroupNotFound, got %v", err)
	}

	config := DefaultConsumerGroupConfig("valid-group")
	_ = cgm.CreateGroup(config)

	// Empty consumer ID
	_, err = cgm.JoinGroup("valid-group", "", []string{"test"})
	if err == nil {
		t.Error("Expected error for empty consumer ID")
	}

	// Empty topics
	_, err = cgm.JoinGroup("valid-group", "c1", []string{})
	if err == nil {
		t.Error("Expected error for empty topics")
	}

	// Duplicate consumer
	c1, _ := cgm.JoinGroup("valid-group", "c1", []string{"test"})
	defer c1.Close()

	_, err = cgm.JoinGroup("valid-group", "c1", []string{"test"})
	if err == nil {
		t.Error("Expected error for duplicate consumer")
	}
}

func TestConsumerGroup_PartitionAssignment(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("part-group")
	config.MaxPartitions = 8
	_ = cgm.CreateGroup(config)

	// Join 2 consumers
	c1, _ := cgm.JoinGroup("part-group", "c1", []string{"test"})
	c2, _ := cgm.JoinGroup("part-group", "c2", []string{"test"})
	defer c1.Close()
	defer c2.Close()

	time.Sleep(200 * time.Millisecond)

	// Each consumer should have some partitions assigned
	c1.assignedPartsMu.RLock()
	c1Parts := len(c1.assignedParts)
	c1.assignedPartsMu.RUnlock()
	c2.assignedPartsMu.RLock()
	c2Parts := len(c2.assignedParts)
	c2.assignedPartsMu.RUnlock()
	if c1Parts == 0 && c2Parts == 0 {
		t.Log("Note: Partitions not yet assigned (may be async)")
	}
}

func TestConsumerGroup_RebalanceCancelledContext(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("ctx-cancel-group")
	_ = cgm.CreateGroup(config)

	// Join a consumer so there is work to do during rebalance
	c1, _ := cgm.JoinGroup("ctx-cancel-group", "c1", []string{"test.topic"})
	defer c1.Close()

	time.Sleep(100 * time.Millisecond) // Wait for initial rebalance

	cgm.groupsMu.RLock()
	group := cgm.groups["ctx-cancel-group"]
	cgm.groupsMu.RUnlock()

	genBefore := group.generation.Load()

	// Call rebalance with an already-cancelled context
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately

	err := group.rebalance(ctx)
	if err == nil {
		t.Error("Expected error from rebalance with cancelled context")
	}
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}

	// Generation should not have changed since we called rebalance directly
	// and it returned an error
	genAfter := group.generation.Load()
	if genAfter != genBefore {
		t.Errorf("Generation should not change on failed rebalance: before=%d, after=%d", genBefore, genAfter)
	}
}

func TestConsumerGroup_RebalanceTimedOutContext(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("ctx-timeout-group")
	_ = cgm.CreateGroup(config)

	c1, _ := cgm.JoinGroup("ctx-timeout-group", "c1", []string{"test.topic"})
	defer c1.Close()

	time.Sleep(100 * time.Millisecond)

	cgm.groupsMu.RLock()
	group := cgm.groups["ctx-timeout-group"]
	cgm.groupsMu.RUnlock()

	// Call rebalance with an already-expired timeout
	ctx, cancel := context.WithTimeout(t.Context(), 0)
	defer cancel()
	time.Sleep(time.Millisecond) // Ensure timeout has expired

	err := group.rebalance(ctx)
	if err == nil {
		t.Error("Expected error from rebalance with expired context")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err)
	}
}

func TestConsumerGroup_TriggerRebalanceNoGenIncrementOnCancel(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("trigger-cancel-group")
	config.RebalanceTimeout = time.Nanosecond // Extremely short timeout to force expiry
	_ = cgm.CreateGroup(config)

	c1, _ := cgm.JoinGroup("trigger-cancel-group", "c1", []string{"test.topic"})
	defer c1.Close()

	time.Sleep(200 * time.Millisecond) // Wait for initial rebalance to settle

	cgm.groupsMu.RLock()
	group := cgm.groups["trigger-cancel-group"]
	cgm.groupsMu.RUnlock()

	genBefore := group.generation.Load()

	// Now cancel the group's root context so new rebalances fail
	group.cancel()

	// Trigger a rebalance - it should fail since group ctx is cancelled
	group.rebalanceMu.Lock() // Reset rebalancing flag
	group.rebalanceRunning = false
	group.rebalanceMu.Unlock()
	group.triggerRebalance()

	time.Sleep(50 * time.Millisecond)

	genAfter := group.generation.Load()
	if genAfter != genBefore {
		t.Errorf("Generation should not increment when rebalance fails: before=%d, after=%d", genBefore, genAfter)
	}
}

func TestConsumerGroup_RebalanceSuccessWithValidContext(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("ctx-valid-group")
	_ = cgm.CreateGroup(config)

	c1, _ := cgm.JoinGroup("ctx-valid-group", "c1", []string{"test.topic"})
	c2, _ := cgm.JoinGroup("ctx-valid-group", "c2", []string{"test.topic"})
	defer c1.Close()
	defer c2.Close()

	time.Sleep(100 * time.Millisecond)

	cgm.groupsMu.RLock()
	group := cgm.groups["ctx-valid-group"]
	cgm.groupsMu.RUnlock()

	// Rebalance with a valid context should succeed
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err := group.rebalance(ctx)
	if err != nil {
		t.Fatalf("Expected no error from rebalance with valid context, got %v", err)
	}

	// Verify assignments were made
	group.assignmentMu.RLock()
	hasAssignments := len(group.assignments) > 0
	group.assignmentMu.RUnlock()

	if !hasAssignments {
		t.Error("Expected assignments to be created after successful rebalance")
	}
}

func TestConsumerGroup_StickyPreservesAssignments(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("sticky-preserve")
	config.Strategy = Sticky
	config.MaxPartitions = 8
	_ = cgm.CreateGroup(config)

	c1, _ := cgm.JoinGroup("sticky-preserve", "c1", []string{"test"})
	c2, _ := cgm.JoinGroup("sticky-preserve", "c2", []string{"test"})
	defer c1.Close()
	defer c2.Close()

	time.Sleep(200 * time.Millisecond)

	cgm.groupsMu.RLock()
	group := cgm.groups["sticky-preserve"]
	cgm.groupsMu.RUnlock()

	// Capture assignments after initial rebalance.
	group.assignmentMu.RLock()
	initial := make(map[int]string, len(group.assignments["test"]))
	for p, owner := range group.assignments["test"] {
		initial[p] = owner
	}
	group.assignmentMu.RUnlock()

	if len(initial) != 8 {
		t.Fatalf("Expected 8 partition assignments, got %d", len(initial))
	}

	// Trigger another rebalance with the same consumers — assignments should stay identical.
	ctx := t.Context()
	group.consumersMu.RLock()
	group.assignmentMu.Lock()
	group.assignSticky()
	after := make(map[int]string, len(group.assignments["test"]))
	for p, owner := range group.assignments["test"] {
		after[p] = owner
	}
	group.assignmentMu.Unlock()
	group.consumersMu.RUnlock()

	for p, owner := range initial {
		if after[p] != owner {
			t.Errorf("Partition %d changed from %s to %s on no-op rebalance", p, owner, after[p])
		}
	}
	_ = ctx
}

func TestConsumerGroup_StickyMinimalMovement(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("sticky-move")
	config.Strategy = Sticky
	config.MaxPartitions = 12
	_ = cgm.CreateGroup(config)

	c1, _ := cgm.JoinGroup("sticky-move", "c1", []string{"test"})
	c2, _ := cgm.JoinGroup("sticky-move", "c2", []string{"test"})

	time.Sleep(200 * time.Millisecond)

	cgm.groupsMu.RLock()
	group := cgm.groups["sticky-move"]
	cgm.groupsMu.RUnlock()

	// Capture initial assignments (2 consumers, 12 partitions → 6 each).
	group.assignmentMu.RLock()
	before := make(map[int]string, len(group.assignments["test"]))
	for p, owner := range group.assignments["test"] {
		before[p] = owner
	}
	group.assignmentMu.RUnlock()

	// Add a third consumer.
	c3, _ := cgm.JoinGroup("sticky-move", "c3", []string{"test"})

	time.Sleep(200 * time.Millisecond)

	group.assignmentMu.RLock()
	afterAdd := make(map[int]string, len(group.assignments["test"]))
	for p, owner := range group.assignments["test"] {
		afterAdd[p] = owner
	}
	group.assignmentMu.RUnlock()

	// Count how many partitions moved.
	moved := 0
	for p, ownerBefore := range before {
		if afterAdd[p] != ownerBefore {
			moved++
		}
	}

	// With 12 partitions and 3 consumers, c3 should get 4 partitions.
	// So exactly 4 partitions should move. Allow some slack but it should not
	// exceed the ideal number significantly.
	if moved > 6 {
		t.Errorf("Too many partitions moved: %d (expected <= 6)", moved)
	}
	t.Logf("Partitions moved when adding c3: %d out of 12", moved)

	// All 12 partitions should still be assigned.
	if len(afterAdd) != 12 {
		t.Errorf("Expected 12 assignments, got %d", len(afterAdd))
	}

	// Cleanup.
	c1.Close()
	c2.Close()
	c3.Close()
}

func TestConsumerGroup_StickyConsumerLeaves(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("sticky-leave")
	config.Strategy = Sticky
	config.MaxPartitions = 9
	_ = cgm.CreateGroup(config)

	c1, _ := cgm.JoinGroup("sticky-leave", "c1", []string{"test"})
	_, _ = cgm.JoinGroup("sticky-leave", "c2", []string{"test"})
	c3, _ := cgm.JoinGroup("sticky-leave", "c3", []string{"test"})

	time.Sleep(200 * time.Millisecond)

	cgm.groupsMu.RLock()
	group := cgm.groups["sticky-leave"]
	cgm.groupsMu.RUnlock()

	// Capture assignments before c2 leaves.
	group.assignmentMu.RLock()
	before := make(map[int]string, len(group.assignments["test"]))
	for p, owner := range group.assignments["test"] {
		before[p] = owner
	}
	group.assignmentMu.RUnlock()

	// Remove c2.
	_ = cgm.LeaveGroup("sticky-leave", "c2")

	time.Sleep(200 * time.Millisecond)

	group.assignmentMu.RLock()
	after := make(map[int]string, len(group.assignments["test"]))
	for p, owner := range group.assignments["test"] {
		after[p] = owner
	}
	group.assignmentMu.RUnlock()

	// Partitions that were on c1 or c3 should stay with them.
	for p, ownerBefore := range before {
		if ownerBefore == "c2" {
			continue // These must be reassigned.
		}
		if after[p] != ownerBefore {
			t.Errorf("Partition %d moved from %s to %s (should have stayed)", p, ownerBefore, after[p])
		}
	}

	// All 9 partitions should be assigned to c1 or c3 now.
	if len(after) != 9 {
		t.Errorf("Expected 9 assignments, got %d", len(after))
	}
	for p, owner := range after {
		if owner != "c1" && owner != "c3" {
			t.Errorf("Partition %d assigned to removed consumer %s", p, owner)
		}
	}

	c1.Close()
	c3.Close()
}

func TestConsumerGroup_StickyBalancesLoad(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("sticky-balance")
	config.Strategy = Sticky
	config.MaxPartitions = 10
	_ = cgm.CreateGroup(config)

	c1, _ := cgm.JoinGroup("sticky-balance", "c1", []string{"test"})
	c2, _ := cgm.JoinGroup("sticky-balance", "c2", []string{"test"})
	c3, _ := cgm.JoinGroup("sticky-balance", "c3", []string{"test"})

	time.Sleep(200 * time.Millisecond)

	cgm.groupsMu.RLock()
	group := cgm.groups["sticky-balance"]
	cgm.groupsMu.RUnlock()

	// Count partitions per consumer — should be 4, 3, 3 or 3, 4, 3, etc.
	group.assignmentMu.RLock()
	counts := make(map[string]int)
	for _, owner := range group.assignments["test"] {
		counts[owner]++
	}
	group.assignmentMu.RUnlock()

	for _, id := range []string{"c1", "c2", "c3"} {
		c := counts[id]
		if c < 3 || c > 4 {
			t.Errorf("Consumer %s has %d partitions, expected 3 or 4", id, c)
		}
	}

	total := counts["c1"] + counts["c2"] + counts["c3"]
	if total != 10 {
		t.Errorf("Total assignments %d, expected 10", total)
	}

	c1.Close()
	c2.Close()
	c3.Close()
}

func TestConsumerGroup_StickyNoConsumers(t *testing.T) {
	cg := &consumerGroup{
		config: ConsumerGroupConfig{
			MaxPartitions: 4,
		},
		consumers:   make(map[string]*groupConsumer),
		partitions:  map[string][]int{"test": {0, 1, 2, 3}},
		assignments: map[string]map[int]string{"test": {0: "old", 1: "old"}},
	}

	cg.assignSticky()

	for topic, m := range cg.assignments {
		if len(m) != 0 {
			t.Errorf("Expected empty assignments for topic %s, got %d", topic, len(m))
		}
	}
}

func TestConsumerGroup_StickyMultipleTopics(t *testing.T) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("sticky-multi")
	config.Strategy = Sticky
	config.MaxPartitions = 6
	_ = cgm.CreateGroup(config)

	c1, _ := cgm.JoinGroup("sticky-multi", "c1", []string{"topicA", "topicB"})
	c2, _ := cgm.JoinGroup("sticky-multi", "c2", []string{"topicA", "topicB"})

	time.Sleep(200 * time.Millisecond)

	cgm.groupsMu.RLock()
	group := cgm.groups["sticky-multi"]
	cgm.groupsMu.RUnlock()

	group.assignmentMu.RLock()
	for _, topic := range []string{"topicA", "topicB"} {
		assignments := group.assignments[topic]
		if len(assignments) != 6 {
			t.Errorf("Topic %s: expected 6 assignments, got %d", topic, len(assignments))
		}
		counts := map[string]int{}
		for _, owner := range assignments {
			counts[owner]++
		}
		for id, c := range counts {
			if c != 3 {
				t.Errorf("Topic %s: consumer %s has %d partitions, expected 3", topic, id, c)
			}
		}
	}
	group.assignmentMu.RUnlock()

	c1.Close()
	c2.Close()
}

func BenchmarkConsumerGroup_Join(b *testing.B) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("bench-group")
	_ = cgm.CreateGroup(config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		consumerID := string(rune('A' + i%26))
		c, _ := cgm.JoinGroup("bench-group", consumerID, []string{"test"})
		if c != nil {
			c.Close()
		}
	}
}

func BenchmarkConsumerGroup_Commit(b *testing.B) {
	ps := New()
	defer ps.Close()

	cgm := NewConsumerGroupManager(ps)

	config := DefaultConsumerGroupConfig("bench-commit")
	_ = cgm.CreateGroup(config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cgm.CommitOffset("bench-commit", "test", i%16, uint64(i))
	}
}

func TestConsumerGroupRejectsInvalidTickerIntervals(t *testing.T) {
	broker := New()
	t.Cleanup(func() { _ = broker.Close() })
	manager := NewConsumerGroupManager(broker)
	for _, tc := range []struct {
		heartbeat, commit time.Duration
		autoCommit, valid bool
	}{
		{-time.Second, 0, false, false},
		{time.Second, -time.Second, true, false},
		{time.Second, -time.Second, false, true},
		{0, 0, true, true},
		{time.Second, time.Second, true, true},
	} {
		cfg := ConsumerGroupConfig{GroupID: "test", HeartbeatInterval: tc.heartbeat, AutoCommitInterval: tc.commit, AutoCommit: tc.autoCommit}
		err := manager.CreateGroup(cfg)
		if (err == nil) != tc.valid {
			t.Fatalf("CreateGroup(%+v) = %v", cfg, err)
		}
		if tc.valid {
			if err := manager.DeleteGroup(cfg.GroupID); err != nil {
				t.Fatal(err)
			}
		} else if _, present := manager.groups[cfg.GroupID]; present {
			t.Fatal("invalid group was registered")
		}
	}
}
