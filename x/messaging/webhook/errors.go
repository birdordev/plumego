package webhook

import "errors"

var (
	// Outbound / delivery

	// ErrQueueFull is returned when the delivery queue has reached capacity.
	ErrQueueFull   = errors.New("webhook: delivery queue full")
	errQueueClosed = errors.New("webhook: delivery queue closed")

	// ErrSignatureMismatch is returned when a webhook signature does not match the expected value.
	ErrSignatureMismatch = errors.New("webhook: signature verification failed")

	// ErrTargetNotFound is returned when a webhook target or delivery cannot be found.
	ErrTargetNotFound = errors.New("webhook: target not found")

	// Inbound verification — shared

	// ErrInvalidHexEncoding is returned when a signature or secret value contains
	// invalid hexadecimal encoding. Replaces the former per-provider duplicates
	// ErrGitHubInvalidEncoding, ErrStripeInvalidEncoding, and ErrInvalidHex.
	ErrInvalidHexEncoding = errors.New("webhook: invalid hex encoding")

	// Inbound verification — GitHub

	ErrGitHubSignature     = errors.New("webhook: invalid github signature")
	ErrGitHubMissingHeader = errors.New("webhook: missing github signature header")

	// Inbound verification — Stripe

	ErrStripeSignature        = errors.New("webhook: invalid stripe signature")
	ErrStripeMissingHeader    = errors.New("webhook: missing stripe signature header")
	ErrStripeInvalidTimestamp = errors.New("webhook: invalid timestamp in signature")
	ErrStripeExpired          = errors.New("webhook: signature expired")
)
