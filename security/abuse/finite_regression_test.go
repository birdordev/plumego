package abuse

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestFiniteRate(t *testing.T) {
	for _, rate := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		cfg := Config{Rate: rate}
		if !errors.Is(cfg.Validate(), ErrInvalidConfig) {
			t.Errorf("rate=%v validated", rate)
		}
		l, err := NewLimiterWithConfig(cfg)
		if l != nil {
			l.Stop()
		}
		if !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("rate=%v constructor err=%v", rate, err)
		}
		fallback := NewLimiter(cfg)
		if fallback.Allow("key").Allowed {
			t.Error("invalid fallback allowed")
		}
		fallback.Stop()
	}
	for _, rate := range []float64{0, 1, math.SmallestNonzeroFloat64, math.MaxFloat64} {
		if err := (Config{Rate: rate}).Validate(); err != nil {
			t.Errorf("finite rate=%v err=%v", rate, err)
		}
	}
}

func TestFiniteRateDecisionTimestampsStayOrdered(t *testing.T) {
	for _, rate := range []float64{math.SmallestNonzeroFloat64, 1, math.MaxFloat64} {
		now := time.Unix(1800000000, 0)
		cfg := DefaultConfig()
		cfg.Rate = rate
		cfg.Capacity = 1
		cfg.Now = func() time.Time { return now }
		limiter, err := NewLimiterWithConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		initial := limiter.Allow("fixture")
		exhausted := limiter.Allow("fixture")
		now = now.Add(time.Second)
		later := limiter.Allow("fixture")
		limiter.Stop()
		if !initial.Allowed || exhausted.Allowed || exhausted.Remaining != 0 ||
			exhausted.RetryAfter < 0 || exhausted.Reset.Before(initial.Reset) ||
			later.RetryAfter < 0 || later.Reset.Before(now) {
			t.Fatalf("rate=%g initial=%+v exhausted=%+v later=%+v", rate, initial, exhausted, later)
		}
		if later.Allowed != (rate >= 1) {
			t.Fatalf("rate=%g recovery=%+v", rate, later)
		}
	}
}

func TestLimiterRetryAfterSaturatesWithSameResetBase(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		rate float64
		wait time.Duration
	}{{math.SmallestNonzeroFloat64, time.Duration(math.MaxInt64)}, {0.25, 4 * time.Second}} {
		l, err := NewLimiterWithConfig(Config{Rate: tc.rate, Capacity: 1, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		initial, next := l.Allow("fixture"), l.Allow("fixture")
		l.Stop()
		if !initial.Allowed || next.Allowed || next.RetryAfter != tc.wait || !next.Reset.Equal(now.Add(tc.wait)) {
			t.Fatalf("rate=%g initial=%+v next=%+v", tc.rate, initial, next)
		}
	}
}
