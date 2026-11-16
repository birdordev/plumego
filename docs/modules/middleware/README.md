# middleware

## Purpose

`middleware` contains transport-only HTTP middleware.

## v1 Status

- `ga` in the Plumego v1 support matrix
- The stable package surface should stay small and expose one canonical
  constructor path per middleware.

## Use this module when

- adding request/response wrappers
- enforcing ordering-sensitive transport behavior
- instrumenting requests without owning business policy

## Do not use this module for

- business validation
- tenant policy catalogs
- tenant resolution or tenant quota enforcement
- service construction
- API version negotiation
- resource or protocol transformation

## First files to read

- `middleware/module.yaml`
- `docs/reference/canonical-style-guide.md`
- the target package under `middleware/*`

## Canonical change shape

- implement `func(http.Handler) http.Handler`
- keep one constructor path per middleware package; delete parallel wrapper families
- keep stable middleware packages single-purpose; split unrelated transport behaviors into separate packages instead of umbrella buckets
- new configurable middleware should prefer `Middleware(Config)` with
  `Config.WithDefaults()` or `DefaultConfig()`; stateful middleware may expose
  `New(Config) *Type` with a `Middleware()` method when they own explicit
  runtime lifecycle
- intentional stable naming exceptions should be documented instead of treated
  as drift; current exceptions are `auth.Authenticate(...)` /
  `auth.Authorize(...)` for dependency-validating auth adapters,
  `cors.StrictDefaultOptions(...)` for strict preset generation, and
  `ratelimit.NewAbuseGuard(...).Middleware()` for lifecycle-owned rate
  limiting
- use `accesslog.Middleware(...)` as the canonical access-log constructor
- add ordering and error-path tests
- keep side effects explicit and local
- keep tenant-aware policy, resolution, and quota behavior in `x/tenant`
- keep auth and security-header transport adapters here, on top of `security/*` primitives
- keep stable rate limiting here as a thin `middleware/abuseguard` HTTP adapter over `security/abuse`, not as a catalog of limiter implementations
- if a task needs the reusable limiter primitive rather than the stable HTTP
  adapter, route it to `x/resilience/ratelimit` instead of widening
  `middleware/abuseguard`
- rate limiting defaults must use the direct `RemoteAddr` peer IP; applications behind trusted proxies can opt into `X-Forwarded-For`/`X-Real-IP` by supplying an explicit `ratelimit.AbuseGuardConfig.KeyFunc`
- if a custom rate-limit `KeyFunc` returns an empty or all-whitespace key,
  `AbuseGuard` falls back to the direct `RemoteAddr` peer IP instead of sharing
  one global empty-key bucket
- production code that lets ratelimit create its own limiter should use
  `ratelimit.NewAbuseGuard(...)`, wire `guard.Middleware()`, and call
  `guard.Stop()` during application shutdown; callers that inject
  `AbuseGuardConfig.Limiter` own that limiter lifecycle themselves
- keep API version negotiation in `x/rest/versioning`
- keep protocol or payload adaptation in `x/gateway/*`
- keep request-id generation policy in middleware-owned packages; `contract` should only carry request-id context/header contracts
- customize request-id generation through `requestid.WithGenerator(...)`; `requestid.NewRequestID()` is a convenience default backed by package-local generator state
- request-id middleware validates incoming and generated IDs with the context
  contract before forwarding them. A rejected header triggers ID generation,
  and a rejected custom ID falls back to the default generator.
- treat request IDs as correlation identifiers only; the default generated ID is decodable and must not be used as a secret, token, nonce, or authorization value
- when `requestid.Middleware()` is absent, stable observability middleware may still stamp a fallback request id so access logs, tracing, and HTTP metrics share one correlation path

## Boundary with observability

- stable `middleware/*` owns transport-only observability primitives such as request IDs, tracing hooks, access logging, and HTTP metrics
- recommended production composition uses standalone `httpmetrics.Middleware(...)`
  and `tracing.Middleware(...)`, with `accesslog.Middleware(accesslog.Config{Logger: logger})` for
  access-log emission only
- the production reference service has a regression test that records one HTTP
  metric for one request, guarding against accidentally duplicating
  observability middleware for the same signal
- exporter catalogs, backend setup, sampling policy, and pipeline composition belong outside stable middleware
- `x/observability` owns broader adapter, export, and integration wiring
- do not turn stable `middleware` into an observability catch-all catalog
- middleware-owned instrumentation and logging callbacks are best-effort;
  callback panics are recovered so observability cannot replace a downstream
  handler panic or prevent canonical transport errors from being written
- tracing start or span-identity failures preserve the original request and
  still invoke the downstream handler; any span returned before identity
  failure is ended once by the normal best-effort finalizer
- accesslog fields use the shared middleware redaction policy before they are
  passed to the configured logger
- `middleware/internal/telemetry` is the shared internal helper surface for
  stable observability middleware. It owns request preparation, safe
  finalization, shared metrics derivation, and redaction helpers, while
  exporter catalogs and backend wiring stay in `x/observability` or the owning
  application

## Production Security Profile

Plumego does not provide a hidden "production mode" bundle. Wire the stack
explicitly in application code so ordering and dependencies stay reviewable.

### Recommended middleware order

The conceptual ordering from outermost to innermost is:

```
requestid → recovery → securityheaders → timeout → bodylimit → cors → abuseguard → auth → httpmetrics/tracing → accesslog → handler
```

Plumego does not provide a RealIP middleware. Applications that need to trust
`X-Forwarded-For` or `X-Real-IP` for client IP extraction should configure
an explicit IP extraction strategy (for example, by supplying a custom
`KeyFunc` to `abuseguard.AbuseGuardConfig`) rather than relying on a generic
realip middleware. Transport-level client IP helpers are available in
`middleware/internal/httputil` for middleware authors.

**Recommended baseline order:**

1. `requestid.Middleware(...)` for correlation. Placed outermost so that every
   subsequent layer — including recovery — can include the request ID in logs.
2. `recovery.Middleware(recovery.Config{Logger: app.Logger()})` to convert
   panics from all downstream middleware and handlers into structured errors.
   Placing it directly after `requestid` ensures request IDs are present in
   panic logs.
3. `securityheaders.Middleware(securityheaders.Config{Policy: policy})` for
   response hardening. Place it outside timeout, bodylimit, CORS, and auth so
   security headers survive synthesized errors and preflight responses.
   Invalid custom header policies fail during construction.
4. `timeout.Middleware(timeout.Config{...})` for bounded request runtime.
5. `bodylimit.Middleware(bodylimit.Config{...})` for request body caps before
   the handler reads the body. Inside timeout, its error responses remain in
   the timeout-owned buffer.
6. `cors.Middleware(cors.CORSOptions{...})` for CORS negotiation. Positioned
   after timeout so preflight requests are subject to the deadline. Use
   `cors.StrictDefaultOptions(origin)` for explicit production origins.
7. `abuseguard.NewAbuseGuard(...).Middleware()` for transport abuse limits when
   the limiter is middleware-owned.
8. `auth.Authenticate(...)` and `auth.Authorize(...)` on protected route groups
   or handlers; both return `(middleware.Middleware, error)` so invalid
   dependencies fail during startup.
9. `httpmetrics.Middleware(...)` and `tracing.Middleware(...)` for transport
   telemetry when needed.
10. `accesslog.Middleware(accesslog.Config{Logger: app.Logger()})` for
    logging-only access logs.

Keep `recovery.Middleware(...)` directly after `requestid.Middleware(...)` in
generated and reference stacks so request IDs are available and all later
transport middleware remains downstream of recovery.
Recovery logs only sanitized panic metadata such as the panic type; raw panic
values are not a stable logging surface because panic payloads can contain
secrets or request-specific data.

Keep tenant resolution, quota, and tenant policy in `x/tenant`. Keep exporter
and telemetry backend wiring in `x/observability`. The stable middleware layer
should remain a set of explicit transport wrappers, not a business policy
catalog.

Recovery forwards informational statuses without marking the final response as
committed. A panic after `100`, `102`, or `103` can still produce a structured
`500`. `http.ErrAbortHandler` is re-thrown unchanged, allowing `net/http` to
abort the response instead of synthesizing an error or logging a normal panic.

### Request ID contract

`requestid.Middleware(...)` provides request correlation only. Generated request
IDs are safe to log as identifiers, but they are not secrets, tokens, nonces, or
authorization material. The default `requestid.NewRequestID()` format embeds a
timestamp component that can be decoded with `requestid.DecodeRequestID(...)`.
Applications that need a different shape or stricter generation policy should
wire `requestid.WithGenerator(...)`.

### Timeout contract

`timeout.Middleware(...)` creates a deadline-bound request context when
`Config.Timeout` is positive. `DefaultConfig()` leaves the timeout disabled;
callers must choose a duration explicitly. An expired middleware or parent
deadline returns a structured `504`. Explicit parent cancellation returns
without writing a synthetic error, and requests already canceled before
execution do not invoke the downstream handler.

Cancellation observed at the response commitment check takes priority over a
completed result or panic. Once that check accepts a downstream result, later
cancellation does not undo response commitment. Timeout does not forcibly stop
or wait for downstream work. Handlers and services must observe
`r.Context().Done()` to stop side effects promptly; body writes after cancellation
return the context error.

Timeout clones outer response headers into a private buffer. Successful
responses replay downstream header changes, including explicit deletion.
If a handler exits with `runtime.Goexit` while the context is still active,
timeout discards its buffer and sends a structured `500`.
Timeout and buffer-limit errors preserve the original outer headers and discard
downstream header changes and partial bodies. Responses larger than
`Config.MaxBufferBytes` or `Config.MaxReplayBytes` become a structured `500`
with the request ID from context. Treat these settings as response limits,
not streaming support. Informational statuses such as `100`, `102`, and `103`
are ignored while buffering and do not replace the final status.
Body writes for 204 and 304 return `http.ErrBodyNotAllowed` before either
response size limit is evaluated.

Panics accepted before cancellation are re-thrown on the request goroutine so
outer recovery middleware can handle them. `Config.OnPanic` observes panics
discarded because of cancellation exactly once, after response handling
finishes. The hook is best-effort and must not block; callback panics are
recovered internally.

### Concurrency limit contract

`concurrencylimit.Middleware(...)` bounds active handlers and optional
queued waiters. Queued requests observe `r.Context().Done()` while waiting for a
worker slot; if the request is canceled before a slot is available, the
middleware returns without invoking the downstream handler or writing a
synthetic queue-timeout error. Cancellation is checked again after slot
admission and before rejection responses, so an observed cancellation takes
priority when cancellation and capacity become available together.
Queue timeout responses include `queue_occupancy` and `queue_capacity` details;
`queue_occupancy` reports the internal queue channel occupancy, which includes
active and waiting requests represented in that channel.

### Gzip compression contract

`compression.Middleware(...)` compresses eligible non-error responses when the client
accepts gzip. It skips websocket upgrades, SSE, already-compressed responses,
common binary content types, range responses (206 or `Content-Range`), and
bodyless responses with status 204 or 304.
All `Accept-Encoding` field lines are evaluated as one list.
Negotiated gzip and identity responses include `Vary: Accept-Encoding` while
preserving other `Vary` values.

`Config.MaxBufferBytes` controls only the pre-compression buffer. If a
response exceeds the limit before gzip output has started, the middleware
bypasses compression and writes the response as-is. Once gzip output has
started, later writes continue through the gzip writer; the middleware does not
switch a partially compressed response back to an uncompressed response.
Gzip snapshots final headers at `WriteHeader` or the first implicit commit.
When the snapshot omits `Content-Type`, gzip can defer the compression decision
until the first body write so it can sniff the content type. Handler changes
after commitment can still supply trailers. A supported `Flush()` before the
first body write commits the selected status and headers and sends later writes
uncompressed. Before a final status is selected, an unsupported flush leaves the
response open and preserves the normal compression decision for later writes.

### Debug error contract

`debug.Middleware(...)` is a development-facing transport helper for replacing
empty or plain-text error responses with structured JSON. Do not wire it into a
production baseline unless the application owner explicitly accepts the risk of
debug metadata exposure.

Debug capture is bounded by `Config.MaxBodyBytes`. If a response
exceeds the capture limit, the middleware stops debug replacement and passes the
original response through. It skips websocket upgrades, CONNECT requests, SSE
requests, streaming content types, and responses with a non-identity content
encoding. JSON replacement preserves the original HTTP status and uses the
`contract` error envelope. It removes the original `Content-Length`, `ETag`,
and digest headers when the body changes. A status without a matching canonical
error type passes through. Interim `1xx`
responses are forwarded separately from the final response. Status `101` is
terminal. Final headers are snapshotted when the status is selected. Later
changes can supply trailers. Before a final status or body is written, an
unsupported `Flush` or `Hijack` leaves debug capture active. Successful flushes
and hijacks switch to pass-through behavior.
If draining the buffered body fails, `http.ResponseController.Flush` returns
the error and a later flush does not replay the buffered bytes.

### CORS contract

`cors.Middleware(...)` applies CORS headers only when the request origin and
preflight attributes match the configured allow lists. Disallowed origins,
methods, headers, and blank `Access-Control-Request-Headers` lists fall through
to the next handler without CORS headers; the middleware does not synthesize a
transport denial response for those cases. Both allowed and fall-through
responses vary by `Origin`. For OPTIONS, they also vary by
`Access-Control-Request-Method` and `Access-Control-Request-Headers`.
Raw option lists are trimmed and blank entries are ignored before matching, so
common configuration whitespace does not silently disable an origin, method, or
header.

Wildcard `AllowedHeaders` preserves requested header names after trimming and
uses the same blank-list validation as explicit allowed-header checks.

The zero-value options default to `AllowedOrigins: ["*"]`. When credentials are
enabled with wildcard origins, the middleware echoes the request `Origin` value
instead of returning `*`, matching browser CORS requirements for credentialed
requests.

Production stacks should pass explicit origins or start from
`cors.StrictDefaultOptions("https://app.example")`. `StrictDefaultOptions`
trims and filters blank origins, keeps the standard method/header defaults, and
returns an error when no valid origin is supplied, so a missing origin list cannot
silently become wildcard access. It also rejects `"*"` because strict CORS
defaults require explicit origins.

### Body limit contract

`bodylimit.Middleware(...)` records an overrun in the body reader without
writing a response from that reader. The response-owning goroutine emits a
structured `413` before its next write or when the downstream call returns,
provided the response has not started and the request context is still active.
Subsequent writes report their bytes as consumed without appending to the error.
An overrun after final headers or a flush cannot replace the committed response.
A zero-length read does not consume or probe the body. Flush follows the
underlying writer chain and returns its error through `http.ResponseController`.
A detected overrun suppresses the downstream flush.
An already committed error response is allowed to finish its body, including
a timeout error whose headers were committed before a concurrent overrun.

This keeps body reads safe when an inner timeout returns before the reader
finishes: late reads cannot write to the original response. Prefer placing
bodylimit inside timeout so all body-limit responses use timeout's private
buffer. Outside timeout, an overrun already observed when the response is
committed takes priority over that response; late overruns never append data.

### Rate limit contract

`ratelimit.NewAbuseGuard(...)` is the lifecycle entrypoint. Register
`guard.Middleware()` and call `guard.Stop()` during application shutdown when
the guard created the limiter. When `AbuseGuardConfig.Limiter` is supplied, the
caller owns that limiter and `guard.Stop()` is a no-op for it.

### Response writer compatibility

Buffering and capture middleware do not all preserve optional response writer
interfaces. Use this matrix when placing middleware around streaming, SSE, or
websocket handlers. The shared conformance suite covers panic propagation,
`Flush`, `Hijack`, post-timeout writes, and partial gzip panic finalization for
the high-risk wrappers represented here.
Each `Unwrap`, `Flush`, and `Hijack` claim in the matrix is also covered by a
shared positive or negative conformance case. See
`middleware/conformance/README.md` for the scope of that test-only suite and
when a shared invariant belongs there instead of in a package-local test.

| Package | Buffers response body | `Unwrap` | `Flush` | `Hijack` | Streaming / websocket guidance |
|---|---:|---:|---:|---:|---|
| `accesslog` | no | yes | yes | yes | compatible with underlying writer support |
| `bodylimit` | no response buffering | yes | yes | yes | request-body limiting only; post-overrun writes are suppressed |
| `coalesce` | bounded capture for waiters | yes | yes | yes | use only for bounded safe responses; not for streaming/SSE/websocket |
| `compression` | pre-compression buffer | yes | yes | yes before compression starts | skips websocket/SSE; gzip output stays gzip once started |
| `debug` | bounded capture | yes | yes | yes | development-only; skips declared streaming, passes through on capture overflow or successful flush/hijack |
| `httpmetrics` | no | yes | yes | yes | compatible with underlying writer support |
| `recovery` | no | yes | yes | yes | compatible with underlying writer support; cannot rewrite responses after headers/body are committed |
| `timeout` | full bounded replay buffer | no | no | no | not for streaming/SSE/websocket; large responses become structured errors |
| `tracing` | no | yes | yes | yes | compatible with underlying writer support |

### Coalesce capture contract

`coalesce.New(...).Middleware()` is a GA stable but high-risk transport response
coalescer, not a cache or a business freshness policy. It is appropriate only
for bounded, safe, non-streaming responses whose variants are fully represented
by the coalesce key. It forwards the leader request response to the leader client
while capturing a bounded copy for concurrent waiters with the same coalesce key.
The default `Config.MaxResponseBytes` is 10MB. If the leader response exceeds
the capture limit, the leader still receives the upstream response, but waiters
receive a structured upstream failure instead of replaying an unbounded
in-memory body. When the transport supports a leader `Flush` or the leader
successfully hijacks the connection, coalesce treats the response as unreplayable.
The leader keeps the pass-through
transport behavior, while waiters receive the same structured upstream failure.
An unsupported flush leaves the response eligible for replay.
Waiters also receive that failure when writing the leader response fails,
including a short write.

Only safe methods should be configured for coalescing. The default covers `GET`
and `HEAD`; configured methods are trimmed, uppercased, and blank entries are
ignored. Coalesced `HEAD` waiters receive the replayed status and headers
without a response body. Do not use coalesce for streaming, SSE, websocket,
long-polling, or response bodies whose correctness depends on per-request side
effects outside the key. When the leader explicitly commits headers with
`WriteHeader`, waiter replay uses that committed header snapshot rather than
later header mutations. Declared and dynamic response trailers are forwarded
to the leader and replayed for non-HEAD waiters.
The handler starts with the leader's outer response headers. Its committed
changes are applied to each waiter's own headers, including deletions and
new values appended to an existing header. Unchanged outer values stay with
their own requests.

`Config.OnCoalesced` is called once for each waiter that successfully receives a
coalesced response. The callback `count` argument is a per-callback event count
and is currently always `1`; timed-out waiters are not counted. Aggregate in the
caller when total coalesced request counts are needed.
An error or short write while replaying to a waiter calls `Config.OnError`
with a fixed diagnostic and does not count as a successful replay.
Waiters also observe their request context while waiting for the leader. If a
waiter request is canceled, coalesce returns without replaying the leader
response or writing a synthetic timeout response. Cancellation observed before
handling a ready leader result or timer takes priority over writing a response.
`Config.OnCoalesced` and `Config.OnError` are synchronous best-effort hooks;
panic from either hook is recovered internally so hooks cannot replace canonical
transport responses.

The default coalesce key is an FNV hash over method, host, URL, and common
variant headers. It is a transport deduplication key, not a security boundary;
handlers with additional response variants should provide an explicit `KeyFunc`.
A custom `KeyFunc` that returns an empty or all-whitespace key fails open:
coalesce forwards that request without creating or joining an in-flight slot.

## Internal transport primitives

`internal/httputil` contains shared response-writing helpers used across middleware packages:

- `EnsureNoSniff(h http.Header)` — sets `X-Content-Type-Options: nosniff` unless already present
- `SafeWrite(w http.ResponseWriter, body []byte)` — writes body and sets the nosniff header; silently no-ops for nil writers
- `AddVary(h http.Header, values ...string)` — appends `Vary` tokens without duplicating existing comma-separated values
- `CopyHeaders(dst, src http.Header)` — overlays cloned source values onto destination headers and preserves destination keys absent from `src`
- `ReplaceHeaders(dst, src http.Header)` — replaces the complete destination header set with cloned source values; use for buffered response replay paths that own the full response header set
- `ClientIP(r *http.Request)` — extracts client IP from `X-Forwarded-For`, `X-Real-IP`, or `RemoteAddr` in that order
- `DirectClientIP(r *http.Request)` — extracts the direct peer IP from `RemoteAddr` only; use for security-sensitive defaults when trusted proxies are not configured
- `ResponseRecorder` — wraps an `http.ResponseWriter` to capture status code, body, and bytes written
- `BufferedResponse` — buffers the full response body with an optional max-bytes overflow guard; supports `WriteTo` for deferred flushing

These are repo-internal helpers; add new callers only through
`specs/dependency-rules.yaml` so intentional internal package use remains
visible.

`CopyHeaders` overlays destination values and preserves destination keys absent
from `src`. Use `ReplaceHeaders` when replaying a fully buffered response that
should remove stale destination headers.

Outer access logging, HTTP metrics, and tracing receive the matched route template through synchronized response observers. Metadata publication carries no services and does not mutate the request or response headers. A missing match continues to use the original path.

HTTP observation middleware preserves informational responses, implicit header-only responses, Flush commitment, and declared/dynamic trailers through the underlying net/http writer. Final status is recorded independently of informational status codes. Ordinary header changes after commitment do not rewrite the transmitted snapshot. Observation records status and byte counts without retaining response bodies; internal capture users such as gateway caching keep their explicit capture recorder.

Observation and recovery wrappers forward `http.ResponseController.Flush` through the underlying writer chain. An unsupported flush leaves an uncommitted response available for the handler to write. Transport flush errors are returned to the caller.
