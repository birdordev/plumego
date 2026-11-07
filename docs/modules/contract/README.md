# contract

## Purpose

`contract` defines Plumego's HTTP transport contracts: structured API errors,
success response envelopes, request metadata context helpers, request ids, and
trace metadata carriers.

## v1 Status

- `ga` in the Plumego v1 support matrix
- Public surface is intentionally small and stdlib-compatible

## Use this module when

- standardizing success or error response shape
- writing HTTP transport response helpers
- carrying router-owned request metadata needed by handlers or middleware
- carrying request id or trace metadata through `context.Context`

## Do not use this module for

- request body binding
- query binding
- validation frameworks or `validate` tag processing
- protocol gateway families
- business DTO ownership
- route matching
- auth identity contracts
- middleware observability policy or field redaction

## First files to read

- `contract/module.yaml`
- `contract/errors.go`
- `contract/response.go`
- `contract/context_core.go`
- `contract/request_id.go`
- `contract/trace.go`

## Public entrypoints

- `HeaderContentType`
- `ContentTypeJSON`
- `NewErrorBuilder`
- `ErrorBuilder`
- `ErrorCategory`
- `ErrorType`
- `ErrorTypeMeta`
- `ErrorType.Meta`
- `APIError`
- `APIError.Status`
- `APIError.Code`
- `APIError.Message`
- `APIError.Category`
- `APIError.Type`
- `APIError.RequestID`
- `APIError.Details`
- `APIError.Error`
- `Code*` transport error constants
- `WriteError`
- `WriteResponse`
- `RequestContext`
- `WithRequestContext`
- `RequestContextFromContext`
- `RequestParamFromContext`
- `WithRequestID`
- `RequestIDFromContext`
- `RequestIDHeader`
- `WithTraceContext`
- `TraceContextFromContext`
- `TraceContext`
- `TraceContext.IsSampled`
- `TraceContext.HasTraceID`
- `TraceContext.HasSpanID`
- `TraceContext.Valid`
- `ErrHandlerNil`
- `ErrResponseWriterNil`
- `ErrInvalidResponseStatus`

## Surface Rules

`contract` owns one success response path and one structured error write path:

- success responses go through `WriteResponse`
- errors go through `NewErrorBuilder` and `WriteError`
- `WriteResponse` accepts only 2xx HTTP statuses; non-2xx responses must use
  `WriteError` or a module-owned representation when the resource intentionally
  returns a status document such as health/readiness
- `WriteResponse` writes `204` and `205` without a response body. Encoding
  failures for other success statuses return before the response is committed.
- If error details cannot be encoded, `WriteError` sends the same status and
  error envelope without `details`. It returns the encoding error and any
  write error for diagnostics. The fallback commits the response, so callers
  should log the error without attempting a second response.
- handlers decode JSON directly with `json.NewDecoder(r.Body).Decode(&dst)`
- handlers read query values directly from `r.URL.Query()`
- validation belongs in the module that owns the request DTO or business rule

`contract` intentionally does not expose a framework-style request bag, JSON
binder, query binder, validation tag framework, or bind-error translation helper.

## Error Taxonomy

`ErrorType.Meta()` is the canonical taxonomy lookup. It owns the default
`Status`, `Category`, and `Code` for every public error type. Builder and writer
normalization must converge back to this table for status and category.
Coarse status-to-category fallback is internal normalization only; callers
should choose a specific `ErrorType` whenever they know the failure shape.
`ErrorBuilder` does not expose direct status or category setters; choose an
`ErrorType` and optionally override only the machine code for registered
extension-owned refinements.

## v1 Breaking Normalization

- `WriteResponse` no longer normalizes invalid statuses or writes non-2xx
  envelopes. It returns `ErrInvalidResponseStatus` before writing.
- `APIError` remains exported as the normalized error value returned by
  `NewErrorBuilder`, but callers should construct values with
  `NewErrorBuilder` instead of literals.
- `ErrorBuilder.Build` and `APIError.Details` clone supported map and slice
  values, preserving nested empty containers and nil values. Cyclic detail
  values rejected by JSON encoding trigger the fallback above.
- Request ids are accepted only when the trimmed value is non-empty, contains no
  control characters, and is at most 128 bytes. Rejected request ids are not
  stored or echoed by `WriteResponse` or `WriteError`.
- `contract` does not own request-id generation policy; middleware or
  application wiring owns generation and header intake.

## Context Metadata

`RequestContext` is router metadata only:

- route params
- matched route pattern
- route name

Use `WithRequestContext`, `RequestContextFromContext`, and
`RequestParamFromContext`; do not write raw context values with exported keys.
Stored maps are defensively copied. Use `RequestParamFromContext` when a caller
needs one route param and does not need a full `RequestContext` snapshot.

`TraceContext` is a string carrier only. Full tracing runtime behavior, typed
trace/span identifiers, propagation policy, collectors, samplers, and exporters
belong in `x/observability`.

## Validation

Module-owned handlers should keep transport validation explicit:

```go
var req createRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    _ = contract.WriteError(w, r, contract.NewErrorBuilder().
        Type(contract.TypeValidation).
        Code(contract.CodeInvalidJSON).
        Message("invalid request body").
        Build())
    return
}
if strings.TrimSpace(req.Name) == "" {
    _ = contract.WriteError(w, r, contract.NewErrorBuilder().
        Type(contract.TypeRequired).
        Code(contract.CodeRequired).
        Message("name is required").
        Detail("field", "name").
        Build())
    return
}
```

Complex validation, domain policy, and service-specific error typing should
remain in the owning stable module, extension module, or reference application.
