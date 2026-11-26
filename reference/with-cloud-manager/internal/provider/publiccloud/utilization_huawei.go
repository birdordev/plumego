package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

type huaweiMetricDimension struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type huaweiMetricID struct {
	Namespace  string                  `json:"namespace"`
	Name       string                  `json:"metric_name"`
	Dimensions []huaweiMetricDimension `json:"dimensions"`
}

func (c *Client) huaweiUtilization(ctx context.Context, region, instance string, window provider.MetricWindow) (provider.InstanceUtilization, error) {
	dimension := huaweiMetricDimension{"instance_id", instance}
	names := []string{"cpu_util", "network_vm_bandwidth_in", "network_vm_bandwidth_out"}
	input := struct {
		Metrics []huaweiMetricID `json:"metrics"`
		Period  string           `json:"period"`
		Filter  string           `json:"filter"`
		From    int64            `json:"from"`
		To      int64            `json:"to"`
	}{Period: "3600", Filter: "max", From: window.From.UnixMilli(), To: window.Until.UnixMilli()}
	for _, name := range names {
		input.Metrics = append(input.Metrics, huaweiMetricID{"SYS.ECS", name, []huaweiMetricDimension{dimension}})
	}
	body, err := json.Marshal(input)
	if err != nil {
		return provider.InstanceUtilization{}, err
	}
	endpoint := url.URL{Scheme: "https", Host: "ces." + region + ".myhuaweicloud.com",
		Path: "/V1.0/" + c.profile.ProjectID + "/batch-query-metric-data"}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return provider.InstanceUtilization{}, invalidUtilization()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var response struct {
		Metrics []struct {
			huaweiMetricID
			Unit       string `json:"unit"`
			Datapoints []struct {
				Timestamp int64    `json:"timestamp"`
				Max       *float64 `json:"max"`
			} `json:"datapoints"`
		} `json:"metrics"`
	}
	if err := c.request(req, &response); err != nil {
		return provider.InstanceUtilization{}, err
	}
	if response.Metrics == nil || len(response.Metrics) > len(names) {
		return provider.InstanceUtilization{}, invalidUtilization()
	}
	result := newUtilization(time.Hour)
	inbound, outbound := make([]provider.MetricValue, len(result.Network)), make([]provider.MetricValue, len(result.Network))
	metrics := map[string][]provider.MetricValue{"cpu_util": result.CPU, "network_vm_bandwidth_in": inbound, "network_vm_bandwidth_out": outbound}
	for _, series := range response.Metrics {
		dest, allowed := metrics[series.Name]
		if !allowed || series.Namespace != "SYS.ECS" || len(series.Dimensions) != 1 ||
			series.Dimensions[0] != dimension || len(series.Datapoints) > len(dest)+1 {
			return provider.InstanceUtilization{}, invalidUtilization()
		}
		delete(metrics, series.Name)
		unit, maxValue := "Byte/s", float64(provider.MaxNetworkBytesPerSecond)
		if series.Name == "cpu_util" {
			unit, maxValue = "%", 100
		}
		if len(series.Datapoints) != 0 && !strings.EqualFold(series.Unit, unit) {
			return provider.InstanceUtilization{}, invalidUtilization()
		}
		grid := newMetricGrid(window, time.Hour)
		for _, p := range series.Datapoints {
			index, ok := grid.position(time.UnixMilli(p.Timestamp))
			value, err := recordMetric(p.Max, 1, maxValue)
			if !ok || err != nil {
				return provider.InstanceUtilization{}, invalidUtilization()
			}
			if index < len(dest) {
				dest[index] = value
			}
		}
	}
	sumAvailableMetrics([][]provider.MetricValue{inbound, outbound}, result.Network)
	return result, nil
}
