// Package idempotency defines stable, storage-agnostic contracts for
// idempotent request processing.
//
// This package contains only interface definitions, sentinel errors, and shared
// types. Concrete implementations (SQL-backed, KV-backed) live in x/data/idempotency.
//
// Typical wiring:
//
//	import (
//		stableidempotency "github.com/birdordev/plumego/store/idempotency"
//		dataidempotency "github.com/birdordev/plumego/x/data/idempotency"
//	)
//
//	store := dataidempotency.NewSQLStore(db, dataidempotency.DefaultSQLConfig()) // concrete implementation
//	var _ stableidempotency.Store = store    // satisfies this interface
package idempotency

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

var (
	ErrNotFound         = errors.New("idempotency: record not found")
	ErrInvalidKey       = errors.New("idempotency: key is required")
	ErrInvalidRecord    = errors.New("idempotency: invalid record")
	ErrExpired          = errors.New("idempotency: record expired")
	ErrRequestMismatch  = errors.New("idempotency: request hash mismatch")
	ErrAlreadyCompleted = errors.New("idempotency: record already completed")
)

// Status describes the lifecycle state of an idempotency record.
type Status string

const (
	// StatusInProgress marks a request that has claimed an idempotency key but
	// has not completed with a replayable response yet.
	StatusInProgress Status = "in_progress"

	// StatusCompleted marks a request that has completed and can replay its
	// stored response for matching duplicate requests.
	StatusCompleted Status = "completed"
)

// Valid reports whether s is a recognized idempotency record status.
func (s Status) Valid() bool {
	switch s {
	case StatusInProgress, StatusCompleted:
		return true
	default:
		return false
	}
}

// Record is the storage-agnostic representation of an idempotency entry.
//
// Store implementations own durable retention and expiry checks. Callers own
// request hashing policy: stores persist RequestHash but do not decide whether
// a duplicate request with a different hash is a conflict.
type Record struct {
	// Key is the caller-provided idempotency key.
	Key string

	// RequestHash identifies the request payload or semantic operation that
	// claimed the key. Concrete implementations define the hash format.
	RequestHash string

	// Status is the lifecycle state of the record.
	Status Status

	// Response is caller-owned. Implementations that retain it after Complete or
	// PutIfAbsent must make a defensive copy. Get returns implementation-owned
	// record values; callers that retain Response should copy it before mutation.
	Response []byte

	// CreatedAt records when the key was first claimed.
	CreatedAt time.Time

	// UpdatedAt records the last state transition time.
	UpdatedAt time.Time

	// ExpiresAt records when the entry should no longer be considered usable.
	ExpiresAt time.Time
}

// Clone returns a copy of r with mutable fields detached from the original.
func (r Record) Clone() Record {
	clone := r
	if r.Response != nil {
		clone.Response = append([]byte(nil), r.Response...)
	}
	return clone
}

// ValidateKey validates a stable idempotency key shape.
func ValidateKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return ErrInvalidKey
	}
	if strings.IndexFunc(key, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: control characters are not allowed", ErrInvalidKey)
	}
	return nil
}

// ValidateRecord validates stable-layer record fields.
func ValidateRecord(record Record) error {
	if err := ValidateKey(record.Key); err != nil {
		return err
	}
	if record.RequestHash == "" {
		return fmt.Errorf("%w: request hash is required", ErrInvalidRecord)
	}
	if !record.Status.Valid() {
		return fmt.Errorf("%w: status %q is invalid", ErrInvalidRecord, record.Status)
	}
	if !record.CreatedAt.IsZero() && !record.ExpiresAt.IsZero() && record.ExpiresAt.Before(record.CreatedAt) {
		return fmt.Errorf("%w: expires before created", ErrInvalidRecord)
	}
	return nil
}

// ValidateCompletion validates whether an in-progress record may be completed
// for the expected request hash at the current time.
func ValidateCompletion(record Record, requestHash string) error {
	return ValidateCompletionAt(record, requestHash, time.Now())
}

// ValidateCompletionAt validates whether an in-progress record may be completed
// for the expected request hash at the supplied time.
func ValidateCompletionAt(record Record, requestHash string, now time.Time) error {
	if err := ValidateRecord(record); err != nil {
		return err
	}
	if requestHash == "" {
		return fmt.Errorf("%w: request hash is required", ErrInvalidRecord)
	}
	if record.RequestHash != requestHash {
		return ErrRequestMismatch
	}
	if !record.ExpiresAt.IsZero() && !record.ExpiresAt.After(now) {
		return ErrExpired
	}
	if record.Status == StatusCompleted {
		return ErrAlreadyCompleted
	}
	return nil
}

// Store is the stable contract implemented by concrete idempotency backends.
//
// The stable layer tracks storage state only. It does not define HTTP retry
// policy, response replay policy, request-hash conflict handling, table schema,
// or backend duplicate-key mechanics.
type Store interface {
	// Get returns a record by key. The bool result reports whether a usable,
	// non-expired record was found without requiring callers to inspect
	// ErrNotFound. Missing and expired records should return found=false with a
	// nil error after any best-effort cleanup. Invalid keys return ErrInvalidKey.
	Get(ctx context.Context, key string) (Record, bool, error)

	// PutIfAbsent stores a new record only when the key is not already claimed by
	// a usable record. The bool result reports whether this call claimed the key.
	// If a usable in-progress or completed record already exists, implementations
	// return inserted=false with nil error; callers can then Get and compare
	// RequestHash if business policy needs conflict detection. A candidate record
	// that is already expired returns ErrExpired.
	PutIfAbsent(ctx context.Context, record Record) (bool, error)

	// Complete marks an existing usable record complete with a replayable
	// response. Missing records and records discovered to be expired during
	// completion return ErrNotFound after any best-effort cleanup.
	Complete(ctx context.Context, key string, response []byte) error

	// Delete removes a record by key. Delete is a storage cleanup operation; a
	// missing key returns ErrNotFound. Invalid keys return ErrInvalidKey.
	Delete(ctx context.Context, key string) error
}

// HashAwareStore is an optional extension for backends that validate the
// request hash while completing a record.
type HashAwareStore interface {
	Store

	// CompleteWithRequestHash marks an existing record complete only when the
	// stored request hash matches requestHash.
	CompleteWithRequestHash(ctx context.Context, key, requestHash string, response []byte) error
}
