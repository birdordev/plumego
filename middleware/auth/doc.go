// Package auth provides authentication and authorization middleware for HTTP
// handlers.
//
// The middleware adapts the authenticator and authorizer contracts from
// [security/authn] into the [middleware.Middleware] signature. Start with
// [Authenticate] to enforce credential verification, then layer [Authorize] or
// [AuthorizeFunc] to gate a specific action and resource.
//
// The package only guards the request boundary: it never owns session
// lifecycle, tenant resolution, or quota policy (those belong in x/tenant).
// Successful authentication leaves a [security/authn.Principal] in the request
// context for downstream handlers.
package auth
