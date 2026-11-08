package cachemanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/birdordev/plumego/x/ai/semanticcache/embedding"
	"github.com/birdordev/plumego/x/ai/semanticcache/vectorstore"
)

// ErrUnsupportedMaintenance reports maintenance operations that require
// backend-specific capabilities not exposed by the base Backend interface.
var ErrUnsupportedMaintenance = errors.New("semantic cache maintenance operation unsupported")

// Manager provides cache management capabilities.
type Manager struct {
	backend  vectorstore.Backend
	provider embedding.Provider
}

// NewManager creates a new cache manager.
func NewManager(backend vectorstore.Backend, provider embedding.Provider) *Manager {
	return &Manager{
		backend:  backend,
		provider: provider,
	}
}

// Stats returns cache statistics.
func (m *Manager) Stats(ctx context.Context) (*CacheStats, error) {
	backendStats, err := m.backend.Stats(ctx)
	if err != nil {
		return nil, err
	}

	stats := &CacheStats{
		TotalEntries:   backendStats.TotalEntries,
		TotalVectors:   backendStats.TotalVectors,
		Dimensions:     backendStats.Dimensions,
		MemoryUsage:    backendStats.MemoryUsage,
		HitRate:        backendStats.HitRate,
		AverageLatency: backendStats.AverageLatency,
		BackendName:    backendStats.Name,
		LastCompaction: backendStats.LastCompaction,
	}

	return stats, nil
}

// Cleanup removes entries based on policy.
func (m *Manager) Cleanup(ctx context.Context, policy CleanupPolicy) error {
	switch policy.Type {
	case CleanupTypeOldest:
		return m.cleanupOldest(ctx, policy.MaxAge)
	case CleanupTypeLeastUsed:
		return m.cleanupLeastUsed(ctx, policy.Threshold)
	case CleanupTypeExpired:
		return m.cleanupExpired(ctx)
	case CleanupTypeAll:
		return m.backend.Clear(ctx)
	default:
		return fmt.Errorf("unknown cleanup policy: %s", policy.Type)
	}
}

// Export exports cache entries to a file.
func (m *Manager) Export(ctx context.Context, path string) error {
	// Get all entries
	// Note: This is a simplified implementation
	// In production, you'd want to implement pagination
	count, err := m.backend.Count(ctx)
	if err != nil {
		return fmt.Errorf("failed to count entries: %w", err)
	}

	if count == 0 {
		return fmt.Errorf("no entries to export")
	}

	// For now, we'll assume backends have a way to list all entries
	// This would need to be added to the Backend interface for full functionality

	export := &CacheExport{
		Version:      "1.0",
		ExportedAt:   time.Now(),
		TotalEntries: count,
		BackendType:  m.backend.Name(),
		Dimensions:   m.provider.Dimensions(),
		// Entries would be populated here
	}

	data, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal export: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write export file: %w", err)
	}

	return nil
}

// Import imports cache entries from a file.
func (m *Manager) Import(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read import file: %w", err)
	}

	var export CacheExport
	if err := json.Unmarshal(data, &export); err != nil {
		return fmt.Errorf("failed to unmarshal import: %w", err)
	}

	// Validate version
	if export.Version != "1.0" {
		return fmt.Errorf("unsupported export version: %s", export.Version)
	}

	// Validate dimensions
	if export.Dimensions != m.provider.Dimensions() {
		return fmt.Errorf("dimension mismatch: export has %d, current provider has %d",
			export.Dimensions, m.provider.Dimensions())
	}

	// Import entries
	if err := m.backend.AddBatch(ctx, export.Entries); err != nil {
		return fmt.Errorf("failed to import entries: %w", err)
	}

	return nil
}

// Compact performs compaction on the cache (backend-specific).
func (m *Manager) Compact(ctx context.Context) error {
	return fmt.Errorf("%w: compact for backend %s", ErrUnsupportedMaintenance, m.backend.Name())
}

// cleanupOldest removes entries older than maxAge.
func (m *Manager) cleanupOldest(ctx context.Context, maxAge time.Duration) error {
	return fmt.Errorf("%w: cleanup oldest requires entry listing support", ErrUnsupportedMaintenance)
}

// cleanupLeastUsed removes least accessed entries below threshold.
func (m *Manager) cleanupLeastUsed(ctx context.Context, threshold int64) error {
	return fmt.Errorf("%w: cleanup least used requires access count filtering support", ErrUnsupportedMaintenance)
}

// cleanupExpired removes entries with expired TTL.
func (m *Manager) cleanupExpired(ctx context.Context) error {
	return fmt.Errorf("%w: cleanup expired requires TTL scanning support", ErrUnsupportedMaintenance)
}

// CacheStats holds cache statistics.
type CacheStats struct {
	TotalEntries   int64         `json:"total_entries"`
	TotalVectors   int64         `json:"total_vectors"`
	Dimensions     int           `json:"dimensions"`
	MemoryUsage    int64         `json:"memory_usage_bytes"`
	HitRate        float64       `json:"hit_rate"`
	AverageLatency time.Duration `json:"average_latency"`
	BackendName    string        `json:"backend_name"`
	LastCompaction time.Time     `json:"last_compaction,omitempty"`
}

// CleanupPolicy defines a cleanup policy.
type CleanupPolicy struct {
	Type      CleanupType
	MaxAge    time.Duration
	Threshold int64
}

// CleanupType represents the type of cleanup.
type CleanupType string

const (
	CleanupTypeOldest    CleanupType = "oldest"
	CleanupTypeLeastUsed CleanupType = "least_used"
	CleanupTypeExpired   CleanupType = "expired"
	CleanupTypeAll       CleanupType = "all"
)

// CacheExport represents an exported cache.
type CacheExport struct {
	Version      string               `json:"version"`
	ExportedAt   time.Time            `json:"exported_at"`
	TotalEntries int64                `json:"total_entries"`
	BackendType  string               `json:"backend_type"`
	Dimensions   int                  `json:"dimensions"`
	Entries      []*vectorstore.Entry `json:"entries"`
}
