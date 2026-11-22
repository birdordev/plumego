package cache

import (
	"container/heap"
	"time"
)

type cacheExpiration struct {
	key   string
	at    time.Time
	index int
}

type expirationQueue []*cacheExpiration

func (q expirationQueue) Len() int           { return len(q) }
func (q expirationQueue) Less(i, j int) bool { return q[i].at.Before(q[j].at) }
func (q expirationQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index, q[j].index = i, j
}
func (q *expirationQueue) Push(value any) {
	item := value.(*cacheExpiration)
	item.index = len(*q)
	*q = append(*q, item)
}
func (q *expirationQueue) Pop() any {
	last := len(*q) - 1
	item := (*q)[last]
	(*q)[last] = nil
	*q = (*q)[:last]
	return item
}

// indexExpirationLocked keeps at most one pending expiry per stored key.
func (mc *MemoryCache) indexExpirationLocked(key string, at time.Time) {
	if item := mc.expirationByKey[key]; item != nil {
		if at.IsZero() {
			heap.Remove(&mc.expirations, item.index)
			delete(mc.expirationByKey, key)
		} else if !at.Equal(item.at) {
			item.at = at
			heap.Fix(&mc.expirations, item.index)
		}
	} else if !at.IsZero() {
		item := &cacheExpiration{key: key, at: at}
		heap.Push(&mc.expirations, item)
		mc.expirationByKey[key] = item
	}
}

func (mc *MemoryCache) deleteStoredKeyLocked(key string) {
	mc.indexExpirationLocked(key, time.Time{})
	mc.store.Delete(key)
}
