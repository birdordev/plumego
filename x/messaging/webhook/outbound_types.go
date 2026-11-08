package webhook

import "time"

// Target represents a webhook endpoint configuration.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	target := webhookout.Target{
//		ID:      "target-123",
//		Name:    "Payment Service",
//		URL:     "https://api.example.com/webhooks",
//		Secret:  "secret-key-here",
//		Events:  []string{"payment.success", "payment.failed"},
//		Enabled: true,
//		Headers: map[string]string{
//			"X-API-Version": "v1",
//		},
//		TimeoutMs:     5000,
//		MaxRetries:    3,
//		BackoffBaseMs: 1000,
//		BackoffMaxMs:  30000,
//		RetryOn429:    boolPtr(true),
//	}
type Target struct {
	// ID is the unique identifier for the target
	ID string `json:"id"`

	// Name is a human-readable name for the target
	Name string `json:"name"`

	// URL is the endpoint URL where webhooks will be sent
	URL string `json:"url"`

	// Secret is used for signing webhook payloads (optional)
	Secret string `json:"secret,omitempty"`

	// Events is a list of event types this target subscribes to
	Events []string `json:"events"`

	// Enabled determines if this target is active
	Enabled bool `json:"enabled"`

	// Headers are custom HTTP headers to include in webhook requests
	Headers map[string]string `json:"headers,omitempty"`

	// TimeoutMs is the request timeout in milliseconds
	TimeoutMs int `json:"timeout_ms,omitempty"`

	// MaxRetries is the maximum number of retry attempts
	MaxRetries int `json:"max_retries,omitempty"`

	// BackoffBaseMs is the base delay for exponential backoff in milliseconds
	BackoffBaseMs int `json:"backoff_base_ms,omitempty"`

	// BackoffMaxMs is the maximum delay for exponential backoff in milliseconds
	BackoffMaxMs int `json:"backoff_max_ms,omitempty"`

	// RetryOn429 determines if 429 (Too Many Requests) responses should be retried
	RetryOn429 *bool `json:"retry_on_429,omitempty"`

	// CreatedAt is the timestamp when the target was created
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the timestamp when the target was last updated
	UpdatedAt time.Time `json:"updated_at"`
}

// TargetPatch represents partial updates to a webhook target.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	enabled := true
//	patch := webhookout.TargetPatch{
//		Enabled: &enabled,
//		URL:     stringPtr("https://new-url.com/webhooks"),
//	}
type TargetPatch struct {
	// Name is a human-readable name for the target
	Name *string `json:"name,omitempty"`

	// URL is the endpoint URL where webhooks will be sent
	URL *string `json:"url,omitempty"`

	// Secret is used for signing webhook payloads (optional)
	Secret *string `json:"secret,omitempty"`

	// Events is a list of event types this target subscribes to
	Events *[]string `json:"events,omitempty"`

	// Enabled determines if this target is active
	Enabled *bool `json:"enabled,omitempty"`

	// Headers are custom HTTP headers to include in webhook requests
	Headers *map[string]string `json:"headers,omitempty"`

	// TimeoutMs is the request timeout in milliseconds
	TimeoutMs *int `json:"timeout_ms,omitempty"`

	// MaxRetries is the maximum number of retry attempts
	MaxRetries *int `json:"max_retries,omitempty"`

	// BackoffBaseMs is the base delay for exponential backoff in milliseconds
	BackoffBaseMs *int `json:"backoff_base_ms,omitempty"`

	// BackoffMaxMs is the maximum delay for exponential backoff in milliseconds
	BackoffMaxMs *int `json:"backoff_max_ms,omitempty"`

	// RetryOn429 determines if 429 (Too Many Requests) responses should be retried
	RetryOn429 *bool `json:"retry_on_429,omitempty"`
}

// TargetFilter is used to filter webhook targets.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	enabled := true
//	filter := webhookout.TargetFilter{
//		Enabled: &enabled,
//		Event:   "payment.success",
//	}
type TargetFilter struct {
	// Enabled filters targets by their enabled status
	Enabled *bool

	// Event filters targets by subscribed event type
	Event string
}

// Event represents a webhook event to be delivered.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	event := webhookout.Event{
//		ID:         "evt-123",
//		Type:       "payment.success",
//		OccurredAt: time.Now(),
//		Data: map[string]any{
//			"amount":     100.00,
//			"currency":   "USD",
//			"customer_id": "cust-456",
//		},
//		Meta: map[string]any{
//			"source": "api",
//		},
//	}
type Event struct {
	// ID is the unique identifier for the event
	ID string `json:"id"`

	// Type is the event type (e.g., "payment.success", "user.created")
	Type string `json:"type"`

	// OccurredAt is when the event occurred
	OccurredAt time.Time `json:"occurred_at"`

	// Data contains the event payload
	Data map[string]any `json:"data"`

	// Meta contains optional metadata about the event
	Meta map[string]any `json:"meta,omitempty"`
}

// DeliveryStatus represents the status of a webhook delivery.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	status := webhookout.DeliverySuccess
//	if err != nil {
//		status = webhookout.DeliveryFailed
//	}
type DeliveryStatus string

const (
	// DeliveryPending indicates the delivery is queued and waiting to be sent
	DeliveryPending DeliveryStatus = "pending"

	// DeliveryRetry indicates the delivery is being retried after a failure
	DeliveryRetry DeliveryStatus = "retrying"

	// DeliverySuccess indicates the delivery was successful
	DeliverySuccess DeliveryStatus = "success"

	// DeliveryFailed indicates the delivery failed after all retry attempts
	DeliveryFailed DeliveryStatus = "failed"

	// DeliveryDead indicates the delivery is dead (too many failures)
	DeliveryDead DeliveryStatus = "dead"
)

// Delivery represents a webhook delivery attempt.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	delivery := webhookout.Delivery{
//		ID:        "del-123",
//		TargetID:  "target-456",
//		EventID:   "evt-789",
//		EventType: "payment.success",
//		Attempt:   1,
//		Status:    webhookout.DeliverySuccess,
//	}
type Delivery struct {
	// ID is the unique identifier for the delivery
	ID string `json:"id"`

	// TargetID is the ID of the target this delivery is for
	TargetID string `json:"target_id"`

	// EventID is the ID of the event being delivered
	EventID string `json:"event_id"`

	// EventType is the type of the event
	EventType string `json:"event_type"`

	// PayloadJSON is the replayable payload (raw JSON) - stored per delivery
	// NOTE: kept as []byte in store; handlers can choose to omit it in list output if desired.
	PayloadJSON []byte `json:"payload_json,omitempty"`

	// Attempt is the number of delivery attempts
	Attempt int `json:"attempt"`

	// Status is the current delivery status
	Status DeliveryStatus `json:"status"`

	// NextAt is the time when the next retry will be attempted
	NextAt *time.Time `json:"next_at,omitempty"`

	// LastHTTPStatus is the HTTP status code from the last attempt
	LastHTTPStatus int `json:"last_http_status,omitempty"`

	// LastError is the error message from the last attempt
	LastError string `json:"last_error,omitempty"`

	// LastDurationMs is the duration of the last attempt in milliseconds
	LastDurationMs int `json:"last_duration_ms,omitempty"`

	// LastRespSnippet is a snippet of the last response body
	LastRespSnippet string `json:"last_resp_snippet,omitempty"`

	// CreatedAt is the timestamp when the delivery was created
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the timestamp when the delivery was last updated
	UpdatedAt time.Time `json:"updated_at"`
}

// DeliveryFilter is used to filter webhook deliveries.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	filter := webhookout.DeliveryFilter{
//		TargetID: stringPtr("target-123"),
//		Status:   (*webhookout.DeliveryStatus)(&webhookout.DeliverySuccess),
//		Limit:    100,
//	}
type DeliveryFilter struct {
	// TargetID filters deliveries by target ID
	TargetID *string

	// Event filters deliveries by event type
	Event *string

	// Status filters deliveries by status
	Status *DeliveryStatus

	// Limit is the maximum number of results to return
	Limit int

	// Cursor is a pagination cursor
	Cursor string

	// From filters deliveries created after this time
	From *time.Time

	// To filters deliveries created before this time
	To *time.Time
}
