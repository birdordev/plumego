package validate

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
)

type createItemRequest struct {
	Name string `json:"name"`
}

type requestValidator struct {
	called bool
	err    error
}

func (v *requestValidator) Validate(value any) error {
	v.called = true
	req, ok := value.(createItemRequest)
	if !ok {
		return errors.New("unexpected request type")
	}
	if v.err != nil {
		return v.err
	}
	if req.Name == "" {
		return errors.New("name is required")
	}
	return nil
}

func TestBindValidJSONWithPassingValidation(t *testing.T) {
	validator := &requestValidator{}
	req := newJSONRequest(`{"name":"widget"}`)

	got, err := Bind[createItemRequest](req, validator)
	if err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	if got.Name != "widget" {
		t.Fatalf("Bind name = %q, want widget", got.Name)
	}
	if !validator.called {
		t.Fatal("Bind did not call validator")
	}
}

func TestBindMissingRequiredFieldReturnsValidationError(t *testing.T) {
	validator := &requestValidator{}
	req := newJSONRequest(`{}`)

	_, err := Bind[createItemRequest](req, validator)
	if err == nil {
		t.Fatal("Bind returned nil error")
	}
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("Bind error = %T, want ValidationError", err)
	}
	if !validator.called {
		t.Fatal("Bind did not call validator")
	}
	if got := validationErr.Type(); got != contract.TypeBadRequest {
		t.Fatalf("ValidationError type = %q, want %q", got, contract.TypeBadRequest)
	}
	if got := validationErr.Code(); got != contract.CodeValidationError {
		t.Fatalf("ValidationError code = %q, want %q", got, contract.CodeValidationError)
	}
	if got := validationErr.Status(); got != http.StatusBadRequest {
		t.Fatalf("ValidationError status = %d, want %d", got, http.StatusBadRequest)
	}
	if !errors.Is(validationErr, validationErr.Err) {
		t.Fatal("ValidationError does not unwrap to validator error")
	}
}

func TestBindMalformedJSONReturnsBeforeValidation(t *testing.T) {
	validator := &requestValidator{}
	req := newJSONRequest(`{"name":`)

	_, err := Bind[createItemRequest](req, validator)
	if err == nil {
		t.Fatal("Bind returned nil error")
	}
	var validationErr ValidationError
	if errors.As(err, &validationErr) {
		t.Fatalf("Bind returned ValidationError for malformed JSON: %v", err)
	}
	if validator.called {
		t.Fatal("Bind called validator after malformed JSON")
	}
}

func TestBindEmptyBodyReturnsBeforeValidation(t *testing.T) {
	validator := &requestValidator{}
	req := newJSONRequest("")

	_, err := Bind[createItemRequest](req, validator)
	if err == nil {
		t.Fatal("Bind returned nil error")
	}
	if validator.called {
		t.Fatal("Bind called validator after empty body")
	}
}

func TestBindValidatorReturningNilPassesThrough(t *testing.T) {
	validator := &requestValidator{}
	req := newJSONRequest(`{"name":"widget"}`)

	got, err := Bind[createItemRequest](req, validator)
	if err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	if got.Name != "widget" {
		t.Fatalf("Bind name = %q, want widget", got.Name)
	}
	if !validator.called {
		t.Fatal("Bind did not call validator")
	}
}

func TestValidationError_Error(t *testing.T) {
	validationErr := newValidationError(errors.New("name is required"))
	got := validationErr.Error()
	if got != "name is required" {
		t.Fatalf("Error() = %q, want %q", got, "name is required")
	}
}

func TestValidationError_Error_FallsBackToAPIError(t *testing.T) {
	// Nil Err should fall back to the embedded APIError message.
	validationErr := ValidationError{
		APIError: contract.NewErrorBuilder().
			Type(contract.TypeBadRequest).
			Code(contract.CodeValidationError).
			Message("validation failed").
			Build(),
	}
	got := validationErr.Error()
	if got != "validation failed" {
		t.Fatalf("Error() = %q, want %q", got, "validation failed")
	}
}

func TestBind_NilValidator(t *testing.T) {
	req := newJSONRequest(`{"name":"widget"}`)
	got, err := Bind[createItemRequest](req, nil)
	if err != nil {
		t.Fatalf("Bind with nil validator returned error: %v", err)
	}
	if got.Name != "widget" {
		t.Fatalf("Bind name = %q, want widget", got.Name)
	}
}

func newJSONRequest(body string) *http.Request {
	req, err := http.NewRequest(http.MethodPost, "/items", strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	return req
}
