package contract

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// ---------------------------------------------------------------------------
// AsAPIError
// ---------------------------------------------------------------------------

func TestAsAPIErrorUnwrapsDirectValue(t *testing.T) {
	original := NewErrorBuilder().Type(TypeNotFound).Message("item not found").Build()

	got, ok := AsAPIError(original)
	if !ok {
		t.Fatal("AsAPIError should find a direct APIError value")
	}
	if got.Code() != CodeResourceNotFound {
		t.Fatalf("code = %q, want %q", got.Code(), CodeResourceNotFound)
	}
	if got.Status() != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", got.Status(), http.StatusNotFound)
	}
}

func TestAsAPIErrorUnwrapsFromWrappedChain(t *testing.T) {
	original := NewErrorBuilder().Type(TypeForbidden).Message("access denied").Build()
	wrapped := fmt.Errorf("service: %w", original)

	got, ok := AsAPIError(wrapped)
	if !ok {
		t.Fatal("AsAPIError should find APIError wrapped with %%w")
	}
	if got.Status() != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", got.Status(), http.StatusForbidden)
	}
}

func TestAsAPIErrorReturnsFalseForNonAPIError(t *testing.T) {
	_, ok := AsAPIError(errors.New("plain error"))
	if ok {
		t.Fatal("AsAPIError should return false for a plain error")
	}
}

func TestAsAPIErrorReturnsFalseForNil(t *testing.T) {
	_, ok := AsAPIError(nil)
	if ok {
		t.Fatal("AsAPIError should return false for nil")
	}
}

func TestAsAPIErrorReturnsFalseForDeepNonAPIError(t *testing.T) {
	_, ok := AsAPIError(fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", errors.New("leaf"))))
	if ok {
		t.Fatal("AsAPIError should return false when no APIError in chain")
	}
}

// ---------------------------------------------------------------------------
// ErrorBuilder.Wrap + APIError.Unwrap
// ---------------------------------------------------------------------------

func TestWrapPreservesCauseAndIsNotSerialized(t *testing.T) {
	cause := errors.New("db connection reset")
	apiErr := NewErrorBuilder().
		Type(TypeInternal).
		Wrap(cause).
		Build()

	if unwrapped := errors.Unwrap(apiErr); unwrapped != cause {
		t.Fatalf("Unwrap() = %v, want original cause", unwrapped)
	}
}

func TestWrapEnablesErrorsIs(t *testing.T) {
	sentinel := errors.New("sentinel")
	apiErr := NewErrorBuilder().Type(TypeInternal).Wrap(sentinel).Build()

	if !errors.Is(apiErr, sentinel) {
		t.Fatal("errors.Is should find sentinel through APIError.Unwrap")
	}
}

func TestWrapCauseNotIncludedInDetails(t *testing.T) {
	cause := errors.New("internal secret")
	apiErr := NewErrorBuilder().Type(TypeInternal).Wrap(cause).Build()

	if len(apiErr.Details()) != 0 {
		t.Fatalf("cause must not appear in Details(), got %v", apiErr.Details())
	}
}

func TestWrapWithNilCauseIsHarmless(t *testing.T) {
	apiErr := NewErrorBuilder().Type(TypeValidation).Wrap(nil).Build()
	if apiErr.Unwrap() != nil {
		t.Fatalf("Unwrap() = %v after Wrap(nil), want nil", apiErr.Unwrap())
	}
}

func TestWrapCausePreservedThroughNormalization(t *testing.T) {
	cause := errors.New("cause")
	apiErr := NewErrorBuilder().Type(TypeNotFound).Wrap(cause).Build()
	normalized := normalizeAPIError(apiErr)
	if errors.Unwrap(normalized) != cause {
		t.Fatal("cause should survive normalization")
	}
}

func TestAsAPIErrorWithWrappedChainContainingCause(t *testing.T) {
	dbErr := errors.New("connection refused")
	apiErr := NewErrorBuilder().Type(TypeInternal).Wrap(dbErr).Build()
	serviceErr := fmt.Errorf("repo.FindUser: %w", apiErr)

	got, ok := AsAPIError(serviceErr)
	if !ok {
		t.Fatal("AsAPIError should find APIError in chain")
	}
	if !errors.Is(got, dbErr) {
		t.Fatal("original cause should be reachable via errors.Is on the extracted APIError")
	}
}

// ---------------------------------------------------------------------------
// isNormalized fast-path
// ---------------------------------------------------------------------------

func TestIsNormalizedFastPathSkipsReNormalization(t *testing.T) {
	built := NewErrorBuilder().
		Type(TypeValidation).
		Message("invalid email").
		Detail("field", "email").
		Build()

	if !built.isNormalized {
		t.Fatal("Build() should set isNormalized = true")
	}

	// Access all fields; they must return stable values without re-normalization.
	if built.Status() != http.StatusBadRequest {
		t.Fatalf("Status() = %d, want %d", built.Status(), http.StatusBadRequest)
	}
	if built.Code() != CodeValidationError {
		t.Fatalf("Code() = %q, want %q", built.Code(), CodeValidationError)
	}
	if built.Category() != CategoryValidation {
		t.Fatalf("Category() = %q, want %q", built.Category(), CategoryValidation)
	}
	if built.Type() != TypeValidation {
		t.Fatalf("Type() = %q, want %q", built.Type(), TypeValidation)
	}
	if built.Message() != "invalid email" {
		t.Fatalf("Message() = %q, want %q", built.Message(), "invalid email")
	}
	if got := built.Details()["field"]; got != "email" {
		t.Fatalf("Details()[field] = %v, want email", got)
	}
}

func TestZeroValueAPIErrorNormalizesOnAccessor(t *testing.T) {
	var zero APIError
	if zero.isNormalized {
		t.Fatal("zero-value APIError must not be pre-normalized")
	}
	// Accessor must fall through to normalizeAPIError and return safe defaults.
	if zero.Status() != http.StatusInternalServerError {
		t.Fatalf("zero Status() = %d, want 500", zero.Status())
	}
	if zero.Code() != CodeInternalError {
		t.Fatalf("zero Code() = %q, want %q", zero.Code(), CodeInternalError)
	}
}

func TestDetailsAlwaysClonesEvenWhenNormalized(t *testing.T) {
	built := NewErrorBuilder().
		Type(TypeValidation).
		Detail("key", "value").
		Build()

	d1 := built.Details()
	d1["key"] = "mutated"

	d2 := built.Details()
	if d2["key"] == "mutated" {
		t.Fatal("Details() must return an isolated copy; mutation of d1 must not affect d2")
	}
}

func TestAsAPIErrorFindsPointerErrors(t *testing.T) {
	want := NewErrorBuilder().Type(TypeNotFound).Build()
	for _, err := range []error{want, fmt.Errorf("value: %w", want), &want, fmt.Errorf("pointer: %w", &want)} {
		got, ok := AsAPIError(err)
		if !ok || got.Status() != want.Status() || got.Type() != want.Type() {
			t.Fatalf("AsAPIError(%T) = %v, %v", err, got, ok)
		}
	}
	var absent *APIError
	for _, err := range []error{absent, fmt.Errorf("nil pointer: %w", absent)} {
		if _, ok := AsAPIError(err); ok {
			t.Fatal("typed nil API error matched")
		}
	}
	value := NewErrorBuilder().Type(TypeBadRequest).Build()
	if got, ok := AsAPIError(errors.Join(absent, &want, value)); !ok || got.Type() != value.Type() {
		t.Fatalf("joined error = %v, %v, want value match", got, ok)
	}
	if got, ok := AsAPIError(errors.Join(absent, &want)); !ok || got.Type() != want.Type() {
		t.Fatalf("joined error = %v, %v, want pointer match", got, ok)
	}
}
