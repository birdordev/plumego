package messaging

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/metrics"
	"github.com/birdordev/plumego/x/observability/recordbuffer"
	testmetrics "github.com/birdordev/plumego/x/observability/testmetrics"
)

func TestReporterRecords(t *testing.T) {
	collector := testmetrics.NewMockCollector()
	reporter := NewSMSMetricsReporter(collector)

	ctx := t.Context()
	reporter.RecordQueueDepth(ctx, "send", SMSQueueStats{Queued: 2, Leased: 1, Dead: 0, Expired: 0})
	reporter.RecordSendLatency(ctx, "tenant-1", "provider-a", 120*time.Millisecond, nil)
	reporter.RecordProviderResult(ctx, "tenant-1", "provider-a", true)
	reporter.RecordRetry(ctx, "tenant-1", "provider-a", 2)
	reporter.RecordStatus(ctx, "tenant-1", messageStatusSent)

	records := collector.GetRecords()
	if len(records) == 0 {
		t.Fatalf("expected records")
	}

	found := false
	for _, rec := range records {
		if rec.Name == MetricSendLatency {
			found = true
			if rec.Labels[LabelProvider] != "provider-a" {
				t.Fatalf("unexpected provider label: %v", rec.Labels)
			}
		}
	}
	if !found {
		t.Fatalf("missing send latency record")
	}
}

func TestSMSPrometheusExporterWritesMetrics(t *testing.T) {
	collector := recordbuffer.NewCollector()
	reporter := NewSMSMetricsReporter(collector)

	ctx := t.Context()
	reporter.RecordQueueDepth(ctx, "send", SMSQueueStats{Queued: 3})
	reporter.RecordSendLatency(ctx, "tenant-1", "provider-a", 150*time.Millisecond, nil)
	reporter.RecordProviderResult(ctx, "tenant-1", "provider-a", true)
	reporter.RecordRetry(ctx, "tenant-1", "provider-a", 2)
	reporter.RecordStatus(ctx, "tenant-1", messageStatusSent)
	reporter.RecordReceiptDelay(ctx, "tenant-1", "provider-a", 2*time.Second)

	exporter, err := NewSMSPrometheusExporter("plumego_test", collector)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics/sms", nil)
	exporter.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	assertMetricsBodyContains(t, body,
		"plumego_test_sms_gateway_queue_depth",
		"plumego_test_sms_gateway_send_latency_seconds_sum",
		"plumego_test_sms_gateway_provider_result_total",
		"plumego_test_sms_gateway_receipt_delay_seconds_sum",
	)
}

func TestSMSPrometheusExporterUsesValueWhenDurationMissing(t *testing.T) {
	collector := recordbuffer.NewCollector()
	collector.Record(t.Context(), metrics.MetricRecord{
		Name:  MetricSendLatency,
		Value: 0.125,
		Labels: metrics.MetricLabels{
			LabelTenant:   "tenant-1",
			LabelProvider: "provider-a",
		},
	})

	exporter, err := NewSMSPrometheusExporter("plumego_test", collector)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics/sms", nil)
	exporter.Handler().ServeHTTP(rec, req)

	assertMetricsBodyContains(t, rec.Body.String(), `plumego_test_sms_gateway_send_latency_seconds_sum{tenant="tenant-1",provider="provider-a"} 0.125000000`)
}

func TestNewSMSPrometheusExporter_NilSource_ReturnsError(t *testing.T) {
	exporter, err := NewSMSPrometheusExporter("plumego_test", nil)
	if !errors.Is(err, ErrNilMetricRecordSource) {
		t.Fatalf("error = %v, want %v", err, ErrNilMetricRecordSource)
	}
	if exporter != nil {
		t.Fatalf("exporter = %v, want nil", exporter)
	}
}

func TestNewSMSPrometheusExporter_ConstructsExporter(t *testing.T) {
	collector := recordbuffer.NewCollector()
	exporter, err := NewSMSPrometheusExporter("", collector)
	if err != nil {
		t.Fatalf("NewSMSPrometheusExporter returned error: %v", err)
	}
	if exporter == nil {
		t.Fatalf("expected exporter")
	}
	if exporter.namespace != "plumego" {
		t.Fatalf("namespace = %q, want plumego", exporter.namespace)
	}
}

func assertMetricsBodyContains(t *testing.T, body string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q\n%s", want, body)
		}
	}
}
