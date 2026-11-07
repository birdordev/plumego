// Package cors provides Cross-Origin Resource Sharing middleware.
//
// [Middleware] attaches CORS response headers based on a [CORSOptions]
// policy. Use [StrictDefaultOptions] for a secure baseline: it rejects
// wildcard origins and reflects the request Origin only when it is explicitly
// allowed. A plain [CORSOptions] value can be used for permissive development
// setups.
package cors
