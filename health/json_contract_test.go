package health

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHealthStatusJSONShape(t *testing.T) {
	timestamp := time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC)
	status := HealthStatus{
		Status:       StatusDegraded,
		Message:      "cache disabled",
		Details:      map[string]any{"enabled": false},
		Timestamp:    timestamp,
		Duration:     1500 * time.Millisecond,
		Dependencies: []string{"db", "cache"},
	}

	fields := marshalObject(t, status)

	assertStringField(t, fields, "status", string(StatusDegraded))
	assertStringField(t, fields, "message", "cache disabled")
	assertStringField(t, fields, "timestamp", timestamp.Format(time.RFC3339))
	assertNumberField(t, fields, "duration", float64((1500 * time.Millisecond).Nanoseconds()))
	assertStringSliceField(t, fields, "dependencies", []string{"db", "cache"})

	details, ok := fields["details"].(map[string]any)
	if !ok {
		t.Fatalf("details = %T, want object", fields["details"])
	}
	if got, ok := details["enabled"].(bool); !ok || got {
		t.Fatalf("details.enabled = %v, want false", details["enabled"])
	}
}

func TestHealthStatusJSONOmitEmptyFields(t *testing.T) {
	status := HealthStatus{
		Status:    StatusHealthy,
		Timestamp: time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC),
	}

	fields := marshalObject(t, status)

	assertFieldAbsent(t, fields, "message")
	assertFieldAbsent(t, fields, "details")
	assertFieldAbsent(t, fields, "duration")
	assertFieldAbsent(t, fields, "dependencies")
}

func TestComponentHealthJSONFlattensEmbeddedStatus(t *testing.T) {
	component := ComponentHealth{
		HealthStatus: HealthStatus{
			Status:    StatusHealthy,
			Timestamp: time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC),
		},
		Enabled: true,
	}

	fields := marshalObject(t, component)

	assertStringField(t, fields, "status", string(StatusHealthy))
	if got, ok := fields["enabled"].(bool); !ok || !got {
		t.Fatalf("enabled = %v, want true", fields["enabled"])
	}
	assertFieldAbsent(t, fields, "HealthStatus")
}

func TestReadinessStatusJSONShape(t *testing.T) {
	timestamp := time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC)
	readiness := ReadinessStatus{
		Ready:      false,
		Reason:     "components failed",
		Timestamp:  timestamp,
		Components: map[string]bool{"db": true, "cache": false},
	}

	fields := marshalObject(t, readiness)

	if got, ok := fields["ready"].(bool); !ok || got {
		t.Fatalf("ready = %v, want false", fields["ready"])
	}
	assertStringField(t, fields, "reason", "components failed")
	assertStringField(t, fields, "timestamp", timestamp.Format(time.RFC3339))

	components, ok := fields["components"].(map[string]any)
	if !ok {
		t.Fatalf("components = %T, want object", fields["components"])
	}
	if got, ok := components["db"].(bool); !ok || !got {
		t.Fatalf("components.db = %v, want true", components["db"])
	}
	if got, ok := components["cache"].(bool); !ok || got {
		t.Fatalf("components.cache = %v, want false", components["cache"])
	}
}

func TestReadinessStatusJSONOmitEmptyFields(t *testing.T) {
	readiness := ReadinessStatus{
		Ready:     true,
		Timestamp: time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC),
	}

	fields := marshalObject(t, readiness)

	assertFieldAbsent(t, fields, "reason")
	assertFieldAbsent(t, fields, "components")
}

func marshalObject(t *testing.T, v any) map[string]any {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return fields
}

func assertStringField(t *testing.T, fields map[string]any, key string, want string) {
	t.Helper()

	got, ok := fields[key].(string)
	if !ok || got != want {
		t.Fatalf("%s = %v, want %q", key, fields[key], want)
	}
}

func assertNumberField(t *testing.T, fields map[string]any, key string, want float64) {
	t.Helper()

	got, ok := fields[key].(float64)
	if !ok || got != want {
		t.Fatalf("%s = %v, want %v", key, fields[key], want)
	}
}

func assertStringSliceField(t *testing.T, fields map[string]any, key string, want []string) {
	t.Helper()

	got, ok := fields[key].([]any)
	if !ok {
		t.Fatalf("%s = %T, want array", key, fields[key])
	}
	if len(got) != len(want) {
		t.Fatalf("%s len = %d, want %d", key, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %v, want %q", key, i, got[i], want[i])
		}
	}
}

func assertFieldAbsent(t *testing.T, fields map[string]any, key string) {
	t.Helper()

	if _, ok := fields[key]; ok {
		t.Fatalf("%s should be omitted", key)
	}
}

func TestHealthStateWireAndReadinessAgree(t *testing.T) {
	for _, tc := range []struct {
		state        HealthState
		known, ready bool
	}{
		{"", false, false},
		{"unknown", false, false},
		{StatusHealthy, true, true},
		{StatusDegraded, true, true},
		{StatusUnhealthy, true, false},
	} {
		fields := marshalObject(t, HealthStatus{Status: tc.state})
		assertStringField(t, fields, "status", string(tc.state))
		if tc.state.IsKnown() != tc.known || tc.state.IsReady() != tc.ready {
			t.Errorf("state %q: known=%t ready=%t", tc.state, tc.state.IsKnown(), tc.state.IsReady())
		}
	}
}

func TestReadinessEmptyComponentsWireAndClone(t *testing.T) {
	for _, components := range []map[string]bool{nil, {}} {
		status := ReadinessStatus{Components: components}
		assertFieldAbsent(t, marshalObject(t, status), "components")
		if cloned := status.Clone().Components; (cloned == nil) != (components == nil) {
			t.Fatalf("Clone Components=%v for input=%v", cloned, components)
		}
	}
	status := ReadinessStatus{Components: map[string]bool{"db": true}}
	cloned := status.Clone()
	cloned.Components["db"] = false
	if !status.Components["db"] {
		t.Fatal("clone changed original status")
	}
}

func TestHealthDurationUsesSignedNanoseconds(t *testing.T) {
	for _, duration := range []time.Duration{0, time.Millisecond, -time.Millisecond} {
		fields := marshalObject(t, HealthStatus{Duration: duration})
		if duration == 0 {
			assertFieldAbsent(t, fields, "duration")
		} else {
			assertNumberField(t, fields, "duration", float64(duration.Nanoseconds()))
		}
	}
}
