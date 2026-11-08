package metrics_test

import (
	"context"
	"fmt"
	"time"

	"github.com/birdordev/plumego/metrics"
)

func ExampleBaseMetricsCollector() {
	collector := metrics.NewBaseMetricsCollector()
	collector.ObserveHTTP(context.Background(), "GET", "/users", 200, 128, 25*time.Millisecond)

	stats := collector.GetStats()
	fmt.Printf("records=%d errors=%d\n", stats.TotalRecords, stats.ErrorRecords)

	// Output:
	// records=1 errors=0
}

func ExampleNewMultiCollector() {
	left := metrics.NewBaseMetricsCollector()
	right := metrics.NewBaseMetricsCollector()
	collector := metrics.NewMultiCollector(left, right)

	collector.ObserveHTTP(context.Background(), "POST", "/batch", 202, 64, 10*time.Millisecond)

	fmt.Printf("left=%d right=%d\n", left.GetStats().TotalRecords, right.GetStats().TotalRecords)

	// Output:
	// left=1 right=1
}

func ExampleNewHTTPRecord() {
	record := metrics.NewHTTPRecord("GET", "/healthz", 503, 15*time.Millisecond)

	fmt.Printf("%s status=%s seconds=%.3f\n", record.Name, record.Labels["status"], record.Value)

	// Output:
	// http_request status=503 seconds=0.015
}
