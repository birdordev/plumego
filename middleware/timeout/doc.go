// Package timeout provides request deadline enforcement middleware.
//
// [Middleware] enforces a maximum duration for request processing. When the
// downstream handler misses the deadline, the request context is canceled and
// a 504 Gateway Timeout response is returned. Responses are buffered up to a
// configured limit so a timed-out request cannot emit a partial response body.
//
// As with the standard library's http.TimeoutHandler, the deadline only
// cancels the request context: the handler goroutine keeps running until it
// observes the cancellation and returns. Handlers must check the context to
// stop work promptly, otherwise CPU and resources are still consumed after the
// 504 has been sent.
package timeout
