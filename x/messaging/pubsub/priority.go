package pubsub

import (
	"container/heap"
	"sync"
)

// Priority levels (lower number = higher priority)
const (
	PriorityHighest = 0
	PriorityHigh    = 1
	PriorityNormal  = 2
	PriorityLow     = 3
	PriorityLowest  = 4
	PriorityDefault = PriorityNormal
)

// PriorityMessage wraps a message with priority for queue ordering.
type PriorityMessage struct {
	Message  Message
	Priority int
	Sequence uint64 // for FIFO within same priority
	index    int    // index in min-heap (priorityQueue)
	rindex   int    // index in max-heap (evictionQueue)
}

// priorityQueue implements heap.Interface for priority-based message delivery.
type priorityQueue []*PriorityMessage

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	// Lower priority number = higher priority
	if pq[i].Priority != pq[j].Priority {
		return pq[i].Priority < pq[j].Priority
	}
	// Same priority: FIFO by sequence
	return pq[i].Sequence < pq[j].Sequence
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *priorityQueue) Push(x any) {
	n := len(*pq)
	item := x.(*PriorityMessage)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[0 : n-1]
	return item
}

// evictionQueue is a max-heap by priority (worst item at root) for O(log n) eviction.
// When priorities are equal the oldest item (lowest Sequence) is evicted first.
type evictionQueue []*PriorityMessage

func (eq evictionQueue) Len() int { return len(eq) }

func (eq evictionQueue) Less(i, j int) bool {
	if eq[i].Priority != eq[j].Priority {
		return eq[i].Priority > eq[j].Priority // highest number = worst = at root
	}
	return eq[i].Sequence < eq[j].Sequence // oldest first for FIFO within same level
}

func (eq evictionQueue) Swap(i, j int) {
	eq[i], eq[j] = eq[j], eq[i]
	eq[i].rindex = i
	eq[j].rindex = j
}

func (eq *evictionQueue) Push(x any) {
	n := len(*eq)
	item := x.(*PriorityMessage)
	item.rindex = n
	*eq = append(*eq, item)
}

func (eq *evictionQueue) Pop() any {
	old := *eq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.rindex = -1
	*eq = old[:n-1]
	return item
}

// PriorityBuffer is a thread-safe priority queue for messages.
// It maintains two heaps internally:
//   - pq: min-heap (highest-priority item at root) for O(log n) dequeue
//   - eq: max-heap (lowest-priority item at root) for O(log n) eviction
type PriorityBuffer struct {
	mu       sync.Mutex
	pq       priorityQueue // min-heap: highest priority at root
	eq       evictionQueue // max-heap: lowest priority at root
	capacity int
	sequence uint64
	notify   chan struct{}
	closed   bool
}

// NewPriorityBuffer creates a new priority buffer.
func NewPriorityBuffer(capacity int) *PriorityBuffer {
	if capacity <= 0 {
		capacity = 16
	}
	pb := &PriorityBuffer{
		pq:       make(priorityQueue, 0, capacity),
		eq:       make(evictionQueue, 0, capacity),
		capacity: capacity,
		notify:   make(chan struct{}, 1),
	}
	heap.Init(&pb.pq)
	heap.Init(&pb.eq)
	return pb
}

// Push adds a message to the buffer with the given priority.
// Returns (dropped message, true) if capacity was exceeded.
// Finding and removing the worst item is O(log n) via the eviction heap.
func (pb *PriorityBuffer) Push(msg Message, priority int) (*Message, bool) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if pb.closed {
		return nil, false
	}

	pb.sequence++
	pm := &PriorityMessage{
		Message:  msg,
		Priority: priority,
		Sequence: pb.sequence,
	}

	var dropped *Message

	if len(pb.pq) >= pb.capacity {
		// eq[0] is the worst item (highest priority number, oldest sequence on tie).
		worst := pb.eq[0]
		if priority <= worst.Priority {
			// New item is at least as good as the worst: evict the worst.
			droppedMsg := worst.Message
			dropped = &droppedMsg
			heap.Remove(&pb.eq, 0)           // O(log n)
			heap.Remove(&pb.pq, worst.index) // O(log n)
		} else {
			// New item is worse than everything in the buffer: drop it.
			return &msg, true
		}
	}

	heap.Push(&pb.pq, pm) // O(log n)
	heap.Push(&pb.eq, pm) // O(log n)

	// Notify waiting consumers
	select {
	case pb.notify <- struct{}{}:
	default:
	}

	return dropped, dropped != nil
}

// Pop removes and returns the highest priority message.
func (pb *PriorityBuffer) Pop() (Message, int, bool) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if len(pb.pq) == 0 || pb.closed {
		return Message{}, 0, false
	}

	pm := heap.Pop(&pb.pq).(*PriorityMessage) // O(log n)
	heap.Remove(&pb.eq, pm.rindex)            // O(log n): keep eviction heap in sync
	return pm.Message, pm.Priority, true
}

// Peek returns the highest priority message without removing it.
func (pb *PriorityBuffer) Peek() (Message, int, bool) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if len(pb.pq) == 0 || pb.closed {
		return Message{}, 0, false
	}

	pm := pb.pq[0]
	return pm.Message, pm.Priority, true
}

// Len returns the number of messages in the buffer.
func (pb *PriorityBuffer) Len() int {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	return len(pb.pq)
}

// Cap returns the capacity of the buffer.
func (pb *PriorityBuffer) Cap() int {
	return pb.capacity
}

// Notify returns a channel that signals when messages are available.
func (pb *PriorityBuffer) Notify() <-chan struct{} {
	return pb.notify
}

// Close closes the buffer.
func (pb *PriorityBuffer) Close() {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if pb.closed {
		return
	}
	pb.closed = true
	close(pb.notify)
}

// IsClosed returns whether the buffer is closed.
func (pb *PriorityBuffer) IsClosed() bool {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	return pb.closed
}

// Drain removes all messages and returns them in priority order.
func (pb *PriorityBuffer) Drain() []Message {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	msgs := make([]Message, len(pb.pq))
	for i := 0; len(pb.pq) > 0; i++ {
		pm := heap.Pop(&pb.pq).(*PriorityMessage)
		msgs[i] = pm.Message
	}
	clear(pb.eq)
	pb.eq = pb.eq[:0]
	return msgs
}

// Stats returns buffer statistics.
func (pb *PriorityBuffer) Stats() PriorityBufferStats {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	stats := PriorityBufferStats{
		Size:       len(pb.pq),
		Capacity:   pb.capacity,
		ByPriority: make(map[int]int),
	}

	for _, pm := range pb.pq {
		stats.ByPriority[pm.Priority]++
	}

	return stats
}

// PriorityBufferStats contains buffer statistics.
type PriorityBufferStats struct {
	Size       int
	Capacity   int
	ByPriority map[int]int
}

// GetMessagePriority extracts priority from message metadata.
// Returns PriorityDefault if not set.
func GetMessagePriority(msg Message) int {
	if msg.Meta == nil {
		return PriorityDefault
	}

	priStr, ok := msg.Meta["X-Priority"]
	if !ok {
		return PriorityDefault
	}

	switch priStr {
	case "highest", "0":
		return PriorityHighest
	case "high", "1":
		return PriorityHigh
	case "normal", "2":
		return PriorityNormal
	case "low", "3":
		return PriorityLow
	case "lowest", "4":
		return PriorityLowest
	default:
		return PriorityDefault
	}
}

// SetMessagePriority sets priority in message metadata.
func SetMessagePriority(msg *Message, priority int) {
	if msg.Meta == nil {
		msg.Meta = make(map[string]string)
	}

	var priStr string
	switch priority {
	case PriorityHighest:
		priStr = "highest"
	case PriorityHigh:
		priStr = "high"
	case PriorityNormal:
		priStr = "normal"
	case PriorityLow:
		priStr = "low"
	case PriorityLowest:
		priStr = "lowest"
	default:
		priStr = "normal"
	}

	msg.Meta["X-Priority"] = priStr
}
