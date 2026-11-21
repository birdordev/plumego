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

func TestComponentEnabledAndStatusHaveIndependentWireValues(t *testing.T) {
	for _, original := range []ComponentHealth{
		{Enabled: false, HealthStatus: HealthStatus{Status: StatusHealthy}},
		{Enabled: true, HealthStatus: HealthStatus{Status: "unknown"}},
	} {
		clone := original.Clone()
		fields := marshalObject(t, clone)
		assertStringField(t, fields, "status", string(original.Status))
		if got := fields["enabled"]; got != original.Enabled || clone.Enabled != original.Enabled ||
			clone.Status != original.Status {
			t.Fatalf("component = %+v, wire enabled=%v", clone, got)
		}
	}
}

func TestReadinessClonePreservesTimestampOffsetAndReason(t *testing.T) {
	at := time.Date(2026, 3, 4, 7, 8, 9, 0, time.FixedZone("fixture", 5*3600+1800))
	original := ReadinessStatus{Timestamp: at, Reason: "warming", Components: map[string]bool{"db": true}}
	clone := original.Clone()
	clone.Components["db"] = false
	if clone.Timestamp != at || !original.Components["db"] {
		t.Fatalf("source=%+v clone=%+v", original, clone)
	}
	fields := marshalObject(t, clone)
	assertStringField(t, fields, "timestamp", "2026-03-04T07:08:09+05:30")
	assertStringField(t, fields, "reason", "warming")
}

func TestDetailsWireShapeDistinguishesMissingMapAndNullMembers(t *testing.T) {
	for _, input := range []map[string]any{nil, {}} {
		fields := marshalObject(t, (HealthStatus{Details: input}).Clone())
		assertFieldAbsent(t, fields, "details")
	}
	withValues := (HealthStatus{Details: map[string]any{"null": nil, "zero": 0}}).Clone()
	result, ok := marshalObject(t, withValues)["details"].(map[string]any)
	if !ok {
		t.Fatal("missing details object")
	}
	if n, ok := result["null"]; !ok || n != nil {
		t.Fatalf("null member=%v", result)
	}
	assertNumberField(t, result, "zero", 0)
}

func TestReadinessZeroValueHasExplicitTimeAndState(t *testing.T) {
	for _, status := range []ReadinessStatus{{}, {Ready: false, Reason: "waiting"}} {
		fields := marshalObject(t, status)
		if fields["timestamp"] != "0001-01-01T00:00:00Z" ||
			fields["ready"] != false || fields["components"] != nil {
			t.Fatalf("zero timestamp fields=%v", fields)
		}
		if status.Reason == "" && fields["reason"] != nil {
			t.Fatalf("empty reason=%v", fields["reason"])
		}
		if status.Reason != "" && fields["reason"] != "waiting" {
			t.Fatalf("explicit reason=%v", fields["reason"])
		}
	}
}

func TestComponentHealthJSONEmbedsStateAndExplicitEnabled(t *testing.T) {
	for _, c := range []ComponentHealth{
		{}, {HealthStatus: HealthStatus{Status: StatusHealthy, Dependencies: []string{}}, Enabled: true},
		{HealthStatus: HealthStatus{Status: StatusUnhealthy, Dependencies: []string{"fixture"}}},
	} {
		fields := marshalObject(t, c)
		if fields["enabled"] != c.Enabled || fields["status"] != string(c.Status) || fields["timestamp"] == nil ||
			(fields["dependencies"] == nil) != (len(c.Dependencies) == 0) {
			t.Fatalf("model=%+v fields=%v", c, fields)
		}
	}
}

func TestStatusModelPreservesUnknownAndDetailsWhileRejectingReadiness(t *testing.T) {
	for _, state := range []HealthState{"fixture-unknown", "", StatusDegraded} {
		value := HealthStatus{Status: state, Details: map[string]any{"fixture": "value"}}
		clone := value.Clone()
		fields := marshalObject(t, clone)
		if fields["status"] != string(state) || clone.Status.IsKnown() != (state == StatusDegraded) ||
			clone.Status.IsReady() != (state == StatusDegraded) || fields["details"] == nil {
			t.Fatalf("state=%s fields=%v", state, fields)
		}
	}
}

func TestHealthDetailsExplicitNullSurvivesCloneAndEncoding(t *testing.T) {
	for _, details := range []map[string]any{nil, {}, {"fixture": nil}} {
		cloned := (HealthStatus{Status: StatusHealthy, Details: details}).Clone()
		fields := marshalObject(t, cloned)
		if len(details) == 0 {
			assertFieldAbsent(t, fields, "details")
		} else {
			if got, exists := fields["details"].(map[string]any)["fixture"]; !exists || got != nil {
				t.Fatalf("details=%v", fields["details"])
			}
		}
	}
}

func TestComponentDurationCloneJSONRoundTrip(t *testing.T) {
	for _, duration := range []time.Duration{0, time.Nanosecond} {
		original := ComponentHealth{Enabled: true, HealthStatus: HealthStatus{Status: StatusHealthy, Duration: duration}}
		cloned := original.Clone()
		fields := marshalObject(t, cloned)
		if duration == 0 {
			assertFieldAbsent(t, fields, "duration")
		} else {
			assertNumberField(t, fields, "duration", 1)
		}
		buf, err := json.Marshal(cloned)
		if err != nil {
			t.Fatal(err)
		}
		var decoded ComponentHealth
		if err := json.Unmarshal(buf, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Duration != duration || decoded.Status != original.Status || !decoded.Enabled {
			t.Fatalf("round trip: %+v", decoded)
		}
	}
}

func TestHealthTimestampWithOffsetRoundTrips(t *testing.T) {
	at := time.Date(2026, time.November, 21, 13, 5, 4, 123, time.FixedZone("fixture", 5*60*60+30*60))
	original := HealthStatus{Status: StatusHealthy, Timestamp: at}
	assertStringField(t, marshalObject(t, original), "timestamp", "2026-11-21T13:05:04.000000123+05:30")
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded HealthStatus
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	_, offset := decoded.Timestamp.Zone()
	if !decoded.Timestamp.Equal(at) || offset != 19800 || decoded.Status != original.Status {
		t.Fatalf("decoded=%+v offset=%d", decoded, offset)
	}
}

func TestFalseReadinessComponentSurvivesJSONRoundTrip(t *testing.T) {
	original := ReadinessStatus{Ready: true, Components: map[string]bool{"available": true, "warming": false}}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ReadinessStatus
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if warming, exists := decoded.Components["warming"]; !exists || warming || !decoded.Ready || !decoded.Components["available"] {
		t.Fatalf("decoded=%+v", decoded)
	}
}

func TestUnknownHealthStateAndDependenciesRoundTrip(t *testing.T) {
	original := HealthStatus{Status: "fixture unknown", Dependencies: []string{"store", "queue"}}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var got HealthStatus
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != original.Status || got.Status.IsReady() || got.Status.IsKnown() || len(got.Dependencies) != 2 ||
		got.Dependencies[0] != "store" || got.Dependencies[1] != "queue" {
		t.Fatalf("decoded=%+v", got)
	}
}
