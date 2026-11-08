package healthhttp

import (
	"context"
	"github.com/spcent/plumego/health"
	"testing"
)

type manualGenerationChecker struct{ entered, release chan struct{} }

func (c manualGenerationChecker) Name() string { return "database" }
func (c manualGenerationChecker) Check(context.Context) error {
	close(c.entered)
	<-c.release
	return nil
}

func TestManualReadinessSupersedesAggregate(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "draining", true: "ready"}[ready], func(t *testing.T) {
			m, err := NewManager(Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			c := manualGenerationChecker{make(chan struct{}), make(chan struct{})}
			if err := m.RegisterComponent(c); err != nil {
				t.Fatal(err)
			}
			done := make(chan health.HealthStatus, 1)
			go func() { done <- m.CheckAllComponents(context.Background()) }()
			<-c.entered
			if ready {
				m.MarkReady()
			} else {
				m.MarkNotReady("draining")
			}
			before := m.Readiness()
			close(c.release)
			result := <-done
			after := m.Readiness()
			if result.Status != health.StatusUnhealthy || after.Ready != before.Ready || after.Reason != before.Reason || !after.Timestamp.Equal(before.Timestamp) {
				t.Fatalf("old aggregate overwrote manual state: before=%+v after=%+v result=%+v", before, after, result)
			}
			// A newer explicit check may update readiness after the manual decision.
			if err := m.UnregisterComponent("database"); err != nil {
				t.Fatal(err)
			}
			m.CheckAllComponents(context.Background())
			if !m.Readiness().Ready {
				t.Fatal("new aggregate should be permitted to publish")
			}
		})
	}
}

func TestManualUpdatesAfterClose(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	before := m.Readiness()
	m.MarkReady()
	m.MarkNotReady("override")
	after := m.Readiness()
	if after.Ready || after.Reason != before.Reason || !after.Timestamp.Equal(before.Timestamp) {
		t.Fatalf("manual update changed closed state: %+v", after)
	}
}
