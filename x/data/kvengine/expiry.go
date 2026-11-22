package kvengine

import "container/heap"

// expiryQueue tracks one deadline per live entry. A shard owns its queue.
type expiryQueue struct {
	entries   []*Entry
	positions map[*Entry]int
}

func (q *expiryQueue) add(entry *Entry) {
	if entry.ExpireAt.IsZero() {
		return
	}
	if q.positions == nil {
		q.positions = make(map[*Entry]int)
	}
	heap.Push(q, entry)
}

func (q *expiryQueue) remove(entry *Entry) {
	if i, exists := q.positions[entry]; exists {
		heap.Remove(q, i)
	}
}

func (q *expiryQueue) Len() int { return len(q.entries) }
func (q *expiryQueue) Less(i, j int) bool {
	return q.entries[i].ExpireAt.Before(q.entries[j].ExpireAt)
}
func (q *expiryQueue) Swap(i, j int) {
	q.entries[i], q.entries[j] = q.entries[j], q.entries[i]
	q.positions[q.entries[i]] = i
	q.positions[q.entries[j]] = j
}
func (q *expiryQueue) Push(value any) {
	entry := value.(*Entry)
	q.positions[entry] = len(q.entries)
	q.entries = append(q.entries, entry)
}
func (q *expiryQueue) Pop() any {
	i := len(q.entries) - 1
	entry := q.entries[i]
	delete(q.positions, entry)
	q.entries[i] = nil
	q.entries = q.entries[:i]
	return entry
}
