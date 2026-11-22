package store

import (
	"container/heap"
	"time"
)

const (
	availabilityDeadline = iota
	leaseDeadline
	expiryDeadline
)

// A deadline heap can be traversed without removing records. This allows
// caller-provided clocks to move backwards across Reserve calls.
type taskDeadlines struct {
	kind      int
	entries   []*memRecord
	positions map[*memRecord]int
}

func (q *taskDeadlines) deadline(rec *memRecord) time.Time {
	switch q.kind {
	case leaseDeadline:
		return rec.task.LeaseUntil
	case expiryDeadline:
		return rec.task.ExpiresAt
	default:
		return rec.task.AvailableAt
	}
}

func (q *taskDeadlines) visit(i int, cutoff time.Time, f func(*memRecord)) {
	if i >= len(q.entries) || q.deadline(q.entries[i]).After(cutoff) {
		return
	}
	f(q.entries[i])
	q.visit(2*i+1, cutoff, f)
	q.visit(2*i+2, cutoff, f)
}

func (q *taskDeadlines) Len() int { return len(q.entries) }
func (q *taskDeadlines) Less(i, j int) bool {
	return q.deadline(q.entries[i]).Before(q.deadline(q.entries[j]))
}
func (q *taskDeadlines) Swap(i, j int) {
	q.entries[i], q.entries[j] = q.entries[j], q.entries[i]
	q.positions[q.entries[i]], q.positions[q.entries[j]] = i, j
}
func (q *taskDeadlines) Push(value any) {
	rec := value.(*memRecord)
	if q.positions == nil {
		q.positions = make(map[*memRecord]int)
	}
	q.positions[rec] = len(q.entries)
	q.entries = append(q.entries, rec)
}
func (q *taskDeadlines) Pop() any {
	i := len(q.entries) - 1
	rec := q.entries[i]
	delete(q.positions, rec)
	q.entries[i] = nil
	q.entries = q.entries[:i]
	return rec
}
func (q *taskDeadlines) push(rec *memRecord) { heap.Push(q, rec) }
func (q *taskDeadlines) remove(rec *memRecord) {
	if i, ok := q.positions[rec]; ok {
		heap.Remove(q, i)
	}
}
func (q *taskDeadlines) fix(rec *memRecord) {
	heap.Fix(q, q.positions[rec])
}

func (m *MemStore) queueRecord(rec *memRecord) {
	queue := m.queued[rec.task.Topic]
	if queue == nil {
		queue = m.spareQueue
		m.spareQueue = nil
		if queue == nil {
			queue = &taskDeadlines{kind: availabilityDeadline}
		}
		m.queued[rec.task.Topic] = queue
	}
	queue.push(rec)
	if !rec.task.ExpiresAt.IsZero() {
		m.expires.push(rec)
	}
}

func (m *MemStore) unqueueRecord(rec *memRecord) {
	queue := m.queued[rec.task.Topic]
	queue.remove(rec)
	if queue.Len() == 0 {
		delete(m.queued, rec.task.Topic)
		if m.spareQueue == nil && cap(queue.entries) <= 64 {
			m.spareQueue = queue
		}
	}
	m.expires.remove(rec)
}
