# x/gateway

## Purpose

`x/gateway` is the only app-facing extension surface for gateway and edge transport work.

## v1 Status

- `beta` in the Plumego v1 support matrix
- Included in repository release scope with beta compatibility obligations
- Promoted at `v0.2.0` after release-backed evidence showed no exported
  `x/gateway/*` API changes across refs `d2c25c3` and `ec70358`, with `edge`
  owner sign-off recorded in `docs/evidence/extension/x-gateway.md`

## Use this module when

- the task is reverse proxy behavior
- the task is gateway routing, rewriting, or balancing
- edge transport adapters or gateway-local health behavior are involved

## Do not use this module for

- application bootstrap
- reusable resource-interface conventions
- business-specific gateway policy hidden in shared helpers

## First files to read

- `x/gateway/module.yaml`
- `x/gateway/entrypoints.go`
- `specs/extension-taxonomy.yaml`

## Main risks when changing this module

- proxy behavior regression
- transport determinism regression
- hidden global state in adapters

## Boundary rules

- `x/gateway` is the only app-facing surface for edge transport work; do not duplicate gateway routing logic in stable `router` or stable `middleware`
- keep circuit breaker, retry, and balancer state instance-scoped; do not introduce package-level globals or implicit registration at import time
- keep proxy rewrite, transform, and cache adapters contained within `x/gateway/*` subpackages; do not push edge-transport policy into stable roots
- do not couple discovery (`x/gateway/discovery`) selection to gateway-only defaults; discovery backend choice belongs to the caller's wiring
- keep `RegisterRoute` and `RegisterProxy` explicit about invalid args: nil routers, blank paths, and nil handlers must return errors instead of hiding app wiring mistakes

For WebSocket upgrades, HTTPS backends use TLS with the transport's
`TLSClientConfig` and HTTP/1.1 ALPN. The upgrade uses the configured proxy
timeout and validates the upstream certificate under the transport's TLS policy.

## Current test coverage

- `newBackendCircuitBreaker`: nil-config defaults (closed state), explicit config, Trip/Reset lifecycle
- `NewGateway`, `NewGatewayE`, `NewGatewayBackendPool` (valid URLs, invalid URL error), `NewGatewayProtocolRegistry`
- `RegisterRoute`: valid wiring (route reachable), nil router error, blank path error, nil handler error
- `RegisterProxy`: valid proxy wiring with live test server, nil router error, blank path error
- balancer, backend, health, proxy, rewrite, transform, cache, and protocolmw subpackages each have dedicated test files

## Runnable edge example

`x/gateway/example_test.go` shows the recommended app-facing path: create a
router in app-local wiring, call `RegisterProxy` with an explicit backend target,
and handle invalid dynamic configuration as an error. Discovery remains
caller-owned; the example does not install a discovery default.

Core app examples should use the current explicit lifecycle: build from
`core.DefaultConfig()`, call `core.New(cfg, core.AppDependencies{...})`, register
gateway handlers through `app.Any` or `app.AddRoute`, then prepare and serve via
`Prepare` and `Server`. Do not use removed raw router escape hatches in examples.

## Beta readiness

`x/gateway` satisfies the current coverage and boundary portions of
`docs/reference/extension-stability-policy.md`: gateway construction, backend pools,
proxy registration, route registration, circuit-breaker lifecycle, balancer,
backend, health, rewrite, transform, cache, and protocol middleware behavior
have focused tests.

The module is beta. The beta evidence in
`docs/evidence/extension/x-gateway.md` records two release refs, matching API
snapshots, no exported API changes, and `edge` owner sign-off. Discovery
backend selection remains caller-owned and must not become a gateway default.

## Canonical change shape

- keep gateway behavior explicit and adapter-local
- use `x/gateway` as the only app-facing entrypoint for edge transport work
- use `NewGatewayE` or `RegisterProxy` for dynamic or user-provided gateway
  configuration so invalid targets return errors instead of panicking; reserve
  `NewGateway` for panic-compatible static wiring
- route reusable resource-interface work to `x/rest`
- protocol middleware default error responses use stable codes and safe stage details; raw adapter, transform, executor, encoder, and read errors remain available only to caller-provided error hooks

## Subordinate packages

Open subordinate packages only when the task is already narrower than the
app-facing gateway entrypoint.

### `x/gateway/discovery`

Use `x/gateway/discovery` for service lookup, resolver behavior, and explicit
discovery adapters selected by caller wiring. It is not an application
bootstrap surface and does not install gateway defaults.

Start with:

- `x/gateway/discovery/module.yaml`
- `specs/task-routing.yaml`
- `specs/extension-taxonomy.yaml`

Public constructors:

- `NewStatic`
- `NewConsul`
- `NewKubernetes`
- `NewEtcd`

Available backends:

| Backend | Constructor | Notes |
| --- | --- | --- |
| `Static` | `NewStatic(services)` | Fixed URL map for tests and simple deployments |
| `Consul` | `NewConsul(address, ConsulConfig)` | HashiCorp Consul health API with long-poll watching |
| `Kubernetes` | `NewKubernetes(KubernetesConfig)` | Kubernetes Endpoints API with in-cluster credential auto-detection |
| `Etcd` | `NewEtcd(endpoints, EtcdConfig)` | etcd v3 HTTP gateway with explicit registration and health updates |

All backends implement the `Discovery` interface. `Resolve` returns ready
backend URLs and `Watch` delivers backend-list updates. The proxy applies the
same circuit breaker configuration to static and discovered backends. A
discovery snapshot retains health and breaker state for URLs still present;
removing a URL discards its state. `Register`,
`Deregister`, and `Health` are supported by etcd. Static and Kubernetes return
`ErrNotSupported` for unsupported mutation operations.

Validate narrow discovery work with:

- `go test -race -timeout 60s ./x/gateway/discovery/...`
- `go test -timeout 20s ./x/gateway/discovery/...`
- `go vet ./x/gateway/discovery/...`

### `x/gateway/ipc`

Use `x/gateway/ipc` for explicit inter-process transport behavior and
client/server communication between processes. Do not use it as a substitute
for in-process messaging (`x/messaging/pubsub`) or durable queues
(`x/messaging/mq`).

Start with:

- `x/gateway/ipc/module.yaml`
- `x/gateway/ipc/ipc.go`
- this primer

Public entrypoints include `NewServer`, `NewHeartbeatClient`, `NewPool`,
`NewFramedClient`, `NewStreamClient`, and `NewRateLimitedServer`.

Keep IPC connection strings and socket paths out of stable roots. Transport
contracts and process-wide side effects must stay explicit and reviewable.

Validate narrow IPC work with:

- `go test -race -timeout 60s ./x/gateway/ipc/...`
- `go test -timeout 20s ./x/gateway/ipc/...`
- `go vet ./x/gateway/ipc/...`

The HTTP cache forwards responses as they are written. When the body exceeds
`MaxSize`, it releases the captured body and skips cache insertion.

Outbound HTTPS connections negotiate HTTP/2 when the upstream supports it.
The proxy flushes server-sent event responses as upstream data arrives.
Protocol middleware bypasses body buffering when the adapter registry is empty.
When adapters are registered, `protocolmw.Config.MaxBodyBytes` can set a
positive limit on the buffered request body.
