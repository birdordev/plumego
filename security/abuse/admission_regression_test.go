package abuse

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestFullLimiterPreservesExistingBucket(t *testing.T) {
	now := time.Now()
	limiter, err := NewLimiterWithConfig(Config{Rate: 1, Capacity: 1, MaxEntries: 1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Stop()
	if !limiter.Allow("same").Allowed {
		t.Fatal("first request denied")
	}
	for i := 0; i < 3; i++ {
		if limiter.Allow("same").Allowed {
			t.Fatal("existing bucket was refilled by eviction")
		}
	}
	if got := limiter.Metrics().Evictions; got != 0 {
		t.Fatalf("unexpected evictions: %d", got)
	}
	if !limiter.Allow("new").Allowed {
		t.Fatal("new key was not admitted")
	}
	if got := limiter.bucketCount.Load(); got != 1 {
		t.Fatalf("buckets=%d", got)
	}
}

func TestConcurrentAdmissionRespectsCapacity(t *testing.T) {
	limiter, err := NewLimiterWithConfig(Config{Rate: 1, Capacity: 1, MaxEntries: 4, Shards: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Stop()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 20; j++ {
				limiter.Allow(fmt.Sprintf("%d-%d", i, j))
				if got := limiter.bucketCount.Load(); got > 4 {
					t.Errorf("capacity exceeded: %d", got)
				}
				if got := limiter.Metrics().Buckets; got > 4 {
					t.Errorf("bucket metric exceeded capacity: %d", got)
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if got := limiter.bucketCount.Load(); got > 4 {
		t.Fatalf("final capacity exceeded: %d", got)
	}
}
