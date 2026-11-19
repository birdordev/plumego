package kvengine

import "sync/atomic"

// LRU management for shard
func (kv *KVStore) moveToFront(shard *Shard, entry *Entry) {
	if shard.lruHead == entry {
		return
	}

	// Only linked entries have a predecessor entry in the index.
	if prev, linked := shard.prev[entry]; linked {
		shard.next[prev] = shard.next[entry]
		if next := shard.next[entry]; next != nil {
			shard.prev[next] = prev
		} else {
			shard.lruTail = prev
		}
	}

	// Add to front
	shard.prev[entry] = nil
	shard.next[entry] = shard.lruHead
	if shard.lruHead != nil {
		shard.prev[shard.lruHead] = entry
	}
	shard.lruHead = entry
	if shard.lruTail == nil {
		shard.lruTail = entry
	}
}

func (kv *KVStore) evictOneLRUSkip(skipKey string) {
	for _, shard := range kv.shards {
		shard.mu.Lock()
		victim := shard.lruTail
		if victim != nil && victim.Key == skipKey {
			victim = shard.prev[victim]
		}
		if victim != nil {
			kv.deleteFromShard(shard, victim.Key, victim)
			// LRU eviction is a cache operation, so it does not write to the WAL.
			atomic.AddInt64(&kv.evictions, 1)
			shard.mu.Unlock()
			return
		}
		shard.mu.Unlock()
	}
}

func (kv *KVStore) deleteFromShard(shard *Shard, key string, entry *Entry) {
	// Remove from map
	delete(shard.data, key)

	// Remove from LRU
	if prev := shard.prev[entry]; prev != nil {
		shard.next[prev] = shard.next[entry]
	} else {
		shard.lruHead = shard.next[entry]
	}
	if next := shard.next[entry]; next != nil {
		shard.prev[next] = shard.prev[entry]
	} else {
		shard.lruTail = shard.prev[entry]
	}

	delete(shard.next, entry)
	delete(shard.prev, entry)

	// Update counters
	atomic.AddInt64(&kv.entries, -1)
	atomic.AddInt64(&kv.memoryUsage, -entry.Size)
}
