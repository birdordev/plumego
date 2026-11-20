package migrate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewNilRunnerReturnsError(t *testing.T) {
	_, err := New(nil)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

type valueRunner struct{ *fakeRunner }

func TestNewValidatesRunnerInstance(t *testing.T) {
	var missing *fakeRunner
	if got, err := New(missing); got != nil || !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("typed nil result = %v, %v, want ErrInvalidConfig", got, err)
	}
	runner := &fakeRunner{}
	for _, input := range []Runner{runner, valueRunner{runner}} {
		runner.up = false
		migrator, err := New(input)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrator.Up(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !runner.up {
			t.Errorf("Up was not delegated for %T", input)
		}
	}
}

func TestMigratorDelegatesOperations(t *testing.T) {
	appliedAt := time.Now()
	runner := &fakeRunner{
		status: []MigrationStatus{
			{Version: 1, Name: "create_widgets", State: "applied", AppliedAt: &appliedAt},
		},
	}
	migrator, err := New(runner)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := migrator.Up(t.Context()); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := migrator.Down(t.Context()); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
	status, err := migrator.Status(t.Context())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(status) != 1 || status[0].Name != "create_widgets" {
		t.Fatalf("status = %#v", status)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if !runner.up || !runner.down || !runner.closed {
		t.Fatalf("runner calls: up=%v down=%v closed=%v", runner.up, runner.down, runner.closed)
	}
}

func TestMigratorWrapsRunnerError(t *testing.T) {
	want := errors.New("boom")
	migrator, err := New(&fakeRunner{upErr: want})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	err = migrator.Up(t.Context())
	if !errors.Is(err, ErrMigrationFailed) || !errors.Is(err, want) {
		t.Fatalf("Up() error = %v, want ErrMigrationFailed wrapping boom", err)
	}
}

type fakeRunner struct {
	up      bool
	down    bool
	closed  bool
	upErr   error
	status  []MigrationStatus
	statusE error
}

func (r *fakeRunner) Up(context.Context) error {
	r.up = true
	return r.upErr
}

func (r *fakeRunner) Down(context.Context) error {
	r.down = true
	return nil
}

func (r *fakeRunner) Status(context.Context) ([]MigrationStatus, error) {
	return r.status, r.statusE
}

func (r *fakeRunner) Close() error {
	r.closed = true
	return nil
}

func TestMigratorStatusOwnsTimestamp(t *testing.T) {
	original := time.Unix(1, 0)
	runner := &fakeRunner{status: []MigrationStatus{
		{Version: 1, State: "applied", AppliedAt: &original},
		{Version: 2, State: "pending"},
	}}
	m, err := New(runner)
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Status(t.Context())
	if err != nil || len(got) != 2 || got[0].AppliedAt == nil || got[1].AppliedAt != nil {
		t.Fatalf("status = %+v, error = %v", got, err)
	}
	*got[0].AppliedAt = time.Unix(2, 0)
	fresh, err := m.Status(t.Context())
	if err != nil || !original.Equal(time.Unix(1, 0)) || !fresh[0].AppliedAt.Equal(original) {
		t.Errorf("runner timestamp = %v; fresh status = %+v, error = %v", original, fresh, err)
	}
	runner.status = []MigrationStatus{}
	if got, err := m.Status(t.Context()); err != nil || got != nil {
		t.Errorf("empty status = %#v, error = %v", got, err)
	}
	runner.statusE = errors.New("read failed")
	if _, err := m.Status(t.Context()); !errors.Is(err, ErrMigrationFailed) || !errors.Is(err, runner.statusE) {
		t.Errorf("status error = %v", err)
	}
}

type failingMigrationRunner struct {
	fakeRunner
	downContext       context.Context
	downErr, closeErr error
}

func (r *failingMigrationRunner) Down(ctx context.Context) error {
	r.downContext = ctx
	return r.downErr
}
func (r *failingMigrationRunner) Close() error { return r.closeErr }

func TestMigratorPreservesDownAndCloseErrors(t *testing.T) {
	runner := &failingMigrationRunner{downErr: errors.New("rollback failure"), closeErr: errors.New("close failure")}
	m, err := New(runner)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := m.Down(ctx); !errors.Is(err, ErrMigrationFailed) || !errors.Is(err, runner.downErr) || runner.downContext != ctx {
		t.Fatalf("Down error=%v, original context=%t", err, runner.downContext == ctx)
	}
	if err := m.Close(); !errors.Is(err, runner.closeErr) {
		t.Fatalf("Close error = %v", err)
	}
}
