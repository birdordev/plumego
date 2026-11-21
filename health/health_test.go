package health

import (
	"maps"
	"testing"
	"time"
)

func TestHealthState_IsReady(t *testing.T) {
	tests := []struct {
		name  string
		state HealthState
		want  bool
	}{
		{name: "healthy", state: StatusHealthy, want: true},
		{name: "degraded", state: StatusDegraded, want: true},
		{name: "unhealthy", state: StatusUnhealthy, want: false},
		{name: "unknown", state: "unknown", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state.IsReady(); got != tc.want {
				t.Errorf("HealthState(%q).IsReady() = %v, want %v", tc.state, got, tc.want)
			}
		})
	}
}

func TestHealthState_IsKnown(t *testing.T) {
	tests := []struct {
		name  string
		state HealthState
		want  bool
	}{
		{name: "healthy", state: StatusHealthy, want: true},
		{name: "degraded", state: StatusDegraded, want: true},
		{name: "unhealthy", state: StatusUnhealthy, want: true},
		{name: "unknown", state: "unknown", want: false},
		{name: "empty", state: "", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state.IsKnown(); got != tc.want {
				t.Errorf("HealthState(%q).IsKnown() = %v, want %v", tc.state, got, tc.want)
			}
		})
	}
}

func TestHealthStatusCloneDetachesMutableFields(t *testing.T) {
	original := HealthStatus{
		Status:       StatusDegraded,
		Message:      "cache disabled",
		Details:      map[string]any{"enabled": false},
		Timestamp:    time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC),
		Duration:     1500 * time.Millisecond,
		Dependencies: []string{"db", "cache"},
	}

	clone := original.Clone()
	clone.Details["enabled"] = true
	clone.Dependencies[0] = "queue"

	if got := original.Details["enabled"]; got != false {
		t.Fatalf("original details mutated to %v", got)
	}
	if got := original.Dependencies[0]; got != "db" {
		t.Fatalf("original dependency mutated to %q", got)
	}
	if clone.Status != original.Status || clone.Message != original.Message || clone.Timestamp != original.Timestamp || clone.Duration != original.Duration {
		t.Fatalf("clone lost scalar fields: got %+v, want %+v", clone, original)
	}
}

func TestHealthStatusClonePreservesNilFields(t *testing.T) {
	var original HealthStatus

	clone := original.Clone()

	if clone.Details != nil {
		t.Fatalf("Details = %#v, want nil", clone.Details)
	}
	if clone.Dependencies != nil {
		t.Fatalf("Dependencies = %#v, want nil", clone.Dependencies)
	}
}

func TestComponentHealthCloneDetachesEmbeddedStatus(t *testing.T) {
	original := ComponentHealth{
		HealthStatus: HealthStatus{
			Status:       StatusHealthy,
			Details:      map[string]any{"enabled": true},
			Dependencies: []string{"db"},
		},
		Enabled: true,
	}

	clone := original.Clone()
	clone.Details["enabled"] = false
	clone.Dependencies[0] = "cache"
	clone.Enabled = false

	if got := original.Details["enabled"]; got != true {
		t.Fatalf("original details mutated to %v", got)
	}
	if got := original.Dependencies[0]; got != "db" {
		t.Fatalf("original dependency mutated to %q", got)
	}
	if !original.Enabled {
		t.Fatalf("original enabled mutated to false")
	}
}

func TestReadinessStatusCloneDetachesComponents(t *testing.T) {
	original := ReadinessStatus{
		Ready:      false,
		Reason:     "components failed",
		Timestamp:  time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC),
		Components: map[string]bool{"db": true, "cache": false},
	}

	clone := original.Clone()
	clone.Components["cache"] = true

	if got := original.Components["cache"]; got {
		t.Fatalf("original component readiness mutated to %v", got)
	}
	if clone.Ready != original.Ready || clone.Reason != original.Reason || clone.Timestamp != original.Timestamp {
		t.Fatalf("clone lost scalar fields: got %+v, want %+v", clone, original)
	}
}

func TestReadinessStatusClonePreservesNilComponents(t *testing.T) {
	var original ReadinessStatus

	clone := original.Clone()

	if clone.Components != nil {
		t.Fatalf("Components = %#v, want nil", clone.Components)
	}
}

func TestHealthStatusClonePreservesEmptyDependencies(t *testing.T) {
	status := HealthStatus{Dependencies: []string{}}
	if dependencies := status.Clone().Dependencies; dependencies == nil || len(dependencies) != 0 {
		t.Fatalf("dependencies = %#v, want non-nil empty slice", dependencies)
	}
}

func TestClonePreservesEmptyMapsAndSharesNestedDetails(t *testing.T) {
	details := (HealthStatus{Details: map[string]any{}}).Clone().Details
	components := (ReadinessStatus{Components: map[string]bool{}}).Clone().Components
	if details == nil || len(details) != 0 || components == nil || len(components) != 0 {
		t.Fatalf("cloned Details = %#v, Components = %#v, want non-nil empty maps", details, components)
	}

	nested := map[string]string{"state": "before"}
	original := HealthStatus{Details: map[string]any{"nested": nested}}
	clone := original.Clone()
	clone.Details["nested"].(map[string]string)["state"] = "after"
	delete(clone.Details, "nested")
	if nested["state"] != "after" || original.Details["nested"] == nil {
		t.Fatalf("nested details should be shared and top-level map detached: %v", original.Details)
	}
}

func TestHealthStatusCloneKeepsTimestampAndSeparatesDependencies(t *testing.T) {
	stamp := time.Date(2026, 1, 3, 7, 12, 0, 0, time.FixedZone("fixture", 1800))
	nested := map[string]int{"value": 1}
	original := HealthStatus{Timestamp: stamp, Status: StatusDegraded, Dependencies: []string{"primary"}, Details: map[string]any{"shared": nested}}
	cloned := original.Clone()
	cloned.Dependencies[0] = "changed"
	cloned.Details["local"] = true
	nested["value"] = 2
	if original.Dependencies[0] != "primary" || original.Details["local"] != nil || cloned.Timestamp != stamp ||
		cloned.Status != StatusDegraded || cloned.Details["shared"].(map[string]int)["value"] != 2 {
		t.Fatalf("original=%+v clone=%+v", original, cloned)
	}
}

func TestHealthClonesKeepIndependentTopLevelAndSharedNestedValues(t *testing.T) {
	type nestedDetails map[string]string
	nested := nestedDetails{"value": "before"}
	status := HealthStatus{Details: map[string]any{"plain": "before", "nested": nested}}
	before := status.Clone()
	status.Details["plain"] = "after"
	nested["value"] = "after"
	after := status.Clone()
	before.Details["local"] = true
	if before.Details["plain"] != "before" || after.Details["plain"] != "after" || after.Details["local"] != nil {
		t.Fatalf("before=%v after=%v", before.Details, after.Details)
	}
	if before.Details["nested"].(nestedDetails)["value"] != "after" ||
		after.Details["nested"].(nestedDetails)["value"] != "after" {
		t.Fatal("cloning changed nested ownership")
	}
	before.Details["nested"].(nestedDetails)["value"] = "changed through clone"
	if status.Details["nested"].(nestedDetails)["value"] != "changed through clone" || marshalObject(t, before)["details"] == nil {
		t.Fatalf("unexpected nested shape: %v", status.Details)
	}
}

func TestReadinessClonesKeepNilDistinctFromWritableEmptyMap(t *testing.T) {
	for _, source := range []map[string]bool{nil, {}, {"fixture": false}} {
		before := ReadinessStatus{Components: source}
		one, two := before.Clone(), before.Clone()
		if (one.Components == nil) != (source == nil) || !maps.Equal(two.Components, source) {
			t.Fatalf("clones=%v %v", one.Components, two.Components)
		}
		if one.Components != nil {
			one.Components["fixture"] = true
			if two.Components["fixture"] || source["fixture"] {
				t.Fatal("readiness maps shared")
			}
		}
		if fields := marshalObject(t, two); (fields["components"] == nil) != (len(source) == 0) {
			t.Fatalf("json=%v", fields)
		}
	}
}

func TestComponentCloneKeepsDependencyAppendAndEnabledIndependent(t *testing.T) {
	deps := make([]string, 1, 3)
	deps[0] = "fixture"
	source := ComponentHealth{HealthStatus: HealthStatus{Status: StatusHealthy, Dependencies: deps}, Enabled: true}
	a, b := source.Clone(), source.Clone()
	a.Dependencies = append(a.Dependencies, "local")
	a.Dependencies[0] = "changed"
	a.Enabled = false
	if len(source.Dependencies) != 1 || source.Dependencies[0] != "fixture" || deps[:2][1] != "" ||
		len(b.Dependencies) != 1 || b.Dependencies[0] != "fixture" || !b.Enabled || b.Status != StatusHealthy {
		t.Fatalf("source=%+v first=%+v second=%+v", source, a, b)
	}
}

func TestReadinessEmptyReasonOmittedRegardlessOfReady(t *testing.T) {
	for _, ready := range []bool{false, true} {
		cloned := (ReadinessStatus{Ready: ready}).Clone()
		fields := marshalObject(t, cloned)
		assertFieldAbsent(t, fields, "reason")
		if got := fields["ready"]; got != ready || cloned.Reason != "" {
			t.Fatalf("fields=%v clone=%+v", fields, cloned)
		}
	}
}

func TestHealthCloneDependenciesDoNotReuseSpareCapacity(t *testing.T) {
	storage := []string{"primary", "spare"}
	original := HealthStatus{Dependencies: storage[:1]}
	detached := original.Clone()
	detached.Dependencies[0] = "updated"
	detached.Dependencies = append(detached.Dependencies, "new")
	if storage[0] != "primary" || storage[1] != "spare" || len(original.Dependencies) != 1 {
		t.Fatalf("storage=%v original=%+v", storage, original)
	}
}
