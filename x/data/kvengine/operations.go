package kvengine

import (
	"fmt"
	"sort"
	"sync/atomic"
	"time"
)

// Set stores a value and applies capacity eviction after the WAL accepts the write.
func (kv *KVStore) Set(key string, value []byte, ttl time.Duration) (err error) {
	start := time.Now()
	defer func() {
		kv.recordMetrics("set", key, time.Since(start), err, false)
	}()

	if kv.isClosed() {
		return ErrStoreClosed
	}
	if kv.opts.ReadOnly {
		return ErrStoreClosed
	}

	kv.opsMu.Lock()
	defer kv.opsMu.Unlock()
	if kv.isClosed() {
		return ErrStoreClosed
	}

	var expireAt time.Time
	if ttl > 0 {
		expireAt = time.Now().Add(ttl)
	}

	size := int64(len(key)) + int64(len(value)) + 64
	maxMemory := int64(kv.opts.MaxMemoryMB) * 1024 * 1024
	if size > maxMemory {
		return fmt.Errorf("value too large: size %d exceeds max memory %d", size, maxMemory)
	}

	version := atomic.AddInt64(&kv.version, 1)

	// Write to WAL first (before updating memory)
	walEntry := WALEntry{
		Op:       opSet,
		Key:      key,
		Value:    value,
		ExpireAt: expireAt,
		Version:  version,
	}
	walEntry.CRC = kv.calculateCRC(walEntry)

	// Honor the configured WAL sync mode before changing resident entries.
	if err := kv.writeWALEntry(walEntry); err != nil {
		return fmt.Errorf("WAL write failed: %w", err)
	}

	kv.putEntry(&Entry{
		Key:      key,
		Value:    append([]byte(nil), value...), // defensive copy
		ExpireAt: expireAt,
		Size:     size,
		Version:  version,
	}, maxMemory)
	return nil
}

// putEntry publishes a value that fits on its own, under opsMu or during startup.
func (kv *KVStore) putEntry(entry *Entry, maxMemory int64) {
	shard := kv.getShard(entry.Key)
	kv.makeRoomForSet(shard, entry.Key, entry.Size, maxMemory)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if oldEntry := shard.data[entry.Key]; oldEntry != nil {
		kv.deleteFromShard(shard, entry.Key, oldEntry)
	}

	shard.data[entry.Key] = entry
	shard.expiring.add(entry)
	kv.moveToFront(shard, entry)

	atomic.AddInt64(&kv.entries, 1)
	atomic.AddInt64(&kv.memoryUsage, entry.Size)
}

// makeRoomForSet runs with opsMu held or during startup. The incoming entry fits on its own,
// and other goroutines can only remove entries until Set publishes it.
func (kv *KVStore) makeRoomForSet(shard *Shard, key string, size, maxMemory int64) {
	for {
		shard.mu.RLock()
		entries := atomic.LoadInt64(&kv.entries)
		memory := atomic.LoadInt64(&kv.memoryUsage)
		if old := shard.data[key]; old != nil {
			entries--
			memory -= old.Size
		}
		shard.mu.RUnlock()

		if entries < int64(kv.opts.MaxEntries) && memory <= maxMemory-size {
			return
		}
		// Expiration may remove the last victim while eviction scans the shards.
		kv.evictOneLRUSkip(key)
	}
}

func (kv *KVStore) Get(key string) (value []byte, err error) {
	start := time.Now()
	hit := false
	defer func() {
		kv.recordMetrics("get", key, time.Since(start), err, hit)
	}()

	if kv.isClosed() {
		return nil, ErrStoreClosed
	}

	shard := kv.getShard(key)

	// Use read lock for the entire operation to prevent race conditions
	shard.mu.RLock()
	entry, exists := shard.data[key]

	// Check if key exists
	if !exists {
		shard.mu.RUnlock()
		atomic.AddInt64(&kv.misses, 1)
		return nil, ErrKeyNotFound
	}

	// Check expiration
	if !entry.ExpireAt.IsZero() && time.Now().After(entry.ExpireAt) {
		shard.mu.RUnlock()

		// Upgrade to write lock to delete expired entry
		shard.mu.Lock()
		// Double-check under write lock
		if e, exists := shard.data[key]; exists {
			if !e.ExpireAt.IsZero() && time.Now().After(e.ExpireAt) {
				kv.deleteFromShard(shard, key, e)
				// No need to log to WAL - expired entries are handled during recovery
				shard.mu.Unlock()
				atomic.AddInt64(&kv.misses, 1)
				return nil, ErrKeyExpired
			}
		}
		shard.mu.Unlock()

		// Entry was refreshed by another goroutine, retry the read
		return kv.Get(key)
	}

	// Create defensive copy while holding read lock
	valueCopy := append([]byte(nil), entry.Value...)
	shard.mu.RUnlock()

	// Update LRU with minimal lock time
	// Use TryLock to avoid blocking readers if contention is high.
	// If we can't get the lock immediately, we skip the LRU update.
	// This trades LRU accuracy for read throughput under high concurrency.
	if shard.mu.TryLock() {
		// Recheck existence and expiration under write lock
		if e, exists := shard.data[key]; exists {
			if !e.ExpireAt.IsZero() && time.Now().After(e.ExpireAt) {
				shard.mu.Unlock()
				atomic.AddInt64(&kv.misses, 1)
				return nil, ErrKeyExpired
			}
			// Move to front (LRU)
			kv.moveToFront(shard, e)
		}
		shard.mu.Unlock()
	}

	atomic.AddInt64(&kv.hits, 1)
	hit = true
	return valueCopy, nil
}

func (kv *KVStore) Delete(key string) (err error) {
	start := time.Now()
	defer func() {
		kv.recordMetrics("delete", key, time.Since(start), err, false)
	}()

	if kv.isClosed() {
		return ErrStoreClosed
	}
	if kv.opts.ReadOnly {
		return ErrStoreClosed
	}

	kv.opsMu.Lock()
	defer kv.opsMu.Unlock()
	if kv.isClosed() {
		return ErrStoreClosed
	}

	// Write to WAL first (before updating memory)
	version := atomic.AddInt64(&kv.version, 1)
	walEntry := WALEntry{
		Op:      opDelete,
		Key:     key,
		Version: version,
	}
	walEntry.CRC = kv.calculateCRC(walEntry)

	// Honor the configured WAL sync mode before changing resident entries.
	if err := kv.writeWALEntry(walEntry); err != nil {
		return fmt.Errorf("WAL write failed: %w", err)
	}

	shard := kv.getShard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	entry, exists := shard.data[key]
	if !exists {
		return ErrKeyNotFound
	}

	kv.deleteFromShard(shard, key, entry)

	return nil
}

func (kv *KVStore) Exists(key string) bool {
	if kv.isClosed() {
		return false
	}

	shard := kv.getShard(key)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	entry, exists := shard.data[key]
	if !exists {
		return false
	}

	// Check expiration
	if !entry.ExpireAt.IsZero() && time.Now().After(entry.ExpireAt) {
		return false
	}

	return true
}

func (kv *KVStore) Keys() []string {
	if kv.isClosed() {
		return nil
	}

	var keys []string
	now := time.Now()

	for _, shard := range kv.shards {
		shard.mu.RLock()
		for key, entry := range shard.data {
			if entry.ExpireAt.IsZero() || now.Before(entry.ExpireAt) {
				keys = append(keys, key)
			}
		}
		shard.mu.RUnlock()
	}

	sort.Strings(keys)
	return keys
}

func (kv *KVStore) Size() int {
	return int(atomic.LoadInt64(&kv.entries))
}

func (kv *KVStore) GetStats() Stats {
	hits := atomic.LoadInt64(&kv.hits)
	misses := atomic.LoadInt64(&kv.misses)

	var hitRatio float64
	if hits+misses > 0 {
		hitRatio = float64(hits) / float64(hits+misses)
	}

	return Stats{
		Entries:     atomic.LoadInt64(&kv.entries),
		Hits:        hits,
		Misses:      misses,
		Evictions:   atomic.LoadInt64(&kv.evictions),
		MemoryUsage: atomic.LoadInt64(&kv.memoryUsage),
		WALSize:     atomic.LoadInt64(&kv.walSize),
		HitRatio:    hitRatio,
	}
}
