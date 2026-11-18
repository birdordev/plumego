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
