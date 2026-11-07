// Package debug provides development-oriented error response middleware.
//
// [Middleware] replaces empty or plain-text error responses with structured
// JSON error details (request path, query, and optionally body) so handlers can
// be inspected locally. It is intended for development environments only; see
// the security note on [Config] before enabling request-body inclusion.
package debug
