package ratelimit

import (
	"testing"
	"time"
)

func TestUpdateRateSettlesPreviousInterval(t *testing.T) {
	start := time.Unix(100, 0)
	b := TokenBucket{rate: 2, burst: 20, lastCheck: start}
	b.updateRate(10, 20, start.Add(time.Second))
	if b.tokens != 2 {
		t.Fatalf("tokens after raising rate: %v", b.tokens)
	}
	b.updateRate(0, 20, start.Add(2*time.Second))
	if b.tokens != 12 {
		t.Fatalf("tokens after disabling: %v", b.tokens)
	}
	b.updateRate(1, 20, start.Add(10*time.Second))
	if b.tokens != 12 || !b.lastCheck.Equal(start.Add(10*time.Second)) {
		t.Fatalf("disabled interval generated tokens: %v", b.tokens)
	}
	b.replenish(start.Add(11 * time.Second))
	if b.tokens != 13 {
		t.Fatalf("tokens after re-enabling: %v", b.tokens)
	}
}

func TestKeyedUpdateRateKeepsSharedBurst(t *testing.T) {
	buckets := NewKeyed(0, 4)
	existing := buckets.bucket("existing")
	for _, tc := range []struct {
		key   string
		burst int64
		want  int64
	}{
		{"zero", 0, 4},
		{"negative", -1, 4},
		{"shrink", 2, 2},
	} {
		buckets.UpdateRate(0, tc.burst)
		fresh := buckets.bucket(tc.key)
		if got, newCount := existing.Remaining(), fresh.Remaining(); got != tc.want || newCount != tc.want {
			t.Errorf("burst %d gives existing %d and fresh %d, want %d", tc.burst, got, newCount, tc.want)
		}
	}
	if !existing.AllowN(2) {
		t.Fatal("could not drain existing bucket")
	}
	buckets.UpdateRate(1, 0)
	if fresh := buckets.bucket("with-rate"); fresh.rate != 1 || fresh.burst != 2 {
		t.Errorf("fresh bucket has rate %v and burst %d", fresh.rate, fresh.burst)
	}
	existing.mu.Lock()
	existing.replenish(existing.lastCheck.Add(time.Second))
	got := existing.tokens
	existing.mu.Unlock()
	if got != 1 {
		t.Errorf("tokens after replenishment = %v, want 1", got)
	}
}
