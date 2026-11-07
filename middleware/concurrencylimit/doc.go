// Package concurrencylimit provides bounded-concurrency request middleware.
//
// [Middleware] caps the number of requests processed concurrently and queues
// excess requests up to a bounded depth. When no slot frees up in time the
// queued request is rejected, so a slow upstream can never exhaust the server.
// Start from [DefaultConfig] or tune [Config] for the target concurrency and
// queue behavior.
package concurrencylimit
