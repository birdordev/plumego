package distributed

import (
	"fmt"
	"testing"
	"time"

	"github.com/birdordev/plumego/store/cache"
)

func BenchmarkHashRingGet(b *testing.B) {
	ring := NewConsistentHashRing(nil)

	// Add nodes
	for i := 0; i < 10; i++ {
		node := NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
		ring.Add(node)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i%10000)
		_, _ = ring.Get(key)
	}
}

func BenchmarkHashRingGetN(b *testing.B) {
	ring := NewConsistentHashRing(nil)

	// Add nodes
	for i := 0; i < 10; i++ {
		node := NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
		ring.Add(node)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i%10000)
		_, _ = ring.GetN(key, 3)
	}
}

func BenchmarkDistributedCacheSet(b *testing.B) {
	nodes := make([]CacheNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	config := DefaultConfig()
	config.ReplicationFactor = 1
	dc := New(nodes, config)
	defer dc.Close()

	ctx := b.Context()
	value := []byte("benchmark-value")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i)
		dc.Set(ctx, key, value, time.Minute)
	}
}

func BenchmarkDistributedCacheGet(b *testing.B) {
	nodes := make([]CacheNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	config := DefaultConfig()
	config.ReplicationFactor = 1
	dc := New(nodes, config)
	defer dc.Close()

	ctx := b.Context()
	value := []byte("benchmark-value")

	// Pre-populate
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key%d", i)
		dc.Set(ctx, key, value, time.Minute)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i%10000)
		_, _ = dc.Get(ctx, key)
	}
}

func BenchmarkDistributedCacheSetWithReplication(b *testing.B) {
	nodes := make([]CacheNode, 5)
	for i := 0; i < 5; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	config := DefaultConfig()
	config.ReplicationFactor = 3
	config.ReplicationMode = ReplicationSync
	dc := New(nodes, config)
	defer dc.Close()

	ctx := b.Context()
	value := []byte("benchmark-value")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i)
		dc.Set(ctx, key, value, time.Minute)
	}
}

func BenchmarkDistributedCacheSetAsyncReplication(b *testing.B) {
	nodes := make([]CacheNode, 5)
	for i := 0; i < 5; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	config := DefaultConfig()
	config.ReplicationFactor = 3
	config.ReplicationMode = ReplicationAsync
	dc := New(nodes, config)
	defer dc.Close()

	ctx := b.Context()
	value := []byte("benchmark-value")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i)
		dc.Set(ctx, key, value, time.Minute)
	}
}

func BenchmarkDistributedCacheIncr(b *testing.B) {
	nodes := make([]CacheNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	dc := New(nodes, DefaultConfig())
	defer dc.Close()

	ctx := b.Context()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("counter%d", i%100)
		_, _ = dc.Incr(ctx, key, 1)
	}
}

func BenchmarkDistributedCacheParallelSet(b *testing.B) {
	nodes := make([]CacheNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	dc := New(nodes, DefaultConfig())
	defer dc.Close()

	ctx := b.Context()
	value := []byte("benchmark-value")

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key%d", i)
			dc.Set(ctx, key, value, time.Minute)
			i++
		}
	})
}

func BenchmarkDistributedCacheParallelGet(b *testing.B) {
	nodes := make([]CacheNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	dc := New(nodes, DefaultConfig())
	defer dc.Close()

	ctx := b.Context()
	value := []byte("benchmark-value")

	// Pre-populate
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key%d", i)
		dc.Set(ctx, key, value, time.Minute)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key%d", i%10000)
			_, _ = dc.Get(ctx, key)
			i++
		}
	})
}

// Benchmark different node counts
func BenchmarkDistributedCache_3Nodes(b *testing.B) {
	benchmarkWithNodeCount(b, 3)
}

func BenchmarkDistributedCache_5Nodes(b *testing.B) {
	benchmarkWithNodeCount(b, 5)
}

func BenchmarkDistributedCache_10Nodes(b *testing.B) {
	benchmarkWithNodeCount(b, 10)
}

func benchmarkWithNodeCount(b *testing.B, nodeCount int) {
	nodes := make([]CacheNode, nodeCount)
	for i := 0; i < nodeCount; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	dc := New(nodes, DefaultConfig())
	defer dc.Close()

	ctx := b.Context()
	value := []byte("benchmark-value")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key%d", i)
		dc.Set(ctx, key, value, time.Minute)
	}
}
