# x/ai

## Purpose

`x/ai` is the experimental capability family for AI-related adapters and workflows.
Import the owning subpackage directly; the root `x/ai` package is only a
module marker and not a canonical bootstrap surface.

## v1 Status

- `experimental` in the Plumego v1 support matrix
- Included in repository release scope, but compatibility is not frozen

## Use this module when

- the task is clearly AI capability work
- provider, session, streaming, tool, or orchestration work is involved

## Do not use this module for

- stable root abstractions
- generic HTTP bootstrap

## Stability tiers

The family is still experimental overall. `x/ai/module.yaml` remains the
family-level tier index and currently declares these subpackage tiers:

- stable-tier subpackages: `provider`, `session`, `streaming`, `tool`
- experimental subpackages: `orchestration`, `semanticcache`, `marketplace`, `distributed`, `resilience`

Treat additional supporting subpackages under `x/ai/*` as experimental unless
the manifest explicitly promotes them.

For package-level machine-readable metadata, `x/ai/provider/module.yaml`,
`x/ai/session/module.yaml`, `x/ai/streaming/module.yaml`, and
`x/ai/tool/module.yaml` now own the status, owner, risk, review, and
validation metadata for those stable-tier packages. `x/ai/module.yaml` remains
the canonical family index for the tier split itself.

## Subpackage beta evidence

The root `x/ai` family remains experimental. Beta evaluation is tracked only at
the stable-tier subpackage level:

- `provider`: `docs/evidence/extension/x-ai-provider.md`
- `session`: `docs/evidence/extension/x-ai-session.md`
- `streaming`: `docs/evidence/extension/x-ai-streaming.md`
- `tool`: `docs/evidence/extension/x-ai-tool.md`

These records are evidence for compatibility-oriented adoption of the named
subpackage only. They do not promote `x/ai` as a family and do not cover
`orchestration`, `semanticcache`, `marketplace`, `distributed`, `resilience`, or
other experimental AI packages.

The subpackage evidence records are tracked in
`specs/extension-beta-evidence.yaml` under `subpackage_candidates`. The
promotion check validates those entries against the `stability_tiers.stable`
section in `x/ai/module.yaml`; root `x/ai` remains `experimental`. The package
manifests for `provider`, `session`, `streaming`, and `tool` now carry the
package-level machine-readable metadata while the evidence records remain the
promotion ledger. Release comparison commands in the evidence records apply
only to the named subpackage and must not be used as root-family promotion
evidence.

## Recommended stable-tier adoption path

For new AI service work that needs a compatibility-oriented path, start with
only the stable-tier subpackages:

1. Use `x/ai/provider` for provider-neutral request, response, streaming, and
   adapter selection contracts.
2. Use `x/ai/session` for explicit session state, message lifecycle, and
   storage ownership.
3. Use `x/ai/tool` for local tool registration, allow-list policy, and
   invocation boundaries.
4. Add `x/ai/streaming` only when the service needs streaming coordination or
   SSE-backed progress updates.

Wire these packages in the owning application, handler, or extension
constructor. Do not hide provider selection, session storage, stream managers,
or tool registries behind package globals or implicit registration.

Keep `x/ai/orchestration`, `x/ai/semanticcache`, `x/ai/marketplace`,
`x/ai/distributed`, and `x/ai/resilience` on the experimental path until their
contracts have separate promotion evidence. If a service needs those packages,
document the experimental dependency in the consuming module instead of treating
the whole `x/ai` family as beta-ready.

## Common entrypoints

- `x/ai/provider` — provider abstraction and provider adapters
- `x/ai/session` — AI session lifecycle and state handling
- `x/ai/streaming` — streaming primitives and stream coordination
- `x/ai/tool` — tool registration and execution policy
- `x/ai/orchestration` — multi-step agent workflow composition
- `x/ai/semanticcache` — embedding/vector-backed semantic cache flows

## Resilience primitive relationship

`x/resilience` owns reusable circuit breaker and rate-limit primitives for
cross-extension use. The older `x/ai/circuitbreaker` and `x/ai/ratelimit`
packages remain AI-provider-specific packages, but `x/ai/resilience` now
composes only the shared `x/resilience/*` primitives.

New cross-family resilience work should start in `x/resilience`. New AI provider
wrapping should still use `x/ai/resilience`, but the first migration slice now
lets `NewResilientProviderE` compose directly with
`x/resilience/ratelimit.KeyedBuckets` through the canonical `Config.RateLimiter`
field. The circuit-breaker side follows the same rule: use
`Config.CircuitBreaker` with
`x/resilience/circuitbreaker.CircuitBreaker`. `x/ai/resilience` no longer
accepts AI-local limiter or breaker implementations as alternate config paths.
Dynamic composition should prefer `NewResilientProviderE` so invalid provider
wiring returns an error instead of panicking. `x/ai/ratelimit.TokenBucketLimiter`
owns a cleanup goroutine only when constructed with a cleanup interval, and
callers should call `Close` when that background cleanup is enabled.

Boundary details are recorded in
`docs/concepts/x-ai-resilience-boundary.md`. For v1 cleanup, do not migrate
public types between `x/ai/*` and `x/resilience` inline with feature work.
new generic breaker or limiter algorithms belong in `x/resilience`, while
AI-provider wrapping, fallback, request keying, and AI error classification
stay in `x/ai/resilience`.

## Metrics collector relationship

Stable `metrics` remains the canonical small collector contract for cross-module
recording. `x/ai/metrics` still owns AI-local conveniences such as `Tag`,
`TagsE`, `MemoryCollector`, and the minimal Prometheus text exporter used by AI
instrumentation tests and examples.

When AI instrumentation should publish into the stable collector surface, prefer
`x/ai/metrics.NewAggregateCollectorAdapter(...)` with a stable
`metrics.AggregateCollector`. The adapter records AI counter, gauge, histogram,
and timing calls as stable `MetricRecord` events. It intentionally does not add
a second stable metric-kind contract; keep metric names distinct when different
semantic series would otherwise share the same name.

## Compatibility panic wrappers

Several `x/ai` packages still expose panic-based compatibility helpers while the
family remains experimental or while stable-tier callers migrate gradually:

- `resilience.NewResilientProvider` and
  `semanticcache.NewSemanticCachingProvider` are deprecated panic shims. Prefer
  `NewResilientProviderE` and `NewSemanticCachingProviderE`.
- `metrics.Tags` remains only for trusted static key-value pairs. Prefer
  `metrics.TagsE` whenever tag input is built dynamically.

These wrappers are tracked in `specs/deprecation-inventory.yaml` and should be
removed when their remaining compatibility callers are migrated.

## Streaming error contract

- `x/ai/sse` and `x/ai/streaming` HTTP handlers use structured `contract.WriteError` responses for setup and request failures.
- Default SSE stream setup, invalid JSON, workflow callback, and handler error-event paths use safe public messages and do not expose provider, callback, decoder, or stream creation internals.
- `x/ai/streaming` terminal SSE payloads use local typed DTO structs instead of one-off maps.
- Use `provider.Manager.Register` to register providers and
  `streaming.StreamManager.Register` to associate streams with workflow IDs.
  Both return an error for invalid registration input. Handle the error in
  application wiring before using the registration.

```go
if err := manager.Register(provider); err != nil {
    return err
}
if err := streamManager.Register(workflowID, stream); err != nil {
    return err
}
```

The stable-tier entrypoints are `provider`, `session`, `streaming`, and `tool`.
Use other `x/ai/*` packages with experimental-module expectations.

## Runnable offline examples

- `x/ai/provider/example_test.go` — mock-backed provider usage without network calls
- `x/ai/session/example_test.go` — in-memory session lifecycle and active-context retrieval
- `x/ai/streaming/example_test.go` — SSE-backed progress updates with an in-memory recorder
- `x/ai/tool/example_test.go` — offline tool registration, policy filtering, and execution

## First files to read

- `x/ai/module.yaml`
- the owning subpackage under `x/ai/*`
- `specs/repo.yaml`

## Boundary rules

- keep AI wiring explicit in handlers or owning extensions
- do not add hidden provider globals or registration side effects
- use `provider.AutoToolChoice`, `provider.NoneToolChoice`, and `provider.AnyToolChoice` for fresh tool-choice values; the older exported `ToolChoiceAuto`, `ToolChoiceNone`, and `ToolChoiceAny` variables remain only for stable API compatibility
- keep transport-only concerns out of `x/ai`
- keep generic resilience primitives in `x/resilience`; keep AI-provider orchestration in `x/ai/resilience`
- do not require stable roots to know AI internals

## Validation focus

- `go test -race -timeout 60s ./x/ai/...`
- `go test -timeout 20s ./x/ai/...`
- `go vet ./x/ai/...`
- when stable-tier APIs change, add coverage at the provider, session, streaming, or tool boundary instead of documenting speculative guarantees

## Current test coverage

Stable-tier packages have unit and contract tests alongside their example files:

- `x/ai/provider` — Manager routing delegation, Complete/CompleteStream dispatch, custom Router injection via `WithRouter`; `ClaudeProvider` and `OpenAIProvider` adapter tests cover constructor defaults, option overrides (BaseURL, HTTPClient), successful completion (offline via `httptest.NewServer`), and API error paths (401/429 responses)
- `x/ai/session` — session CRUD, TTL expiry, message append with token counting, auto-trim, Update, pagination, context key/value store
- `x/ai/tool` — Registry register/get/list/execute, policy filtering via `AllowListPolicy`, builtin tools (echo, calculator, timestamp, read-file, write-file, bash), error result metrics

Experimental-tier packages have basic contract tests:

- `x/ai/semanticcache/cachemanager` — cache stats, cleanup all, warming from queries, and unsupported maintenance operations (`Compact`, selective cleanup policies, `WarmFromFile`) return `ErrUnsupportedMaintenance`
- `x/ai/marketplace` — Manager contract tests covering PublishAgent, GetAgent, SearchAgents, ListAgentVersions, RateAgent, IsAgentInstalled, ListInstalledAgents, InstallAgent error path, install/uninstall round-trip; two pre-existing bugs fixed (mutex deadlock in `UpdateDownloadCount`, `InstallationRecord.Metadata` type assertion after JSON round-trip)
