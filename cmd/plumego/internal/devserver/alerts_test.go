package devserver

import (
	"testing"

	"github.com/birdordev/plumego/x/observability/devtools"
	windowmetrics "github.com/birdordev/plumego/x/observability/windowmetrics"
)

func TestEvaluateRequestAlerts(t *testing.T) {
	snapshot := &devtools.DevHTTPSnapshot{
		Total: devtools.DevHTTPSeries{
			Count:      100,
			ErrorCount: 5,
			Duration: windowmetrics.AggregatorStats{
				P95: 600,
				P99: 1500,
			},
		},
		Routes: []devtools.DevHTTPSeries{
			{
				Method:     "GET",
				Path:       "/slow",
				Count:      20,
				ErrorCount: 2,
				Duration: windowmetrics.AggregatorStats{
					P95: 800,
				},
			},
		},
	}

	alerts, thresholds := evaluateRequestAlerts(snapshot)
	if len(alerts) == 0 {
		t.Fatal("expected alerts, got none")
	}
	if thresholds.TotalP95MS <= 0 || thresholds.RouteP95MS <= 0 {
		t.Fatalf("expected thresholds to be set, got %+v", thresholds)
	}
}
