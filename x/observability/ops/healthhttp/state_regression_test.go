package healthhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spcent/plumego/health"
)

type functionChecker struct {
	name  string
	check func(context.Context) error
}

func (c functionChecker) Name() string                    { return c.name }
func (c functionChecker) Check(ctx context.Context) error { return c.check(ctx) }

func TestCanceledAndEmptyAggregationStayConsistent(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	m.MarkNotReady("starting")
	if result := m.CheckAllComponents(t.Context()); result.Status != health.StatusHealthy || !m.Readiness().Ready {
		t.Fatal("empty successful aggregation must publish ready state")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result := m.CheckAllComponents(ctx); result.Status.IsReady() || m.Readiness().Ready {
		t.Fatal("canceled aggregation must not publish ready state")
	}
	calls := 0
	if err := m.RegisterComponent(functionChecker{"db", func(context.Context) error { calls++; return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckComponent(ctx, "db"); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("canceled check: error=%v, calls=%d", err, calls)
	}
}

func TestReplacedCheckerCannotPublishStaleSuccess(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	if err := m.RegisterComponent(functionChecker{"db", func(context.Context) error { close(started); <-release; return nil }}); err != nil {
		t.Fatal(err)
	}
	result := make(chan health.HealthStatus, 1)
	go func() { result <- m.CheckAllComponents(t.Context()) }()
	<-started
	if err := m.RegisterComponent(functionChecker{"db", func(context.Context) error { return errors.New("replacement unavailable") }}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if got := <-result; got.Status.IsReady() || m.Readiness().Ready {
		t.Fatal("stale success made an unchecked replacement ready")
	}
	status, _ := m.GetComponentHealth("db")
	if status.Status != health.StatusUnhealthy {
		t.Fatalf("replacement state = %s", status.Status)
	}
}

func TestReadinessSnapshotAndClose(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterComponent(functionChecker{"db", func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	m.CheckAllComponents(t.Context())
	snapshot := m.Readiness()
	snapshot.Components["db"] = false
	if !m.Readiness().Components["db"] {
		t.Fatal("readiness snapshot mutates manager state")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m.MarkReady()
	if m.Readiness().Ready || m.GetOverallHealth().Status.IsReady() {
		t.Fatal("closed manager became ready")
	}
}

func TestUnknownHealthFailsClosed(t *testing.T) {
	state, _ := calculateOverallStatus(map[string]*health.ComponentHealth{"unknown": {HealthStatus: health.HealthStatus{Status: "unexpected"}, Enabled: true}})
	if state.IsReady() {
		t.Fatalf("unknown component became ready through aggregate state %s", state)
	}
}

func TestInFlightChecksCannotRestoreReadiness(t *testing.T) {
	for _, transition := range []string{"cancel", "close"} {
		t.Run(transition, func(t *testing.T) {
			m, err := NewManager(Config{})
			if err != nil {
				t.Fatal(err)
			}
			started, release := make(chan struct{}), make(chan struct{})
			if err := m.RegisterComponent(functionChecker{"db", func(context.Context) error { close(started); <-release; return nil }}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan health.HealthStatus, 1)
			go func() { done <- m.CheckAllComponents(ctx) }()
			<-started
			if transition == "cancel" {
				cancel()
			} else if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			close(release)
			if status := <-done; status.Status.IsReady() || m.Readiness().Ready {
				t.Fatalf("late success restored readiness after %s", transition)
			}
		})
	}
}

func TestOlderCheckCannotOverwriteNewerFailure(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	calls := 0
	var callsMu sync.Mutex
	if err := m.RegisterComponent(functionChecker{"db", func(context.Context) error {
		callsMu.Lock()
		calls++
		call := calls
		callsMu.Unlock()
		if call == 1 {
			close(started)
			<-release
			return nil
		}
		return errors.New("newer check failed")
	}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan health.HealthStatus, 1)
	go func() { done <- m.CheckAllComponents(t.Context()) }()
	<-started
	if err := m.CheckComponent(t.Context(), "db"); err == nil {
		t.Fatal("expected newer failure")
	}
	close(release)
	result := <-done
	component, _ := m.GetComponentHealth("db")
	if component.Status.IsReady() || result.Status.IsReady() || m.Readiness().Ready {
		t.Fatal("older success overwrote newer failed check")
	}
}

func TestHealthResponsesDoNotExposeCheckerErrorText(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	backendErr := errors.New("connection contained private-health-canary")
	if err := m.RegisterComponent(functionChecker{"db", func(context.Context) error { return backendErr }}); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckComponent(t.Context(), "db"); !errors.Is(err, backendErr) {
		t.Fatalf("checker error classification lost: %v", err)
	}
	recorder := httptest.NewRecorder()
	ReadinessHandlerWithManager(m).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "private-health-canary") {
		t.Fatal("health response exposes raw checker error")
	}
}
