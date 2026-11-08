package devserver

import "github.com/birdordev/plumego/x/observability/devtools"

// RequestAlertThresholds defines default alert thresholds.
type RequestAlertThresholds struct {
	TotalP95MS     float64 `json:"total_p95_ms"`
	TotalP99MS     float64 `json:"total_p99_ms"`
	TotalErrorRate float64 `json:"total_error_rate_pct"`
	RouteP95MS     float64 `json:"route_p95_ms"`
	RouteErrorRate float64 `json:"route_error_rate_pct"`
	MinTotalCount  int64   `json:"min_total_count"`
	MinRouteCount  int64   `json:"min_route_count"`
}

// RequestAlert captures a bottleneck alert.
type RequestAlert struct {
	Level     string  `json:"level"`
	Code      string  `json:"code"`
	Message   string  `json:"message"`
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Method    string  `json:"method,omitempty"`
	Path      string  `json:"path,omitempty"`
}

func defaultAlertThresholds() RequestAlertThresholds {
	return RequestAlertThresholds{
		TotalP95MS:     500,
		TotalP99MS:     1000,
		TotalErrorRate: 1.0,
		RouteP95MS:     500,
		RouteErrorRate: 2.0,
		MinTotalCount:  20,
		MinRouteCount:  10,
	}
}

func evaluateRequestAlerts(snapshot *devtools.DevHTTPSnapshot) ([]RequestAlert, RequestAlertThresholds) {
	thresholds := defaultAlertThresholds()
	if snapshot == nil {
		return nil, thresholds
	}

	alerts := make([]RequestAlert, 0)
	total := snapshot.Total

	if total.Count >= thresholds.MinTotalCount {
		if total.Duration.P95 > thresholds.TotalP95MS {
			alerts = append(alerts, RequestAlert{
				Level:     "warn",
				Code:      "total_p95_high",
				Message:   "Total P95 latency is above threshold",
				Metric:    "total.p95_ms",
				Value:     total.Duration.P95,
				Threshold: thresholds.TotalP95MS,
			})
		}
		if total.Duration.P99 > thresholds.TotalP99MS {
			alerts = append(alerts, RequestAlert{
				Level:     "error",
				Code:      "total_p99_high",
				Message:   "Total P99 latency is above threshold",
				Metric:    "total.p99_ms",
				Value:     total.Duration.P99,
				Threshold: thresholds.TotalP99MS,
			})
		}

		totalErrorRate := percent(total.ErrorCount, total.Count)
		if totalErrorRate > thresholds.TotalErrorRate {
			alerts = append(alerts, RequestAlert{
				Level:     "error",
				Code:      "total_error_rate_high",
				Message:   "Total error rate is above threshold",
				Metric:    "total.error_rate_pct",
				Value:     totalErrorRate,
				Threshold: thresholds.TotalErrorRate,
			})
		}
	}

	for _, route := range snapshot.Routes {
		if route.Count < thresholds.MinRouteCount {
			continue
		}

		if route.Duration.P95 > thresholds.RouteP95MS {
			alerts = append(alerts, RequestAlert{
				Level:     "warn",
				Code:      "route_p95_high",
				Message:   "Route P95 latency is above threshold",
				Metric:    "route.p95_ms",
				Value:     route.Duration.P95,
				Threshold: thresholds.RouteP95MS,
				Method:    route.Method,
				Path:      route.Path,
			})
		}

		routeErrorRate := percent(route.ErrorCount, route.Count)
		if routeErrorRate > thresholds.RouteErrorRate {
			alerts = append(alerts, RequestAlert{
				Level:     "warn",
				Code:      "route_error_rate_high",
				Message:   "Route error rate is above threshold",
				Metric:    "route.error_rate_pct",
				Value:     routeErrorRate,
				Threshold: thresholds.RouteErrorRate,
				Method:    route.Method,
				Path:      route.Path,
			})
		}
	}

	return alerts, thresholds
}

func percent(count int64, total int64) float64 {
	if total == 0 {
		return 0
	}
	return (float64(count) / float64(total)) * 100
}
