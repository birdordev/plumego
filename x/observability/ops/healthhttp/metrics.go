package healthhttp

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
)

// MetricsHandler creates a handler that exposes tracked health metrics.
func MetricsHandler(tracker *Tracker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireTracker(tracker, w, r) {
			return
		}
		metrics := tracker.GetMetrics()
		_ = contract.WriteResponse(w, r, http.StatusOK, metrics, nil)
	})
}

// HealthReportHandler creates a handler that exposes comprehensive health report.
func HealthReportHandler(tracker *Tracker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireTracker(tracker, w, r) {
			return
		}
		report := tracker.GenerateReport()
		_ = writeHealthResponse(w, r, httpStatusForHealth(report.HealthStatus.Status), report)
	})
}
