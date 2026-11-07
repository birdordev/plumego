package requestid

import (
	"sync"
	"testing"
	"time"
)

func TestNewRequestIDGeneration(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := NewRequestID()
		if len(id) != idWidth {
			t.Errorf("expected request ID length %d, got %d", idWidth, len(id))
		}
		if ids[id] {
			t.Errorf("duplicate request ID generated: %s", id)
		}
		ids[id] = true
	}
}

func TestDecodeRequestID(t *testing.T) {
	originalID := NewRequestID()
	unixMilli, r, seqVal, err := DecodeRequestID(originalID)
	if err != nil {
		t.Errorf("failed to decode valid request ID: %v", err)
	}

	if unixMilli < epochMilli {
		t.Errorf("decoded timestamp before epoch: %d", unixMilli)
	}
	if r < 0 || r >= randMax {
		t.Errorf("decoded random value out of range: %d", r)
	}
	if seqVal < 0 || seqVal > seqMax {
		t.Errorf("decoded sequence value out of range: %d", seqVal)
	}
}

func TestGeneratedRequestIDExposesTimestampComponent(t *testing.T) {
	before := time.Now().Add(-time.Second).UnixMilli()
	id := NewRequestID()
	after := time.Now().Add(time.Second).UnixMilli()

	unixMilli, _, _, err := DecodeRequestID(id)
	if err != nil {
		t.Fatalf("failed to decode generated request ID: %v", err)
	}
	if unixMilli < before || unixMilli > after {
		t.Fatalf("decoded timestamp = %d, want between %d and %d", unixMilli, before, after)
	}
}

func TestInvalidRequestID(t *testing.T) {
	_, _, _, err := DecodeRequestID("")
	if err != errInvalidBase62 {
		t.Errorf("expected errInvalidBase62 for empty string, got %v", err)
	}

	_, _, _, err = DecodeRequestID("short")
	if err != errInvalidBase62 {
		t.Errorf("expected errInvalidBase62 for wrong length, got %v", err)
	}

	_, _, _, err = DecodeRequestID("!!!@#$%^&*()")
	if err != errInvalidBase62 {
		t.Errorf("expected errInvalidBase62 for invalid characters, got %v", err)
	}
}

func TestConcurrentGeneration(t *testing.T) {
	const numGoroutines = 100
	const idsPerGoroutine = 100
	var wg sync.WaitGroup

	ids := make(map[string]bool, numGoroutines*idsPerGoroutine)
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < idsPerGoroutine; j++ {
				id := NewRequestID()
				mu.Lock()
				if ids[id] {
					t.Errorf("duplicate ID generated in concurrent test: %s", id)
				}
				ids[id] = true
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
}

func TestPackageDefaultGenerationUsesPrivateState(t *testing.T) {
	id := NewRequestID()
	if len(id) != idWidth {
		t.Fatalf("expected request ID length %d, got %d", idWidth, len(id))
	}

	if _, _, _, err := DecodeRequestID(id); err != nil {
		t.Fatalf("failed to decode generated ID: %v", err)
	}
}

func TestGeneratorStruct(t *testing.T) {
	gen := NewRequestIDGenerator()

	for i := 0; i < 50; i++ {
		id := gen.Generate()
		if len(id) != idWidth {
			t.Errorf("expected length %d, got %d", idWidth, len(id))
		}

		if _, _, _, err := DecodeRequestID(id); err != nil {
			t.Errorf("failed to decode generated ID: %v", err)
		}
	}
}

func TestZeroValueGenerator(t *testing.T) {
	var gen RequestIDGenerator

	id := gen.Generate()
	if len(id) != idWidth {
		t.Fatalf("expected id length %d, got %d", idWidth, len(id))
	}

	if _, _, _, err := DecodeRequestID(id); err != nil {
		t.Fatalf("failed to decode id from zero value generator: %v", err)
	}
}

func TestTimestampOrdering(t *testing.T) {
	// The generator clamps the timestamp to the previous millisecond and bumps
	// the sequence within the same millisecond, so ordering is guaranteed by
	// construction; no wall-clock sleeps are needed.
	gen := NewRequestIDGenerator()

	ids := make([]string, 10)
	for i := 0; i < 10; i++ {
		ids[i] = gen.Generate()
	}

	for i := 1; i < len(ids); i++ {
		ts1, _, _, _ := DecodeRequestID(ids[i-1])
		ts2, _, _, _ := DecodeRequestID(ids[i])
		if ts2 < ts1 {
			t.Errorf("timestamp not increasing: %d >= %d", ts1, ts2)
		}
	}
}

func BenchmarkRequestIDGeneration(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewRequestID()
	}
}

func BenchmarkConcurrentGeneration(b *testing.B) {
	var wg sync.WaitGroup
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = NewRequestID()
		}()
	}
	wg.Wait()
}

func TestRandomness(t *testing.T) {
	const testCount = 1000
	randValues := make([]int, testCount)

	for i := 0; i < testCount; i++ {
		id := NewRequestID()
		_, r, _, err := DecodeRequestID(id)
		if err != nil {
			t.Errorf("failed to decode ID for randomness test: %v", err)
			return
		}
		randValues[i] = r
	}

	sum := 0
	for _, r := range randValues {
		sum += r
	}
	avg := float64(sum) / float64(testCount)

	expectedAvg := float64(randMax) / 2.0
	if avg < expectedAvg-700 || avg > expectedAvg+700 {
		t.Errorf("random distribution seems skewed: average %f, expected ~%f", avg, expectedAvg)
	}
}
