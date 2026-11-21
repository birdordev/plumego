package requestid

import (
	"math"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestDecodeRequestIDRejectsOverflow(t *testing.T) {
	const maxID = "0LygHa16AHYF"
	const overflowID = "0LygHa16AHYG"
	if got := encodeBase62Fixed(math.MaxUint64, idWidth); got != maxID {
		t.Fatalf("encoded maximum = %q", got)
	}
	if _, random, seq, err := DecodeRequestID(maxID); err != nil || random != randMask || seq != seqMax {
		t.Fatalf("decode maximum: random=%d seq=%d error=%v", random, seq, err)
	}
	for _, invalid := range []string{overflowID, maxID[1:], maxID[:idWidth-1] + "!"} {
		if _, _, _, err := DecodeRequestID(invalid); err != errInvalidBase62 {
			t.Fatalf("decode boundary %q: %v", invalid, err)
		}
	}
}

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

func TestGenerateRecoversWhenClockCatchesUpToExhaustedSequence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := NewRequestIDGenerator()
		g.lastMs = time.Now().Add(3 * time.Millisecond).UnixMilli()
		g.seq = int32(seqMax)
		done := make(chan string, 1)
		go func() { done <- g.Generate() }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("generated a duplicate timestamp and sequence while clock was behind")
		default:
		}
		time.Sleep(5 * time.Millisecond)
		id := <-done
		next := g.Generate()
		if len(id) != idWidth || id == next {
			t.Fatalf("first=%q next=%q", id, next)
		}
	})
}

func TestDecodeRequestIDPaddedFieldRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		offset           int64
		random, sequence int
	}{{}, {1, 2, 3}, {(1 << tsBits) - 1, randMask, seqMax}} {
		packed := uint64(tc.offset)<<shiftTS | uint64(tc.random)<<shiftRand | uint64(tc.sequence)
		id := encodeBase62Fixed(packed, idWidth)
		at, random, sequence, err := DecodeRequestID(id)
		if len(id) != idWidth || at != epochMilli+tc.offset || random != tc.random || sequence != tc.sequence || err != nil {
			t.Fatalf("length=%d decoded fields=%d,%d,%d error=%v", len(id), at, random, sequence, err)
		}
	}
}
