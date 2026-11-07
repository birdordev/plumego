// Package compression provides response compression middleware.
//
// [Middleware] negotiates the client Accept-Encoding header and compresses
// eligible responses with gzip. Use [DefaultConfig] for a sensible baseline or
// a custom [Config] to tune minimum thresholds and compressible content types.
package compression
