// Package observability is the app-facing entrypoint for broader observability
// adapters and export wiring.
//
// Transport observability primitives (access logs, HTTP metrics, tracing)
// remain in stable middleware packages; this family owns exporters, adapters,
// and diagnostics that build on them.
package observability
