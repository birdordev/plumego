# security

## Purpose

`security` contains reviewable authentication, header, input-safety, and abuse-guard primitives.

## v1 Status

- `ga` in the Plumego v1 support matrix
- Public compatibility is expected for the stable package surface

## Use this module when

- verifying tokens or signatures
- enforcing security headers
- validating hostile or malformed input

## Do not use this module for

- app bootstrap
- business-specific authorization policy hidden in middleware
- logging secrets or tokens

## First files to read

- `security/module.yaml`
- the target package under `security/*`
- `AGENTS.md` security rules

## Canonical change shape

- fail closed
- keep verification explicit
- add negative tests for invalid input and invalid credentials
- keep `Principal`, `Authenticator`, `Authorizer`, and the canonical `WithPrincipal(...)` / `PrincipalFromContext(...)` accessors in `security/authn`
- parse bearer credentials with an exact `Bearer` scheme and whitespace delimiter; query-string tokens remain ignored
- keep JWT, header, and signature logic in `security/*` as primitives and policies
- keep session revocation, token-version invalidation, and tenant-session sentinel errors in `x/tenant/session`, not in stable `security/*`
- keep reusable resilience primitives such as circuit breakers in `x/resilience`, not in stable `security/*`
- route HTTP adapter wiring through `middleware/auth` and `middleware/securityheaders`

## Production Profile Relationship

Use `security/*` for reviewable primitives and policies:

- v1 security primitives fail closed by default; retained compatibility paths
  are limited to entries explicitly documented in `specs/deprecation-inventory.yaml`.
- `security/authn` owns principals, authenticators, authorizers, and context accessors.
- `security/authn` context helpers defensively copy mutable principal fields.
- `security/authn.StaticToken` compares fixed credentials through fixed-length digest comparison.
- `security/headers` owns header policies consumed by `middleware/securityheaders`.
- `security/headers.Policy.Validate` reports unsafe configured header names and values before runtime; `Policy.Apply` fails closed without writing headers when policy validation fails, and `Policy.ApplyChecked` also returns the validation error.
- Production-facing examples should prefer `middleware/securityheaders.Middleware(security.Config{...})` or direct `Policy.ApplyChecked` when error handling is required.
- `middleware/securityheaders.Middleware` validates custom policies during construction instead of applying a partially invalid policy at runtime.
- `security/headers` treats requests as HTTPS only for direct TLS, an all-HTTPS `X-Forwarded-Proto` chain, or an all-HTTPS RFC `Forwarded` proto chain; `X-Forwarded-Ssl` alone is ignored.
- `security/headers.CSPBuilder` rejects unsafe directive values so caller-provided semicolons or controls cannot create extra directives; `Build` returns an empty header on invalid sources, while `BuildChecked` or `Validate` return the error.
- `security/input` owns input-safety checks and rejects unsafe HTTP header names or values before they reach transport adapters.
- `security/input.ValidateURL` rejects embedded userinfo credentials, non-decimal or out-of-range URL ports, and unsafe relative URL forms such as network-path, raw-control, or raw-backslash paths; it is a URL shape and scheme check, not an SSRF boundary.
- `security/input.ValidatePublicURL` is the SSRF-sensitive helper for absolute HTTP(S) fetch targets with literal localhost, private, link-local, metadata, multicast, and unspecified IP targets rejected.
- `security/input.ValidateEmail` applies DNS-style domain label checks.
- `security/input` sanitizer helpers are lossy best-effort defense-in-depth utilities, not replacements for context-aware sanitizers, parameterized queries, or output encoding.
- `security/input.BestEffortSanitizeHTML` covers script blocks and quoted or unquoted inline event handlers as a basic defense-in-depth helper.
- `security/input.BestEffortSanitizeSQL` removes line comments, semicolons, common SQL keywords, and single-line or multiline block comments as a defense-in-depth helper only.
- `security/abuse` owns abuse guard decisions consumed by `middleware/abuseguard`.
- `security/abuse.Config.Validate` and `NewLimiterWithConfig` treat zero fields as omitted defaults and reject explicit negative values; `NewLimiter` fails closed with a disabled limiter when explicit configuration is invalid.
- `security/abuse.Allow` denies empty limiter keys, and `AllowKey` additionally returns `ErrInvalidKey` for callers that need to classify the extraction failure.
- `security/abuse` reports limiter bucket metrics from the same accounting path used for eviction and cleanup decisions.
- `security/jwt` and `security/password` own token and password primitives.
- `security/jwt` verification fails closed when configured issuer, configured audience, or subject claims are missing or mismatched.
- `security/jwt` rejects malformed or oversized compact token envelopes before payload decoding.
- `security/jwt` verification requires a valid JWT header type, matching header/payload key IDs, and valid persisted signing key material.
- `security/jwt` rejects tokens at or after the expiration boundary, including configured clock skew; verification does not add an extra second of validity. Expiration checks avoid adding skew to untrusted timestamps.
- `security/jwt` rejects persisted Ed25519 keys whose seed, private key, and public key are inconsistent. Key decoding and validation errors omit persisted field values and storage key names; raw decoder errors are not retained in the error chain.
- `security/jwt.JWTManager` accepts a minimal key-store interface instead of binding to a concrete stable store implementation.
- `security/jwt.JWTManager` treats an absent active signing-key marker as recoverable, but fails startup when a persisted active marker cannot be read.
- `security/jwt.JWTManager.RotateKey` returns signing-key metadata only; HMAC secrets and Ed25519 private keys remain internal.
- `security/jwt.JWTConfig.Validate` rejects negative rotation intervals and clock skew values.
- `security/jwt` verification is read-only with respect to signing key rotation; key rotation happens while issuing tokens or through explicit rotation.
- `security/jwt` verification rejects tokens that omit issued-at, not-before, or expiration claims; issued-at values in the future beyond configured clock skew are invalid.
- `security/jwt` generation and verification honor canceled caller contexts before expensive work.
- `security/jwt` generation, verification, and key rotation evaluate time through a single manager clock path so boundary behavior stays consistent.
- `security/jwt` context and principal helpers defensively copy mutable role and permission slices.
- `security/jwt` authorizer helpers fail closed for empty policies or empty permission targets unless callers set the explicit `AllowEmpty` compatibility flag.
- `security/password` exposes sentinel errors for invalid cost, invalid stored hash, and password mismatch so callers can classify failures with `errors.Is`.
- `security/password` enforces `MinimumCost` and `MaximumCost` bounds for generated and stored PBKDF2 hashes, rejects plaintext password inputs over `MaxPasswordLength` with `ErrPasswordTooLong`, and validates stored salt/hash lengths before verification.

HTTP request wiring belongs in `middleware/auth`, `middleware/securityheaders`, and
`middleware/abuseguard`. Application-specific authorization decisions should be
constructor-injected into those adapters rather than hidden behind package
globals.
