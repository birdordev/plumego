package requestid

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func BenchmarkNewRequestID(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewRequestID()
	}
}

func BenchmarkRequestIDGenerationParallel(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = NewRequestID()
		}
	})
}

func BenchmarkRequestIDMiddleware(b *testing.B) {
	mw := Middleware()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		handler.ServeHTTP(w, req)
	}
}

func TestParallelRequestIDsRemainUniqueAcrossPoolWrap(t *testing.T) {
	const workers = 8
	const idsPerWorker = randomPoolSize / workers * 3
	generator := NewRequestIDGenerator()
	ids := make(chan string, workers*idsPerWorker)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for range idsPerWorker {
				ids <- generator.Generate()
			}
		}()
	}
	wg.Wait()
	close(ids)
	seen := make(map[string]struct{}, workers*idsPerWorker)
	for id := range ids {
		if _, ok := seen[id]; ok || len(id) != idWidth {
			t.Fatalf("duplicate or invalid-width ID %q", id)
		}
		seen[id] = struct{}{}
	}
}

func BenchmarkRequestIDPoolParallel(b *testing.B) {
	generator := NewRequestIDGenerator()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = generator.Generate()
		}
	})
}
