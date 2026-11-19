// Package migrate defines a dependency-free migration runner contract for
// x/data callers.
package migrate

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrInvalidConfig is returned when migrator construction input is invalid.
	ErrInvalidConfig = errors.New("migrate: invalid config")

	// ErrMigrationFailed is returned when a migration operation fails.
	ErrMigrationFailed = errors.New("migrate: operation failed")
)

// Runner executes migration operations.
type Runner interface {
	Up(context.Context) error
	Down(context.Context) error
	Status(context.Context) ([]MigrationStatus, error)
	Close() error
}

// Migrator executes SQL migrations through a caller-provided runner.
type Migrator struct {
	runner Runner
}

// MigrationStatus describes one migration and whether it is pending or applied.
type MigrationStatus struct {
	Version   int64      `json:"version"`
	Name      string     `json:"name"`
	State     string     `json:"state"`
	AppliedAt *time.Time `json:"applied_at,omitempty"`
}

// New creates a migrator from a caller-provided runner.
func New(runner Runner) (*Migrator, error) {
	if runner == nil {
		return nil, fmt.Errorf("%w: runner is nil", ErrInvalidConfig)
	}
	return &Migrator{runner: runner}, nil
}

// Up applies all pending migrations.
func (m *Migrator) Up(ctx context.Context) error {
	if m == nil || m.runner == nil {
		return fmt.Errorf("%w: migrator is nil", ErrMigrationFailed)
	}
	if err := m.runner.Up(ctx); err != nil {
		return fmt.Errorf("%w: up: %w", ErrMigrationFailed, err)
	}
	return nil
}

// Down rolls back one applied migration.
func (m *Migrator) Down(ctx context.Context) error {
	if m == nil || m.runner == nil {
		return fmt.Errorf("%w: migrator is nil", ErrMigrationFailed)
	}
	if err := m.runner.Down(ctx); err != nil {
		return fmt.Errorf("%w: down: %w", ErrMigrationFailed, err)
	}
	return nil
}

// Status returns a caller-owned snapshot of pending and applied migrations.
func (m *Migrator) Status(ctx context.Context) ([]MigrationStatus, error) {
	if m == nil || m.runner == nil {
		return nil, fmt.Errorf("%w: migrator is nil", ErrMigrationFailed)
	}
	statuses, err := m.runner.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: status: %w", ErrMigrationFailed, err)
	}
	snapshot := append([]MigrationStatus(nil), statuses...)
	for i := range snapshot {
		if snapshot[i].AppliedAt != nil {
			appliedAt := *snapshot[i].AppliedAt
			snapshot[i].AppliedAt = &appliedAt
		}
	}
	return snapshot, nil
}

// Close releases provider resources.
func (m *Migrator) Close() error {
	if m == nil || m.runner == nil {
		return nil
	}
	return m.runner.Close()
}
