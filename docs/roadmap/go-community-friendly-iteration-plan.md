# Plumego: Go Community-Friendly Iteration Plan

**Version:** 1.0  
**Date:** June 2026  
**Audience:** Maintainers, contributors, and code agents  
**Goal:** Position Plumego as the standard-library-first toolkit for Go developers who value explicit, maintainable, agent-friendly services.

---

## Executive Summary

Plumego is a well-architected HTTP toolkit that deserves recognition in the Go community, but its narrative is unclear and its adoption barriers are higher than they need to be. The project has:

- ✅ Zero external dependencies in the stable core
- ✅ Explicit wiring and transparent boundaries (`specs/`, `reference/`, `module.yaml`)
- ✅ Nine stable root packages with full v1 compatibility
- ✅ Seven beta extensions covering major use cases
- ✅ Agent-friendly control plane (tasks, specs, checks)

But it faces:

- ❌ **Positioning unclear:** Readers confuse Plumego with Gin/Echo/Fiber clones instead of understanding it as a stdlib-first toolkit
- ❌ **High first-impression friction:** "Quick Start" jumps to `plumego.New()` before explaining the design philosophy
- ❌ **Adoption path under-documented:** Getting from "hello world" to "production" lacks intermediate checkpoints
- ❌ **Missing v1 readiness artifacts:** No STABILITY.md, COMPATIBILITY.md, or migration matrix
- ❌ **Extension ecosystem unclear:** 15 `x/*` packages with mixed maturity, but no clear "which should I use?" guide
- ❌ **Examples lag:** `reference/` is good but incomplete for common patterns
- ❌ **Documentation too distributed:** Important constraints scattered across AGENTS.md, CLAUDE.md, specs/, docs/operations/, and docs/concepts/

**This plan proposes 20 small, mergeable PRs that clarify positioning, improve first-contact experience, solidify v1 readiness, and make the repository more agent-friendly.**

---

## Current Value Assessment

### What Plumego solves

1. **For Go developers tired of "frameworks":** Plumego keeps you in control. Handlers are plain `http.Handler`. Middleware wraps `http.Handler`. Wiring is explicit in your `main` package. No magic registration, no context service-locators, no reflection-based DI.

2. **For teams building maintainable services:** The stable core (9 packages, ~2K LOC each) is small enough to understand completely. `reference/standard-service` shows the only correct wiring pattern. `specs/dependency-rules.yaml` prevents accidental coupling.

3. **For projects using code agents:** Plumego's control plane (`specs/`, `tasks/`, `reference/`, `module.yaml`, `internal/checks/`) makes scope, boundaries, and validation discoverable. Agents can read the repository and understand what's safe to change without human context.

4. **For services that need to grow:** Start with just `core`, `router`, `contract`. Add `middleware`, `security`, `store`, `health`, `log`, `metrics` only as responsibilities emerge. Optional `x/*` families layer on capabilities without pulling in the stable surface.

### Why it should exist alongside stdlib, Gin, Echo, Fiber, Chi

| Framework | Position | Tradeoff |
|-----------|----------|----------|
| `http.ServeMux` | Minimal routing | Zero framework learning; high wiring boilerplate |
| **Plumego** | **Thin layer on stdlib** | **Small API surface; explicit wiring; stdlib compatibility** |
| Chi | Lightweight router | Pleasant router API; middleware composition pattern diverges from stdlib |
| Gin | Fast + convenient | Excellent devX; hides implicit binding, response wrapping, panics-as-errors |
| Echo | Feature-rich | Broader conventions; less transparent; steeper onboarding for small services |
| Fiber | High performance | Fasthttp (non-stdlib); significant framework coupling |

Plumego is **not** trying to be the fastest or the most convenient. It's the **explicitly maintained** choice for developers who want:
- Zero surprises (explicit > implicit)
- Testability without test helpers
- Minimal dependencies (focus on your service, not framework internals)
- Code that reads the same way for 3 years as it did day 1
- Repositories that agents can safely maintain

### What risks confuse new users

1. **"Is this a Gin clone?"** — No, but we haven't explained why we're NOT one.
2. **"When do I use `plumego.New()` vs. `core.New()`?"** — This is a real API design question that is documented but not highlighted.
3. **"Is `x/ai` production-ready?"** — Unclear; needs explicit "stable surface" marking within experimental families.
4. **"Can I mix Plumego with other middleware?"** — Yes, but that needs a dedicated migration guide.
5. **"What's the difference between `reference/standard-service` and `reference/with-rest`?"** — Both are canonical; unclear when to copy which.
6. **"Am I supposed to read AGENTS.md?"** — It's primarily for maintainers; external readers get lost in agent context.

### What must be clarified before v1 is truly "done"

1. A STABILITY.md file that clearly states what is and is not stable
2. Migration guides for coming FROM other frameworks and for future breaking changes
3. Boundary documentation for each module (what goes in, what doesn't)
4. Clear "agent-safe" vs. "maintainer-only" docs
5. A public-API inventory (what's exported, what's internal, what's beta)
6. A v1 release readiness checklist

---

## Positioning Recommendation

### Core positioning

**"Plumego is the standard-library-first HTTP toolkit for Go developers building explicit, maintainable, agent-friendly services."**

Expand to:

> Plumego combines the familiar shapes of `net/http` with just enough structure to build production services without a framework. Handlers are plain `func(http.ResponseWriter, *http.Request)`. Middleware wraps `http.Handler`. Wiring is visible in your `main` package. The stable core has zero external dependencies. Optional `x/*` families add capabilities without baking them into the learning path.
>
> Choose Plumego if you:
> - Want to understand every line of your HTTP server's wiring
> - Prefer stdlib shapes and patterns
> - Expect your service to live for years with predictable maintenance
> - Use code agents to assist development
> - Value small, testable, refactorable code over convenience

### Messaging hierarchy

**Level 1 — Why Plumego exists:**
- Standard library first (all stable roots use only stdlib)
- Explicit wiring (no hidden binding, no context service-locators)
- Small stable surface (9 packages, flat learning curve)
- Agent-friendly (control plane in `specs/`, `reference/`, `module.yaml`)
- Optional capabilities (extensions layer on, don't bloat core)

**Level 2 — What you can build:**
- Plain JSON APIs (start with `reference/standard-service`)
- REST with CRUD conventions (`x/rest`)
- Multi-tenant services (`x/tenant`)
- Real-time WebSocket (`x/websocket`)
- Edge/gateway (`x/gateway`)
- Observability (`x/observability`)
- Messaging/webhooks (`x/messaging`)

**Level 3 — Who this is for:**
- Go developers tired of framework magic
- Teams building long-lived services
- Projects using AI/code agents
- Organizations standardizing on stdlib patterns

### Where NOT to position Plumego

- ❌ **Not a "Gin/Echo replacement."** (Position as complementary to stdlib, not competitive with frameworks.)
- ❌ **Not the fastest.** (We're not chasing Fiber's fasthttp performance.)
- ❌ **Not a "batteries-included" framework.** (We're stdlib-first by design.)
- ❌ **Not for teams who want zero wiring code.** (We're explicit, not magic.)

---

## Stable Core Proposal

### Current state (v1.1.0)

**9 stable roots (GA, full v1 compatibility):**
- `core` — App construction, lifecycle, routes, middleware attachment
- `router` — Route matching, params, groups, reverse URL generation
- `contract` — Response writers, error builders, transport binding
- `middleware` — Transport-layer middleware composition + standard packages
- `security` — Auth, JWT, password, headers, abuse guards
- `store` — Storage contracts + in-memory primitives (cache, KV, file, DB, idempotency)
- `health` — Health/readiness status models
- `log` — Minimal logging interfaces + default logger
- `metrics` — Minimal metrics contracts

**7 beta extensions (API stable across cited release refs):**
- `x/rest`, `x/websocket`, `x/gateway`, `x/observability`, `x/tenant`, `x/frontend`, `x/messaging` (app-facing)

**Remaining 8 `x/*` are experimental** (APIs may change in any minor version):
- `x/ai` (mostly; `x/ai/provider`, `x/ai/session`, `x/ai/streaming`, `x/ai/tool` have beta evidence)
- `x/data` (mostly; `x/data/file`, `x/data/idempotency` have beta evidence)
- `x/fileapi`, `x/openapi`, `x/resilience`, `x/rpc`, `x/validate`, `x/gateway/discovery`, `x/gateway/ipc`, `x/observability/devtools`, `x/observability/ops`

### Proposal: No changes to stable surface

The nine stable roots are well-designed and need no API changes. The beta extensions are also appropriately scoped. This plan proposes **zero public API changes**. Instead, we focus on:

1. **Clarity:** Documenting what's stable, beta, and experimental
2. **Examples:** Completing `reference/` scenarios
3. **Adoption:** Improving the path from 5 minutes to 1 day
4. **Boundaries:** Making module responsibilities explicit
5. **Artifacts:** Adding v1 readiness documents

### Module boundaries (to clarify, not change)

| Module | Owns | Does NOT own |
|--------|------|--------------|
| `core` | App, routes, middleware attachment, lifecycle | Response formatting, middleware implementations, storage, business logic |
| `router` | Route matching, params, groups, metadata | Response building, request binding, service injection |
| `contract` | Success/error responses, request accessors, metadata | Validation logic, business DTO assembly, storage queries |
| `middleware` | Transport-layer concerns (logging, tracing, timeouts, CORS, recovery) | Business logic injection, DTO assembly, domain-policy branching |
| `security` | Auth adapters, JWT, password hashing, abuse guards | User/role/permission stores, business policy enforcement |
| `store` | Storage contracts + in-memory implementations (cache, KV, file, idempotency) | ORM wiring, business repositories, query builders |
| `health` | Health check models and status propagation | Business-logic health determination, actual probes |
| `log` | Logging interfaces + default logger | Application business logging (callers own that) |
| `metrics` | Metrics contracts and collectors | Metric definition (callers own what to measure) |

---

## Adoption Path Audit

### Current state

README.md, docs/start/, and reference/ exist, but the path is rough:

1. **5 minutes:** README quick-start works. But positioning is mixed up with API details.
2. **30 minutes:** docs/start/adoption-path.md and reference/standard-service README exist but are scattered. No clear "next 25 minutes" roadmap.
3. **1 day:** docs/start/adoption-path.md section §3 exists, but it points to 80 files, not a clear sequence.

### Major gaps

1. **No "Why Plumego?"** page. Developers land on README and don't understand the positioning until halfway through.
2. **No "Quick reference" card.** Many services just need: routing, request context, structured responses. Should be 1 page.
3. **Migration guides incomplete.** from-gin.md, from-echo.md, from-chi.md exist but are sparse. from-stdmux is missing.
4. **No "when to use `x/*`" decision tree.** Choosing between 15 extensions is daunting.
5. **No "architecture checklist."** What should a production Plumego service have? (config, logging, metrics, structured errors, request ID, graceful shutdown)
6. **Examples missing:**
   - `examples/hello` — minimal 5-minute runnable
   - `examples/standard-api` — 30-minute CRUD JSON API
   - `examples/with-tenant` — multi-tenant example (separate from reference/)
   - `examples/with-middleware-compat` — mixing Plumego with third-party middleware

### Proposed adoption flow (2-hour onboarding)

1. **0-3 min:** Skim README "Positioning" section (NEW)
2. **3-5 min:** Run examples/hello, see it work
3. **5-15 min:** Copy reference/standard-service, add one GET endpoint
4. **15-30 min:** Add config, logging, graceful shutdown (via reference/standard-service deep dive)
5. **30-60 min:** Choose one capability (REST, tenant, observability) and add it
6. **60-120 min:** Read AGENTS.md and understand the control plane

---

## Major Gaps

### Documentation gaps

| Gap | Severity | Impact | Proposed fix |
|-----|----------|--------|--------------|
| No STABILITY.md or COMPATIBILITY.md | HIGH | Users can't tell what's safe to depend on | New files clarifying v1 guarantee |
| "Why Plumego?" narrative missing | HIGH | Developers don't understand positioning | Restructure README; add POSITIONING.md |
| No clear "stable vs. beta vs. experimental" marking in main docs | HIGH | Extension choices are confusing | Add extension decision tree + maturity checklist |
| AGENTS.md is in main README pointing | MEDIUM | External readers confused by maintainer docs | Move to docs/operations/, link from main README clearly |
| No "module responsibility" boundary docs | MEDIUM | Developers don't know where to add features | Add docs/modules/INDEX.md with module map |
| Missing "architecture checklist" | MEDIUM | Production services lack standard patterns | Add docs/start/production-checklist.md |
| No examples/ directory with simple runnable code | HIGH | "Hello world" is not copy-paste friendly | Add examples/hello, examples/standard-api |
| module.yaml missing in some packages | MEDIUM | Agents can't query ownership/validation | Add missing module.yaml files |
| No roadmap/versioning docs | LOW | Unclear what's coming in v1.1, v1.2, v2 | Add docs/release/ROADMAP.md (this file) |

### Example gaps

| Missing | Why it matters | Proposed |
|---------|----------------|----------|
| `examples/hello` | Developers want copy-paste quick start | 10 lines, runnable in < 1 min |
| `examples/standard-api` | 30-min path to CRUD API | Expand reference/standard-service as standalone example |
| `examples/error-handling` | Common task; currently scattered in docs | Focused example of contract.WriteError, recovery, logging |
| `examples/middleware-third-party` | Many have existing middleware stacks | Show how to use Chi/gorilla/other middleware with Plumego |
| `examples/tenant-auth` | Multi-tenant + auth is a common ask | Combine x/tenant + security middleware |
| `examples/observability-stack` | Prometheus/OTel integration is needed | Minimal example of x/observability wiring |

### Code gaps

| Gap | Severity | Impact | Fix |
|-----|----------|--------|-----|
| Some x/* packages lack module.yaml | MEDIUM | Agents can't query owner, risk, validation | Add to all x/* roots |
| Deprecation inventory incomplete | LOW | Future removals will be unclear | Review specs/deprecation-inventory.yaml; document all deprecated symbols |
| No public API snapshot/inventory | MEDIUM | Version sync is manual | Auto-generate list of exported symbols per module |
| Missing tests for edge cases in x/* | LOW | Experimental packages may have quality gaps | Audit coverage, mark test blockers in module.yaml |

---

## Iteration Strategy

### Principles

1. **No public API changes.** Stable roots stay stable. Extensions stay at current status.
2. **Small, mergeable PRs.** Each task is ~1 file or ~50 lines. No multi-file refactors.
3. **Focus on clarity and adoption.** Documentation, examples, and artifacts over features.
4. **Verify before shipping.** Every change has a verification step (see matrix below).
5. **Agent-friendly first.** Changes preserve and extend the control plane, not replace it.

### Release cadence for this iteration

- **Phase 1 (Docs + Examples):** Tasks 1-8 (this month) — positioning, adoption path, examples
- **Phase 2 (Artifacts + Boundaries):** Tasks 9-15 (next month) — STABILITY.md, module docs, extension checklist
- **Phase 3 (Polish + Verification):** Tasks 16-20 (end of next month) — roadmap, release notes, v1 readiness checklist

Each phase is released as a PR so progress is visible.

---

## PR Queue (20 Tasks)

### Phase 1: Positioning, Examples, and Adoption Path

#### 1. **Restructure README with "Why Plumego?"**

- **Objective:** Move positioning to the top; clarify what Plumego is NOT
- **Files:** README.md (insert new "Why Plumego?" section after the title badge)
- **Changes:** 
  - Add 3-sentence "Positioning" paragraph
  - Add comparison table (stdlib, Plumego, Chi, Gin, Echo, Fiber)
  - Keep existing quick-start but add "Read this first" link
  - Move stdlib comparison higher (was section 4, move to section 2)
- **Non-goals:** No API changes; no feature additions
- **Definition of Done:** README reads as "Plumego is stdlib-first" before "quick start"
- **Tests:** None (docs only)
- **Risk:** LOW (docs only)
- **Rollback:** Revert README.md

#### 2. **Add POSITIONING.md (2-minute read)**

- **Objective:** Standalone page explaining why Plumego exists and who should use it
- **Files:** NEW docs/start/POSITIONING.md
- **Content:**
  - 3 paragraphs on design philosophy
  - Table of "Choose Plumego if…" vs. "Choose [framework] if…"
  - Link to migration guides
  - Link to adoption path
- **Definition of Done:** Runs as a standalone article; is linked from README
- **Tests:** None (docs only)
- **Risk:** LOW
- **Rollback:** Delete docs/start/POSITIONING.md; revert README link

#### 3. **Add STABILITY.md (v1 guarantee documentation)**

- **Objective:** Clarify what's stable, beta, and experimental
- **Files:** NEW STABILITY.md (root level, alongside README.md)
- **Content:**
  - Table of 9 stable roots with v1 guarantee
  - Table of 7 beta extensions with release refs
  - Table of experimental packages with note
  - SemVer expectations (no breaking changes in v1, deprecation notices required for beta removals, experimental has no guarantee)
  - "What you can safely depend on" checklist
- **Definition of Done:** Covers all packages; is referenced from README; clarifies v1 guarantee
- **Tests:** Verify no dangling package names (spot-check 3 experimentals are listed)
- **Risk:** LOW
- **Rollback:** Delete STABILITY.md; revert README link

#### 4. **Add COMPATIBILITY.md (forward and backward)**

- **Objective:** Guide users on version upgrades and migration
- **Files:** NEW COMPATIBILITY.md (root level)
- **Content:**
  - Section: "How to upgrade from v1.x to v1.y" (patch/minor releases)
  - Section: "How to upgrade from v1 to v2" (what will change, timeline, announcement process)
  - Link to migration guides (from-gin, from-echo, from-chi, from-stdmux)
  - Link to deprecation inventory
  - When to expect v2 (timeline TBD)
- **Definition of Done:** Covers all upgrade paths; is referenced from README; mentions deprecation policy
- **Tests:** Verify links to migration guides exist
- **Risk:** LOW
- **Rollback:** Delete COMPATIBILITY.md; revert README link

#### 5. **Create examples/hello (minimal runnable)**

- **Objective:** Copy-paste 10-line example that works in 1 minute
- **Files:** NEW examples/hello/main.go, examples/hello/go.mod, examples/hello/README.md
- **Content:**
  - main.go: 10 lines, one route, one response
  - go.mod: single dependency on plumego@latest
  - README.md: 3 steps (init, get, run)
- **Definition of Done:** `go run main.go` works; outputs "started at :8080"; curl :8080/ping returns JSON
- **Tests:** make test should compile it
- **Risk:** LOW
- **Rollback:** Delete examples/hello/

#### 6. **Create examples/standard-api (30-minute CRUD example)**

- **Objective:** Standalone copy-paste example for a simple JSON API
- **Files:** NEW examples/standard-api/ (copy of reference/standard-service with cleanup)
- **Content:**
  - Same structure as reference/standard-service but with explanatory comments
  - Focus on: config, routes, handlers, error responses
  - Omit: middleware order tricks, advanced observability
  - README.md: "Run this example" + explanation of each file
- **Definition of Done:** Standalone; `go run ./cmd/api` works; exposes /users CRUD endpoints
- **Tests:** make test should compile; make verify-example-api should run basic curl tests
- **Risk:** LOW (copying existing code)
- **Rollback:** Delete examples/standard-api/

#### 7. **Create examples/error-handling (common patterns)**

- **Objective:** Show how to use contract.WriteError, recovery, logging patterns
- **Files:** NEW examples/error-handling/main.go, examples/error-handling/README.md
- **Content:**
  - Handler with input validation (contract.WriteError)
  - Panic recovery (via middleware)
  - Logging on error
  - Custom error codes
- **Definition of Done:** Runs; curl with bad input returns proper error; curl with panic returns 500
- **Tests:** make test should compile; manual curl tests
- **Risk:** LOW
- **Rollback:** Delete examples/error-handling/

#### 8. **Add docs/start/production-checklist.md (architecture guide)**

- **Objective:** Help developers understand what a production Plumego service should have
- **Files:** NEW docs/start/production-checklist.md
- **Content:**
  - ✅ Config loading and validation
  - ✅ Structured logging
  - ✅ Graceful shutdown
  - ✅ Request ID carriage
  - ✅ Metrics collection
  - ✅ Error handling and recovery
  - ✅ Health checks
  - ✅ Rate limiting / abuse guards
  - For each: "Plumego module" + "reference example"
- **Definition of Done:** Every checkbox has a link to reference code or example
- **Tests:** None (docs)
- **Risk:** LOW
- **Rollback:** Delete docs/start/production-checklist.md

---

### Phase 2: Artifacts, Boundaries, and Extension Clarity

#### 9. **Add docs/modules/INDEX.md (module responsibility map)**

- **Objective:** Clarify what each stable root owns and doesn't own
- **Files:** NEW docs/modules/INDEX.md
- **Content:**
  - Short intro to stable roots
  - Table of module + "owns" + "doesn't own"
  - Link to each module's README for details
  - Diagram of module relationships (ASCII)
- **Definition of Done:** Covers all 9 stable roots; clarifies boundaries; is linked from README
- **Tests:** Verify all 9 modules are listed; verify links exist
- **Risk:** LOW
- **Rollback:** Delete docs/modules/INDEX.md; revert README link

#### 10. **Add extension decision tree (docs/start/extension-guide.md)**

- **Objective:** Help users choose between 15+ extensions
- **Files:** NEW docs/start/extension-guide.md
- **Content:**
  - Decision flowchart (ASCII): "Do you need…?"
  - Matrix of extensions with status, use-case, maturity, link to primer
  - Filtering by maturity level (beta only, or including experimental)
  - When NOT to use an extension
- **Definition of Done:** Makes it obvious which extension to try for common use-cases; links to all x/* primer docs
- **Risk:** LOW
- **Rollback:** Delete docs/start/extension-guide.md

#### 11. **Add migration guide for http.ServeMux (docs/guides/migration/from-stdmux.md)**

- **Objective:** Common path: developers upgrading from plain stdlib
- **Files:** NEW docs/guides/migration/from-stdmux.md
- **Content:**
  - http.ServeMux style vs. Plumego style (side-by-side examples)
  - Route param extraction (stdlib manual vs. Plumego context)
  - Response writing (stdlib vs. contract.WriteResponse)
  - Middleware (http.HandlerFunc wrapper vs. Plumego middleware)
  - Testing (httptest compatibility)
- **Definition of Done:** Covers main differences; is usable as a 10-minute read
- **Tests:** None (docs)
- **Risk:** LOW
- **Rollback:** Delete docs/guides/migration/from-stdmux.md

#### 12. **Add docs/guides/extending-plumego.md (for extension authors)**

- **Objective:** Guide for community authors building on Plumego
- **Files:** NEW docs/guides/extending-plumego.md
- **Content:**
  - When to write an extension vs. library vs. reference
  - How to name and structure an extension
  - How to maintain stdlib compatibility
  - How to document an extension
  - Checklist for beta-stability evidence
  - Link to community-extension.yaml schema
- **Definition of Done:** External developers could use this to build and propose an extension
- **Tests:** None (docs)
- **Risk:** LOW
- **Rollback:** Delete docs/guides/extending-plumego.md

#### 13. **Add missing module.yaml files (esp. x/* roots)**

- **Objective:** Ensure every package has a module.yaml for agent querying
- **Files:** Add module.yaml to x/openapi, x/rpc, x/resilience, x/validate, x/fileapi (any missing)
- **Content:** Standard fields: owner, risk, responsibilities, validation commands, deprecation status
- **Definition of Done:** All x/* and core/ have module.yaml; `make verify-manifests` passes
- **Tests:** make verify-manifests (uses internal/checks/module-manifests)
- **Risk:** LOW
- **Rollback:** Delete new module.yaml files

#### 14. **Add docs/reference/api-surface.md (public API inventory)**

- **Objective:** Machine-generated (or manually curated) list of what's exported per module
- **Files:** NEW docs/reference/api-surface.md (or auto-generated file)
- **Content:**
  - For each stable root: list of exported types, functions, interfaces
  - Mark which are "primary" (commonly used) vs. "internal API" (rarely needed directly)
  - Link to godoc for each
  - Note any deprecated symbols (with link to COMPATIBILITY.md)
- **Definition of Done:** Complete inventory; is referenced from STABILITY.md
- **Tests:** Verify count of exported symbols is correct (spot-check 3)
- **Risk:** LOW
- **Rollback:** Delete docs/reference/api-surface.md

#### 15. **Add docs/release/ROADMAP.md (versioning and timeline)**

- **Objective:** Communicate the v1 roadmap and expectations for v2
- **Files:** NEW docs/release/ROADMAP.md
- **Content:**
  - v1.1.0 status (current stable)
  - v1.2-1.3 expected features (focus on stability, not new capability)
  - v2 timeline and breaking-change policy (rough timeline only, not commitments)
  - How to report bugs vs. request features
  - Link to STABILITY.md and COMPATIBILITY.md
- **Definition of Done:** Realistic; doesn't over-commit; is referenced from README
- **Tests:** None (docs)
- **Risk:** LOW
- **Rollback:** Delete docs/release/ROADMAP.md

---

### Phase 3: Polish, Verification, and v1 Readiness

#### 16. **Update docs/start/adoption-path.md (cleaner flow)**

- **Objective:** Restructure to follow the 2-hour onboarding path
- **Files:** docs/start/adoption-path.md (edit)
- **Changes:**
  - Add "read this if coming from another framework" checklist at the top
  - Section 1: 5 min (examples/hello + quick concept intro)
  - Section 2: 15 min (reference/standard-service deep dive)
  - Section 3: 15 min (choose and add one extension)
  - Section 4: 30 min (understand control plane / AGENTS.md)
  - Add "now you're ready for production" checklist
- **Definition of Done:** New developers follow this linearly; takes roughly stated time
- **Tests:** Get 2-3 volunteers to follow path; time it; collect feedback
- **Risk:** MEDIUM (changes adoption path UX; may be confusing if not tested)
- **Rollback:** Git revert docs/start/adoption-path.md

#### 17. **Add README_CN.md improvements (mirror English changes)**

- **Objective:** Keep Chinese README in sync with English (positioning, stability, examples)
- **Files:** README_CN.md (edit)
- **Changes:** Mirror tasks 1-3 changes in Chinese
- **Definition of Done:** README_CN has same structure and links as README.md
- **Tests:** Spot-check translated sections make sense
- **Risk:** LOW (docs only; verify no translation errors)
- **Rollback:** Git revert README_CN.md

#### 18. **Add docs/operations/agent-external-reference.md (non-maintainer guide)**

- **Objective:** Explain the control plane for external developers using code agents
- **Files:** NEW docs/operations/agent-external-reference.md
- **Content:**
  - What specs/, tasks/, reference/, module.yaml are for
  - How to read module.yaml (owner, risk, responsibilities, validation)
  - How to understand task-routing.yaml (which modules own which changes)
  - How to check boundaries (dependency-rules, reference-layout)
  - How to verify changes (make gates vs. make validate-diff)
  - When to ask a human (analysis mode, unclear ownership)
- **Definition of Done:** External developers understand Plumego's control plane; can use agents safely
- **Tests:** None (docs)
- **Risk:** LOW
- **Rollback:** Delete docs/operations/agent-external-reference.md

#### 19. **Add v1 READINESS.md (final checklist)**

- **Objective:** Track that v1 is truly "done" on positioning, stability, examples, and artifacts
- **Files:** NEW READINESS.md (root level)
- **Content:**
  - Section: Positioning (README, POSITIONING.md, positioning clear)
  - Section: Stability (STABILITY.md, COMPATIBILITY.md, no dangling status)
  - Section: Adoption (examples/hello, examples/standard-api, adoption-path clear)
  - Section: Boundaries (module.yaml complete, docs/modules/INDEX.md, extension-guide.md)
  - Section: Control Plane (agent-external-reference.md, specs updated)
  - Section: Verification (all gates pass, all examples compile)
  - Section: Release (release notes, version tag, announcement ready)
  - Checklist with ✅/❌ for each item
- **Definition of Done:** All items are ✅ before v1 release
- **Tests:** None (living document, maintained manually)
- **Risk:** LOW (internal checklist only)
- **Rollback:** Delete READINESS.md

#### 20. **Create docs/release/V1-RELEASE-NOTES.md (announcement template)**

- **Objective:** Prepare v1 release notes highlighting positioning and stability
- **Files:** NEW docs/release/V1-RELEASE-NOTES.md
- **Content:**
  - "What is Plumego?" (3-sentence summary)
  - "What's stable?" (9 stable roots + 7 beta extensions)
  - "What can I build?" (link to reference/, examples/)
  - "Where do I start?" (link to adoption-path, examples/hello)
  - "Is this production-ready?" (yes, if using stable roots; beta for listed extensions; experimental noted)
  - "How is this different from [framework]?" (comparison matrix)
  - "What's next?" (link to ROADMAP.md)
  - Key commits and authors
- **Definition of Done:** Blog post / GitHub release ready
- **Tests:** None (marketing)
- **Risk:** LOW
- **Rollback:** Delete docs/release/V1-RELEASE-NOTES.md

---

## Verification Matrix

### Tests to run for each PR

| PR # | Files | Command | Pass Criteria |
|------|-------|---------|---------------|
| 1-4 | *.md (docs) | `make verify-links` (check links) | All links resolve |
| 5-7 | examples/ | `make verify-examples` (compile + basic tests) | `go test ./...` passes |
| 8-15 | docs/ | `make verify-links` + manual review | No 404s; content makes sense |
| 16 | docs/start/ | Get 2-3 volunteers to follow path; time it | Takes roughly stated time |
| 17 | README_CN.md | Manual review of translation | Reads naturally in Chinese |
| 18 | docs/operations/ | Manual review | Explains control plane clearly |
| 19 | READINESS.md | Manual verification | Checklist is complete and accurate |
| 20 | docs/release/ | Manual review | Release notes are compelling |

### Full validation before release

```bash
# Code gates (all examples compile, tests pass, no vet errors)
make gates

# Check all links in markdown (new tool or manual spot-check)
grep -r "http" docs/ README.md | grep -v "github.com/birdordev/plumego" | head -20

# Verify examples run
go run ./examples/hello/main.go &
sleep 1 && curl http://localhost:8080/ping
killall main

# Verify docs are in sync with code
go run ./internal/checks/module-manifests
go run ./internal/checks/dependency-rules
go run ./internal/checks/public-entrypoints-sync

# Spot-check deprecation inventory is accurate
go run ./internal/checks/deprecation-inventory -strict
```

---

## Claude/Codex Usage Notes

### How to use this plan with code agents

**For agents maintaining Plumego:**

1. Read this document before any PR
2. Follow the PR queue in order (Phase 1 → 2 → 3)
3. Each PR is 1 task: don't combine them
4. Before editing: read the Context file(s) listed in the task
5. After editing: verify with the command in the Verification Matrix
6. When done: open a PR with summary of what changed, verified by which commands
7. If you get stuck: check `specs/stop-condition-handlers.yaml` for guidance

**For external agents using Plumego:**

1. Read POSITIONING.md to understand the project
2. Read STABILITY.md to know what's safe to depend on
3. Read docs/modules/INDEX.md to understand ownership
4. For any change: check specs/task-routing.yaml to find the owning module
5. Read the owning module's module.yaml for validation commands
6. Read the owning module's README for the design
7. If boundaries are unclear: check specs/dependency-rules.yaml
8. Run `make validate-diff` to check your change before submitting

---

## Final v1 Readiness Checklist

Before calling v1 "final," verify:

### Positioning ✅
- [ ] README starts with "Why Plumego?" and comparison table
- [ ] POSITIONING.md exists and is linked from README
- [ ] Comparison with stdlib, Chi, Gin, Echo, Fiber is clear and accurate
- [ ] "Not for teams who want zero wiring code" is explicitly stated

### Stability ✅
- [ ] STABILITY.md lists all 9 stable roots with v1 guarantee
- [ ] STABILITY.md lists all 7 beta extensions with release refs
- [ ] STABILITY.md lists all experimental packages
- [ ] COMPATIBILITY.md explains upgrade paths
- [ ] No dangling package names (all listed packages exist)
- [ ] `docs/reference/extension-stability-policy.md` is referenced from both

### Adoption Path ✅
- [ ] examples/hello runs in 1 minute
- [ ] examples/standard-api is runnable and annotated
- [ ] docs/start/adoption-path.md flows linearly (5 min → 30 min → 1 day)
- [ ] All links in adoption-path point to existing files
- [ ] volunteers can follow path without asking questions

### Boundaries ✅
- [ ] docs/modules/INDEX.md maps all 9 stable roots
- [ ] Each module's README has "owns" and "doesn't own" sections
- [ ] module.yaml is present in core/, router/, contract/, middleware/, security/, store/, health/, log/, metrics/
- [ ] module.yaml is present in all x/* roots (even experimental)
- [ ] module.yaml fields are consistent and accurate

### Control Plane ✅
- [ ] specs/task-routing.yaml is accurate
- [ ] specs/dependency-rules.yaml prevents stable→x/* imports
- [ ] internal/checks/ passes all baseline checks
- [ ] AGENTS.md is move-friendly (can be relocated without breaking understanding)
- [ ] docs/operations/agent-external-reference.md guides external agents

### Verification ✅
- [ ] `make gates` passes (all tests, vet, coverage)
- [ ] `make validate-diff` passes (quicker validation)
- [ ] All examples compile
- [ ] All links in docs/ resolve
- [ ] Release notes are ready

### Release ✅
- [ ] Version tag is prepared (v1.x.x)
- [ ] docs/release/V1-RELEASE-NOTES.md is finalized
- [ ] CHANGELOG.md is updated
- [ ] GitHub release is drafted
- [ ] Announcement is queued for blog/Twitter/community

---

## Summary of Changes

### Files to create (new)
- docs/start/POSITIONING.md
- STABILITY.md
- COMPATIBILITY.md
- docs/start/production-checklist.md
- docs/modules/INDEX.md
- docs/start/extension-guide.md
- docs/guides/migration/from-stdmux.md
- docs/guides/extending-plumego.md
- docs/reference/api-surface.md
- docs/release/ROADMAP.md
- docs/operations/agent-external-reference.md
- READINESS.md
- docs/release/V1-RELEASE-NOTES.md
- examples/hello/ (main.go, go.mod, README.md)
- examples/standard-api/ (copy of reference/standard-service)
- examples/error-handling/ (main.go, README.md)
- module.yaml (missing packages under x/)

### Files to edit (major changes)
- README.md (add "Why Plumego?" section; restructure intro)
- README_CN.md (mirror English changes)
- docs/start/adoption-path.md (cleaner flow, better structure)

### Files to edit (minor changes)
- docs/concepts/extension-maturity.md (link to new docs)
- docs/reference/extension-stability-policy.md (reference from STABILITY.md)
- AGENTS.md (add note about external vs. internal sections)
- Makefile (add `make verify-examples`, `make verify-links` if missing)

### Files to verify (no changes needed)
- specs/task-routing.yaml
- specs/dependency-rules.yaml
- specs/module-manifest.schema.yaml
- All module.yaml files (completeness check only)

---

## Commands to Run for Verification

After all PRs are merged:

```bash
# Full validation suite
make gates

# Minimal diff validation (recommended before opening PR)
make validate-diff

# Check all links in docs (manual or add to Makefile)
grep -r "http" docs/ README.md | grep -v "github.com/birdordev/plumego" | wc -l

# Verify examples compile
go test ./examples/...

# Verify manifests are complete and consistent
go run ./internal/checks/module-manifests
go run ./internal/checks/extension-maturity
go run ./internal/checks/deprecation-inventory -strict

# Verify boundaries
go run ./internal/checks/dependency-rules
go run ./internal/checks/reference-layout

# Spot-check key API surfaces (godoc)
go doc -short github.com/birdordev/plumego/core | head -30
go doc -short github.com/birdordev/plumego/router | head -30
go doc -short github.com/birdordev/plumego/contract | head -30
```

---

## Conclusion

Plumego is a well-architected, production-ready toolkit. This plan clarifies positioning, improves adoption, and solidifies v1 readiness without any public API changes or risky refactors. Over 20 focused PRs, we will:

1. **Explain why Plumego exists** (not try to be Gin/Echo)
2. **Make it easier to start** (examples, adoption path, migration guides)
3. **Clarify what's safe to depend on** (STABILITY.md, COMPATIBILITY.md)
4. **Help extensions grow safely** (decision trees, maturity markers, extension guide)
5. **Support agents as first-class maintainers** (control plane documentation, clear boundaries)

The result is a project that is **friendly to Go developers**, **safe for code agents**, and **ready for widespread adoption** in the community.

---

**End of document.** Next step: Open Phase 1 PRs starting with task 1 (README restructure).
