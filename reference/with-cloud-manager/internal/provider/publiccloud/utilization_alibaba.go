package publiccloud

import (
	"context"
	"net/url"
	"time"

	"with-cloud-manager/internal/provider"
)

const aliUtilizationPeriod = 10 * time.Minute
const aliUtilizationChunk = 360 * aliUtilizationPeriod

type alibabaUtilizationRow struct {
	InstanceID string `json:"InstanceId"`
	TimeStamp  string
	CPU        *float64
	InternetRX *float64
	InternetTX *float64
	IntranetRX *float64
	IntranetTX *float64
}

func (r alibabaUtilizationRow) network() (provider.MetricValue, error) {
	var network float64
	known := true
	for _, raw := range []*float64{r.InternetRX, r.InternetTX, r.IntranetRX, r.IntranetTX} {
		value, err := recordMetric(raw, 1000/(8*aliUtilizationPeriod.Seconds()), provider.MaxNetworkBytesPerSecond)
		if err != nil {
			return provider.MetricValue{}, err
		}
		network += value.Value
		known = known && value.Known
	}
	if !known {
		return provider.MetricValue{}, nil
	}
	return provider.MetricValue{Known: true, Value: network}, nil
}

func (c *Client) alibabaUtilization(ctx context.Context, region, instance string, window provider.MetricWindow) (provider.InstanceUtilization, error) {
	result := newUtilization(aliUtilizationPeriod)
	for from := window.From; from.Before(window.Until); {
		until := from.Add(aliUtilizationChunk)
		if until.After(window.Until) {
			until = window.Until
		}
		batch := provider.MetricWindow{From: from, Until: until}
		offset := int(from.Sub(window.From) / aliUtilizationPeriod)
		if err := c.alibabaUtilizationChunk(ctx, region, instance, batch, result.CPU[offset:], result.Network[offset:]); err != nil {
			return provider.InstanceUtilization{}, err
		}
		from = until
	}
	return result, nil
}

func (c *Client) alibabaUtilizationChunk(ctx context.Context, region, instance string, window provider.MetricWindow, cpu, network []provider.MetricValue) error {
	query := url.Values{"RegionId": {region}, "InstanceId": {instance}, "Period": {"600"},
		"StartTime": {window.From.UTC().Format(time.RFC3339)}, "EndTime": {window.Until.UTC().Format(time.RFC3339)}}
	var response struct {
		Code        string
		MonitorData struct{ InstanceMonitorData []alibabaUtilizationRow }
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeInstanceMonitorData", "2014-05-26", query, &response); err != nil {
		return err
	}
	if response.Code != "" {
		return cloudFailure(response.Code)
	}
	grid := newMetricGrid(window, aliUtilizationPeriod)
	rows := response.MonitorData.InstanceMonitorData
	if rows == nil || len(rows) > len(grid.seen) {
		return invalidUtilization()
	}
	for _, row := range rows {
		stamp, err := time.Parse(time.RFC3339Nano, row.TimeStamp)
		if err != nil || row.InstanceID != instance {
			return invalidUtilization()
		}
		index, ok := grid.position(stamp)
		if !ok {
			return invalidUtilization()
		}
		load, err := recordMetric(row.CPU, 1, 100)
		if err != nil {
			return err
		}
		rate, err := row.network()
		if err != nil {
			return err
		}
		if index < len(grid.seen)-1 {
			cpu[index], network[index] = load, rate
		}
	}
	return nil
}
