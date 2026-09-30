package contract

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------------------------------------------------------------------------
// APIError accessor methods
// ---------------------------------------------------------------------------

func TestAPIErrorErrorMethod(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeValidation).
		Code(CodeValidationError).
		Message("something went wrong").
		Build()

	if got := err.Error(); got != "something went wrong" {
		t.Fatalf("Error() = %q, want %q", got, "something went wrong")
	}
}

func TestAPIErrorStatusMethod(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeNotFound).
		Build()

	if got := err.Status(); got != http.StatusNotFound {
		t.Fatalf("Status() = %d, want %d", got, http.StatusNotFound)
	}
}

func TestAPIErrorStatusMethodNormalizesDefault(t *testing.T) {
	// Zero-value APIError should normalize to 500.
	var zero APIError
	if got := zero.Status(); got != http.StatusInternalServerError {
		t.Fatalf("Status() on zero = %d, want %d", got, http.StatusInternalServerError)
	}
}

func TestAPIErrorCodeMethod(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeForbidden).
		Build()

	if got := err.Code(); got != CodeForbidden {
		t.Fatalf("Code() = %q, want %q", got, CodeForbidden)
	}
}

func TestAPIErrorCodeMethodCustom(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeNotFound).
		Code("CUSTOM_NOT_FOUND").
		Message("not found").
		Build()

	if got := err.Code(); got != "CUSTOM_NOT_FOUND" {
		t.Fatalf("Code() = %q, want %q", got, "CUSTOM_NOT_FOUND")
	}
}

func TestAPIErrorMessageMethod(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("database unavailable").
		Build()

	if got := err.Message(); got != "database unavailable" {
		t.Fatalf("Message() = %q, want %q", got, "database unavailable")
	}
}

func TestAPIErrorMessageMethodDefaultsToStatusText(t *testing.T) {
	var zero APIError
	if got := zero.Message(); got != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("Message() = %q, want %q", got, http.StatusText(http.StatusInternalServerError))
	}
}

func TestAPIErrorCategoryMethod(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeUnauthorized).
		Build()

	if got := err.Category(); got != CategoryAuth {
		t.Fatalf("Category() = %q, want %q", got, CategoryAuth)
	}
}

func TestAPIErrorCategoryMethodDefaultsToServer(t *testing.T) {
	var zero APIError
	if got := zero.Category(); got != CategoryServer {
		t.Fatalf("Category() on zero = %q, want %q", got, CategoryServer)
	}
}

func TestAPIErrorTypeMethod(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeTimeout).
		Build()

	if got := err.Type(); got != TypeTimeout {
		t.Fatalf("Type() = %q, want %q", got, TypeTimeout)
	}
}

func TestAPIErrorTypeMethodEmptyForUntyped(t *testing.T) {
	err := NewErrorBuilder().
		Code(CodeBadRequest).
		Message("bad request").
		Build()

	// No Type() call on builder — errorType should remain empty.
	if got := err.Type(); got != "" {
		t.Fatalf("Type() on untyped error = %q, want empty", got)
	}
}

func TestAPIErrorRequestIDMethod(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal error").
		RequestID("req-abc").
		Build()

	if got := err.RequestID(); got != "req-abc" {
		t.Fatalf("RequestID() = %q, want %q", got, "req-abc")
	}
}

func TestAPIErrorRequestIDMethodEmpty(t *testing.T) {
	err := NewErrorBuilder().Type(TypeInternal).Build()
	if got := err.RequestID(); got != "" {
		t.Fatalf("RequestID() = %q, want empty", got)
	}
}

func TestAPIErrorDetailsMethod(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeValidation).
		Code(CodeValidationError).
		Message("validation failed").
		Detail("field", "email").
		Detail("reason", "format").
		Build()

	got := err.Details()
	if got == nil {
		t.Fatal("Details() returned nil, want non-nil map")
	}
	if got["field"] != "email" {
		t.Fatalf("Details()[field] = %v, want %q", got["field"], "email")
	}
	if got["reason"] != "format" {
		t.Fatalf("Details()[reason] = %v, want %q", got["reason"], "format")
	}
}

func TestAPIErrorDetailsMethodReturnsIsolatedCopy(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeValidation).
		Message("validation failed").
		Detail("field", "email").
		Build()

	got1 := err.Details()
	got1["field"] = "mutated"

	got2 := err.Details()
	if got2["field"] != "email" {
		t.Fatalf("Details() copy not isolated: got %q, want %q", got2["field"], "email")
	}
}

func TestAPIErrorDetailsMethodNilWhenEmpty(t *testing.T) {
	err := NewErrorBuilder().Type(TypeInternal).Message("internal").Build()
	if got := err.Details(); got != nil {
		t.Fatalf("Details() on error with no details = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// All 8 accessor methods in a single table-driven test
// ---------------------------------------------------------------------------

func TestAPIErrorAccessorMethodsCoverage(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeRateLimited).
		Code(CodeRateLimited).
		Message("rate limit exceeded").
		RequestID("req-999").
		Detail("limit", "100").
		Build()

	if got := err.Error(); got != "rate limit exceeded" {
		t.Fatalf("Error() = %q", got)
	}
	if got := err.Status(); got != http.StatusTooManyRequests {
		t.Fatalf("Status() = %d", got)
	}
	if got := err.Code(); got != CodeRateLimited {
		t.Fatalf("Code() = %q", got)
	}
	if got := err.Message(); got != "rate limit exceeded" {
		t.Fatalf("Message() = %q", got)
	}
	if got := err.Category(); got != CategoryRateLimit {
		t.Fatalf("Category() = %q", got)
	}
	if got := err.Type(); got != TypeRateLimited {
		t.Fatalf("Type() = %q", got)
	}
	if got := err.RequestID(); got != "req-999" {
		t.Fatalf("RequestID() = %q", got)
	}
	details := err.Details()
	if details == nil || details["limit"] != "100" {
		t.Fatalf("Details() = %v", details)
	}
}

// ---------------------------------------------------------------------------
// RouteState methods
// ---------------------------------------------------------------------------

func TestRouteStateSetPatternAndSetName(t *testing.T) {
	rs := GetRouteState()
	defer func() {
		rs.Reset()
		PutRouteState(rs)
	}()

	rs.SetPattern("/users/:id")
	rs.SetName("users.show")

	if rs.pattern != "/users/:id" {
		t.Fatalf("SetPattern: pattern = %q, want %q", rs.pattern, "/users/:id")
	}
	if rs.name != "users.show" {
		t.Fatalf("SetName: name = %q, want %q", rs.name, "users.show")
	}
}

func TestRouteStateAddParam(t *testing.T) {
	rs := GetRouteState()
	defer func() {
		rs.Reset()
		PutRouteState(rs)
	}()

	rs.AddParam("id", "42")
	rs.AddParam("slug", "hello-world")

	if rs.paramN != 2 {
		t.Fatalf("AddParam: paramN = %d, want 2", rs.paramN)
	}
	if rs.paramKeys[0] != "id" || rs.paramVals[0] != "42" {
		t.Fatalf("AddParam[0]: got (%q, %q), want (id, 42)", rs.paramKeys[0], rs.paramVals[0])
	}
	if rs.paramKeys[1] != "slug" || rs.paramVals[1] != "hello-world" {
		t.Fatalf("AddParam[1]: got (%q, %q), want (slug, hello-world)", rs.paramKeys[1], rs.paramVals[1])
	}
}

func TestRouteStateAddParamSilentlyDropsBeyondMax(t *testing.T) {
	rs := GetRouteState()
	defer func() {
		rs.Reset()
		PutRouteState(rs)
	}()

	for i := 0; i < maxInlineParams+5; i++ {
		rs.AddParam("k", "v")
	}
	if rs.paramN != maxInlineParams {
		t.Fatalf("paramN = %d, want %d (max)", rs.paramN, maxInlineParams)
	}
}

func TestRouteStateReset(t *testing.T) {
	rs := GetRouteState()
	defer func() {
		PutRouteState(rs)
	}()

	rs.SetPattern("/items/:id")
	rs.SetName("items.show")
	rs.AddParam("id", "7")

	rs.Reset()

	if rs.pattern != "" {
		t.Fatalf("Reset: pattern = %q, want empty", rs.pattern)
	}
	if rs.name != "" {
		t.Fatalf("Reset: name = %q, want empty", rs.name)
	}
	if rs.paramN != 0 {
		t.Fatalf("Reset: paramN = %d, want 0", rs.paramN)
	}
	// All inline slots should be zeroed.
	for i := 0; i < maxInlineParams; i++ {
		if rs.paramKeys[i] != "" || rs.paramVals[i] != "" {
			t.Fatalf("Reset: slot %d not zeroed: key=%q val=%q", i, rs.paramKeys[i], rs.paramVals[i])
		}
	}
}

func TestWithRouteState(t *testing.T) {
	rs := GetRouteState()
	defer func() {
		rs.Reset()
		PutRouteState(rs)
	}()

	rs.SetPattern("/orders/:id")
	rs.SetName("orders.show")
	rs.AddParam("id", "99")

	req := httptest.NewRequest(http.MethodGet, "/orders/99", nil)
	ctx := WithRouteState(req.Context(), rs)

	rc := RequestContextFromContext(ctx)
	if rc.RoutePattern != "/orders/:id" {
		t.Fatalf("RoutePattern = %q, want /orders/:id", rc.RoutePattern)
	}
	if rc.RouteName != "orders.show" {
		t.Fatalf("RouteName = %q, want orders.show", rc.RouteName)
	}
	if rc.Params["id"] != "99" {
		t.Fatalf("Params[id] = %q, want 99", rc.Params["id"])
	}
}

func TestWithRouteStateEmptyParams(t *testing.T) {
	rs := GetRouteState()
	defer func() {
		rs.Reset()
		PutRouteState(rs)
	}()

	rs.SetPattern("/health")
	rs.SetName("health")
	// No params added.

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	ctx := WithRouteState(req.Context(), rs)

	rc := RequestContextFromContext(ctx)
	if rc.RoutePattern != "/health" {
		t.Fatalf("RoutePattern = %q, want /health", rc.RoutePattern)
	}
	if rc.Params != nil {
		t.Fatalf("Params = %v, want nil", rc.Params)
	}
}

func TestWithRouteStateNilPointerSkipped(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithRouteState(req.Context(), nil)

	// RequestContextFromContext should fall through to empty result when rs is nil.
	rc := RequestContextFromContext(ctx)
	if rc.RoutePattern != "" || rc.RouteName != "" || len(rc.Params) != 0 {
		t.Fatalf("expected empty RequestContext for nil RouteState, got %+v", rc)
	}
}

// ---------------------------------------------------------------------------
// cloneReflectValue edge cases via Details()
// ---------------------------------------------------------------------------

func TestDetailsCloneNilInterfaceValue(t *testing.T) {
	var nilVal interface{} = nil
	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("key", nilVal).
		Build()

	got := err.Details()
	// The key should be present; the value should be nil.
	v, ok := got["key"]
	if !ok {
		t.Fatal("Details(): key missing for nil interface value")
	}
	if v != nil {
		t.Fatalf("Details(): expected nil value for nil interface, got %v (%T)", v, v)
	}
}

func TestDetailsCloneArrayType(t *testing.T) {
	arr := [3]string{"a", "b", "c"}
	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("arr", arr).
		Build()

	got := err.Details()
	cloned, ok := got["arr"].([3]string)
	if !ok {
		// Array types may fall back to passthrough — ensure value is preserved.
		if got["arr"] == nil {
			t.Fatal("Details(): array value missing")
		}
		return
	}
	if cloned[0] != "a" || cloned[1] != "b" || cloned[2] != "c" {
		t.Fatalf("Details(): cloned array = %v, want [a b c]", cloned)
	}
}

func TestDetailsCloneDeeplyNestedMapFallsBackGracefully(t *testing.T) {
	// Build a map nested 20 levels deep — exceeds the depth limit of 16.
	inner := map[string]any{"leaf": "value"}
	for i := 0; i < 20; i++ {
		inner = map[string]any{"child": inner}
	}

	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("deep", inner).
		Build()

	// The call must not panic and must return a non-nil details map.
	got := err.Details()
	if got == nil {
		t.Fatal("Details(): nil map for deeply nested value")
	}
	if _, ok := got["deep"]; !ok {
		t.Fatal("Details(): key missing for deeply nested value")
	}
}

func TestDetailsCloneMapWithNonStringKey(t *testing.T) {
	// map[int]string has a non-string key — cloneReflectValue returns (v, false)
	// so it falls back to passthrough (the original value is returned as-is).
	intKeyMap := map[int]string{1: "one", 2: "two"}
	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("intmap", intKeyMap).
		Build()

	got := err.Details()
	if got == nil {
		t.Fatal("Details(): nil map for int-keyed map value")
	}
	v, ok := got["intmap"]
	if !ok {
		t.Fatal("Details(): key missing for int-keyed map value")
	}
	// Must not panic on type assertion; the passthrough value is the original.
	if _, ok2 := v.(map[int]string); !ok2 {
		t.Fatalf("Details(): expected passthrough map[int]string, got %T", v)
	}
}

func TestDetailsCloneInterfaceWrappingInterface(t *testing.T) {
	// Wrap a concrete string inside an interface{} — cloneReflectValue should
	// recurse into the Elem() and return the cloned scalar.
	var inner interface{} = "wrapped-string"
	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("wrapped", inner).
		Build()

	got := err.Details()
	if got == nil {
		t.Fatal("Details(): nil map for interface-wrapped value")
	}
	v, ok := got["wrapped"]
	if !ok {
		t.Fatal("Details(): key missing for interface-wrapped value")
	}
	if v != "wrapped-string" {
		t.Fatalf("Details(): expected %q, got %v (%T)", "wrapped-string", v, v)
	}
}

func TestDetailsCloneNilMapReturnsNil(t *testing.T) {
	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Build()

	if got := err.Details(); got != nil {
		t.Fatalf("Details() on error without details = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// cloneDetailValue — []map[string]any branch (currently 0%)
// ---------------------------------------------------------------------------

func TestDetailsCloneSliceOfMaps(t *testing.T) {
	original := []map[string]any{
		{"name": "alice", "score": 10},
		{"name": "bob", "score": 20},
	}
	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("items", original).
		Build()

	got := err.Details()
	items, ok := got["items"].([]map[string]any)
	if !ok {
		t.Fatalf("Details(): expected []map[string]any, got %T", got["items"])
	}
	if len(items) != 2 {
		t.Fatalf("Details(): expected 2 items, got %d", len(items))
	}

	// Mutate original and verify isolation.
	original[0]["name"] = "mutated"
	if items[0]["name"] != "alice" {
		t.Fatalf("Details(): expected isolation from original, got %q", items[0]["name"])
	}
}

// ---------------------------------------------------------------------------
// cloneAnyMap — branch where all keys are empty (len(out)==0) returns nil
// ---------------------------------------------------------------------------

func TestDetailsCloneAnyMapAllEmptyKeysReturnsNil(t *testing.T) {
	// Build directly to bypass the builder's key-filtering, then call Details().
	// The builder already filters empty keys, so construct the APIError directly.
	apiErr := APIError{
		status:   http.StatusBadRequest,
		code:     CodeBadRequest,
		message:  "bad",
		category: CategoryClient,
		details:  map[string]any{"": "ignored"},
	}
	// cloneAnyMap skips empty keys; with only an empty key, len(out)==0 → nil.
	if got := apiErr.Details(); got != nil {
		t.Fatalf("Details() with only empty key should return nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// cloneReflectValue — depth > 16 branch
// ---------------------------------------------------------------------------

func TestCloneReflectValueDepthExceeded(t *testing.T) {
	// A map nested > 16 levels triggers the depth guard.  cloneReflectValue
	// returns (v, false) and cloneReflectDetailValue falls back to passthrough.
	// Build 18 levels deep using map[string]any so each level IS a string-keyed
	// map — the depth limit fires before it can fully clone.
	inner := map[string]any{"leaf": "value"}
	for i := 0; i < 17; i++ {
		inner = map[string]any{"child": inner}
	}

	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("deep", inner).
		Build()

	// Must not panic. The value is either cloned (if cloneAnyMap handled it) or
	// passed through; either way the key must be present.
	got := err.Details()
	if _, ok := got["deep"]; !ok {
		t.Fatal("Details(): key 'deep' missing for depth-exceeded nested map")
	}
}

// ---------------------------------------------------------------------------
// cloneReflectValue — Interface case that recurses into Elem()
// ---------------------------------------------------------------------------

func TestCloneReflectValueInterfaceElem(t *testing.T) {
	// map[string]interface{} containing a typed []uint — hits the Interface
	// case inside cloneReflectValue (the map value kind is Interface for
	// map[string]any, and the elem is a []uint which goes through reflect Slice).
	counts := []uint{1, 2, 3}
	detail := map[string]any{"counts": counts}

	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Details(detail).
		Build()

	// Mutate original after build.
	counts[0] = 99

	got := err.Details()
	cloned, ok := got["counts"].([]uint)
	if !ok {
		t.Fatalf("Details(): expected []uint, got %T", got["counts"])
	}
	if cloned[0] != 1 {
		t.Fatalf("Details(): expected isolated copy, got %v", cloned[0])
	}
}

// ---------------------------------------------------------------------------
// makeAssignable — ConvertibleTo branch
// ---------------------------------------------------------------------------

func TestMakeAssignableConvertibleTo(t *testing.T) {
	// map[string][]uint — cloneReflectValue handles the slice element via
	// makeAssignable; the cloned reflect.Value may need Convert to match the
	// target type (e.g. when the value was obtained via Index() and needs
	// explicit re-typing).  Use a typed uint slice inside a map[string]any.
	uslice := []uint32{10, 20, 30}
	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("u32", uslice).
		Build()

	uslice[0] = 99
	got := err.Details()
	v, ok := got["u32"].([]uint32)
	if !ok {
		t.Fatalf("Details(): expected []uint32, got %T", got["u32"])
	}
	if v[0] != 10 {
		t.Fatalf("Details(): expected isolated copy, got %v", v[0])
	}
}

// ---------------------------------------------------------------------------
// RequestParamFromContext — RouteState fast-path param not found + fallback
// ---------------------------------------------------------------------------

func TestRequestParamFromContextRouteStateNotFound(t *testing.T) {
	rs := GetRouteState()
	defer func() {
		rs.Reset()
		PutRouteState(rs)
	}()
	rs.AddParam("id", "42")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithRouteState(req.Context(), rs)

	// Existing param is found.
	if got := RequestParamFromContext(ctx, "id"); got != "42" {
		t.Fatalf("RequestParamFromContext with RouteState: got %q, want 42", got)
	}
	// Missing param returns empty string.
	if got := RequestParamFromContext(ctx, "missing"); got != "" {
		t.Fatalf("RequestParamFromContext missing: got %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// WithRequestID and RequestIDFromContext — nil context branches
// ---------------------------------------------------------------------------

func TestWithRequestIDNilContext(t *testing.T) {
	// Must not panic; should still store the id under a background context.
	ctx := WithRequestID(nil, "req-nil")
	if got := RequestIDFromContext(ctx); got != "req-nil" {
		t.Fatalf("WithRequestID(nil): got %q, want req-nil", got)
	}
}

func TestRequestIDFromContextNilContext(t *testing.T) {
	if got := RequestIDFromContext(nil); got != "" {
		t.Fatalf("RequestIDFromContext(nil) = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// writeJSON — nil writer and no-body status branches
// ---------------------------------------------------------------------------

func TestWriteJSONNilWriter(t *testing.T) {
	err := writeJSON(nil, http.StatusOK, map[string]any{})
	if err != ErrResponseWriterNil {
		t.Fatalf("writeJSON(nil, ...): expected ErrResponseWriterNil, got %v", err)
	}
}

func TestWriteJSONInvalidStatus(t *testing.T) {
	w := httptest.NewRecorder()
	err := writeJSON(w, http.StatusBadRequest, map[string]any{})
	if err != ErrInvalidResponseStatus {
		t.Fatalf("writeJSON with 4xx: expected ErrInvalidResponseStatus, got %v", err)
	}
}

func TestWriteJSONNoBodyStatus(t *testing.T) {
	w := httptest.NewRecorder()
	err := writeJSON(w, http.StatusNoContent, nil)
	if err != nil {
		t.Fatalf("writeJSON(204): unexpected error: %v", err)
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("writeJSON(204): got status %d, want 204", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("writeJSON(204): expected no body, got %q", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// putJSONBuffer — nil guard
// ---------------------------------------------------------------------------

func TestPutJSONBufferNilDoesNotPanic(t *testing.T) {
	// putJSONBuffer(nil) must not panic.
	putJSONBuffer(nil)
}

// ---------------------------------------------------------------------------
// categoryForStatus — fallthrough returning ""
// ---------------------------------------------------------------------------

func TestCategoryForStatusBelowBadRequest(t *testing.T) {
	// status < 400 (not in the specific-status table) should return "".
	if got := categoryForStatus(http.StatusOK); got != "" {
		t.Fatalf("categoryForStatus(200) = %q, want empty", got)
	}
	if got := categoryForStatus(http.StatusContinue); got != "" {
		t.Fatalf("categoryForStatus(100) = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// cloneStringMap — nil branch
// ---------------------------------------------------------------------------

func TestCloneStringMapNilInput(t *testing.T) {
	// Nil map in RequestContext.Params should propagate as nil (not an empty map).
	ctx := WithRequestContext(nil, RequestContext{Params: nil, RoutePattern: "/x"})
	rc := RequestContextFromContext(ctx)
	if rc.Params != nil {
		t.Fatalf("cloneStringMap(nil): expected nil Params, got %v", rc.Params)
	}
}

// ---------------------------------------------------------------------------
// ErrorBuilder.Type — empty string branch (returns early)
// ---------------------------------------------------------------------------

func TestErrorBuilderTypeEmptyStringIsNoop(t *testing.T) {
	got := NewErrorBuilder().
		Type("").
		Message("no type set").
		Build()

	// Empty type string hits the early-return in Type(); errorType stays "".
	if got.errorType != "" {
		t.Fatalf("Type(%q): errorType = %q, want empty", "", got.errorType)
	}
}

// ---------------------------------------------------------------------------
// normalizeTypedAPIError — code fallback from meta when code is empty
// ---------------------------------------------------------------------------

func TestNormalizeTypedAPIErrorFillsEmptyCode(t *testing.T) {
	// Build an APIError with a known type but no code; normalizeAPIError should
	// fill it from the type meta.
	raw := APIError{
		status:    http.StatusNotFound,
		category:  CategoryClient,
		message:   "not found",
		errorType: TypeNotFound,
		// code intentionally left empty
	}
	got := normalizeAPIError(raw)
	if got.code != CodeResourceNotFound {
		t.Fatalf("normalizeAPIError with empty code: got %q, want %q", got.code, CodeResourceNotFound)
	}
}

// ---------------------------------------------------------------------------
// normalizeUntypedAPIError — category fallback when category is empty
// ---------------------------------------------------------------------------

func TestNormalizeUntypedAPIErrorFillsEmptyCategory(t *testing.T) {
	raw := APIError{
		status:  http.StatusConflict,
		code:    CodeConflict,
		message: "conflict",
		// category intentionally left empty
	}
	got := normalizeAPIError(raw)
	if got.category == "" {
		t.Fatalf("normalizeAPIError with empty category: category is still empty")
	}
	if got.category != CategoryClient {
		t.Fatalf("normalizeAPIError with empty category: got %q, want %q", got.category, CategoryClient)
	}
}

// ---------------------------------------------------------------------------
// cloneDetailValue — []int, []int64, []float64, []bool typed slice branches
// ---------------------------------------------------------------------------

func TestDetailsCloneTypedScalarSlices(t *testing.T) {
	ints := []int{1, 2, 3}
	int64s := []int64{10, 20, 30}
	float64s := []float64{1.1, 2.2}
	bools := []bool{true, false}

	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("ints", ints).
		Detail("int64s", int64s).
		Detail("float64s", float64s).
		Detail("bools", bools).
		Build()

	// Mutate originals after build.
	ints[0] = 99
	int64s[0] = 99
	float64s[0] = 99.9
	bools[0] = false

	got := err.Details()

	if v, ok := got["ints"].([]int); !ok || v[0] != 1 {
		t.Fatalf("Details()[ints]: expected isolated [1 2 3], got %v (type %T)", got["ints"], got["ints"])
	}
	if v, ok := got["int64s"].([]int64); !ok || v[0] != 10 {
		t.Fatalf("Details()[int64s]: expected isolated [10 20 30], got %v (type %T)", got["int64s"], got["int64s"])
	}
	if v, ok := got["float64s"].([]float64); !ok || v[0] != 1.1 {
		t.Fatalf("Details()[float64s]: expected isolated [1.1 2.2], got %v (type %T)", got["float64s"], got["float64s"])
	}
	if v, ok := got["bools"].([]bool); !ok || v[0] != true {
		t.Fatalf("Details()[bools]: expected isolated [true false], got %v (type %T)", got["bools"], got["bools"])
	}
}

// ---------------------------------------------------------------------------
// cloneReflectValue — depth > 16 reached via reflect path
// cloneReflectValue is also reached when a []uint or similar typed slice is
// stored; depth > 16 is tested here using a recursive reflect construction.
// ---------------------------------------------------------------------------

func TestCloneReflectValueDepthGuardViaReflect(t *testing.T) {
	// []interface{} with 17 levels of wrapping — the reflect Interface branch
	// recurses, eventually hitting depth > 16.
	var nested interface{} = "leaf"
	for i := 0; i < 18; i++ {
		nested = []interface{}{nested}
	}

	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("deep", nested).
		Build()

	// Must not panic; key must be present.
	got := err.Details()
	if _, ok := got["deep"]; !ok {
		t.Fatal("Details(): key 'deep' missing for deeply nested interface value")
	}
}

// ---------------------------------------------------------------------------
// cloneReflectValue — nil Interface branch
// ---------------------------------------------------------------------------

func TestCloneReflectValueNilInterface(t *testing.T) {
	// A map[string]any with a nil-interface value exercises the Interface
	// case where v.IsNil() == true in cloneReflectValue (after reflect
	// dispatches through the Interface kind on the outer any).
	m := map[string]any{"nilval": (*int)(nil)}

	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Details(m).
		Build()

	got := err.Details()
	if got == nil {
		t.Fatal("Details(): nil result for map containing nil pointer value")
	}
}

// ---------------------------------------------------------------------------
// cloneReflectValue — nil Slice and nil Map branches (non-any typed)
// ---------------------------------------------------------------------------

func TestCloneReflectValueNilTypedSlice(t *testing.T) {
	var nilSlice []uint
	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("s", nilSlice).
		Build()

	got := err.Details()
	// A nil []uint should survive the clone (either as nil or passthrough);
	// the key may be omitted since a nil slice maps to nil — both are
	// acceptable. The call must not panic regardless.
	_, _ = got["s"]
}

func TestCloneReflectValueNilTypedMap(t *testing.T) {
	var nilMap map[string]uint
	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("m", nilMap).
		Build()

	// Must not panic. Key presence not required for nil-typed maps.
	_ = err.Details()
}

// ---------------------------------------------------------------------------
// parseTraceID / parseSpanID — invalid hex branch
// ---------------------------------------------------------------------------

func TestParseTraceIDInvalidHex(t *testing.T) {
	// 32-char string with non-hex characters → hex.DecodeString fails.
	_, err := parseTraceID("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")
	if err == nil {
		t.Fatal("parseTraceID with invalid hex: expected error, got nil")
	}
}

func TestParseSpanIDInvalidHex(t *testing.T) {
	// 16-char string with non-hex characters.
	_, err := parseSpanID("zzzzzzzzzzzzzzzz")
	if err == nil {
		t.Fatal("parseSpanID with invalid hex: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// codeForStatus — fallthrough to CodeInternalError for sub-400
// ---------------------------------------------------------------------------

func TestCodeForStatusBelowBadRequest(t *testing.T) {
	// statusCode < 400 hits the final return CodeInternalError.
	if got := codeForStatus(http.StatusOK); got != CodeInternalError {
		t.Fatalf("codeForStatus(200) = %q, want %q", got, CodeInternalError)
	}
}

// ---------------------------------------------------------------------------
// writeJSON — encoding failure path (chan int is not JSON-serializable)
// ---------------------------------------------------------------------------

func TestWriteJSONEncodingFailure(t *testing.T) {
	w := httptest.NewRecorder()
	// A channel cannot be JSON-encoded, which exercises the encErr branch.
	err := writeJSON(w, http.StatusOK, map[string]any{"ch": make(chan int)})
	if err == nil {
		t.Fatal("writeJSON with unencodable payload: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// cloneReflectValue — Interface kind branch
// Reached when cloneReflectValue recurses into elements of a map whose value
// type is a named interface (so iter.Value().Kind() == reflect.Interface).
// ---------------------------------------------------------------------------

// namedInterface is a named interface type used to produce a reflect.Value
// with Kind() == reflect.Interface inside a typed map.
type namedInterface interface {
	Value() string
}

type namedInterfaceImpl struct{ v string }

func (n namedInterfaceImpl) Value() string { return n.v }

func TestCloneReflectValueInterfaceKindBranch(t *testing.T) {
	// map[string]namedInterface — when iterated via reflect, each map value
	// has Kind() == reflect.Interface, exercising that branch.
	m := map[string]namedInterface{
		"key": namedInterfaceImpl{"hello"},
	}

	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("m", m).
		Build()

	// Must not panic. The value may be passed through since the cloned
	// Interface handling may return (v, false) if assignability fails.
	got := err.Details()
	if _, ok := got["m"]; !ok {
		// passthrough is acceptable
		t.Fatal("Details(): key 'm' missing for named-interface map")
	}
}

func TestCloneReflectValueNilInterfaceKindBranch(t *testing.T) {
	// map[string]namedInterface with a nil value — exercises IsNil() == true
	// inside the Interface case of cloneReflectValue.
	m := map[string]namedInterface{
		"nilkey": nil,
	}

	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("m", m).
		Build()

	// Must not panic.
	_ = err.Details()
}

// stringNamedInterface is a named interface whose implementor holds a string
// (a clonable scalar), so cloneReflectValue can successfully clone the Elem()
// and then reach the AssignableTo checks in the Interface case.
type stringNamedInterface interface {
	StringValue() string
}

type stringNamedInterfaceImpl string

func (s stringNamedInterfaceImpl) StringValue() string { return string(s) }

func TestCloneReflectValueInterfaceAssignableBranch(t *testing.T) {
	// When the Elem() of an Interface value is a string (clonable), the clone
	// succeeds and execution reaches the AssignableTo checks.
	// map[string]stringNamedInterface holds a string-based concrete type.
	m := map[string]stringNamedInterface{
		"k": stringNamedInterfaceImpl("hello"),
	}

	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("m", m).
		Build()

	// Must not panic; value present.
	got := err.Details()
	if _, ok := got["m"]; !ok {
		t.Fatal("Details(): key 'm' missing for stringNamedInterface map")
	}
}

// ---------------------------------------------------------------------------
// cloneReflectValue — depth > 16 via recursive map iteration
// ---------------------------------------------------------------------------

func TestCloneReflectValueDepthGuardViaMapIteration(t *testing.T) {
	// A map[string]map[string]... chain 18 levels deep. Since each level
	// is a typed map[string]string-compatible nesting, the reflect Map
	// branch will recurse and eventually hit depth > 16.
	// Build as map[string]any so it enters the reflect path on second level.
	inner := map[string]any{"v": "leaf"}
	for i := 0; i < 18; i++ {
		inner = map[string]any{"child": map[string]any(inner)}
	}

	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("deep", inner).
		Build()

	// cloneAnyMap handles the outer map[string]any level by level.
	// When the reflect path hits depth > 16, it returns false → passthrough.
	got := err.Details()
	if _, ok := got["deep"]; !ok {
		t.Fatal("Details(): key 'deep' missing for deeply nested typed map")
	}
}

// ---------------------------------------------------------------------------
// makeAssignable — ConvertibleTo path
// Triggered when the reflect-cloned value needs a Convert() call to match
// the map/slice element type.  A map[string]int32 whose cloned element comes
// back as int32 (assignable), but we can trigger the Convert path by using a
// named type that is convertible but not directly assignable.
// ---------------------------------------------------------------------------

type myInt32 int32

func TestMakeAssignableConvertiblePath(t *testing.T) {
	// map[string]myInt32 — cloneReflectValue will try to clone each value
	// (kind Int32) and then makeAssignable(int32Value, myInt32Type).
	// Since myInt32 is not the same type as int32 it is not assignable, but
	// it IS convertible, so the Convert path is exercised.
	m := map[string]myInt32{"x": 42}

	err := NewErrorBuilder().
		Type(TypeInternal).
		Message("internal").
		Detail("m", m).
		Build()

	got := err.Details()
	if got == nil {
		t.Fatal("Details(): nil map for map[string]myInt32")
	}
	// The value is either cloned (via Convert) or passed through.
	if _, ok := got["m"]; !ok {
		t.Fatal("Details(): key 'm' missing for myInt32 map")
	}
}

// ---------------------------------------------------------------------------
// normalizeUntypedAPIError — category empty + categoryForStatus returns ""
// ---------------------------------------------------------------------------

func TestNormalizeUntypedAPIErrorCategoryFallsBackToServer(t *testing.T) {
	// status 200 → categoryForStatus returns "" → fallback to CategoryServer.
	raw := APIError{
		status:  http.StatusOK,
		code:    CodeBadRequest,
		message: "ok but treated as error",
		// category and errorType both empty
	}
	got := normalizeAPIError(raw)
	// normalizeErrorHTTPStatus(200) → (500, true) since 200 < 400.
	// invalidStatus=true → category set to CategoryServer.
	if got.category != CategoryServer {
		t.Fatalf("expected CategoryServer, got %q", got.category)
	}
}

// ---------------------------------------------------------------------------
// RequestContextFromContext — nil fallthrough to empty RequestContext
// (exercises the final return RequestContext{} branch)
// ---------------------------------------------------------------------------

func TestRequestContextFromContextNoKey(t *testing.T) {
	// context.Background() has no routeStateKey and no requestContextKey.
	rc := RequestContextFromContext(t.Context())
	if rc.RoutePattern != "" || rc.RouteName != "" || len(rc.Params) != 0 {
		t.Fatalf("expected empty RequestContext, got %+v", rc)
	}
}
