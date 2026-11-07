// Package httpmetrics provides HTTP metrics collection middleware.
//
// [Middleware] observes each request/response and forwards structured metrics
// to a [metrics.HTTPObserver] (aliased as [Observer]). Wire it into the
// handler chain when an HTTP-level metric collector is configured; a nil
// observer is a safe no-op passthrough.
package httpmetrics
