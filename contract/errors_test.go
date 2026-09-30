package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorBuilder(t *testing.T) {
	builder := NewErrorBuilder()

	err := builder.
		Type(TypeValidation).
		Code(CodeValidationError).
		Message("test error message").
		Detail("field", "email").
		Detail("value", "invalid").
		Build()

	if err.status != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, err.status)
	}

	if err.category != CategoryValidation {
		t.Fatalf("expected category %s, got %s", CategoryValidation, err.category)
	}

	if err.code != CodeValidationError {
		t.Fatalf("expected code %s, got %s", CodeValidationError, err.code)
	}

	if err.message != "test error message" {
		t.Fatalf("expected message %s, got %s", "test error message", err.message)
	}

	if err.details["field"] != "email" {
		t.Fatalf("expected field detail, got %v", err.details["field"])
	}
}

func TestBuilderTypeOverwritesPriorFields(t *testing.T) {
	got := NewErrorBuilder().
		Code(CodeInternalError).
		Type(TypeNotFound).
		Build()

	if got.status != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, got.status)
	}
	if got.code != CodeResourceNotFound {
		t.Fatalf("expected code %q, got %q", CodeResourceNotFound, got.code)
	}
	if got.category != CategoryClient {
		t.Fatalf("expected category %q, got %q", CategoryClient, got.category)
	}
}

func TestBuilderTypeStatusAndCategoryRemainCanonical(t *testing.T) {
	got := NewErrorBuilder().
		Type(TypeNotFound).
		Code("CUSTOM_NOT_FOUND").
		Build()

	if got.status != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, got.status)
	}
	if got.category != CategoryClient {
		t.Fatalf("expected category %q, got %q", CategoryClient, got.category)
	}
	if got.code != "CUSTOM_NOT_FOUND" {
		t.Fatalf("expected custom code to be preserved, got %q", got.code)
	}
}

func TestErrorTypeMetaIsNameable(t *testing.T) {
	// The explicit ErrorTypeMeta declaration is the point of this test:
	// Meta() results must be assignable to a named ErrorTypeMeta variable.
	//nolint:staticcheck // S1021: the standalone typed declaration is the assertion under test
	var meta ErrorTypeMeta
	meta = TypeNotFound.Meta()
	if meta.Status != http.StatusNotFound {
		t.Fatalf("status=%d, want %d", meta.Status, http.StatusNotFound)
	}
	if meta.Code != CodeResourceNotFound {
		t.Fatalf("code=%q, want %q", meta.Code, CodeResourceNotFound)
	}
	if meta.Category != CategoryClient {
		t.Fatalf("category=%q, want %q", meta.Category, CategoryClient)
	}
}

func TestGatewayTimeoutErrorTypeMeta(t *testing.T) {
	meta := TypeGatewayTimeout.Meta()
	if meta.Status != http.StatusGatewayTimeout {
		t.Fatalf("status=%d, want %d", meta.Status, http.StatusGatewayTimeout)
	}
	if meta.Code != CodeGatewayTimeout {
		t.Fatalf("code=%q, want %q", meta.Code, CodeGatewayTimeout)
	}
	if meta.Category != CategoryTimeout {
		t.Fatalf("category=%q, want %q", meta.Category, CategoryTimeout)
	}
}

func TestErrorTypeTaxonomyMatrix(t *testing.T) {
	cases := []struct {
		errorType ErrorType
		category  ErrorCategory
		code      string
		status    int
	}{
		{TypeValidation, CategoryValidation, CodeValidationError, http.StatusBadRequest},
		{TypeBadRequest, CategoryClient, CodeBadRequest, http.StatusBadRequest},
		{TypeInvalidRequest, CategoryClient, CodeInvalidRequest, http.StatusUnprocessableEntity},
		{TypeRequired, CategoryValidation, CodeRequired, http.StatusBadRequest},
		{TypeInvalidFormat, CategoryValidation, CodeInvalidFormat, http.StatusBadRequest},
		{TypeOutOfRange, CategoryValidation, CodeOutOfRange, http.StatusBadRequest},
		{TypeDuplicate, CategoryValidation, CodeDuplicate, http.StatusBadRequest},
		{TypeUnauthorized, CategoryAuth, CodeUnauthorized, http.StatusUnauthorized},
		{TypeForbidden, CategoryAuth, CodeForbidden, http.StatusForbidden},
		{TypeInvalidToken, CategoryAuth, CodeInvalidToken, http.StatusUnauthorized},
		{TypeExpiredToken, CategoryAuth, CodeExpiredToken, http.StatusUnauthorized},
		{TypeNotFound, CategoryClient, CodeResourceNotFound, http.StatusNotFound},
		{TypeConflict, CategoryClient, CodeConflict, http.StatusConflict},
		{TypeAlreadyExists, CategoryClient, CodeAlreadyExists, http.StatusConflict},
		{TypeGone, CategoryClient, CodeGone, http.StatusGone},
		{TypeNotAcceptable, CategoryClient, CodeNotAcceptable, http.StatusNotAcceptable},
		{TypePayloadTooLarge, CategoryClient, CodeRequestBodyTooLarge, http.StatusRequestEntityTooLarge},
		{TypeInternal, CategoryServer, CodeInternalError, http.StatusInternalServerError},
		{TypeUnavailable, CategoryServer, CodeUnavailable, http.StatusServiceUnavailable},
		{TypeTimeout, CategoryTimeout, CodeTimeout, http.StatusRequestTimeout},
		{TypeRateLimited, CategoryRateLimit, CodeRateLimited, http.StatusTooManyRequests},
		{TypeMaintenance, CategoryServer, CodeMaintenance, http.StatusServiceUnavailable},
		{TypeMethodNotAllowed, CategoryClient, CodeMethodNotAllowed, http.StatusMethodNotAllowed},
		{TypeNotImplemented, CategoryServer, CodeNotImplemented, http.StatusNotImplemented},
		{TypeBadGateway, CategoryServer, CodeBadGateway, http.StatusBadGateway},
		{TypeGatewayTimeout, CategoryTimeout, CodeGatewayTimeout, http.StatusGatewayTimeout},
	}

	if len(cases) != len(errorTypeLookup) {
		t.Fatalf("taxonomy cases=%d, lookup entries=%d", len(cases), len(errorTypeLookup))
	}

	for _, tc := range cases {
		t.Run(string(tc.errorType), func(t *testing.T) {
			meta := tc.errorType.Meta()
			if meta.Category != tc.category || meta.Code != tc.code || meta.Status != tc.status {
				t.Fatalf("Meta() = {category:%q code:%q status:%d}, want {category:%q code:%q status:%d}",
					meta.Category, meta.Code, meta.Status, tc.category, tc.code, tc.status)
			}

			got := NewErrorBuilder().Type(tc.errorType).Build()
			if got.category != tc.category || got.code != tc.code || got.status != tc.status {
				t.Fatalf("builder = {category:%q code:%q status:%d}, want {category:%q code:%q status:%d}",
					got.category, got.code, got.status, tc.category, tc.code, tc.status)
			}
		})
	}
}

func TestStatusCategoryFallbackDoesNotReplaceErrorTypeMeta(t *testing.T) {
	tests := []struct {
		name           string
		errorType      ErrorType
		metaStatus     int
		metaCategory   ErrorCategory
		statusCategory ErrorCategory
	}{
		{
			name:           "validation type is more precise than generic 422 status",
			errorType:      TypeValidation,
			metaStatus:     http.StatusBadRequest,
			metaCategory:   CategoryValidation,
			statusCategory: CategoryClient,
		},
		{
			name:           "gateway timeout type keeps 504 while status fallback only categorizes it",
			errorType:      TypeGatewayTimeout,
			metaStatus:     http.StatusGatewayTimeout,
			metaCategory:   CategoryTimeout,
			statusCategory: CategoryTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := tt.errorType.Meta()
			if meta.Status != tt.metaStatus || meta.Category != tt.metaCategory {
				t.Fatalf("Meta() = {status:%d category:%q}, want {status:%d category:%q}",
					meta.Status, meta.Category, tt.metaStatus, tt.metaCategory)
			}

			if got := categoryForStatus(http.StatusUnprocessableEntity); tt.errorType == TypeValidation && got != tt.statusCategory {
				t.Fatalf("categoryForStatus(422) = %q, want %q", got, tt.statusCategory)
			}
			if got := categoryForStatus(tt.metaStatus); tt.errorType == TypeGatewayTimeout && got != tt.statusCategory {
				t.Fatalf("categoryForStatus(%d) = %q, want %q", tt.metaStatus, got, tt.statusCategory)
			}
		})
	}
}

func TestUnknownErrorTypeMetaFailsClosed(t *testing.T) {
	meta := ErrorType("extension_unknown").Meta()
	if meta.Category != CategoryServer {
		t.Fatalf("category=%q, want %q", meta.Category, CategoryServer)
	}
	if meta.Code != CodeInternalError {
		t.Fatalf("code=%q, want %q", meta.Code, CodeInternalError)
	}
	if meta.Status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", meta.Status, http.StatusInternalServerError)
	}
}

func TestNormalizeTypedAPIErrorKeepsCanonicalStatusAndCategory(t *testing.T) {
	got := normalizeAPIError(APIError{
		status:    http.StatusConflict,
		code:      "CUSTOM_NOT_FOUND",
		message:   "custom not found",
		category:  CategoryServer,
		errorType: TypeNotFound,
	})

	if got.status != http.StatusNotFound {
		t.Fatalf("status=%d, want %d", got.status, http.StatusNotFound)
	}
	if got.category != CategoryClient {
		t.Fatalf("category=%s, want %s", got.category, CategoryClient)
	}
	if got.code != "CUSTOM_NOT_FOUND" {
		t.Fatalf("code=%s, want %s", got.code, "CUSTOM_NOT_FOUND")
	}
	if got.errorType != TypeNotFound {
		t.Fatalf("type=%s, want %s", got.errorType, TypeNotFound)
	}
}

func TestErrorBuilderChaining(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeNotFound).
		Code(CodeResourceNotFound).
		Message("resource not found").
		Detail("resource", "user").
		Detail("id", "123").
		Build()

	if err.status != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, err.status)
	}

	if err.details["resource"] != "user" || err.details["id"] != "123" {
		t.Fatalf("expected details to be set, got %v", err.details)
	}
}

func TestCommonErrorBuilders(t *testing.T) {
	// Test validation error via builder
	valErr := NewErrorBuilder().
		Type(TypeValidation).
		Code(CodeValidationError).
		Message("validation failed for field 'email': invalid format").
		Detail("field", "email").
		Detail("validation_message", "invalid format").
		Build()
	if valErr.category != CategoryValidation {
		t.Fatalf("expected validation category")
	}
	if valErr.details["field"] != "email" {
		t.Fatalf("expected field detail")
	}

	// Test not found error via builder
	notFoundErr := NewErrorBuilder().
		Type(TypeNotFound).
		Code(CodeResourceNotFound).
		Message("resource 'user' not found").
		Detail("resource", "user").
		Build()
	if notFoundErr.category != CategoryClient {
		t.Fatalf("expected client category for not found")
	}
	if notFoundErr.details["resource"] != "user" {
		t.Fatalf("expected resource detail")
	}

	// Test unauthorized error via builder
	authErr := NewErrorBuilder().
		Type(TypeUnauthorized).
		Code(CodeUnauthorized).
		Message("invalid token").
		Build()
	if authErr.category != CategoryAuth {
		t.Fatalf("expected authentication category")
	}

	// Test timeout error via builder
	timeoutErr := NewErrorBuilder().
		Type(TypeTimeout).
		Code(CodeTimeout).
		Message("database timeout").
		Build()
	if timeoutErr.category != CategoryTimeout {
		t.Fatalf("expected timeout category")
	}

	// Test rate limit error via builder
	rateLimitErr := NewErrorBuilder().
		Type(TypeRateLimited).
		Code(CodeRateLimited).
		Message("too many requests").
		Build()
	if rateLimitErr.category != CategoryRateLimit {
		t.Fatalf("expected rate limit category")
	}
}

func TestErrorResponseWriting(t *testing.T) {
	// Test WriteError function
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	err := APIError{
		status:   http.StatusBadRequest,
		code:     CodeValidationError,
		message:  "validation failed",
		category: CategoryValidation,
		details:  map[string]any{"field": "email"},
	}

	_ = WriteError(recorder, req, err)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, recorder.Code)
	}

	if ct := recorder.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected content type application/json, got %s", ct)
	}

	var response errorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response.Error.Code != CodeValidationError {
		t.Fatalf("expected code in response")
	}
}

func TestErrorResponseWithRequestID(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithRequestID(req.Context(), "req-123")
	req = req.WithContext(ctx)

	err := APIError{
		status:   http.StatusInternalServerError,
		code:     CodeInternalError,
		message:  "internal server error",
		category: CategoryServer,
	}

	_ = WriteError(recorder, req, err)

	var response errorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response.RequestID != "req-123" {
		t.Fatalf("expected request id in response")
	}
}

func TestWriteErrorPreservesRequestID(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithRequestID(req.Context(), "context-req-id")
	req = req.WithContext(ctx)

	err := APIError{
		status:    http.StatusBadRequest,
		code:      CodeValidationError,
		message:   "validation failed",
		category:  CategoryValidation,
		requestID: "explicit-req-id",
	}

	_ = WriteError(recorder, req, err)

	var response errorResponse
	if decodeErr := json.NewDecoder(recorder.Body).Decode(&response); decodeErr != nil {
		t.Fatalf("failed to decode response: %v", decodeErr)
	}

	if response.RequestID != "explicit-req-id" {
		t.Fatalf("expected explicit request id to be preserved")
	}
}

func TestWriteErrorNormalizesExplicitRequestID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "trim", id: " explicit-req-id ", want: "explicit-req-id"},
		{name: "unsafe", id: "bad\nrequest", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)

			_ = WriteError(recorder, req, APIError{
				status:    http.StatusBadRequest,
				code:      CodeValidationError,
				message:   "validation failed",
				category:  CategoryValidation,
				requestID: tt.id,
			})

			var response errorResponse
			if decodeErr := json.NewDecoder(recorder.Body).Decode(&response); decodeErr != nil {
				t.Fatalf("failed to decode response: %v", decodeErr)
			}
			if response.RequestID != tt.want {
				t.Fatalf("expected request id %q, got %q", tt.want, response.RequestID)
			}
		})
	}
}

func TestErrorBuilderRequestIDNormalizesSafety(t *testing.T) {
	got := NewErrorBuilder().RequestID(" req-123 ").Build()
	if got.requestID != "req-123" {
		t.Fatalf("expected trimmed request id, got %q", got.requestID)
	}

	got = NewErrorBuilder().RequestID("bad\trequest").Build()
	if got.requestID != "" {
		t.Fatalf("expected unsafe request id to be dropped, got %q", got.requestID)
	}
}

func TestWriteErrorDefaults(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	// Test with minimal error (no status, code, or category)
	err := APIError{message: "test message"}

	_ = WriteError(recorder, req, err)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected default status to be internal server error")
	}

	var response errorResponse
	_ = json.NewDecoder(recorder.Body).Decode(&response)

	if response.Error.Code != CodeInternalError {
		t.Fatalf("expected default code %q, got %q", CodeInternalError, response.Error.Code)
	}

	if response.Error.Category != CategoryServer {
		t.Fatalf("expected default category to be server")
	}
}

func TestWriteErrorDefaultsMessage(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	_ = WriteError(recorder, req, APIError{})

	var response errorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if response.Error.Message != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("expected default message %q, got %q", http.StatusText(http.StatusInternalServerError), response.Error.Message)
	}
}

func TestWriteErrorDefaultCodeUsesCanonicalMachineCode(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   string
	}{
		{name: "bad request", status: http.StatusBadRequest, want: CodeBadRequest},
		{name: "unprocessable", status: http.StatusUnprocessableEntity, want: CodeInvalidRequest},
		{name: "unknown client", status: http.StatusTeapot, want: CodeInvalidRequest},
		{name: "gateway timeout", status: http.StatusGatewayTimeout, want: CodeGatewayTimeout},
		{name: "service unavailable", status: http.StatusServiceUnavailable, want: CodeUnavailable},
		{name: "unknown server", status: http.StatusLoopDetected, want: CodeInternalError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)

			if err := WriteError(recorder, req, APIError{
				status:   tt.status,
				message:  "error",
				category: categoryForStatus(tt.status),
			}); err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}

			var response errorResponse
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if response.Error.Code != tt.want {
				t.Fatalf("expected code %q, got %q", tt.want, response.Error.Code)
			}
		})
	}
}

func TestWriteErrorPreservesExplicitCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if err := WriteError(recorder, req, APIError{
		status:   http.StatusBadRequest,
		code:     "CUSTOM_STABLE_CODE",
		message:  "bad request",
		category: CategoryClient,
	}); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	var response errorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if response.Error.Code != "CUSTOM_STABLE_CODE" {
		t.Fatalf("expected explicit code to be preserved, got %q", response.Error.Code)
	}
}

func TestErrorBuilderBuildDefaultsMessage(t *testing.T) {
	err := NewErrorBuilder().Build()
	if err.message != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("expected default message %q, got %q", http.StatusText(http.StatusInternalServerError), err.message)
	}
}

func TestErrorBuilderWithType(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeValidation).
		Code(CodeValidationError).
		Message("validation warning").
		Build()

	if err.errorType != TypeValidation {
		t.Fatalf("expected type to be set on APIError")
	}
}

func TestErrorBuilderDropsInvalidType(t *testing.T) {
	err := NewErrorBuilder().
		Code(CodeBadRequest).
		Message("bad request").
		Type(ErrorType("unknown_type")).
		Build()

	if err.errorType != "" {
		t.Fatalf("expected invalid type to be dropped, got %q", err.errorType)
	}
}

func TestWriteErrorDropsInvalidType(t *testing.T) {
	recorder := httptest.NewRecorder()

	if err := WriteError(recorder, nil, APIError{
		status:    http.StatusBadRequest,
		code:      CodeBadRequest,
		message:   "bad request",
		category:  CategoryClient,
		errorType: ErrorType("unknown_type"),
	}); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	var response errorResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if response.Error.Type != "" {
		t.Fatalf("expected invalid type to be omitted, got %q", response.Error.Type)
	}
}

func TestErrorBuilderDetails(t *testing.T) {
	builder := NewErrorBuilder()
	details := map[string]any{
		"field":  "email",
		"value":  "invalid",
		"reason": "format",
	}

	err := builder.
		Type(TypeValidation).
		Code(CodeValidationError).
		Message("validation failed").
		Details(details).
		Build()

	if len(err.details) != 3 {
		t.Fatalf("expected 3 details, got %d", len(err.details))
	}

	if err.details["field"] != "email" || err.details["value"] != "invalid" {
		t.Fatalf("expected details to be copied correctly")
	}
}

func TestErrorBuilderDetailsAreIsolatedAfterBuild(t *testing.T) {
	builder := NewErrorBuilder().
		Code(CodeValidationError).
		Message("validation failed").
		Detail("field", "email")

	first := builder.Build()
	builder.Detail("field", "name").Detail("other", "value")
	second := builder.Build()

	if first.details["field"] != "email" {
		t.Fatalf("expected first build details to stay isolated, got %v", first.details["field"])
	}
	if _, ok := first.details["other"]; ok {
		t.Fatalf("expected first build not to observe later detail, got %+v", first.details)
	}
	if second.details["field"] != "name" || second.details["other"] != "value" {
		t.Fatalf("expected second build to include current details, got %+v", second.details)
	}
}

func TestErrorBuilderDetailsDeepCloneJSONLikeValues(t *testing.T) {
	fields := []any{
		map[string]any{"field": "email", "codes": []string{"required"}},
	}
	details := map[string]any{
		"fields": fields,
		"labels": []string{"alpha"},
	}

	got := NewErrorBuilder().
		Type(TypeValidation).
		Code(CodeValidationError).
		Message("validation failed").
		Details(details).
		Build()

	fields[0].(map[string]any)["field"] = "mutated"
	fields[0].(map[string]any)["codes"].([]string)[0] = "mutated"
	details["labels"].([]string)[0] = "mutated"

	gotFields := got.details["fields"].([]any)
	gotField := gotFields[0].(map[string]any)
	if gotField["field"] != "email" {
		t.Fatalf("expected nested map detail to be isolated, got %+v", gotField)
	}
	if gotField["codes"].([]string)[0] != "required" {
		t.Fatalf("expected nested slice detail to be isolated, got %+v", gotField["codes"])
	}
	if got.details["labels"].([]string)[0] != "alpha" {
		t.Fatalf("expected top-level slice detail to be isolated, got %+v", got.details["labels"])
	}
}

func TestErrorBuilderDetailsCloneTypedJSONLikeValues(t *testing.T) {
	rules := map[string][]string{"email": {"required", "email"}}
	groups := []map[string]string{{"name": "primary"}}
	counts := []uint{1, 2}
	details := map[string]any{
		"rules":  rules,
		"groups": groups,
		"counts": counts,
	}

	got := NewErrorBuilder().
		Type(TypeValidation).
		Message("validation failed").
		Details(details).
		Build()

	rules["email"][0] = "mutated"
	groups[0]["name"] = "mutated"
	counts[0] = 99

	gotRules := got.details["rules"].(map[string][]string)
	if gotRules["email"][0] != "required" {
		t.Fatalf("expected typed map slice detail to be isolated, got %+v", gotRules)
	}
	gotGroups := got.details["groups"].([]map[string]string)
	if gotGroups[0]["name"] != "primary" {
		t.Fatalf("expected typed slice map detail to be isolated, got %+v", gotGroups)
	}
	gotCounts := got.details["counts"].([]uint)
	if gotCounts[0] != 1 {
		t.Fatalf("expected typed uint slice detail to be isolated, got %+v", gotCounts)
	}
}

func TestErrorBuilderDetailsUnsupportedValuesRemainPassthrough(t *testing.T) {
	type detailStruct struct {
		Field string
	}
	unsupported := &detailStruct{Field: "email"}

	got := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("unsupported", unsupported).
		Build()

	unsupported.Field = "mutated"
	gotUnsupported := got.details["unsupported"].(*detailStruct)
	if gotUnsupported.Field != "mutated" {
		t.Fatalf("expected unsupported pointer detail to remain compatibility passthrough, got %+v", gotUnsupported)
	}
}

func TestErrorBuilderDetailsTypedContainersWithUnsupportedValuesRemainPassthrough(t *testing.T) {
	type detailStruct struct {
		Field string
	}
	pointerValues := map[string]*detailStruct{
		"email": {Field: "email"},
	}
	structValues := map[string]detailStruct{
		"email": {Field: "email"},
	}

	got := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("pointer_values", pointerValues).
		Detail("struct_values", structValues).
		Build()

	pointerValues["email"].Field = "mutated"
	structValues["email"] = detailStruct{Field: "mutated"}

	gotPointerValues := got.details["pointer_values"].(map[string]*detailStruct)
	if gotPointerValues["email"].Field != "mutated" {
		t.Fatalf("expected typed map with pointer values to remain compatibility passthrough, got %+v", gotPointerValues)
	}
	gotStructValues := got.details["struct_values"].(map[string]detailStruct)
	if gotStructValues["email"].Field != "mutated" {
		t.Fatalf("expected typed map with struct values to remain compatibility passthrough, got %+v", gotStructValues)
	}
}

func TestWriteErrorDeepClonesDetailsBeforeEncoding(t *testing.T) {
	details := map[string]any{
		"fields": []any{map[string]any{"field": "email"}},
	}
	apiErr := APIError{
		status:   http.StatusBadRequest,
		code:     CodeValidationError,
		message:  "validation failed",
		category: CategoryValidation,
		details:  details,
	}

	rec := httptest.NewRecorder()
	if err := WriteError(rec, nil, apiErr); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	details["fields"].([]any)[0].(map[string]any)["field"] = "mutated"

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	fields, ok := resp.Error.Details["fields"].([]any)
	if !ok || len(fields) != 1 {
		t.Fatalf("expected encoded fields detail, got %+v", resp.Error.Details)
	}
	field, ok := fields[0].(map[string]any)
	if !ok || field["field"] != "email" {
		t.Fatalf("expected encoded detail to preserve original value, got %+v", fields[0])
	}
}

func TestZeroValueErrorBuilderDetailDoesNotPanic(t *testing.T) {
	var builder ErrorBuilder

	got := builder.
		Code(CodeBadRequest).
		Message("bad request").
		Detail("field", "name").
		Build()

	if got.details["field"] != "name" {
		t.Fatalf("expected zero-value builder detail to be set, got %+v", got.details)
	}
}

func TestWriteErrorZeroValueDefaults(t *testing.T) {
	w := httptest.NewRecorder()
	_ = WriteError(w, nil, APIError{})

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestErrorBuilderIgnoresEmptyDetailKeys(t *testing.T) {
	got := NewErrorBuilder().
		Type(TypeBadRequest).
		Code(CodeBadRequest).
		Message("bad request").
		Detail("", "ignored").
		Detail("field", "name").
		Details(map[string]any{
			"":       "also ignored",
			"reason": "missing",
		}).
		Build()

	if _, ok := got.details[""]; ok {
		t.Fatalf("expected empty detail key to be omitted, got %+v", got.details)
	}
	if got.details["field"] != "name" || got.details["reason"] != "missing" {
		t.Fatalf("expected non-empty details to remain, got %+v", got.details)
	}
}

func TestZeroValueErrorBuilderIgnoresEmptyDetailKey(t *testing.T) {
	var builder ErrorBuilder

	got := builder.
		Code(CodeBadRequest).
		Message("bad request").
		Detail("", "ignored").
		Build()

	if len(got.details) != 0 {
		t.Fatalf("expected no details for empty key, got %+v", got.details)
	}
}

func TestWriteErrorNormalizesInvalidStatus(t *testing.T) {
	w := httptest.NewRecorder()

	if err := WriteError(w, nil, APIError{
		status:   700,
		message:  "invalid status",
		category: CategoryClient,
	}); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected invalid status to normalize to 500, got %d", w.Code)
	}

	var response errorResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Category != CategoryServer {
		t.Fatalf("expected category %q, got %q", CategoryServer, response.Error.Category)
	}
	if response.Error.Code != CodeInternalError {
		t.Fatalf("expected code %q, got %q", CodeInternalError, response.Error.Code)
	}
}

func TestWriteErrorNormalizesNonErrorStatus(t *testing.T) {
	w := httptest.NewRecorder()

	if err := WriteError(w, nil, APIError{
		status:   http.StatusOK,
		code:     CodeBadRequest,
		message:  "bad request",
		category: CategoryClient,
	}); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected non-error status to normalize to 500, got %d", w.Code)
	}

	var response errorResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Category != CategoryServer {
		t.Fatalf("expected category %q, got %q", CategoryServer, response.Error.Category)
	}
	if response.Error.Code != CodeBadRequest {
		t.Fatalf("expected explicit code to be preserved, got %q", response.Error.Code)
	}
}

func TestWriteErrorEncodingFailureDoesNotCommitHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()

	err := WriteError(recorder, nil, NewErrorBuilder().
		Type(TypeBadRequest).
		Code(CodeInternalError).
		Message("bad detail").
		Detail("bad", make(chan int)).
		Build())
	if err == nil {
		t.Fatal("expected encoding error, got nil")
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected headers to remain uncommitted, got status %d", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("expected no body on encoding failure, got %q", recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "" {
		t.Fatalf("expected no content type on encoding failure, got %q", contentType)
	}
}

func TestWriteErrorIgnoresEmptyDetailKeys(t *testing.T) {
	w := httptest.NewRecorder()

	if err := WriteError(w, nil, APIError{
		status:   http.StatusBadRequest,
		code:     CodeBadRequest,
		message:  "bad request",
		category: CategoryClient,
		details: map[string]any{
			"":      "ignored",
			"field": "name",
		},
	}); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	var response errorResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := response.Error.Details[""]; ok {
		t.Fatalf("expected empty detail key to be omitted, got %+v", response.Error.Details)
	}
	if response.Error.Details["field"] != "name" {
		t.Fatalf("expected non-empty detail to remain, got %+v", response.Error.Details)
	}
}

func TestErrorBuilderBuildFillsDefaults(t *testing.T) {
	// A builder with no explicit Status/Code/Category should produce a fully-
	// populated APIError after Build().
	got := NewErrorBuilder().Message("something went wrong").Build()

	if got.status == 0 {
		t.Error("Build() must set a non-zero Status")
	}
	if got.code == "" {
		t.Error("Build() must set a non-empty Code")
	}
	if got.category == "" {
		t.Error("Build() must set a non-empty Category")
	}

	w := httptest.NewRecorder()
	_ = WriteError(w, nil, got)
	if w.Code != got.status {
		t.Fatalf("expected status %d, got %d", got.status, w.Code)
	}
}

func TestErrorBuilderTypeDerivesCategory(t *testing.T) {
	got := NewErrorBuilder().
		Type(TypeBadRequest).
		Code(CodeBadRequest).
		Message("bad request").
		Build()

	if got.category != CategoryClient {
		t.Fatalf("expected category %q, got %q", CategoryClient, got.category)
	}
}
