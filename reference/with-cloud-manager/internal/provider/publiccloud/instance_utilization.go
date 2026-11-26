package publiccloud

import (
	"context"
	"time"

	"with-cloud-manager/internal/provider"
)

func (c *Client) InstanceUtilization(ctx context.Context, scope provider.Scope, instance string, window provider.MetricWindow) (provider.InstanceUtilization, error) {
	if err := c.checkInstanceScope(scope, instance); err != nil {
		return provider.InstanceUtilization{}, err
	}
	if !window.Valid() {
		return provider.InstanceUtilization{}, invalidUtilization()
	}
	var result provider.InstanceUtilization
	var err error
	switch c.name {
	case provider.TencentCVM:
		result, err = c.tencentUtilization(ctx, scope.Region, instance, window)
	case provider.AlibabaECS:
		result, err = c.alibabaUtilization(ctx, scope.Region, instance, window)
	case provider.HuaweiECS:
		result, err = c.huaweiUtilization(ctx, scope.Region, instance, window)
	default:
		return provider.InstanceUtilization{}, &provider.Failure{Kind: provider.Unsupported}
	}
	if err != nil {
		return provider.InstanceUtilization{}, err
	}
	if !result.Valid(window) {
		return provider.InstanceUtilization{}, invalidUtilization()
	}
	return result, nil
}

func invalidUtilization() error {
	return &provider.Failure{Kind: provider.InvalidResponse}
}

func newUtilization(period time.Duration) provider.InstanceUtilization {
	count := int(provider.IdleWindow / period)
	return provider.InstanceUtilization{Period: period,
		CPU: make([]provider.MetricValue, count), Network: make([]provider.MetricValue, count)}
}

type metricGrid struct {
	window provider.MetricWindow
	period time.Duration
	seen   []bool
}

func newMetricGrid(window provider.MetricWindow, period time.Duration) metricGrid {
	// 额外一个槽位用于识别厂商返回的右边界采样，以及右边界的重复记录。
	return metricGrid{window: window, period: period, seen: make([]bool, int(window.Until.Sub(window.From)/period)+1)}
}

func (g metricGrid) position(stamp time.Time) (int, bool) {
	if stamp.Before(g.window.From) || stamp.After(g.window.Until) {
		return 0, false
	}
	offset := stamp.Sub(g.window.From)
	index := int(offset / g.period)
	if offset%g.period != 0 || g.seen[index] {
		return 0, false
	}
	g.seen[index] = true
	return index, true
}

func sumAvailableMetrics(series [][]provider.MetricValue, into []provider.MetricValue) {
	for i := range into {
		var total float64
		known := true
		for _, values := range series {
			total += values[i].Value
			known = known && values[i].Known
		}
		if known {
			into[i] = provider.MetricValue{Known: true, Value: total}
		}
	}
}

func recordMetric(value *float64, scale, maximum float64) (provider.MetricValue, error) {
	if value == nil {
		return provider.MetricValue{}, nil
	}
	normalized := *value * scale
	if normalized < 0 || normalized > maximum {
		return provider.MetricValue{}, invalidUtilization()
	}
	return provider.MetricValue{Known: true, Value: normalized}, nil
}
