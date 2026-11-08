package abuse

import (
	"errors"
	"math"
	"testing"
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
