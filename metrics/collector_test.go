package metrics

import (
	"errors"
	"maps"
	"strconv"
	"testing"
	"time"
)

func TestBaseMetricsCollectorRecordTracksTotalsAndBreakdown(t *testing.T) {
	collector := NewBaseMetricsCollector()
	labels := MetricLabels{"route": "/users"}

	collector.Record(t.Context(), MetricRecord{
		Name:   "custom_http_metric",
		Value:  1,
		Labels: labels,
	})

	labels["route"] = "/mutated"

	stats := collector.GetStats()
	if stats.TotalRecords != 1 {
		t.Fatalf("expected one total record, got %d", stats.TotalRecords)
	}
	if stats.NameBreakdown["custom_http_metric"] != 1 {
		t.Fatalf("expected name breakdown to track custom metric")
	}

	collector.Record(t.Context(), MetricRecord{
		Name:   "custom_http_metric",
		Value:  1,
		Labels: labels,
	})

	stats = collector.GetStats()
	if stats.TotalRecords != 2 {
		t.Fatalf("expected two total records, got %d", stats.TotalRecords)
	}
	if stats.NameBreakdown["custom_http_metric"] != 2 {
		t.Fatalf("expected custom metric count 2, got %d", stats.NameBreakdown["custom_http_metric"])
	}
}

func TestBaseMetricsCollectorObserveHTTP(t *testing.T) {
	collector := NewBaseMetricsCollector()
	duration := 125 * time.Millisecond

	collector.ObserveHTTP(t.Context(), "GET", "/test", 200, 100, duration)

	stats := collector.GetStats()
	if stats.TotalRecords != 1 {
		t.Fatalf("expected 1 total record, got %d", stats.TotalRecords)
	}
	if stats.NameBreakdown[MetricHTTPRequest] != 1 {
		t.Fatalf("expected HTTP name breakdown of 1, got %d", stats.NameBreakdown[MetricHTTPRequest])
	}
}

func TestBaseMetricsCollectorObserveHTTPMatchesRecord(t *testing.T) {
	var observed, recorded BaseMetricsCollector
	statuses := []int{200, 399, 400, 404, 500}
	for _, status := range statuses {
		observed.ObserveHTTP(t.Context(), "GET", "/test", status, 100, time.Millisecond)
		recorded.Record(t.Context(), NewHTTPRecord("GET", "/test", status, time.Millisecond))
	}
	got, want := observed.GetStats(), recorded.GetStats()
	if got.TotalRecords != int64(len(statuses)) || got.ErrorRecords != 3 || got.ActiveSeries != 1 || got.StartTime.IsZero() {
		t.Fatalf("unexpected observed stats: %+v", got)
	}
	if got.TotalRecords != want.TotalRecords || got.ErrorRecords != want.ErrorRecords ||
		got.ActiveSeries != want.ActiveSeries || !maps.Equal(got.NameBreakdown, want.NameBreakdown) {
		t.Fatalf("observed stats = %+v, recorded stats = %+v", got, want)
	}
}

func TestNewHTTPRecordUsesCanonicalShape(t *testing.T) {
	duration := 125 * time.Millisecond
	before := time.Now()

	record := NewHTTPRecord("POST", "/submit", 201, duration)
	after := time.Now()

	if record.Name != MetricHTTPRequest {
		t.Fatalf("record name = %q, want %q", record.Name, MetricHTTPRequest)
	}
	if record.Timestamp.Before(before) || record.Timestamp.After(after) {
		t.Fatalf("record timestamp = %v, want within [%v, %v]", record.Timestamp, before, after)
	}
	if record.Value != duration.Seconds() {
		t.Fatalf("record value = %v, want %v", record.Value, duration.Seconds())
	}
	if record.Duration != duration {
		t.Fatalf("record duration = %v, want %v", record.Duration, duration)
	}
	if record.Labels[labelMethod] != "POST" {
		t.Fatalf("method label = %q, want POST", record.Labels[labelMethod])
	}
	if record.Labels[labelPath] != "/submit" {
		t.Fatalf("path label = %q, want /submit", record.Labels[labelPath])
	}
	if record.Labels[labelStatus] != "201" {
		t.Fatalf("status label = %q, want 201", record.Labels[labelStatus])
	}
}

func TestBaseMetricsCollectorRecordDoesNotRequireRecordNormalization(t *testing.T) {
	collector := NewBaseMetricsCollector()

	collector.Record(t.Context(), MetricRecord{Name: "custom_metric"})

	stats := collector.GetStats()
	if stats.TotalRecords != 1 {
		t.Fatalf("expected one total record, got %d", stats.TotalRecords)
	}
	if stats.NameBreakdown["custom_metric"] != 1 {
		t.Fatalf("expected custom metric breakdown to be tracked, got %d", stats.NameBreakdown["custom_metric"])
	}
}

func TestBaseMetricsCollectorClassifiesHTTPStatusErrors(t *testing.T) {
	collector := NewBaseMetricsCollector()

	collector.ObserveHTTP(t.Context(), "GET", "/missing", 404, 100, 50*time.Millisecond)
	collector.Record(t.Context(), MetricRecord{
		Name: MetricHTTPRequest,
		Labels: MetricLabels{
			labelMethod: "POST",
			labelPath:   "/submit",
			labelStatus: "500",
		},
	})
	collector.Record(t.Context(), MetricRecord{
		Name:  "owner_metric_error",
		Error: errors.New("boom"),
	})

	stats := collector.GetStats()
	if stats.TotalRecords != 3 {
		t.Fatalf("expected 3 total records, got %d", stats.TotalRecords)
	}
	if stats.ErrorRecords != 3 {
		t.Fatalf("expected HTTP and explicit errors to be classified, got %d", stats.ErrorRecords)
	}
}

func TestBaseMetricsCollectorIgnoresInvalidHTTPStatusForErrorClassification(t *testing.T) {
	collector := NewBaseMetricsCollector()

	collector.Record(t.Context(), MetricRecord{
		Name: MetricHTTPRequest,
		Labels: MetricLabels{
			labelMethod: "GET",
			labelPath:   "/broken",
			labelStatus: "not-a-status",
		},
	})

	stats := collector.GetStats()
	if stats.ErrorRecords != 0 {
		t.Fatalf("expected invalid HTTP status label to stay non-error, got %d", stats.ErrorRecords)
	}
}

func TestBaseMetricsCollectorClear(t *testing.T) {
	for _, initialized := range []bool{false, true} {
		var collector BaseMetricsCollector
		if initialized {
			collector.ObserveHTTP(t.Context(), "GET", "/test", 200, 100, 50*time.Millisecond)
		}
		beforeClear := time.Now()
		collector.Clear()
		afterClear := time.Now()

		stats := collector.GetStats()
		if stats.TotalRecords != 0 || len(stats.NameBreakdown) != 0 {
			t.Fatalf("initialized=%v: cleared stats = %+v", initialized, stats)
		}
		if stats.StartTime.Before(beforeClear) || stats.StartTime.After(afterClear) {
			t.Fatalf("initialized=%v: start time = %v, want within clear window", initialized, stats.StartTime)
		}
		stats.NameBreakdown["caller"] = 1
		if len(collector.GetStats().NameBreakdown) != 0 {
			t.Fatal("snapshot mutation affected collector")
		}
	}
}

func TestBaseMetricsCollectorStatsCloneNameBreakdown(t *testing.T) {
	collector := NewBaseMetricsCollector()
	collector.Record(t.Context(), MetricRecord{Name: "custom_metric"})

	stats := collector.GetStats()
	stats.NameBreakdown["custom_metric"] = 999

	stats = collector.GetStats()
	if stats.NameBreakdown["custom_metric"] != 1 {
		t.Fatalf("expected collector stats clone to protect internal breakdown, got %d", stats.NameBreakdown["custom_metric"])
	}
}

func TestBaseMetricsCollectorZeroValueStatsShape(t *testing.T) {
	var collector BaseMetricsCollector

	stats := collector.GetStats()
	if stats.NameBreakdown == nil {
		t.Fatalf("expected zero-value base collector stats to include an initialized name breakdown")
	}

	stats.NameBreakdown["caller_owned"] = 1
	stats = collector.GetStats()
	if stats.NameBreakdown["caller_owned"] != 0 {
		t.Fatalf("expected zero-value base collector stats map to be caller-owned")
	}
}

func TestBaseMetricsCollectorNilReceiverNoops(t *testing.T) {
	var collector *BaseMetricsCollector

	collector.Record(t.Context(), MetricRecord{Name: "ignored"})
	collector.ObserveHTTP(t.Context(), "GET", "/ignored", 200, 0, time.Millisecond)
	collector.Clear()

	stats := collector.GetStats()
	if stats.TotalRecords != 0 {
		t.Fatalf("expected nil receiver total records to stay zero, got %d", stats.TotalRecords)
	}
	if stats.NameBreakdown == nil {
		t.Fatalf("expected nil receiver stats to include initialized name breakdown")
	}
}

func TestNewHTTPRecordOwnsLabelsForEachObservation(t *testing.T) {
	first := NewHTTPRecord("GET", "/first", 200, time.Second)
	second := NewHTTPRecord("POST", "/second", 500, 250*time.Millisecond)
	first.Labels[labelStatus] = "modified"
	if second.Labels[labelStatus] != "500" || second.Value != 0.25 || second.Timestamp.IsZero() {
		t.Fatalf("second record = %+v", second)
	}
}

func TestUnnamedRecordsCountWithoutCreatingSeries(t *testing.T) {
	c := NewBaseMetricsCollector()
	for _, record := range []MetricRecord{{}, {Name: "named"}, {Error: errors.New("fixture")}} {
		c.Record(t.Context(), record)
	}
	stats := c.GetStats()
	if stats.TotalRecords != 3 || stats.ErrorRecords != 1 || stats.ActiveSeries != 1 ||
		len(stats.NameBreakdown) != 1 || stats.NameBreakdown["named"] != 1 {
		t.Fatalf("mixed records = %+v", stats)
	}
	stats.NameBreakdown["invented"] = 2
	if c.GetStats().NameBreakdown["invented"] != 0 {
		t.Fatal("caller mutated collector statistics")
	}
	c.Clear()
	if stats := c.GetStats(); stats.TotalRecords != 0 || stats.ErrorRecords != 0 || stats.ActiveSeries != 0 {
		t.Fatalf("cleared records = %+v", stats)
	}
}

func TestResetAtBoundedCardinalityKeepsPriorSnapshotIndependent(t *testing.T) {
	c := NewBaseMetricsCollector()
	for i := range 256 {
		c.Record(t.Context(), MetricRecord{Name: strconv.Itoa(i)})
	}
	before := c.GetStats()
	c.Clear()
	c.Record(t.Context(), MetricRecord{Name: "0"})
	after := c.GetStats()
	if before.ActiveSeries != 256 || before.TotalRecords != 256 || len(before.NameBreakdown) != 256 ||
		after.ActiveSeries != 1 || after.TotalRecords != 1 || after.NameBreakdown["0"] != 1 {
		t.Fatalf("sizes: before=%d/%d after=%d/%d", before.ActiveSeries, before.TotalRecords, after.ActiveSeries, after.TotalRecords)
	}
}

func TestHTTPRecordCombinesStatusAndExplicitErrorOnce(t *testing.T) {
	collector := NewBaseMetricsCollector()
	cause := errors.New("fixture")
	for _, record := range []MetricRecord{
		{Name: MetricHTTPRequest, Labels: MetricLabels{labelStatus: "200"}, Error: cause},
		{Name: MetricHTTPRequest, Labels: MetricLabels{labelStatus: "400"}, Error: cause},
		{Name: MetricHTTPRequest, Labels: MetricLabels{labelStatus: "404"}},
		{Name: MetricHTTPRequest, Labels: MetricLabels{labelStatus: "204"}},
		{Name: "other", Labels: MetricLabels{labelStatus: "400"}},
	} {
		collector.Record(t.Context(), record)
	}
	if stats := collector.GetStats(); stats.TotalRecords != 5 || stats.ErrorRecords != 3 {
		t.Fatalf("record counts=%+v", stats)
	}
}

func TestNamelessRecordsCountWithoutCreatingSeries(t *testing.T) {
	c := NewBaseMetricsCollector()
	c.Record(t.Context(), MetricRecord{})
	c.Record(t.Context(), MetricRecord{Error: errors.New("fixture")})
	c.ObserveHTTP(t.Context(), "GET", "/fixture", 204, 0, time.Second)
	before := c.GetStats()
	if before.TotalRecords != 3 || before.ErrorRecords != 1 || before.ActiveSeries != 1 ||
		len(before.NameBreakdown) != 1 || before.NameBreakdown[MetricHTTPRequest] != 1 {
		t.Fatalf("stats=%+v", before)
	}
	c.Clear()
	if after := c.GetStats(); after.TotalRecords != 0 || after.ErrorRecords != 0 || after.ActiveSeries != 0 ||
		len(after.NameBreakdown) != 0 || after.StartTime.Before(before.StartTime) {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
}

func TestHTTPRecordOwnsLabelsAndRetainsSubsecondDuration(t *testing.T) {
	d := 734567 * time.Nanosecond
	a := NewHTTPRecord("GET", "/sample", 202, d)
	b := NewHTTPRecord("HEAD", "/sample", 204, 0)
	a.Labels[labelPath] = "changed"
	if a.Duration != d || a.Value != d.Seconds() || b.Duration != 0 || b.Value != 0 || len(a.Labels) != 3 || len(b.Labels) != 3 ||
		b.Labels[labelPath] != "/sample" || b.Labels[labelMethod] != "HEAD" || a.Labels[labelStatus] != "202" || b.Labels[labelStatus] != "204" ||
		a.Name != MetricHTTPRequest || b.Name != MetricHTTPRequest || a.Timestamp.IsZero() || b.Timestamp.IsZero() {
		t.Fatalf("first=%+v second=%+v", a, b)
	}
}

func TestBaseHTTPStatusCountersAtSaturation(t *testing.T) {
	c := NewBaseMetricsCollector()
	c.stats.TotalRecords = 1<<63 - 1
	c.stats.NameBreakdown[MetricHTTPRequest] = 1<<63 - 1
	c.ObserveHTTP(t.Context(), "GET", "/", 399, 0, 0)
	if got := c.GetStats().ErrorRecords; got != 0 {
		t.Fatalf("399 errors=%d", got)
	}
	c.ObserveHTTP(t.Context(), "GET", "/", 400, 0, 0)
	s := c.GetStats()
	if s.TotalRecords != 1<<63-1 || s.NameBreakdown[MetricHTTPRequest] != 1<<63-1 || s.ErrorRecords != 1 {
		t.Fatalf("400 stats=%+v", s)
	}
	c.Clear()
	c.ObserveHTTP(t.Context(), "GET", "/", 200, 0, 0)
	if s := c.GetStats(); s.TotalRecords != 1 || s.NameBreakdown[MetricHTTPRequest] != 1 || s.ErrorRecords != 0 {
		t.Fatalf("after clear: %+v", s)
	}
}

func TestNamelessRecordsKeepIndependentErrorCountAtSaturation(t *testing.T) {
	c := NewBaseMetricsCollector()
	c.stats.TotalRecords = 1<<63 - 1
	c.Record(t.Context(), MetricRecord{Error: errors.New("fixture error")})
	c.Record(t.Context(), MetricRecord{})
	got := c.GetStats()
	if got.TotalRecords != 1<<63-1 || got.ErrorRecords != 1 || got.ActiveSeries != 0 || len(got.NameBreakdown) != 0 {
		t.Fatalf("stats=%+v", got)
	}
}
