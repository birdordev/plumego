// Package messaging is the canonical app-facing entrypoint for the messaging
// family.
//
// It is the only import point for building on the explicit queue, pubsub,
// scheduler, and webhook adapters; reach for the specific subpackage when an
// adapter's full API is needed.
package messaging
