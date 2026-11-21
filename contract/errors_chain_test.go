package contract

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestAsAPIErrorSkipsTypedNilBranch(t *testing.T) {
	var absent *APIError
	want := NewErrorBuilder().Type(TypeNotFound).Build()
	if _, ok := AsAPIError(fmt.Errorf("wrapped: %w", absent)); ok {
		t.Fatal("typed nil matched")
	}
	got, ok := AsAPIError(errors.Join(absent, want))
	if !ok || got.Code() != want.Code() {
		t.Fatalf("error=%v ok=%t", got, ok)
	}
}

func TestAsAPIErrorSharedBranchesChooseFirstValue(t *testing.T) {
	pointed := NewErrorBuilder().Type(TypeInternal).Build()
	first := NewErrorBuilder().Type(TypeNotFound).Build()
	last := NewErrorBuilder().Type(TypeUnavailable).Build()
	shared := errors.Join(&pointed, errors.New("ordinary error"))
	tree := errors.Join(shared, errors.Join(shared, first), last)
	if got, ok := AsAPIError(tree); !ok || got.Code() != first.Code() {
		t.Fatalf("first value = %v, %t", got, ok)
	}
}

type delegatePointerAPIError struct{ value *APIError }

func (e delegatePointerAPIError) Error() string { return "fixture" }
func (e delegatePointerAPIError) As(target any) bool {
	if ptr, ok := target.(**APIError); ok {
		*ptr = e.value
		return true
	}
	return false
}

func TestAsDelegatedPointerIsFallbackToLaterValue(t *testing.T) {
	pointer := NewErrorBuilder().Type(TypeUnavailable).Build()
	value := NewErrorBuilder().Type(TypeNotFound).Build()
	delegated := delegatePointerAPIError{value: &pointer}
	if got, ok := AsAPIError(delegated); !ok || got.Code() != pointer.Code() {
		t.Fatalf("fallback=%v", got)
	}
	if got, ok := AsAPIError(errors.Join(delegated, value)); !ok || got.Code() != value.Code() {
		t.Fatalf("preferred=%v", got)
	}
}

func TestKnownTypeNormalizesStatusAndPreservesExplicitCode(t *testing.T) {
	for _, beforeType := range []bool{true, false} {
		builder := NewErrorBuilder()
		wantCode := "FIXTURE"
		if beforeType {
			wantCode = TypeMethodNotAllowed.Meta().Code
			builder.Code("FIXTURE")
		}
		builder.Type(TypeMethodNotAllowed)
		if !beforeType {
			builder.Code("FIXTURE")
		}
		raw := builder.Build()
		raw.status = http.StatusBadRequest
		raw.category = ErrorCategory("fixture")
		extracted, ok := AsAPIError(fmt.Errorf("wrapper: %w", raw))
		if !ok || extracted.Status() != http.StatusBadRequest {
			t.Fatalf("extracted status=%d found=%t", extracted.Status(), ok)
		}
		normalized := normalizeAPIError(extracted)
		if normalized.Status() != http.StatusMethodNotAllowed || normalized.Code() != wantCode || normalized.Type() != TypeMethodNotAllowed || normalized.Category() != TypeMethodNotAllowed.Meta().Category {
			t.Fatalf("normalized=%+v ok=%t", normalized, ok)
		}
		rec := httptest.NewRecorder()
		if err := WriteError(rec, nil, extracted); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Body.String(), fmt.Sprintf("\"code\":%q", wantCode)) {
			t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
		}
	}
}
