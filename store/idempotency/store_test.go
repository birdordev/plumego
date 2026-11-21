package idempotency

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// compile-time: noopStore must satisfy Store.
var _ Store = noopStore{}

// compile-time: hashAwareNoopStore must satisfy HashAwareStore.
var _ HashAwareStore = hashAwareNoopStore{}

func TestSentinelErrorsAreNonNilAndDistinct(t *testing.T) {
	sentinels := []struct {
		name string
		err  error
	}{
		{"ErrNotFound", ErrNotFound},
		{"ErrInvalidKey", ErrInvalidKey},
		{"ErrInvalidRecord", ErrInvalidRecord},
		{"ErrExpired", ErrExpired},
		{"ErrRequestMismatch", ErrRequestMismatch},
		{"ErrAlreadyCompleted", ErrAlreadyCompleted},
	}
	for _, s := range sentinels {
		if s.err == nil {
			t.Errorf("%s is nil", s.name)
		}
	}
	if errors.Is(ErrNotFound, ErrInvalidKey) || errors.Is(ErrNotFound, ErrInvalidRecord) ||
		errors.Is(ErrNotFound, ErrExpired) || errors.Is(ErrNotFound, ErrRequestMismatch) ||
		errors.Is(ErrNotFound, ErrAlreadyCompleted) || errors.Is(ErrInvalidKey, ErrInvalidRecord) ||
		errors.Is(ErrInvalidKey, ErrExpired) || errors.Is(ErrInvalidKey, ErrRequestMismatch) ||
		errors.Is(ErrInvalidKey, ErrAlreadyCompleted) || errors.Is(ErrInvalidRecord, ErrExpired) ||
		errors.Is(ErrInvalidRecord, ErrRequestMismatch) || errors.Is(ErrInvalidRecord, ErrAlreadyCompleted) ||
		errors.Is(ErrExpired, ErrRequestMismatch) || errors.Is(ErrExpired, ErrAlreadyCompleted) ||
		errors.Is(ErrRequestMismatch, ErrAlreadyCompleted) {
		t.Error("sentinel errors must not wrap each other")
	}
}

func TestSentinelErrorMessagesAreNamespaced(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "not found", err: ErrNotFound, want: "idempotency: record not found"},
		{name: "invalid key", err: ErrInvalidKey, want: "idempotency: key is required"},
		{name: "expired", err: ErrExpired, want: "idempotency: record expired"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("error string = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusConstants(t *testing.T) {
	if StatusInProgress != "in_progress" {
		t.Errorf("StatusInProgress = %q, want %q", StatusInProgress, "in_progress")
	}
	if StatusCompleted != "completed" {
		t.Errorf("StatusCompleted = %q, want %q", StatusCompleted, "completed")
	}
	if StatusInProgress == StatusCompleted {
		t.Error("StatusInProgress and StatusCompleted must differ")
	}
}

func TestStatusValid(t *testing.T) {
	if !StatusInProgress.Valid() {
		t.Fatal("StatusInProgress should be valid")
	}
	if !StatusCompleted.Valid() {
		t.Fatal("StatusCompleted should be valid")
	}
	if Status("failed").Valid() {
		t.Fatal("unknown status should be invalid")
	}
}

func TestRecordZeroValue(t *testing.T) {
	var r Record
	if r.Key != "" || r.RequestHash != "" || r.Status != "" || r.Response != nil {
		t.Fatalf("unexpected zero-value record: %+v", r)
	}
	if !r.CreatedAt.IsZero() || !r.UpdatedAt.IsZero() || !r.ExpiresAt.IsZero() {
		t.Fatalf("zero-value record should have zero timestamps: %+v", r)
	}
}

func TestRecordFields(t *testing.T) {
	response := []byte("body")
	r := Record{
		Key:         "k",
		RequestHash: "sha256:request",
		Status:      StatusInProgress,
		Response:    response,
	}
	if r.Key != "k" {
		t.Errorf("Key = %q, want %q", r.Key, "k")
	}
	if r.RequestHash != "sha256:request" {
		t.Errorf("RequestHash = %q, want %q", r.RequestHash, "sha256:request")
	}
	if r.Status != StatusInProgress {
		t.Errorf("Status = %q, want %q", r.Status, StatusInProgress)
	}
	if string(r.Response) != "body" {
		t.Errorf("Response = %q, want %q", r.Response, "body")
	}

	response[0] = 'B'
	if string(r.Response) != "Body" {
		t.Errorf("Record value should expose caller-owned response bytes, got %q", r.Response)
	}
}

func TestRecordCloneCopiesResponse(t *testing.T) {
	original := Record{
		Key:         "k",
		RequestHash: "sha256:request",
		Status:      StatusCompleted,
		Response:    []byte("body"),
	}

	clone := original.Clone()
	original.Response[0] = 'B'
	clone.Response[1] = 'A'

	if string(original.Response) != "Body" {
		t.Fatalf("original response = %q, want Body", original.Response)
	}
	if string(clone.Response) != "bAdy" {
		t.Fatalf("clone response = %q, want bAdy", clone.Response)
	}
	if (Record{}).Clone().Response != nil {
		t.Fatal("nil response should clone to nil")
	}
	if got := (Record{Response: []byte{}}).Clone().Response; got == nil || len(got) != 0 {
		t.Fatalf("empty response should clone to a non-nil empty slice, got %#v", got)
	}
}

func TestValidateKey(t *testing.T) {
	if err := ValidateKey("request-123"); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	for _, key := range []string{"", "   ", "bad\nkey"} {
		err := ValidateKey(key)
		if err == nil || !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("expected ErrInvalidKey for %q, got %v", key, err)
		}
	}
}

func TestValidateRecord(t *testing.T) {
	now := time.Now()
	valid := Record{
		Key:         "request-123",
		RequestHash: "sha256:request",
		Status:      StatusInProgress,
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Minute),
	}
	if err := ValidateRecord(valid); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(Record) Record
		want error
	}{
		{
			name: "invalid key",
			mut: func(record Record) Record {
				record.Key = ""
				return record
			},
			want: ErrInvalidKey,
		},
		{
			name: "missing request hash",
			mut: func(record Record) Record {
				record.RequestHash = ""
				return record
			},
			want: ErrInvalidRecord,
		},
		{
			name: "invalid status",
			mut: func(record Record) Record {
				record.Status = Status("failed")
				return record
			},
			want: ErrInvalidRecord,
		},
		{
			name: "expires before created",
			mut: func(record Record) Record {
				record.ExpiresAt = record.CreatedAt.Add(-time.Second)
				return record
			},
			want: ErrInvalidRecord,
		},
	}
	for _, tc := range cases {
		err := ValidateRecord(tc.mut(valid))
		if err == nil || !errors.Is(err, tc.want) {
			t.Fatalf("%s: expected %v, got %v", tc.name, tc.want, err)
		}
	}
}

func TestValidateCompletion(t *testing.T) {
	now := time.Now()
	valid := Record{
		Key:         "request-123",
		RequestHash: "sha256:request",
		Status:      StatusInProgress,
		CreatedAt:   now.Add(-time.Minute),
		ExpiresAt:   now.Add(time.Minute),
	}
	if err := ValidateCompletionAt(valid, "sha256:request", now); err != nil {
		t.Fatalf("valid completion rejected: %v", err)
	}

	cases := []struct {
		name        string
		record      Record
		requestHash string
		want        error
	}{
		{
			name:        "missing expected hash",
			record:      valid,
			requestHash: "",
			want:        ErrInvalidRecord,
		},
		{
			name:        "request hash mismatch",
			record:      valid,
			requestHash: "sha256:different",
			want:        ErrRequestMismatch,
		},
		{
			name: "already completed",
			record: func() Record {
				record := valid
				record.Status = StatusCompleted
				return record
			}(),
			requestHash: "sha256:request",
			want:        ErrAlreadyCompleted,
		},
		{
			name: "expired",
			record: func() Record {
				record := valid
				record.ExpiresAt = now.Add(-time.Second)
				return record
			}(),
			requestHash: "sha256:request",
			want:        ErrExpired,
		},
		{
			name: "completed at expiry",
			record: func() Record {
				record := valid
				record.Status = StatusCompleted
				record.ExpiresAt = now
				return record
			}(),
			requestHash: "sha256:request",
			want:        ErrExpired,
		},
		{
			name: "invalid record",
			record: func() Record {
				record := valid
				record.Status = Status("failed")
				return record
			}(),
			requestHash: "sha256:request",
			want:        ErrInvalidRecord,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCompletionAt(tc.record, tc.requestHash, now)
			if err == nil || !errors.Is(err, tc.want) {
				t.Fatalf("ValidateCompletionAt error = %v, want %v", err, tc.want)
			}
		})
	}
}

// noopStore is a compile-time-only implementation used to verify the interface.
type noopStore struct{}

func (noopStore) Get(context.Context, string) (Record, bool, error) {
	return Record{}, false, ErrNotFound
}

func (noopStore) PutIfAbsent(context.Context, Record) (bool, error) {
	return false, ErrInvalidKey
}

func (noopStore) Complete(context.Context, string, []byte) error {
	return ErrExpired
}

func (noopStore) Delete(context.Context, string) error {
	return nil
}

type hashAwareNoopStore struct {
	noopStore
}

func (hashAwareNoopStore) CompleteWithRequestHash(context.Context, string, string, []byte) error {
	return ErrRequestMismatch
}

func TestValidateCompletionAtChecksMismatchThenExpiryThenStatus(t *testing.T) {
	now := time.Now()
	record := Record{Key: "fixture", RequestHash: "stored", Status: StatusCompleted, ExpiresAt: now}
	for _, tc := range []struct {
		hash string
		at   time.Time
		want error
	}{
		{"other", now, ErrRequestMismatch},
		{"stored", now, ErrExpired},
		{"stored", now.Add(-time.Nanosecond), ErrAlreadyCompleted},
	} {
		err := ValidateCompletionAt(record, tc.hash, tc.at)
		if !errors.Is(err, tc.want) || strings.Contains(err.Error(), record.RequestHash) {
			t.Fatalf("classification=%v want=%v", err, tc.want)
		}
	}
}

func TestValidateKeyKeepsLegalUnicodeAndBoundarySpaces(t *testing.T) {
	for _, key := range []string{" 订单-é ", "\u3000fixture\u00a0", "e\u0301"} {
		if err := ValidateKey(key); err != nil {
			t.Errorf("valid key %q: %v", key, err)
		}
	}
	for _, key := range []string{"\u3000\u00a0", "fixture\u0085suffix", "\tfixture"} {
		if err := ValidateKey(key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("invalid key %q: %v", key, err)
		}
	}
}

func TestCompletionExpirationAndStatusUseSuppliedClock(t *testing.T) {
	expires := time.Now()
	for _, status := range []Status{StatusInProgress, StatusCompleted} {
		record := Record{Key: "fixture", RequestHash: "fixture", Status: status, ExpiresAt: expires}
		for _, offset := range []time.Duration{-1, 0, 1} {
			var want error
			if offset >= 0 {
				want = ErrExpired
			} else if status == StatusCompleted {
				want = ErrAlreadyCompleted
			}
			if got := ValidateCompletionAt(record, "fixture", expires.Add(offset)); !errors.Is(got, want) {
				t.Fatalf("status=%s offset=%v err=%v want=%v", status, offset, got, want)
			}
		}
		if err := ValidateCompletionAt(record, "different", expires); !errors.Is(err, ErrRequestMismatch) {
			t.Fatalf("mismatch=%v", err)
		}
		if record.Status != status || record.ExpiresAt != expires {
			t.Fatalf("record changed: %+v", record)
		}
	}
}

func TestRecordResponseClonePreservesEmptyAndNilAndCopiesPayload(t *testing.T) {
	stamp := time.Now()
	for _, source := range [][]byte{nil, {}, []byte("sample")} {
		record := Record{Key: "fixture", CreatedAt: stamp, UpdatedAt: stamp, ExpiresAt: stamp.Add(time.Hour), Response: source}
		a, b := record.Clone(), record.Clone()
		if (a.Response == nil) != (source == nil) || !a.CreatedAt.Equal(stamp) || !a.UpdatedAt.Equal(stamp) || !a.ExpiresAt.Equal(record.ExpiresAt) {
			t.Fatalf("clone=%+v", a)
		}
		if len(a.Response) > 0 {
			a.Response[0] = 'X'
			if string(source) != "sample" || string(b.Response) != "sample" {
				t.Fatal("response ownership changed")
			}
		}
	}
}

func TestRecordStatusValidationMatchesExactEnumValues(t *testing.T) {
	for _, tc := range []struct {
		state Status
		valid bool
	}{
		{StatusInProgress, true}, {StatusCompleted, true}, {"", false}, {"Completed", false}, {"completed ", false},
	} {
		err := ValidateRecord(Record{Status: tc.state, Key: "fixture", RequestHash: "fixture"})
		if tc.state.Valid() != tc.valid || tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("state=%q valid=%t error=%v", tc.state, tc.state.Valid(), err)
		}
	}
}

func TestClonedRecordExpiryAndResponseCanChangeIndependently(t *testing.T) {
	now := time.Unix(60, 0)
	original := Record{Key: "fixture", Status: StatusInProgress, RequestHash: "fixture", ExpiresAt: now.Add(time.Second), Response: []byte("body")}
	clone := original.Clone()
	clone.ExpiresAt = now
	clone.Response[0] = 'X'
	if err := ValidateCompletionAt(original, "fixture", now); err != nil || string(original.Response) != "body" || !original.ExpiresAt.After(now) {
		t.Fatalf("original=%+v err=%v", original, err)
	}
	if err := ValidateCompletionAt(clone, "fixture", now); !errors.Is(err, ErrExpired) {
		t.Fatalf("clone expiry=%v", err)
	}
}
