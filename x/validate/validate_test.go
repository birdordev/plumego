package validate

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestBindReturnsEOFForMissingRequestBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		body io.ReadCloser
	}{{"nil body", nil}, {"NoBody", http.NoBody}} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://example.invalid", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Body = tc.body
			if out, err := BindJSON[createItemRequest](req); out != (createItemRequest{}) || !errors.Is(err, io.EOF) {
				t.Fatalf("BindJSON = %+v, %v, want zero value and EOF", out, err)
			}
			validator := &requestValidator{}
			if _, err := Bind[createItemRequest](req, validator); !errors.Is(err, io.EOF) || validator.called {
				t.Errorf("Bind error = %v, validator called = %v, want EOF before validation", err, validator.called)
			}
		})
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

func TestBindValidationErrorHTTPResponse(t *testing.T) {
	sentinel := errors.New("test validator detail")
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := Bind[createItemRequest](r, &requestValidator{err: sentinel})
		var validationErr ValidationError
		if !errors.Is(err, sentinel) || !errors.As(err, &validationErr) {
			t.Fatalf("Bind = %v, want ValidationError wrapping the validator error", err)
		}
		if err := contract.WriteError(w, r, validationErr.APIError); err != nil {
			t.Fatal(err)
		}
	})
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"item"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var response struct {
		Error struct {
			Code, Message, Category, Type string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	got := response.Error
	if w.Code != http.StatusBadRequest || got.Code != contract.CodeValidationError || got.Message != "validation failed" ||
		got.Category != "client_error" || got.Type != "bad_request" || strings.Contains(w.Body.String(), sentinel.Error()) {
		t.Errorf("code = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestBindPreservesPartialValueOnTypeError(t *testing.T) {
	type request struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	validator := &requestValidator{}
	value, err := Bind[request](newJSONRequest(`{"name":"item","count":"unknown"}`), validator)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) || typeErr.Field != "count" || value.Name != "item" || value.Count != 0 || validator.called {
		t.Fatalf("Bind = %+v, %v; validator called = %t", value, err, validator.called)
	}
}
