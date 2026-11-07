// Package bodylimit provides request body size limiting middleware.
//
// [Middleware] reads the incoming body up to a configured maximum and rejects
// larger requests, protecting downstream handlers from unbounded uploads.
// Configure it with [DefaultConfig] or a custom [Config]; a permissive maximum
// disables enforcement for that route.
package bodylimit
