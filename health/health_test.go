package health

import (
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
