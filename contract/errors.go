package contract

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
)

// ErrorCategory describes the high-level class of an API error for observability.
type ErrorCategory string

const (
	// CategoryClient covers 4xx errors caused by client input.
	CategoryClient ErrorCategory = "client_error"
	// CategoryServer covers 5xx errors caused by infrastructure or server logic.
	CategoryServer ErrorCategory = "server_error"
	// CategoryTimeout covers timeout errors.
	CategoryTimeout ErrorCategory = "timeout_error"
	// CategoryValidation covers input validation errors.
	CategoryValidation ErrorCategory = "validation_error"
	// CategoryAuth covers authentication and authorization errors.
	CategoryAuth ErrorCategory = "auth_error"
	// CategoryRateLimit covers rate limiting errors.
	CategoryRateLimit ErrorCategory = "rate_limit_error"
)

// ErrorType represents specific error types for better categorization.
type ErrorType string

const (
	// Validation errors
	TypeValidation     ErrorType = "validation_failure"
	TypeBadRequest     ErrorType = "bad_request"
	TypeInvalidRequest ErrorType = "invalid_request"
	TypeRequired       ErrorType = "required_field_missing"
	TypeInvalidFormat  ErrorType = "invalid_format"
	TypeOutOfRange     ErrorType = "value_out_of_range"
	TypeDuplicate      ErrorType = "duplicate_value"

	// Authentication/Authorization errors
	TypeUnauthorized ErrorType = "unauthorized_request"
	TypeForbidden    ErrorType = "forbidden_request"
	TypeInvalidToken ErrorType = "invalid_token"
	TypeExpiredToken ErrorType = "expired_token"

	// Resource errors
	TypeNotFound        ErrorType = "resource_not_found"
	TypeConflict        ErrorType = "resource_conflict"
	TypeAlreadyExists   ErrorType = "resource_already_exists"
	TypeGone            ErrorType = "resource_gone"
	TypeNotAcceptable   ErrorType = "not_acceptable"
	TypePayloadTooLarge ErrorType = "payload_too_large"

	// System errors
	TypeInternal         ErrorType = "internal_error"
	TypeUnavailable      ErrorType = "service_unavailable"
	TypeTimeout          ErrorType = "timeout_failure"
	TypeRateLimited      ErrorType = "rate_limited"
	TypeMaintenance      ErrorType = "maintenance_mode"
	TypeMethodNotAllowed ErrorType = "method_not_allowed"
	TypeNotImplemented   ErrorType = "not_implemented"
	TypeBadGateway       ErrorType = "bad_gateway"
	TypeGatewayTimeout   ErrorType = "gateway_timeout"
)

// ErrorTypeMeta holds the canonical Category, Code, and HTTP status for an ErrorType.
type ErrorTypeMeta struct {
	Category ErrorCategory
	Code     string
	Status   int
}

// errorTypeLookup maps every ErrorType to its canonical metadata.
// Use ErrorType.Meta() to look up a type's defaults rather than duplicating
// switch statements across the codebase.
var errorTypeLookup = map[ErrorType]ErrorTypeMeta{
	// Validation
	TypeValidation:     {CategoryValidation, CodeValidationError, http.StatusBadRequest},
	TypeBadRequest:     {CategoryClient, CodeBadRequest, http.StatusBadRequest},
	TypeInvalidRequest: {CategoryClient, CodeInvalidRequest, http.StatusUnprocessableEntity},
	TypeRequired:       {CategoryValidation, CodeRequired, http.StatusBadRequest},
	TypeInvalidFormat:  {CategoryValidation, CodeInvalidFormat, http.StatusBadRequest},
	TypeOutOfRange:     {CategoryValidation, CodeOutOfRange, http.StatusBadRequest},
	TypeDuplicate:      {CategoryValidation, CodeDuplicate, http.StatusBadRequest},
	// Auth
	TypeUnauthorized: {CategoryAuth, CodeUnauthorized, http.StatusUnauthorized},
	TypeForbidden:    {CategoryAuth, CodeForbidden, http.StatusForbidden},
	TypeInvalidToken: {CategoryAuth, CodeInvalidToken, http.StatusUnauthorized},
	TypeExpiredToken: {CategoryAuth, CodeExpiredToken, http.StatusUnauthorized},
	// Resource
	TypeNotFound:        {CategoryClient, CodeResourceNotFound, http.StatusNotFound},
	TypeConflict:        {CategoryClient, CodeConflict, http.StatusConflict},
	TypeAlreadyExists:   {CategoryClient, CodeAlreadyExists, http.StatusConflict},
	TypeGone:            {CategoryClient, CodeGone, http.StatusGone},
	TypeNotAcceptable:   {CategoryClient, CodeNotAcceptable, http.StatusNotAcceptable},
	TypePayloadTooLarge: {CategoryClient, CodeRequestBodyTooLarge, http.StatusRequestEntityTooLarge},
	// System
	TypeInternal:         {CategoryServer, CodeInternalError, http.StatusInternalServerError},
	TypeUnavailable:      {CategoryServer, CodeUnavailable, http.StatusServiceUnavailable},
	TypeTimeout:          {CategoryTimeout, CodeTimeout, http.StatusRequestTimeout},
	TypeRateLimited:      {CategoryRateLimit, CodeRateLimited, http.StatusTooManyRequests},
	TypeMaintenance:      {CategoryServer, CodeMaintenance, http.StatusServiceUnavailable},
	TypeMethodNotAllowed: {CategoryClient, CodeMethodNotAllowed, http.StatusMethodNotAllowed},
	TypeNotImplemented:   {CategoryServer, CodeNotImplemented, http.StatusNotImplemented},
	TypeBadGateway:       {CategoryServer, CodeBadGateway, http.StatusBadGateway},
	TypeGatewayTimeout:   {CategoryTimeout, CodeGatewayTimeout, http.StatusGatewayTimeout},
}

var categoryByHTTPStatus = map[int]ErrorCategory{
	http.StatusUnauthorized:        CategoryAuth,
	http.StatusForbidden:           CategoryAuth,
	http.StatusTooManyRequests:     CategoryRateLimit,
	http.StatusRequestTimeout:      CategoryTimeout,
	http.StatusGatewayTimeout:      CategoryTimeout,
	http.StatusBadRequest:          CategoryClient,
	http.StatusNotFound:            CategoryClient,
	http.StatusConflict:            CategoryClient,
	http.StatusUnprocessableEntity: CategoryClient,
}

var codeByHTTPStatus = map[int]string{
	http.StatusBadRequest:            CodeBadRequest,
	http.StatusUnauthorized:          CodeUnauthorized,
	http.StatusForbidden:             CodeForbidden,
	http.StatusNotFound:              CodeResourceNotFound,
	http.StatusMethodNotAllowed:      CodeMethodNotAllowed,
	http.StatusConflict:              CodeConflict,
	http.StatusGone:                  CodeGone,
	http.StatusRequestEntityTooLarge: CodeRequestBodyTooLarge,
	http.StatusUnprocessableEntity:   CodeInvalidRequest,
	http.StatusTooManyRequests:       CodeRateLimited,
	http.StatusRequestTimeout:        CodeTimeout,
	http.StatusNotImplemented:        CodeNotImplemented,
	http.StatusBadGateway:            CodeBadGateway,
	http.StatusGatewayTimeout:        CodeGatewayTimeout,
	http.StatusServiceUnavailable:    CodeUnavailable,
}

// Meta returns the canonical Category, Code, and HTTP status for the ErrorType.
// If the type is unrecognized, it returns server-error defaults.
func (t ErrorType) Meta() ErrorTypeMeta {
	if m, ok := errorTypeLookup[t]; ok {
		return m
	}
	return ErrorTypeMeta{CategoryServer, CodeInternalError, http.StatusInternalServerError}
}

// APIError represents a normalized error payload for HTTP responses and logging.
//
// Callers outside this package should build APIError values through
// NewErrorBuilder(), rather than struct literals, to guarantee that all
// required fields are populated consistently. APIError is intentionally opaque;
// use its read-only methods to inspect normalized values.
//
// APIError implements the error interface and participates in errors.As/Is chains:
//
//	var target contract.APIError
//	if errors.As(err, &target) { ... }
//
// Use ErrorBuilder.Wrap to attach a diagnostic cause; the cause is available via
// errors.Unwrap and is never included in the JSON response.
type APIError struct {
	status       int
	code         string
	message      string
	category     ErrorCategory
	errorType    ErrorType
	requestID    string
	details      map[string]any
	cause        error // not serialized; accessible via errors.Unwrap
	isNormalized bool
}

// Error implements the error interface for APIError
func (e APIError) Error() string {
	return e.message
}

// Unwrap returns the underlying cause error stored via ErrorBuilder.Wrap,
// enabling APIError to participate in errors.Is and errors.As chains.
// The cause is never included in the JSON response.
func (e APIError) Unwrap() error {
	return e.cause
}

// Status returns the normalized HTTP status associated with the error.
func (e APIError) Status() int {
	if e.isNormalized {
		return e.status
	}
	return normalizeAPIError(e).status
}

// Code returns the normalized machine-readable error code.
func (e APIError) Code() string {
	if e.isNormalized {
		return e.code
	}
	return normalizeAPIError(e).code
}

// Message returns the normalized client-facing error message.
func (e APIError) Message() string {
	if e.isNormalized {
		return e.message
	}
	return normalizeAPIError(e).message
}

// Category returns the normalized high-level error category.
func (e APIError) Category() ErrorCategory {
	if e.isNormalized {
		return e.category
	}
	return normalizeAPIError(e).category
}

// Type returns the normalized error type, or an empty value for untyped errors.
func (e APIError) Type() ErrorType {
	if e.isNormalized {
		return e.errorType
	}
	return normalizeAPIError(e).errorType
}

// RequestID returns the normalized request id associated with the error.
func (e APIError) RequestID() string {
	if e.isNormalized {
		return e.requestID
	}
	return normalizeAPIError(e).requestID
}

// Details returns an isolated copy of the error detail map.
// Scalar values, slices, and nested maps are deep-copied. Struct values stored
// in the map are returned as interface copies and do not have their internal
// reference fields deep-copied.
func (e APIError) Details() map[string]any {
	if e.isNormalized {
		return cloneAnyMap(e.details)
	}
	return cloneAnyMap(normalizeAPIError(e).details)
}

func categoryForStatus(status int) ErrorCategory {
	if category, ok := categoryByHTTPStatus[status]; ok {
		return category
	}
	if status >= http.StatusInternalServerError {
		return CategoryServer
	}
	if status >= http.StatusBadRequest {
		return CategoryClient
	}
	return ""
}

// ErrorBuilder provides a fluent builder for creating APIError instances.
type ErrorBuilder struct {
	err APIError
}

// NewErrorBuilder creates a new error builder with default values.
func NewErrorBuilder() *ErrorBuilder {
	return &ErrorBuilder{
		err: APIError{
			status:  http.StatusInternalServerError,
			details: make(map[string]any),
		},
	}
}

// Message sets the error message for the error.
func (b *ErrorBuilder) Message(message string) *ErrorBuilder {
	b.err.message = message
	return b
}

// Code sets an extension-owned machine code while preserving the type's
// canonical status and category.
func (b *ErrorBuilder) Code(code string) *ErrorBuilder {
	b.err.code = code
	return b
}

// Type sets the error type and populates Category, Code, and Status with the
// canonical values for that type. Build normalizes typed errors back to the
// type's canonical Status and Category, so callers may only customize Code
// and other non-classification fields after Type.
func (b *ErrorBuilder) Type(errorType ErrorType) *ErrorBuilder {
	if errorType == "" {
		return b
	}
	meta := errorType.Meta()
	b.err.errorType = errorType
	b.err.category = meta.Category
	b.err.code = meta.Code
	b.err.status = meta.Status
	return b
}

// RequestID sets the request id for the error.
func (b *ErrorBuilder) RequestID(requestID string) *ErrorBuilder {
	if requestID, ok := normalizeRequestID(requestID); ok {
		b.err.requestID = requestID
	} else {
		b.err.requestID = ""
	}
	return b
}

// Wrap stores cause as the underlying error for diagnostic purposes.
// The cause is not included in the JSON response and is accessible only
// via errors.Unwrap, errors.Is, or errors.As on the built APIError.
func (b *ErrorBuilder) Wrap(cause error) *ErrorBuilder {
	b.err.cause = cause
	return b
}

// Detail adds a detail field to the error.
func (b *ErrorBuilder) Detail(key string, value any) *ErrorBuilder {
	if key == "" {
		return b
	}
	b.ensureDetails()
	b.err.details[key] = value
	return b
}

// Details sets multiple detail fields for the error.
func (b *ErrorBuilder) Details(details map[string]any) *ErrorBuilder {
	b.ensureDetails()
	for k, v := range details {
		if k == "" {
			continue
		}
		b.err.details[k] = v
	}
	return b
}

func (b *ErrorBuilder) ensureDetails() {
	if b.err.details == nil {
		b.err.details = make(map[string]any)
	}
}

// Build creates the final APIError instance.
// It fills any missing status, code, and category with safe defaults so that
// every value returned by a builder is fully populated.
func (b *ErrorBuilder) Build() APIError {
	return normalizeAPIError(b.err)
}

// AsAPIError unwraps err using errors.As and reports whether an APIError was found.
// It supports errors wrapped with fmt.Errorf("...: %w", apiErr).
func AsAPIError(err error) (APIError, bool) {
	var target APIError
	if errors.As(err, &target) {
		return target, true
	}
	return APIError{}, false
}

func normalizeAPIError(err APIError) APIError {
	err, invalidStatus := normalizeAPIErrorBase(err)
	err, typed := normalizeTypedAPIError(err)
	if !typed {
		normalizeUntypedAPIError(&err, invalidStatus)
	}
	applyDefaultAPIErrorMessage(&err)
	err.isNormalized = true
	return err
}

func normalizeAPIErrorBase(err APIError) (APIError, bool) {
	status, invalid := normalizeErrorHTTPStatus(err.status)
	err.status = status
	err.details = cloneAnyMap(err.details)
	err.requestID = normalizeRequestIDOrEmpty(err.requestID)
	return err, invalid
}

func normalizeTypedAPIError(err APIError) (APIError, bool) {
	if err.errorType != "" {
		if meta, ok := errorTypeLookup[err.errorType]; ok {
			err.status = meta.Status
			err.category = meta.Category
			if err.code == "" {
				err.code = meta.Code
			}
			return err, true
		} else {
			err.errorType = ""
		}
	}
	return err, false
}

func normalizeUntypedAPIError(err *APIError, invalidStatus bool) {
	if invalidStatus {
		err.category = CategoryServer
		if err.code == "" {
			err.code = CodeInternalError
		}
	} else if err.code == "" {
		err.code = codeForStatus(err.status)
	}

	if err.category == "" {
		err.category = categoryForStatus(err.status)
		if err.category == "" {
			err.category = CategoryServer
		}
	}
}

func normalizeRequestIDOrEmpty(requestID string) string {
	if requestID, ok := normalizeRequestID(requestID); ok {
		return requestID
	}
	return ""
}

func applyDefaultAPIErrorMessage(err *APIError) {
	if err.message == "" {
		err.message = http.StatusText(err.status)
	}
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	cloner := make(detailCloner)
	out := cloner.cloneMap(in)
	if len(out) == 0 {
		return nil
	}
	return out
}

// detailCloner tracks containers on the active path so cycles are preserved
// for the JSON encoder to reject, without sharing those containers with input.
type detailCloner map[detailReference]any

type detailReference struct {
	typ reflect.Type
	ptr uintptr
	len int
}

func (c detailCloner) cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	key := detailReference{typ: reflect.TypeFor[map[string]any](), ptr: reflect.ValueOf(in).Pointer()}
	if existing, ok := c[key]; ok {
		return existing.(map[string]any)
	}
	out := make(map[string]any, len(in))
	c[key] = out
	for k, v := range in {
		if k != "" {
			out[k] = c.cloneValue(v)
		}
	}
	delete(c, key)
	return out
}

func cloneDetailSlice[T any](c detailCloner, in []T, clone func(T) T) []T {
	if in == nil {
		return nil
	}
	key := detailReference{typ: reflect.TypeFor[[]T](), ptr: reflect.ValueOf(in).Pointer(), len: len(in)}
	if existing, ok := c[key]; ok {
		return existing.([]T)
	}
	out := make([]T, len(in))
	c[key] = out
	for i, item := range in {
		out[i] = clone(item)
	}
	delete(c, key)
	return out
}

func (c detailCloner) cloneValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return c.cloneMap(v)
	case []any:
		return cloneDetailSlice(c, v, c.cloneValue)
	case []map[string]any:
		return cloneDetailSlice(c, v, c.cloneMap)
	case []string:
		return slices.Clone(v)
	case []int:
		return slices.Clone(v)
	case []int64:
		return slices.Clone(v)
	case []float64:
		return slices.Clone(v)
	case []bool:
		return slices.Clone(v)
	default:
		if cloned, ok := cloneReflectDetailValue(value); ok {
			return cloned
		}
		return value
	}
}

func cloneReflectDetailValue(value any) (any, bool) {
	v := reflect.ValueOf(value)
	if !v.IsValid() {
		return value, true
	}
	cloned, ok := cloneReflectValue(v, 0)
	if !ok {
		return value, false
	}
	return cloned.Interface(), true
}

func cloneReflectValue(v reflect.Value, depth int) (reflect.Value, bool) {
	if depth > 16 {
		return v, false
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type()), true
		}
		cloned, ok := cloneReflectValue(v.Elem(), depth+1)
		if !ok {
			return v, false
		}
		if cloned.Type().AssignableTo(v.Type()) {
			return cloned, true
		}
		if cloned.Type().AssignableTo(v.Type().Elem()) {
			out := reflect.New(v.Type()).Elem()
			out.Set(cloned)
			return out, true
		}
		return v, false
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return v, false
		}
		if v.IsNil() {
			return reflect.Zero(v.Type()), true
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			cloned, ok := cloneReflectValue(iter.Value(), depth+1)
			if !ok {
				return v, false
			}
			cloned, ok = makeAssignable(cloned, v.Type().Elem())
			if !ok {
				return v, false
			}
			out.SetMapIndex(iter.Key(), cloned)
		}
		return out, true
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type()), true
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			cloned, ok := cloneReflectValue(v.Index(i), depth+1)
			if !ok {
				return v, false
			}
			cloned, ok = makeAssignable(cloned, v.Type().Elem())
			if !ok {
				return v, false
			}
			out.Index(i).Set(cloned)
		}
		return out, true
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			cloned, ok := cloneReflectValue(v.Index(i), depth+1)
			if !ok {
				return v, false
			}
			cloned, ok = makeAssignable(cloned, v.Type().Elem())
			if !ok {
				return v, false
			}
			out.Index(i).Set(cloned)
		}
		return out, true
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return v, true
	default:
		return v, false
	}
}

func makeAssignable(v reflect.Value, target reflect.Type) (reflect.Value, bool) {
	if v.Type().AssignableTo(target) {
		return v, true
	}
	if v.Type().ConvertibleTo(target) {
		return v.Convert(target), true
	}
	return v, false
}

func normalizeErrorHTTPStatus(status int) (int, bool) {
	if status == 0 {
		return http.StatusInternalServerError, false
	}
	if status < http.StatusBadRequest || status > 599 {
		return http.StatusInternalServerError, true
	}
	return status, false
}

func codeForStatus(status int) string {
	if code, ok := codeByHTTPStatus[status]; ok {
		return code
	}
	if status >= http.StatusInternalServerError {
		return CodeInternalError
	}
	if status >= http.StatusBadRequest {
		return CodeInvalidRequest
	}
	return CodeInternalError
}
