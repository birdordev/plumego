package contract

// Canonical API error codes used by the contract module.
const (
	CodeBadRequest       = "BAD_REQUEST"
	CodeValidationError  = "VALIDATION_ERROR"
	CodeResourceNotFound = "RESOURCE_NOT_FOUND"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeForbidden        = "FORBIDDEN"
	CodeTimeout          = "TIMEOUT"
	CodeInternalError    = "INTERNAL_ERROR"
	CodeRateLimited      = "RATE_LIMITED"

	// Validation sub-codes
	CodeRequired      = "REQUIRED_FIELD_MISSING"
	CodeInvalidFormat = "INVALID_FORMAT"
	CodeOutOfRange    = "VALUE_OUT_OF_RANGE"
	CodeDuplicate     = "DUPLICATE_VALUE"

	// Auth sub-codes
	CodeInvalidToken = "INVALID_TOKEN"
	CodeExpiredToken = "EXPIRED_TOKEN"

	// Resource sub-codes
	CodeConflict      = "RESOURCE_CONFLICT"
	CodeAlreadyExists = "RESOURCE_ALREADY_EXISTS"
	CodeGone          = "RESOURCE_GONE"
	CodeNotAcceptable = "NOT_ACCEPTABLE"

	// System sub-codes
	CodeUnavailable      = "SERVICE_UNAVAILABLE"
	CodeMaintenance      = "MAINTENANCE_MODE"
	CodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	CodeNotImplemented   = "NOT_IMPLEMENTED"
	CodeBadGateway       = "BAD_GATEWAY"
	CodeGatewayTimeout   = "GATEWAY_TIMEOUT"
	CodeInvalidRequest   = "INVALID_REQUEST"

	// Request parsing codes
	CodeRequestBodyTooLarge  = "REQUEST_BODY_TOO_LARGE"
	CodeUnsupportedMediaType = "UNSUPPORTED_MEDIA_TYPE"
	CodePreconditionFailed   = "PRECONDITION_FAILED"
	CodeInvalidJSON          = "INVALID_JSON"
	CodeInvalidQuery         = "INVALID_QUERY"

	// Transport-layer codes used by infrastructure middleware (concurrencylimit,
	// singleflight). Values intentionally match the historical string shape for
	// backward compatibility with client integrations.
	CodeServerBusy         = "server_busy"
	CodeServerQueueTimeout = "server_queue_timeout"
	CodeUpstreamFailed     = "upstream_failed"
)
