# x/websocket

## Purpose

`x/websocket` owns websocket server helpers and explicit route registration for websocket transport, plus an RFC 6455 client `Dial` for outbound websocket connections.

## v1 Status

- `beta` in the Plumego v1 support matrix
- Included in repository release scope with beta compatibility obligations
- Promoted at `v0.2.0` after release-backed evidence showed no exported
  `x/websocket` API changes across refs `d2c25c3` and `ec70358`, with
  `realtime` owner sign-off recorded in
  `docs/evidence/extension/x-websocket.md`

## Use this module when

- the task is websocket transport behavior
- the change is connection lifecycle or hub behavior
- a use-case needs to dial an outbound websocket server (e.g., for active health probes); use `Dial` rather than adding a third-party websocket dependency

## Do not use this module for

- app bootstrap
- generic HTTP routing outside websocket transport

## First files to read

- `x/websocket/module.yaml`
- `x/websocket/websocket.go`
- `reference/standard-service` when checking canonical bootstrap shape

## Public entrypoints

- `New`
- `Server`
- `WebSocketConfig`
- `DefaultWebSocketConfig`
- `ServerConfig`
- `Message`
- `MessageHandler`
- `RoomNameValidator`
- `TokenAuthenticator`
- `RoomAuthorizer`
- `SimpleRoomAuth`
- `NewSimpleRoomAuth`
- `SimpleHS256TokenAuth`
- `NewSimpleHS256TokenAuth`
- `SecurityConfig`
- `SecurityMetrics`
- `SecureRoomAuth`
- `NewSecureRoomAuth`
- `RoomAuthorization`
- `RoomRequestAuthorizer`
- `ExtractUserInfo`
- `SendBehavior`
- `Conn`
- `NewConn`
- `Outbound`
- `UserInfo`
- `Dial`
- `DialOptions`
- `Hub`
- `HubConfig`
- `HubMetrics`
- `NewHub`
- `NewHubWithConfig`
- `BroadcastResult`
- `ServeWSWithConfig`
- `ServeRoomFanoutWS`
- `ValidateRoomName`
- `MaxRoomNameLength`
- `MessageValidationConfig`
- `DefaultMessageValidationConfig`
- `ValidateTextMessage`
- `SanitizeForLogging`
- `ValidationError`
- `NewValidationError`
- `CloseError`
- `NewCloseError`
- RFC6455 opcode and close-code constants
- documented websocket error sentinels

## Main risks when changing this module

- websocket auth regression
- broadcast behavior regression
- connection lifecycle regression

## Boundary rules

- keep websocket setup explicit and out of `core`; do not add hidden goroutines or global state at import time
- keep transport concerns (`ServeWSWithConfig`) inside `x/websocket`; do not push connection-level logic into stable roots or middleware
- keep product behavior out of `ServeWSWithConfig`; pass `WebSocketConfig.OnMessage` or `ServerConfig.OnMessage` for custom handling and use `ServeRoomFanoutWS` only for the built-in room fanout helper
- keep auth and broadcast gates reviewable and testable in isolation
- require token authentication by default in `ServeWSWithConfig`; pass `TokenAuth` or configure `Secret` through `New`, and set `AllowUnauthenticated` only for room-password-only development or trusted internal flows
- treat origin allow-all as an explicit opt-in through `AllowAllOrigins`; `AllowedOrigins: ["*"]` is not an allow-all shortcut
- treat query-string JWT transport as disabled by default; set `AllowQueryToken` only for trusted non-browser clients that cannot send headers
- keep `DefaultWebSocketConfig` free of environment reads; callers must pass secrets explicitly
- keep `New(WebSocketConfig)` defaulting deterministic: a minimal config with only `Secret` receives the same queue, timeout, route, fanout, and broadcast-body defaults as `DefaultWebSocketConfig`; setting `OnMessage` makes the registered websocket route use the caller handler instead of built-in room fanout
- keep admin broadcast disabled by default; enable it only with a dedicated `BroadcastSecret` or `BroadcastAuthorizer`
- keep admin broadcast request bodies bounded with `BroadcastMaxBytes`
- require `Sec-WebSocket-Version: 13` during handshake
- validate room names before hub registration and admin room-targeted broadcast; the default policy allows only ASCII letters, digits, `.`, `_`, `:`, and `-`, with a maximum length of 128 bytes
- treat `RegisterRoutes` errors as assembly failures; nil registrars, nil hubs, empty websocket paths, and empty enabled broadcast paths must fail visibly
- perform the real Hub join before writing `101 Switching Protocols`; if capacity changes after the pre-check, return a normal HTTP error before upgrade
- treat `Hub.Stop` worker drain as bounded; `SendBlock` connections without their own timeout use the hub worker fallback deadline instead of blocking indefinitely
- treat `Hub.Shutdown` as a hard connection close path, not a WebSocket close-frame handshake
- use `NewConn` for connection construction with explicit validation errors
- use `NewHubWithConfig` for hub configuration with explicit validation errors
- treat `SetReadLimit` as a fail-visible mutator; non-positive limits are rejected
- expect `WriteClose` to return the close-frame write error when the frame cannot be sent
- use `WriteTimeout` or `Conn.SetWriteTimeout` to bound network frame writes; `SendTimeout` only controls enqueue behavior
- read `ReadMessageReader` results to EOF before `Close`; early close hard-closes the parent connection
- reject non-positive ping/pong durations at setter boundaries
- treat `ValidateTextMessage` as transport-level text validation only; do not add heuristic XSS, SQL, or business-content scanners to this package
- use `SanitizeForLogging` before logging user-provided message content; it truncates, replaces invalid UTF-8, and replaces all control characters including newlines and tabs
- reject malformed RFC6455 frames: non-zero RSV bits, reserved opcodes, non-minimal payload lengths, malformed close payloads, and invalid continuation ordering
- reject unsupported public write opcodes before they enter the send queue; application writes are text or binary only
- close invalid inbound payloads with RFC6455 status codes instead of silently dropping them
- treat inbound reads as bounded whole-message reads: `ReadLimit` applies to the total fragmented message, and `ReadMessageReader` reads continuation frames incrementally without claiming an unbounded streaming bypass
- treat `TryJoin` as the only public join path; all joins enforce capacity and closed-state checks
- treat nil connections as invalid hub inputs; they are rejected before capacity or broadcast paths can observe them
- keep duplicate `TryJoin` calls idempotent even when the hub or room is already at capacity
- treat `HubMetrics.ActiveConnections` as unique connections and `HubMetrics.RoomRegistrations` as connection-room registrations
- pass `HubConfig.Logger` for hub logs; the default hub logger discards output
- keep token authentication and room authorization as separate policies; use `SimpleHS256TokenAuth` for compact HS256 tokens and `SimpleRoomAuth` for room passwords
- carry built-in room passwords in the `X-WebSocket-Room-Password` header, not URL query parameters
- pass `RoomNameValidator` only when an application needs a narrower or wider room-name policy than the default transport-safe identifier set
- handle room-password setup errors explicitly; do not hide hash failures behind log-only behavior
- keep security metrics instance-scoped (`SecureRoomAuth.GetMetrics`, `Hub.Metrics`) instead of reintroducing global wrappers
- treat `x/websocket` as the app-facing websocket transport surface; app-level session management belongs in the calling handler
- use `Dial(ctx, url, *DialOptions)` for outbound websocket connections; the returned `*Conn` runs in client framing mode (outgoing frames masked per RFC 6455 §5.3, server frames must arrive unmasked) and the underlying transport is the HTTP client's response body, so per-frame write deadlines are no-ops — bound dial latency through the supplied context or `HTTPClient.Timeout`
- attribute upstream code: the client handshake in `dial.go` is adapted from the ISC-licensed `github.com/coder/websocket` and intentionally omits permessage-deflate, ping/pong callbacks, and subprotocol fallback recovery; do not silently re-add them without a follow-up review

## Current test coverage

- connection configuration (read limit, ping period, pong wait)
- connection construction validation and write-after-close behavior
- hub construction validation, caller-owned logging, and metrics count semantics
- RFC6455 frame parsing negatives for RSV bits, reserved opcodes, non-minimal lengths, close payloads, masking, and continuation ordering
- fragmented and unfragmented read-limit enforcement at and above configured limits
- `Hub` lifecycle: `Stop` idempotency, bounded stop with full send queues, `Shutdown` (empty and with hard-closed connections, context cancellation), `TryJoin`/`Leave`/`RemoveConn` lifecycle, `RangeConns` iteration and early return
- capacity errors: `ErrHubFull`, `ErrRoomFull`, `ErrHubStopped` from `TryJoin`/`CanJoin` after stop or at limit
- broadcast: `BroadcastRoom`, `BroadcastAll`, `TryBroadcastRoom`, `TryBroadcastAll` (positive path, partial delivery, total rejection, metrics-disabled drop accounting, and no-op after stop), race-condition coverage under concurrent goroutines
- security: `ValidateSecurityConfig`, `ValidateWebSocketKey`, `ValidateRoomPassword`, `SimpleHS256TokenAuth`, `SecureRoomAuth`, security metrics, connection limit enforcement
- validation: text message sanitization and control-character handling
- security helper cleanup: byte-safe weak-secret pattern warnings, cloned auth secrets, and log sanitization that removes newline/tab control characters
- server setup: `ServeWSWithConfig` method-not-allowed, bad-request, version-13 requirement, bad-room-password, invalid-config rejection, missing-token rejection, query-token rejection by default, explicit origin allow behavior, config normalization
- room-name policy: invalid direct hub joins, invalid handshake room queries, custom validator allowance, and invalid admin broadcast room targets
- route registration: nil registrar, nil hub, empty websocket path, duplicate routes, and empty enabled broadcast path
- admin broadcast: disabled-by-default behavior, dedicated secret or authorizer validation, JWT-secret rejection, empty body behavior, and oversized-body rejection
- client `Dial`: text and binary roundtrip against the in-package server, header propagation through `DialOptions.HTTPHeader`, scheme rejection (non-`ws`/`wss`/`http`/`https`), non-101 response rejection, invalid `Sec-WebSocket-Accept` rejection, context-deadline cancellation, and `rwcConn` deadline no-op contract

## Beta readiness

`x/websocket` satisfies the current coverage and boundary portions of
`docs/reference/extension-stability-policy.md`: hub lifecycle, shutdown, capacity errors,
broadcast no-op behavior, validation, security checks, and server setup failure
paths have focused tests.

The current development-head runtime stable-readiness gate was recorded on
2026-05-02 and passed race tests, vet, build, boundary checks, manifest checks,
extension evidence checks, and maturity checks.

The module is beta. The beta evidence in
`docs/evidence/extension/x-websocket.md` records two release refs, matching API
snapshots, no exported API changes, and `realtime` owner sign-off.

## Canonical change shape

- keep websocket setup explicit and out of `core`
- keep auth and broadcast gates reviewable
- keep JWT-required, unauthenticated, and origin allow-all behavior explicit in configuration
- keep token authentication and room authorization split; anonymous mode must not require a JWT secret
- use `NewSimpleHS256TokenAuth` and `VerifyJWT`; the older auth aliases were
  removed once their last repo callers migrated
- keep JWT and broadcast secrets caller-provided but internally copied at construction boundaries
- keep query-string JWT transport disabled unless `AllowQueryToken` is explicitly set
- keep room-password credentials out of URL query strings; the built-in room authorizer reads `X-WebSocket-Room-Password`
- keep room names validated before use as map keys or broadcast targets; the default room name policy accepts ASCII letters, digits, `.`, `_`, `:`, and `-`, up to 128 bytes
- keep `DefaultWebSocketConfig` deterministic; read environment variables in application wiring before filling config
- keep admin broadcast separately authorized with `BroadcastSecret` or `BroadcastAuthorizer`; never reuse the JWT `Secret`
- bound admin broadcast request bodies before reading them
- require RFC6455 version 13 in the HTTP upgrade request
- keep route registration fail-visible and handle returned errors at every call site
- keep capacity denial before WebSocket upgrade, including post-hijack capacity races
- keep hub shutdown state-clearing explicit: successful `Shutdown` hard-closes connections, clears rooms, clears room-registration counters, and stops workers
- treat `Shutdown(nil)` as `Shutdown(context.Background())`
- serialize `Stop` with broadcast enqueue so broadcasts cannot report jobs enqueued after workers have been stopped
- keep blocking write enqueue implemented with direct channel/select control flow and explicit cancellation
- keep hub worker writes bounded when a connection uses `SendBlock` without a send timeout
- use `TryBroadcastRoom` and `TryBroadcastAll` when callers need fanout results; `BroadcastRoom` and `BroadcastAll` intentionally ignore `BroadcastResult`
- keep broadcast attempted, enqueued, skipped, and dropped counters as runtime facts
- return an admin broadcast error when every targeted connection rejects the message
- document shutdown as hard-close unless a future card adds non-blocking close-frame delivery
- document `WriteClose` as best-effort close-frame delivery followed by TCP close, not a full peer close handshake
- keep connection constructors and mutable timing setters fail-visible instead of panic-prone
- keep hub construction error-returning through `NewHubWithConfig`; do not reintroduce panic convenience constructors
- keep bounded message readers explicit: closing before EOF abandons the message by closing the connection
- keep full config constructors and join paths error-returning; do not reintroduce compatibility-only bypass helpers
- keep metrics names precise enough to distinguish unique connections from room registrations
- use `MaxRoomRegistrations` for the connection-room registration cap; do not call it total unique connections
- keep broadcast result fields and metrics aligned: attempted, enqueued, skipped, and dropped
- keep auth metrics names precise: JWT verification failures are not JWT secret failures
- keep protocol parsing strict without adding compression or extension negotiation implicitly
- keep validation and protocol failures observable through close frames: `1002` for protocol errors, `1007` for invalid text payloads, `1008` for policy rejection, and `1009` for oversized messages
- keep large-message behavior bounded; do not describe `ReadMessageReader` as unbounded or zero-copy streaming
- keep websocket validation transport-scoped; business-layer XSS, SQL, or payload-policy checks belong in the application handler
- keep log sanitization strict enough for single-line logs by replacing all control characters, including newlines and tabs
- treat `ReadMessage` as full in-memory read with an owned payload copy
- keep handshake failures on stable structured error codes for method, upgrade, key, origin, room, token, join, hijack, and server-configuration failures
- handle room-password setup errors explicitly; do not hide hash failures behind log-only behavior
- keep security metrics instance-scoped (`SecureRoomAuth.GetMetrics`, `Hub.Metrics`) instead of reintroducing global wrappers
- treat `x/websocket` as the app-facing websocket transport surface

## 日志与文件安全

Hub 调试日志使用带引号的转义格式记录房间名和广播目标，防止自定义房间名校验器允许的控制字符注入日志。
