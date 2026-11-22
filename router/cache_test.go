package router

import (
	"fmt"
	"sync"
	"testing"
)

func TestMatcherCacheConcurrentAccess(t *testing.T) {
	cache := newMatchCache(64)
	for i := 0; i < 64; i++ {
		cache.Set(matchCacheKey{path: fmt.Sprintf("key-%d", i)}, &matchResult{})
	}

	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				key := fmt.Sprintf("key-%d", (id+j)%128)
				cache.Get(matchCacheKey{path: key})
			}
		}(i)
	}

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				key := fmt.Sprintf("key-%d", (id*500+j)%128)
				cache.Set(matchCacheKey{path: key}, &matchResult{})
			}
		}(i)
	}

	wg.Wait()

	if count := matchCacheEntryCount(cache); count > 64 {
		t.Fatalf("cache entry count exceeded capacity: %d", count)
	}
}
