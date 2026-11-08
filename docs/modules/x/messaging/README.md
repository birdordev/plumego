# x/messaging

## Purpose

`x/messaging` is the canonical app-facing entrypoint for the messaging family.

## v1 Status

- `beta` in the Plumego v1.1 support matrix for the app-facing service surface
- Included in repository release scope with beta compatibility obligations for
  the documented app-facing `x/messaging` public surface
- Subordinate primitives (`mq`, `pubsub`, `scheduler`, `webhook`) remain
  experimental until their own evidence is recorded.

## Use this module when

- the task is message send orchestration
- the task is app-facing queue, pubsub, scheduler, or webhook wiring
- the task needs a family-level messaging entrypoint instead of a lower-level primitive

## Do not use this module for

- application bootstrap
- stable-root abstractions
- direct low-level primitive work when the task is clearly only `x/messaging/mq` or `x/messaging/pubsub`

## First files to read

- `x/messaging/module.yaml`
- `x/messaging/entrypoints.go`
- `specs/extension-taxonomy.yaml`

## Main risks when changing this module

- message flow regression
- provider global state leakage
- family entrypoint ambiguity returning across sibling packages

## Boundary rules

- `x/messaging` is the app-facing family entrypoint; internal messaging primitives (`x/messaging/mq`, `x/messaging/pubsub`, `x/messaging/scheduler`, `x/messaging/webhook`) must not bypass it for cross-family wiring
- keep send orchestration explicit in `x/messaging`; do not add implicit retry, deduplication, or routing policies at import time
- do not expose transport-layer internals (broker connection strings, topic naming conventions) through `x/messaging` API; keep those local to the owning subordinate package
- keep `x/messaging` transport-agnostic at the family boundary; owning handlers choose the subordinate primitive
- keep queue and worker runtime assembly in `runtime.go`; `Service.New` should consume that runtime and remain focused on messaging orchestration, provider handlers, receipts, metrics, monitoring, and webhook notification wiring

## Canonical change shape

- start app-facing messaging work here
- keep orchestration explicit
- keep `x/messaging/mq`, `x/messaging/pubsub`, `x/messaging/scheduler`, and `x/messaging/webhook` subordinate to this family
- app-facing and admin HTTP success responses use `contract.WriteResponse`; direct JSON writes are reserved for non-JSON streaming surfaces outside this family
- app-facing HTTP success payloads should use local typed DTOs instead of one-off maps
- HTTP error responses expose stable contract codes and user-safe messages; provider, broker, scheduler, and store errors must not be written with raw `err.Error()` text
- SMS metrics exporters should use `NewSMSPrometheusExporterE` when nil metric source wiring must be handled as an error; `NewSMSPrometheusExporter` preserves legacy panic behavior

## Subordinate packages

Open sibling packages only when the task is already known to be narrow:

- `x/messaging/mq`: durable queue primitives and worker coordination
- `x/messaging/pubsub`: in-process broker primitives
- `x/messaging/scheduler`: cron, delayed job, and retry coordination
- `x/messaging/webhook`: inbound verification or outbound delivery mechanics

Subordinate packages are not app-facing discovery roots. Open them directly
only after the owning task is already narrowed to that primitive.

### `x/messaging/mq`

Use `x/messaging/mq` for queue persistence, task workers, dead-letter handling,
and queue-task dedupe adapters. Start with `x/messaging/mq/module.yaml`.

Public entrypoints include `NewTaskQueue`, `NewWorker`, `NewInProcBrokerE`,
`store.NewMemory`, `store.NewSQL`, and `NewKVPersistence`.

MQTT and AMQP server bridges are not implemented. `Config.Validate` fails
closed when `EnableMQTT` or `EnableAMQP` is true, wrapping
`ErrNotImplemented`. `StartMQTTServer` and `StartAMQPServer` remain
compatibility stubs only.

`EnableMQTT`, `MQTTPort`, `EnableAMQP`, and `AMQPPort` are deprecated
compatibility placeholders. They do not enable a partial bridge mode, and the
port fields have no effect while protocol support is unavailable. Leave them
disabled for v1 and prefer direct `x/messaging/pubsub` integration unless a
future card lands a real bridge implementation.

Validate narrow queue work with:

- `go test -race -timeout 60s ./x/messaging/mq/...`
- `go test -timeout 20s ./x/messaging/mq/...`
- `go vet ./x/messaging/mq/...`

### `x/messaging/pubsub`

Use `x/messaging/pubsub` for broker semantics, topic fan-out, replay,
backpressure, distributed pubsub helpers, and debug-friendly in-process broker
support. Start with `x/messaging/pubsub/module.yaml`.

Public entrypoints include `New`, `NewPersistent`, `NewDistributed`,
`NewReplayStore`, `NewBackpressureController`, and `NewPrometheusExporter`.

Exact-topic subscriptions use `Subscribe(ctx, topic, opts)`. Context
cancellation is part of the canonical API, not a compatibility wrapper.

Distributed cluster HTTP endpoints fail closed by default when `AuthToken` is
empty. Set `AllowInsecureAuth` only for local tests or explicitly isolated
development clusters. Malformed cluster JSON requests use
`contract.CodeInvalidJSON`, while success responses use typed DTOs through
`contract.WriteResponse`.

Wrappers that own background goroutines must keep lifecycle state
instance-local, stop workers through `Close`, and make repeated `Close` calls
safe.

Validate narrow pubsub work with:

- `go test -race -timeout 60s ./x/messaging/pubsub/...`
- `go test -timeout 20s ./x/messaging/pubsub/...`
- `go vet ./x/messaging/pubsub/...`

### `x/messaging/scheduler`

Use `x/messaging/scheduler` for scheduler primitives, delayed job execution,
retry coordination, and cron helpers. Start with
`x/messaging/scheduler/module.yaml`.

Public entrypoints include `New`, `NewAdminHandler`, `NewMemoryStore`,
`NewKVStore`, `RetryFixed`, and `RetryExponential`.

Admin HTTP query filters fail closed for malformed boolean values. Invalid
`running`, `paused`, or `asc` values return a structured validation error
instead of silently changing filter behavior.

Validate narrow scheduler work with:

- `go test -race -timeout 60s ./x/messaging/scheduler/...`
- `go test -timeout 20s ./x/messaging/scheduler/...`
- `go vet ./x/messaging/scheduler/...`

### `x/messaging/webhook`

Use `x/messaging/webhook` for narrow webhook verification, provider-specific
inbound handlers, outbound delivery, and pubsub-to-webhook bridge wiring. Start
with `x/messaging/webhook/module.yaml`.

Public entrypoints include `NewService`, `NewInbound`, `NewOutbound`,
`NewMemStore`, `VerifyHMAC`, `VerifyGitHub`, and `VerifyStripe`.

Webhook verification must fail closed. Secrets, tokens, signatures, and HMAC
keys must not be logged or returned in error messages. Delivery retry state must
remain instance-scoped.

Use `ConfigFromReaderE` when constructing outbound config from a reader that
may be nil; `ConfigFromReader` is retained as the panic-compatible wrapper.
Outbound `enabled` query filters accept only `true`, `false`, `1`, or `0`; other
values fail with a structured validation error.

Validate narrow webhook work with:

- `go test -race -timeout 60s ./x/messaging/webhook/...`
- `go test -timeout 20s ./x/messaging/webhook/...`
- `go vet ./x/messaging/webhook/...`

## Validation commands

- `go test -race -timeout 60s ./x/messaging/...`
- `go test -timeout 20s ./x/messaging/...`
- `go vet ./x/messaging/...`

## Webhook publish failure diagnostics

Inbound GitHub and Stripe publish failures return the canonical generic internal error. Logs retain the error type, topic, and event ID, but exclude the publisher's error text, which may contain backend credentials or payload data. Custom logging backends must safely encode these original field values according to the `log.StructuredLogger` contract.
