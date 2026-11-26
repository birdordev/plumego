package publiccloud

import (
	"context"
	"encoding/json"
	"time"

	"with-cloud-manager/internal/provider"
)

type tencentMetricDimension struct{ Name, Value string }

func (c *Client) tencentUtilization(ctx context.Context, region, instance string, window provider.MetricWindow) (provider.InstanceUtilization, error) {
	result := newUtilization(time.Hour)
	cpu, err := c.tencentMetric(ctx, region, instance, "BaseCpuUsage", window, 1, 100)
	if err != nil {
		return provider.InstanceUtilization{}, err
	}
	result.CPU = cpu
	network := make([][]provider.MetricValue, 0, 4)
	for _, metric := range []string{"LanIntraffic", "LanOuttraffic", "WanIntraffic", "WanOuttraffic"} {
		values, err := c.tencentMetric(ctx, region, instance, metric, window, 1e6/8, provider.MaxNetworkBytesPerSecond)
		if err != nil {
			return provider.InstanceUtilization{}, err
		}
		network = append(network, values)
	}
	sumAvailableMetrics(network, result.Network)
	return result, nil
}

func (c *Client) tencentMetric(ctx context.Context, region, instance, metric string, window provider.MetricWindow, scale, maximum float64) ([]provider.MetricValue, error) {
	dimension := tencentMetricDimension{"InstanceId", instance}
	type metricInstance struct{ Dimensions []tencentMetricDimension }
	body, err := json.Marshal(struct {
		Namespace, MetricName, StartTime, EndTime string
		Period, SpecifyStatistics                 int
		Instances                                 []metricInstance
	}{"QCE/CVM", metric, window.From.UTC().Format(time.RFC3339), window.Until.UTC().Format(time.RFC3339),
		3600, 2, []metricInstance{{[]tencentMetricDimension{dimension}}}})
	if err != nil {
		return nil, err
	}
	var response struct {
		MetricName string
		Period     int
		DataPoints []struct {
			Dimensions []tencentMetricDimension
			Timestamps []int64
			MaxValues  []*float64
		}
	}
	if err := c.tencent(ctx, "monitor", region, "GetMonitorData", "2018-07-24", body, &response); err != nil {
		return nil, err
	}
	grid := newMetricGrid(window, time.Hour)
	values := make([]provider.MetricValue, len(grid.seen)-1)
	if response.DataPoints == nil || response.MetricName != metric || response.Period != 3600 || len(response.DataPoints) > 1 {
		return nil, invalidUtilization()
	}
	if len(response.DataPoints) == 0 {
		return values, nil
	}
	row := response.DataPoints[0]
	if len(row.Dimensions) != 1 || row.Dimensions[0] != dimension || len(row.Timestamps) != len(row.MaxValues) ||
		len(row.Timestamps) > len(grid.seen) {
		return nil, invalidUtilization()
	}
	for i, ts := range row.Timestamps {
		position, ok := grid.position(time.Unix(ts, 0))
		value, err := recordMetric(row.MaxValues[i], scale, maximum)
		if !ok || err != nil {
			return nil, invalidUtilization()
		}
		if position < len(values) {
			values[position] = value
		}
	}
	return values, nil
}
