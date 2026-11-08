package healthhttp

import (
	"context"
	"errors"
	"github.com/spcent/plumego/health"
	"sync/atomic"
	"testing"
)

type aggregateChecker struct {
	calls            atomic.Int32
	entered, release chan struct{}
	newerError       error
}

func (c *aggregateChecker) Name() string { return "db" }
func (c *aggregateChecker) Check(context.Context) error {
	if c.calls.Add(1) == 1 {
		close(c.entered)
		<-c.release
		return nil
	}
	return c.newerError
}
func TestAggregateGeneration(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "newer-success", true: "newer-failure"}[failed], func(t *testing.T) {
			m, err := NewManager(Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			c := &aggregateChecker{entered: make(chan struct{}), release: make(chan struct{})}
			if failed {
				c.newerError = errors.New("unavailable")
			}
			if err = m.RegisterComponent(c); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { m.CheckAllComponents(context.Background()); close(done) }()
			<-c.entered
			newest := m.CheckAllComponents(context.Background())
			before := m.Readiness()
			close(c.release)
			<-done
			after := m.Readiness()
			if newest.Status.IsReady() != !failed || after.Ready != before.Ready || !after.Timestamp.Equal(before.Timestamp) || after.Reason != before.Reason {
				t.Fatalf("new=%s before=%+v after=%+v", newest.Status, before, after)
			}
			if got, _ := m.GetComponentHealth("db"); (got.Status == health.StatusHealthy) != !failed {
				t.Fatalf("component=%+v", got)
			}
		})
	}
}
