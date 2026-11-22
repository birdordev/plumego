package validate

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

type capturingRequestValidator struct {
	inputs []any
	err    error
}

func (v *capturingRequestValidator) Validate(input any) error {
	v.inputs = append(v.inputs, input)
	return v.err
}

func TestBindReadsOnlyFirstJSONValue(t *testing.T) {
	const body = `{"name":"first"} {"name":"second"}`
	first := createItemRequest{Name: "first"}
	if got, err := BindJSON[createItemRequest](newJSONRequest(body)); err != nil || got != first {
		t.Errorf("BindJSON = %+v, %v; want %+v", got, err, first)
	}
	validator := &capturingRequestValidator{}
	if got, err := Bind[createItemRequest](newJSONRequest(body), validator); err != nil || got != first {
		t.Errorf("Bind = %+v, %v; want %+v", got, err, first)
	}
	if len(validator.inputs) != 1 || validator.inputs[0] != first {
		t.Errorf("validator inputs = %v", validator.inputs)
	}
}

func TestBindPreservesCallerBodyLimitError(t *testing.T) {
	const limit = 8
	newRequest := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"long value"}`))
		req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, limit)
		return req
	}
	validator := &requestValidator{}
	_, jsonErr := BindJSON[createItemRequest](newRequest())
	_, bindErr := Bind[createItemRequest](newRequest(), validator)
	for _, err := range []error{jsonErr, bindErr} {
		var sizeErr *http.MaxBytesError
		if !errors.As(err, &sizeErr) || sizeErr.Limit != limit {
			t.Errorf("body error: %v", err)
		}
	}
	if validator.called {
		t.Fatal("validator ran on an incomplete body")
	}
}

func TestBindPointerTargetsAndNull(t *testing.T) {
	const body = `{"name":"widget"}`
	failure := errors.New("validator declined pointer input")
	validator := &capturingRequestValidator{err: failure}
	value, err := Bind[*createItemRequest](newJSONRequest(body), validator)
	if value == nil || value.Name != "widget" || len(validator.inputs) != 1 || validator.inputs[0] != value {
		t.Fatalf("pointer Bind = %+v, inputs = %v", value, validator.inputs)
	}
	var validationErr ValidationError
	if !errors.Is(err, failure) || !errors.As(err, &validationErr) || validationErr.Code() != contract.CodeValidationError {
		t.Fatalf("Bind = %v, want classified validation error", err)
	}

	validator = &capturingRequestValidator{}
	value, err = Bind[*createItemRequest](newJSONRequest("null"), validator)
	if value != nil || err != nil || len(validator.inputs) != 1 {
		t.Fatalf("null Bind = %+v, %v; inputs = %v", value, err, validator.inputs)
	}
	if pointer, ok := validator.inputs[0].(*createItemRequest); !ok || pointer != nil {
		t.Fatalf("validator received %v (%T), want a nil *createItemRequest", pointer, validator.inputs[0])
	}
}

type customJSONError string

func (e customJSONError) Error() string { return string(e) }

const decoderRejected customJSONError = "custom decoder rejected fixture"

type customJSONRequest struct{}

func (*customJSONRequest) UnmarshalJSON([]byte) error {
	return decoderRejected
}

func TestBindPreservesCustomUnmarshalError(t *testing.T) {
	validator := &requestValidator{}
	_, jsonErr := BindJSON[customJSONRequest](newJSONRequest(`{}`))
	_, bindErr := Bind[customJSONRequest](newJSONRequest(`{}`), validator)
	for _, err := range []error{jsonErr, bindErr} {
		var validationErr ValidationError
		if !errors.Is(err, decoderRejected) || errors.As(err, &validationErr) {
			t.Fatalf("decode error = %v (%T)", err, err)
		}
	}
	if validator.called {
		t.Fatal("validator ran after decoder failure")
	}
}

func TestBindStrictValidatesOnlyOneCompleteJSONDocument(t *testing.T) {
	for _, tc := range []struct {
		body, media   string
		status        int
		wantValidator bool
	}{
		{`{"name":"widget"}`, "application/json", 200, true},
		{`{"name":"widget"} {}`, "application/json", 400, false},
		{`null`, "application/json", 400, false},
		{`{"name":"widget"}`, "text/plain", 415, false},
		{`{"name":"exceeds the configured byte limit"}`, "application/json", 413, false},
		{`{}`, "application/json", 400, true},
	} {
		r := newJSONRequest(tc.body)
		r.Header.Set("Content-Type", tc.media)
		validator := &requestValidator{}
		out, err := BindStrict[createItemRequest](r, validator, contract.JSONRequestOptions{MaxBytes: 32})
		if validator.called != tc.wantValidator {
			t.Errorf("validator called=%v for %q, want %v", validator.called, tc.body, tc.wantValidator)
		}
		if tc.status == 200 {
			if err != nil || out.Name != "widget" {
				t.Errorf("valid JSON: value=%+v err=%v", out, err)
			}
			continue
		}
		apiErr, ok := contract.AsAPIError(err)
		if tc.wantValidator {
			var validationErr ValidationError
			if !errors.As(err, &validationErr) {
				t.Errorf("validator error missing: %v", err)
			}
			apiErr, ok = validationErr.APIError, true
		}
		if !ok || apiErr.Status() != tc.status {
			t.Errorf("body %q: got %v, want HTTP error %d", tc.body, err, tc.status)
		}
	}
}

type repeatedFormValue struct{}

func (repeatedFormValue) Error() string { return "repeated value" }
func (repeatedFormValue) FieldViolations() []contract.FieldViolation {
	return []contract.FieldViolation{{Source: "body", Field: "name", Code: "duplicate"}}
}

func TestBindFormKeepsSourcesSeparateAndPreservesFieldErrors(t *testing.T) {
	body := "name=&name=second"
	r := httptest.NewRequest(http.MethodPost, "/?name=query&unrelated=true", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	err := BindForm(httptest.NewRecorder(), r, 64, func(values url.Values) error {
		if names := values["name"]; len(names) != 2 || names[0] != "" || names[1] != "second" || values.Has("unrelated") {
			t.Fatalf("body values = %v", values)
		}
		return repeatedFormValue{}
	})
	api, ok := contract.AsAPIError(err)
	if !ok || api.Code() != contract.CodeValidationError || api.Details()["fields"] == nil {
		t.Fatalf("validation error=%v", err)
	}
	if err := BindQuery(r, func(values url.Values) error {
		if values.Get("name") != "query" {
			t.Fatalf("query=%v", values)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.ContentLength = -1
	if api, ok := contract.AsAPIError(BindForm(httptest.NewRecorder(), r, 4, nil)); !ok || api.Status() != 413 {
		t.Fatalf("oversize = %v", api)
	}
}
