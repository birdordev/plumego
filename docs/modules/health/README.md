# health

## Purpose

`health` owns readiness state, component checker contracts, and component or
aggregate health models.

## v1 Status

- `ga` in the Plumego v1 support matrix
- Public compatibility is expected for the stable package surface

## Use this module when

- representing liveness or readiness state
- sharing component health result models across stable and extension packages
- exposing transport-agnostic readiness decisions to an owning HTTP or ops layer

## Do not use this module for

- health manager ownership
- component registration and check orchestration
- retry, timeout, or concurrency policy for checks
- HTTP endpoint ownership
- protected ops endpoints
- gateway diagnostics
- build metadata
- health history export or retention
- metrics, trend analysis, or ops reporting

## First files to read

- `health/module.yaml`
- `health/*.go`
- `x/observability/ops/healthhttp` when the task is HTTP exposure or check orchestration

## Canonical change shape

- keep health state transport-agnostic
- keep execution policy in `x/observability/ops/healthhttp`
- keep `HealthState.IsReady` as the readiness predicate for health states
- expose HTTP handlers from reference or extensions, not from health itself
- keep analytics and reporting in owning extensions, not in stable `health`

## Model semantics

- `HealthState` has three stable values: `healthy`, `degraded`, and
  `unhealthy`
- `HealthState.IsKnown` returns true only for those stable values
- `HealthState.IsReady` returns true for `healthy` and `degraded`, and false
  for `unhealthy` or unknown values
- `HealthStatus` can describe a single component or an aggregate health result
- `ComponentHealth` embeds `HealthStatus` and adds whether that component is
  enabled
- `ReadinessStatus.Components` is omitted from JSON when nil or empty. It maps
  component names to readiness booleans when
  the owning checker exposes per-component decisions
- `HealthStatus.Duration` is encoded as integer nanoseconds in JSON. For
  example, 1500 milliseconds is `1500000000`. A zero duration is omitted.
- `HealthStatus.Clone`, `ComponentHealth.Clone`, and `ReadinessStatus.Clone`
  detach maps and slices before returning model data across ownership
  boundaries; values stored inside `HealthStatus.Details` remain caller-owned

## Boundary with HTTP exposure

- `health` owns state and models, not HTTP routes
- `x/observability/ops/healthhttp` owns health managers, check execution, retries, timeouts, history, metrics, and HTTP handlers
- expose health endpoints from `reference/standard-service` or extension packages such as `x/observability/ops/healthhttp`
- do not let `health` grow transport helpers or endpoint registration APIs
- keep build info, history export, and report-generation surfaces in `x/observability/ops/healthhttp` or other owning extensions
