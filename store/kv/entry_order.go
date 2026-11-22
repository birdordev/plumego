package kvstore

import "container/heap"

// orderedEntries keeps one heap slot per indexed key. An update removes the
// old slot, including when the same key receives a different TTL.
type orderedEntries struct {
	entries    []evictionCandidate
	positions  map[string]int
	byDeadline bool
}

func (h *orderedEntries) Len() int { return len(h.entries) }
func (h *orderedEntries) Less(i, j int) bool {
	a, b := h.entries[i], h.entries[j]
	if h.byDeadline {
		return a.item.ExpireAt.Before(b.item.ExpireAt)
	}
	return compareEvictionCandidates(a, b) < 0
}
func (h *orderedEntries) Swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.positions[h.entries[i].key], h.positions[h.entries[j].key] = i, j
}
func (h *orderedEntries) Push(value any) {
	entry := value.(evictionCandidate)
	if h.positions == nil {
		h.positions = make(map[string]int)
	}
	h.positions[entry.key] = len(h.entries)
	h.entries = append(h.entries, entry)
}
func (h *orderedEntries) Pop() any {
	i := len(h.entries) - 1
	entry := h.entries[i]
	delete(h.positions, entry.key)
	h.entries[i] = evictionCandidate{}
	h.entries = h.entries[:i]
	return entry
}
func (h *orderedEntries) remove(key string) {
	if i, ok := h.positions[key]; ok {
		heap.Remove(h, i)
	}
}

func (kv *KVStore) indexEntry(key string, item *entry) {
	candidate := evictionCandidate{key: key, item: item}
	heap.Push(&kv.oldest, candidate)
	if !item.ExpireAt.IsZero() {
		heap.Push(&kv.expiring, candidate)
	}
	kv.capacityBytes += item.Size
}

// replaceEntry updates the state and both indexes while the store is locked.
func (kv *KVStore) replaceEntry(key string, item *entry) {
	if previous := kv.data[key]; previous != nil {
		kv.capacityBytes -= previous.Size
		kv.oldest.remove(key)
		kv.expiring.remove(key)
	}
	if item == nil {
		delete(kv.data, key)
		return
	}
	kv.data[key] = item
	kv.indexEntry(key, item)
}
