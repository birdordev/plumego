package provider

import (
	"context"
	"math"
	"time"
)

const IdleWindow = 7 * 24 * time.Hour
const MaxNetworkBytesPerSecond = 1e12

type MetricWindow struct {
	From  time.Time
	Until time.Time
}

func (w MetricWindow) Valid() bool {
	return !w.From.IsZero() && w.Until.Sub(w.From) == IdleWindow && w.From.Equal(w.From.Truncate(time.Hour))
}

type MetricValue struct {
	Known bool
	Value float64
}

// InstanceUtilization 的采样按窗口起点等距排列，缺测记录保留对应槽位。
type InstanceUtilization struct {
	Period  time.Duration
	CPU     []MetricValue
	Network []MetricValue
}

type InstanceMetrics interface {
	InstanceUtilization(context.Context, Scope, string, MetricWindow) (InstanceUtilization, error)
}

func (u InstanceUtilization) Valid(window MetricWindow) bool {
	if !window.Valid() || (u.Period != time.Hour && u.Period != 10*time.Minute) {
		return false
	}
	return validUtilizationSeries(u.CPU, int(IdleWindow/u.Period), 100) &&
		validUtilizationSeries(u.Network, int(IdleWindow/u.Period), MaxNetworkBytesPerSecond)
}

func validUtilizationSeries(values []MetricValue, count int, maximum float64) bool {
	if len(values) != count {
		return false
	}
	for _, v := range values {
		if math.IsNaN(v.Value) || math.IsInf(v.Value, 0) || v.Value < 0 || v.Value > maximum || (!v.Known && v.Value != 0) {
			return false
		}
	}
	return true
}
