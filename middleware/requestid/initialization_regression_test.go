package requestid

import (
	"sync"
	"testing"
)

func TestConcurrentZeroValueInitialization(t *testing.T) {
	var gen RequestIDGenerator
	var wg sync.WaitGroup
	start := make(chan struct{})
	ids := make(chan string, 8192)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 256; j++ {
				ids <- gen.Generate()
			}
		}()
	}
	close(start)
	wg.Wait()
	close(ids)
	seen := make(map[string]bool)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
		if _, _, _, err := DecodeRequestID(id); err != nil {
			t.Fatal(err)
		}
	}
}
