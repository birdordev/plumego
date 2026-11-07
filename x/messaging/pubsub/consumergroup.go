package pubsub

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Consumer Group errors

// AssignmentStrategy defines how messages are distributed among consumers
type AssignmentStrategy int

const (
	// RoundRobin distributes messages evenly across all consumers
	RoundRobin AssignmentStrategy = iota

	// Sticky tries to maintain assignments across rebalances
	Sticky

	// Range assigns contiguous partitions to consumers
	Range

	// ConsistentHash uses consistent hashing for assignment
	ConsistentHash
)

// ConsumerGroupConfig configures a consumer group
type ConsumerGroupConfig struct {
	// GroupID is the unique identifier for the consumer group
	GroupID string

	// Strategy is the assignment strategy (default: RoundRobin)
	Strategy AssignmentStrategy

	// SessionTimeout is how long before a consumer is considered dead (default: 10s)
	SessionTimeout time.Duration

	// HeartbeatInterval is how often consumers send heartbeats (default: 3s)
	HeartbeatInterval time.Duration

	// RebalanceTimeout is the maximum time for rebalance (default: 60s)
	RebalanceTimeout time.Duration

	// MaxPartitions is the number of virtual partitions (default: 16)
	MaxPartitions int

	// AutoCommit enables automatic offset commits (default: true)
	AutoCommit bool

	// AutoCommitInterval is how often to auto-commit (default: 5s)
	AutoCommitInterval time.Duration
}

// DefaultConsumerGroupConfig returns default configuration
func DefaultConsumerGroupConfig(groupID string) ConsumerGroupConfig {
	return ConsumerGroupConfig{
		GroupID:            groupID,
		Strategy:           RoundRobin,
		SessionTimeout:     10 * time.Second,
		HeartbeatInterval:  3 * time.Second,
		RebalanceTimeout:   60 * time.Second,
		MaxPartitions:      16,
		AutoCommit:         true,
		AutoCommitInterval: 5 * time.Second,
	}
}

// ConsumerGroupManager manages consumer groups
type ConsumerGroupManager struct {
	ps *InProcBroker

	groups   map[string]*consumerGroup
	groupsMu sync.RWMutex

	// Metrics
	rebalances atomic.Uint64
	commits    atomic.Uint64
	errors     atomic.Uint64
}

// consumerGroup represents a single consumer group
type consumerGroup struct {
	config ConsumerGroupConfig

	consumers   map[string]*groupConsumer
	consumersMu sync.RWMutex

	partitions   map[string][]int          // topic -> partition IDs
	assignments  map[string]map[int]string // topic -> partition -> consumerID
	offsets      map[string]map[int]uint64 // topic -> partition -> offset
	assignmentMu sync.RWMutex

	rebalanceMu      sync.Mutex
	rebalanceRunning bool
	rebalancePending bool

	generation atomic.Uint64

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// groupConsumer represents a consumer in a group
type groupConsumer struct {
	id              string
	group           *consumerGroup
	topics          []string
	assignedParts   map[string][]int // topic -> partitions
	assignedPartsMu sync.RWMutex
	subscription    Subscription
	lastHeartbeat   time.Time
	heartbeatMu     sync.Mutex
	messageChannel  chan Message
	closed          atomic.Bool

	// Per-consumer context used to signal the dispatcher goroutine to exit.
	cancel       context.CancelFunc
	dispatchDone chan struct{}
}

// NewConsumerGroupManager creates a new consumer group manager
func NewConsumerGroupManager(ps *InProcBroker) *ConsumerGroupManager {
	return &ConsumerGroupManager{
		ps:     ps,
		groups: make(map[string]*consumerGroup),
	}
}

// CreateGroup creates a new consumer group
func (cgm *ConsumerGroupManager) CreateGroup(config ConsumerGroupConfig) error {
	if config.GroupID == "" {
		return errors.New("group ID is required")
	}

	// Apply defaults
	if config.SessionTimeout == 0 {
		config.SessionTimeout = 10 * time.Second
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = 3 * time.Second
	}
	if config.RebalanceTimeout == 0 {
		config.RebalanceTimeout = 60 * time.Second
	}
	if config.MaxPartitions == 0 {
		config.MaxPartitions = 16
	}
	if config.AutoCommitInterval == 0 {
		config.AutoCommitInterval = 5 * time.Second
	}

	cgm.groupsMu.Lock()
	defer cgm.groupsMu.Unlock()

	if _, exists := cgm.groups[config.GroupID]; exists {
		return fmt.Errorf("group %s already exists", config.GroupID)
	}

	ctx, cancel := context.WithCancel(context.Background())

	group := &consumerGroup{
		config:      config,
		consumers:   make(map[string]*groupConsumer),
		partitions:  make(map[string][]int),
		assignments: make(map[string]map[int]string),
		offsets:     make(map[string]map[int]uint64),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Start background workers
	group.startWorkers()

	cgm.groups[config.GroupID] = group

	return nil
}

// JoinGroup adds a consumer to a group
func (cgm *ConsumerGroupManager) JoinGroup(groupID, consumerID string, topics []string) (*groupConsumer, error) {
	cgm.groupsMu.RLock()
	group, exists := cgm.groups[groupID]
	cgm.groupsMu.RUnlock()

	if !exists {
		return nil, ErrGroupNotFound
	}

	if consumerID == "" {
		return nil, errors.New("consumer ID is required")
	}

	if len(topics) == 0 {
		return nil, errors.New("at least one topic is required")
	}

	group.consumersMu.Lock()
	defer group.consumersMu.Unlock()

	if _, exists := group.consumers[consumerID]; exists {
		return nil, fmt.Errorf("consumer %s already exists in group", consumerID)
	}

	// Subscribe to all topics with fanout disabled
	sub, err := cgm.ps.Subscribe(context.Background(), topics[0], SubOptions{BufferSize: 100})
	if err != nil {
		return nil, err
	}

	consumerCtx, cancel := context.WithCancel(context.Background())
	consumer := &groupConsumer{
		id:             consumerID,
		group:          group,
		topics:         topics,
		assignedParts:  make(map[string][]int),
		subscription:   sub,
		lastHeartbeat:  time.Now(),
		messageChannel: make(chan Message, 100),
		cancel:         cancel,
		dispatchDone:   make(chan struct{}),
	}

	group.consumers[consumerID] = consumer

	// Trigger rebalance
	go group.triggerRebalance()

	// Start message dispatcher
	consumer.startDispatcher(consumerCtx)

	return consumer, nil
}

// LeaveGroup removes a consumer from a group
func (cgm *ConsumerGroupManager) LeaveGroup(groupID, consumerID string) error {
	cgm.groupsMu.RLock()
	group, exists := cgm.groups[groupID]
	cgm.groupsMu.RUnlock()

	if !exists {
		return ErrGroupNotFound
	}

	group.consumersMu.Lock()
	consumer, exists := group.consumers[consumerID]
	if !exists {
		group.consumersMu.Unlock()
		return ErrConsumerNotFound
	}

	delete(group.consumers, consumerID)
	group.consumersMu.Unlock()

	// Close consumer
	consumer.Close()

	// Trigger rebalance
	go group.triggerRebalance()

	return nil
}

// CommitOffset commits an offset for a partition
func (cgm *ConsumerGroupManager) CommitOffset(groupID, topic string, partition int, offset uint64) error {
	cgm.groupsMu.RLock()
	group, exists := cgm.groups[groupID]
	cgm.groupsMu.RUnlock()

	if !exists {
		return ErrGroupNotFound
	}

	group.assignmentMu.Lock()
	defer group.assignmentMu.Unlock()

	if group.offsets[topic] == nil {
		group.offsets[topic] = make(map[int]uint64)
	}

	group.offsets[topic][partition] = offset
	cgm.commits.Add(1)

	return nil
}

// GetOffset gets the committed offset for a partition
func (cgm *ConsumerGroupManager) GetOffset(groupID, topic string, partition int) (uint64, error) {
	cgm.groupsMu.RLock()
	group, exists := cgm.groups[groupID]
	cgm.groupsMu.RUnlock()

	if !exists {
		return 0, ErrGroupNotFound
	}

	group.assignmentMu.RLock()
	defer group.assignmentMu.RUnlock()

	if group.offsets[topic] == nil {
		return 0, nil
	}

	return group.offsets[topic][partition], nil
}

// DeleteGroup deletes a consumer group
func (cgm *ConsumerGroupManager) DeleteGroup(groupID string) error {
	cgm.groupsMu.Lock()
	defer cgm.groupsMu.Unlock()

	group, exists := cgm.groups[groupID]
	if !exists {
		return ErrGroupNotFound
	}

	// Close group
	group.cancel()
	group.wg.Wait()

	// Close all consumers
	group.consumersMu.Lock()
	for _, consumer := range group.consumers {
		consumer.Close()
	}
	group.consumersMu.Unlock()

	delete(cgm.groups, groupID)

	return nil
}

// triggerRebalance triggers a rebalance for the group.
// If a rebalance is already running the request is marked pending and the
// running goroutine will re-run after it finishes, guaranteeing that every
// join/leave is eventually reflected in the assignments.
func (cg *consumerGroup) triggerRebalance() {
	cg.rebalanceMu.Lock()
	if cg.rebalanceRunning {
		cg.rebalancePending = true
		cg.rebalanceMu.Unlock()
		return
	}
	cg.rebalanceRunning = true
	cg.rebalanceMu.Unlock()

	for {
		cg.rebalanceMu.Lock()
		cg.rebalancePending = false
		cg.rebalanceMu.Unlock()

		ctx, cancel := context.WithTimeout(cg.ctx, cg.config.RebalanceTimeout)
		if err := cg.rebalance(ctx); err == nil {
			cg.generation.Add(1)
		}
		cancel()

		cg.rebalanceMu.Lock()
		pending := cg.rebalancePending
		if !pending {
			cg.rebalanceRunning = false
		}
		cg.rebalanceMu.Unlock()

		if !pending {
			return
		}
	}
}

// rebalance performs the actual rebalancing
func (cg *consumerGroup) rebalance(ctx context.Context) error {
	// Check context before acquiring locks
	if err := ctx.Err(); err != nil {
		return err
	}

	cg.consumersMu.RLock()
	cg.assignmentMu.Lock()
	defer cg.consumersMu.RUnlock()
	defer cg.assignmentMu.Unlock()

	// Collect all topics
	topics := make(map[string]bool)
	for _, consumer := range cg.consumers {
		for _, topic := range consumer.topics {
			topics[topic] = true
		}
	}

	// Check context after collecting topics
	if err := ctx.Err(); err != nil {
		return err
	}

	// Create partitions for each topic
	for topic := range topics {
		partitions := make([]int, cg.config.MaxPartitions)
		for i := 0; i < cg.config.MaxPartitions; i++ {
			partitions[i] = i
		}
		cg.partitions[topic] = partitions
	}

	// Check context before performing assignment
	if err := ctx.Err(); err != nil {
		return err
	}

	// Perform assignment based on strategy
	switch cg.config.Strategy {
	case RoundRobin:
		cg.assignRoundRobin()
	case Sticky:
		cg.assignSticky()
	case Range:
		cg.assignRange()
	case ConsistentHash:
		cg.assignConsistentHash()
	default:
		return ErrInvalidAssignment
	}

	// Check context after assignment
	if err := ctx.Err(); err != nil {
		return err
	}

	// Build new partition assignments per consumer without holding per-consumer locks.
	newParts := make(map[string]map[string][]int) // consumerID -> topic -> partitions
	for topic, partAssignments := range cg.assignments {
		for partition, consumerID := range partAssignments {
			if _, exists := cg.consumers[consumerID]; exists {
				if newParts[consumerID] == nil {
					newParts[consumerID] = make(map[string][]int)
				}
				newParts[consumerID][topic] = append(newParts[consumerID][topic], partition)
			}
		}
	}

	// Apply atomically to each consumer under its own lock.
	for id, consumer := range cg.consumers {
		parts := newParts[id]
		if parts == nil {
			parts = make(map[string][]int)
		}
		consumer.assignedPartsMu.Lock()
		consumer.assignedParts = parts
		consumer.assignedPartsMu.Unlock()
	}

	return nil
}

// assignRoundRobin implements round-robin assignment
func (cg *consumerGroup) assignRoundRobin() {
	// Get sorted consumer IDs for deterministic assignment
	consumerIDs := make([]string, 0, len(cg.consumers))
	for id := range cg.consumers {
		consumerIDs = append(consumerIDs, id)
	}
	sort.Strings(consumerIDs)

	if len(consumerIDs) == 0 {
		return
	}

	consumerIdx := 0

	for topic, partitions := range cg.partitions {
		if cg.assignments[topic] == nil {
			cg.assignments[topic] = make(map[int]string)
		}

		for _, partition := range partitions {
			cg.assignments[topic][partition] = consumerIDs[consumerIdx]
			consumerIdx = (consumerIdx + 1) % len(consumerIDs)
		}
	}
}

// assignSticky implements sticky assignment (tries to preserve previous assignments).
// It keeps existing partition-to-consumer mappings where the consumer still exists,
// revokes the minimum necessary for balance, then distributes unassigned partitions.
func (cg *consumerGroup) assignSticky() {
	consumerIDs := make([]string, 0, len(cg.consumers))
	for id := range cg.consumers {
		consumerIDs = append(consumerIDs, id)
	}
	sort.Strings(consumerIDs)

	if len(consumerIDs) == 0 {
		for topic := range cg.assignments {
			cg.assignments[topic] = make(map[int]string)
		}
		return
	}

	consumerSet := make(map[string]bool, len(consumerIDs))
	for _, id := range consumerIDs {
		consumerSet[id] = true
	}

	for topic, partitions := range cg.partitions {
		prev := cg.assignments[topic]
		if prev == nil {
			prev = make(map[int]string)
		}

		numPartitions := len(partitions)
		numConsumers := len(consumerIDs)
		minParts := numPartitions / numConsumers
		extraSlots := numPartitions % numConsumers // this many consumers get minParts+1

		// Compute per-consumer target: the first extraSlots consumers (sorted)
		// may hold minParts+1; the rest hold minParts. But to minimize movement,
		// we decide which consumers get the extra slot based on who already has more.
		target := make(map[string]int, numConsumers)
		for _, id := range consumerIDs {
			target[id] = minParts
		}

		// Phase 1: Retain valid assignments, track counts per consumer.
		counts := make(map[string]int, numConsumers)
		kept := make(map[int]string, numPartitions)

		for _, p := range partitions {
			owner, ok := prev[p]
			if ok && consumerSet[owner] {
				kept[p] = owner
				counts[owner]++
			}
		}

		// Decide which consumers get the extra partition. Prefer consumers that
		// already have more, to minimize movement.
		if extraSlots > 0 {
			type ccount struct {
				id    string
				count int
			}
			sorted := make([]ccount, len(consumerIDs))
			for i, id := range consumerIDs {
				sorted[i] = ccount{id, counts[id]}
			}
			// Sort by current count descending, then by ID ascending for stability.
			sort.Slice(sorted, func(i, j int) bool {
				if sorted[i].count != sorted[j].count {
					return sorted[i].count > sorted[j].count
				}
				return sorted[i].id < sorted[j].id
			})
			for i := 0; i < extraSlots; i++ {
				target[sorted[i].id] = minParts + 1
			}
		}

		// Phase 2: Revoke partitions from consumers above their target.
		// Iterate partitions in order for determinism; revoke the last-seen
		// partitions of over-assigned consumers.
		var unassigned []int
		for _, p := range partitions {
			owner, ok := kept[p]
			if !ok {
				unassigned = append(unassigned, p)
				continue
			}
			if counts[owner] > target[owner] {
				delete(kept, p)
				counts[owner]--
				unassigned = append(unassigned, p)
			}
		}

		// Phase 3: Distribute unassigned partitions to least-loaded consumers.
		sort.Ints(unassigned)

		for _, p := range unassigned {
			best := consumerIDs[0]
			bestCount := counts[best]
			for _, id := range consumerIDs[1:] {
				if counts[id] < bestCount {
					best = id
					bestCount = counts[id]
				}
			}
			kept[p] = best
			counts[best]++
		}

		cg.assignments[topic] = kept
	}
}

// assignRange implements range assignment
func (cg *consumerGroup) assignRange() {
	consumerIDs := make([]string, 0, len(cg.consumers))
	for id := range cg.consumers {
		consumerIDs = append(consumerIDs, id)
	}
	sort.Strings(consumerIDs)

	if len(consumerIDs) == 0 {
		return
	}

	for topic, partitions := range cg.partitions {
		if cg.assignments[topic] == nil {
			cg.assignments[topic] = make(map[int]string)
		}

		partitionsPerConsumer := len(partitions) / len(consumerIDs)
		remainder := len(partitions) % len(consumerIDs)

		partIdx := 0
		for consumerIdx, consumerID := range consumerIDs {
			count := partitionsPerConsumer
			if consumerIdx < remainder {
				count++
			}

			for i := 0; i < count && partIdx < len(partitions); i++ {
				cg.assignments[topic][partitions[partIdx]] = consumerID
				partIdx++
			}
		}
	}
}

// assignConsistentHash implements consistent hashing assignment
func (cg *consumerGroup) assignConsistentHash() {
	consumerIDs := make([]string, 0, len(cg.consumers))
	for id := range cg.consumers {
		consumerIDs = append(consumerIDs, id)
	}

	if len(consumerIDs) == 0 {
		return
	}

	for topic, partitions := range cg.partitions {
		if cg.assignments[topic] == nil {
			cg.assignments[topic] = make(map[int]string)
		}

		for _, partition := range partitions {
			// Hash partition to consumer
			hash := fnv.New32a()
			_, _ = fmt.Fprintf(hash, "%s:%d", topic, partition)
			consumerIdx := int(hash.Sum32()) % len(consumerIDs)
			cg.assignments[topic][partition] = consumerIDs[consumerIdx]
		}
	}
}

// startWorkers starts background workers for the group
func (cg *consumerGroup) startWorkers() {
	// Heartbeat checker
	cg.wg.Add(1)
	go func() {
		defer cg.wg.Done()
		ticker := time.NewTicker(cg.config.HeartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				cg.checkHeartbeats()
			case <-cg.ctx.Done():
				return
			}
		}
	}()

	// Auto-commit worker (if enabled)
	if cg.config.AutoCommit {
		cg.wg.Add(1)
		go func() {
			defer cg.wg.Done()
			ticker := time.NewTicker(cg.config.AutoCommitInterval)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					// Auto-commit logic would go here
				case <-cg.ctx.Done():
					return
				}
			}
		}()
	}
}

// checkHeartbeats checks for dead consumers
func (cg *consumerGroup) checkHeartbeats() {
	now := time.Now()
	deadline := now.Add(-cg.config.SessionTimeout)

	cg.consumersMu.Lock()
	defer cg.consumersMu.Unlock()

	deadConsumers := []string{}

	for id, consumer := range cg.consumers {
		consumer.heartbeatMu.Lock()
		if consumer.lastHeartbeat.Before(deadline) {
			deadConsumers = append(deadConsumers, id)
		}
		consumer.heartbeatMu.Unlock()
	}

	// Remove dead consumers
	if len(deadConsumers) > 0 {
		for _, id := range deadConsumers {
			if consumer, exists := cg.consumers[id]; exists {
				consumer.Close()
				delete(cg.consumers, id)
			}
		}

		// Trigger rebalance
		go cg.triggerRebalance()
	}
}

// C returns the message channel for the consumer
func (gc *groupConsumer) C() <-chan Message {
	return gc.messageChannel
}

// Heartbeat sends a heartbeat to keep the consumer alive
func (gc *groupConsumer) Heartbeat() {
	gc.heartbeatMu.Lock()
	defer gc.heartbeatMu.Unlock()
	gc.lastHeartbeat = time.Now()
}

// Close closes the consumer.
// It cancels the subscription (which closes the source channel, causing the
// dispatcher goroutine to exit), then waits for the dispatcher to finish
// before closing messageChannel to eliminate the send-on-closed-channel race.
func (gc *groupConsumer) Close() {
	if gc.closed.Swap(true) {
		return
	}

	if gc.subscription != nil {
		gc.subscription.Cancel()
	}

	// Signal the dispatcher goroutine to exit and wait for it to finish
	// before closing messageChannel to prevent a send-on-closed-channel panic.
	gc.cancel()

	// Wait for the dispatcher goroutine to stop before closing the channel it
	// writes to. This prevents a send-on-closed-channel data race.
	if gc.dispatchDone != nil {
		<-gc.dispatchDone
	}

	close(gc.messageChannel)
}

// startDispatcher starts the message dispatcher for this consumer.
// ctx is the per-consumer context; when it is cancelled (via Close) the
// goroutine exits cleanly before messageChannel is closed.
func (gc *groupConsumer) startDispatcher(ctx context.Context) {
	go func() {
		defer close(gc.dispatchDone)
		for {
			select {
			case msg, ok := <-gc.subscription.C():
				if !ok {
					return
				}

				// Stop dispatching once the consumer is marked closed.
				if gc.closed.Load() {
					return
				}

				// Check if this consumer should process this message
				if gc.shouldProcess(msg) {
					select {
					case gc.messageChannel <- msg:
					case <-ctx.Done():
						return
					case <-gc.group.ctx.Done():
						return
					}
				}

			case <-ctx.Done():
				return
			case <-gc.group.ctx.Done():
				return
			}
		}
	}()
}

// shouldProcess determines if this consumer should process the message
func (gc *groupConsumer) shouldProcess(msg Message) bool {
	// Use message ID to determine partition
	hash := fnv.New32a()
	hash.Write([]byte(msg.ID))
	partition := int(hash.Sum32()) % gc.group.config.MaxPartitions

	// Check if assigned
	gc.group.assignmentMu.RLock()
	defer gc.group.assignmentMu.RUnlock()

	for _, topic := range gc.topics {
		if gc.group.assignments[topic] != nil {
			if gc.group.assignments[topic][partition] == gc.id {
				return true
			}
		}
	}

	return false
}

// ConsumerGroupStats holds consumer group statistics
type ConsumerGroupStats struct {
	GroupID         string
	Consumers       int
	Generation      uint64
	Rebalancing     bool
	TotalRebalances uint64
	TotalCommits    uint64
	Errors          uint64
}

// Stats returns statistics for a consumer group
func (cgm *ConsumerGroupManager) Stats(groupID string) (ConsumerGroupStats, error) {
	cgm.groupsMu.RLock()
	group, exists := cgm.groups[groupID]
	cgm.groupsMu.RUnlock()

	if !exists {
		return ConsumerGroupStats{}, ErrGroupNotFound
	}

	group.consumersMu.RLock()
	consumerCount := len(group.consumers)
	group.consumersMu.RUnlock()

	group.rebalanceMu.Lock()
	rebalancing := group.rebalanceRunning
	group.rebalanceMu.Unlock()

	return ConsumerGroupStats{
		GroupID:         groupID,
		Consumers:       consumerCount,
		Generation:      group.generation.Load(),
		Rebalancing:     rebalancing,
		TotalRebalances: cgm.rebalances.Load(),
		TotalCommits:    cgm.commits.Load(),
		Errors:          cgm.errors.Load(),
	}, nil
}
