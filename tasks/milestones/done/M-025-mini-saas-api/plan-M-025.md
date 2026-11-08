# Plan for M-025: Mini SaaS API Use-Case

Milestone: `M-025`
Objective: Design and build `use-cases/mini-saas-api`, a runnable multi-tenant
SaaS backend that demonstrates Plumego's stable roots plus the beta extensions
`x/rest`, `x/tenant`, and `x/observability`.
Constraints: stdlib-first (zero external deps in v1); no stable root API
changes; explicit wiring only (no framework magic); use-case-local `go.mod`
with `replace` per repository policy.
Affected Modules: `use-cases/mini-saas-api` (new); docs registration in
`use-cases/AGENTS.md` and root agent docs.

---

## 1. Product Purpose

**mini-saas-api** is a minimal but credible SaaS backend: a multi-tenant
project tracker ("workspaces" with members and projects). It exists to prove
that Plumego's stable roots plus selected beta extensions can carry a real
SaaS API end to end — signup to authenticated, tenant-isolated, rate-limited,
observable CRUD — without third-party frameworks.

It is a teaching-by-realism artifact, not a product: every SaaS table-stakes
concern it includes (auth, tenancy, RBAC, idempotency, audit, ops) is wired
explicitly and readable in one sitting. It complements `reference/*` apps
(one capability each) by composing several capabilities the way a production
team would.

## 2. Included Plumego Modules

Stable roots (GA, stdlib-only):

| Module | Use |
|---|---|
| `core` | App construction, lifecycle, route registration, graceful shutdown |
| `contract` | `WriteResponse` / `WriteError` + `NewErrorBuilder()` — the only response paths |
| `middleware/{requestid,securityheaders,cors,recovery,accesslog,bodylimit,abuseguard,httpmetrics,timeout}` | Global chain in `internal/app/app.go`, same order as `reference/standard-service`; `abuseguard` additionally guards `/api/v1/auth/*` |
| `security/password` | bcrypt hash/check + `ValidatePasswordStrength` on signup |
| `security/jwt` | `NewJWTManager` + key store from `APP_JWT_SECRET`; claims→`Principal` via `PrincipalFromClaims` |
| `security/authn` | `Principal` context accessors; bearer extraction |
| `store/kv` | Hashed refresh tokens with TTL; rate/quota counters where x/tenant needs a store |
| `store/idempotency` | Replay protection for mutating endpoints (stable root, preferred over `x/data/idempotency`) |
| `health` | `/healthz` liveness + `/readyz` readiness checkers |
| `log` | Structured logging; no secrets ever logged |
| `metrics` | Collector interface consumed by `httpmetrics` |

Beta extensions (per `docs/concepts/extension-maturity.md`):

| Module | Status | Use |
|---|---|---|
| `x/rest` | beta | Resource controller for `projects` CRUD (alongside explicit handlers for auth/tenant, mirroring `reference/with-rest`'s both-styles demo) |
| `x/tenant` | beta (high risk) | Resolution (from JWT claim), per-tenant token-bucket rate limit, fixed-window quota on project count |
| `x/observability` | beta | `NewPrometheusCollector` for `httpmetrics` + `NewPrometheusExporter` on `GET /metrics` |

Not used: any experimental `x/*` (including `x/openapi`, `x/validate`,
`x/data/*` — `store/idempotency` covers the idempotency need from a stable root).

## 3. Explicitly Excluded Features

- **Billing/plans/payments** — `Tenant.Plan` is a plain string label; no metering or Stripe-like integration.
- **Email delivery** — member invites return the membership in the response; no SMTP/external sender.
- **Cookie/server sessions** — JWT access + rotated refresh tokens only; `x/tenant/session` lifecycle is out of scope.
- **External database** — in-memory repositories behind interfaces; SQLite/`store/db` backend deferred (see Risks).
- **WebSocket/realtime** (`x/websocket`), **reverse proxy** (`x/gateway`), **frontend assets** (`x/frontend`), **messaging/queues/webhooks** (`x/messaging`), **AI** (`x/ai`).
- **OpenAPI generation** (`x/openapi` is experimental) — possible follow-up card, not v1.
- **Admin UI, multi-region, org-of-orgs hierarchies, SSO/OAuth** — single-level tenant, password auth only.
- **Token revocation lists / logout-everywhere** — refresh rotation only; documented limitation.

## 4. Directory Layout

Copies the canonical `reference/standard-service` shape (style guide §3):

```text
use-cases/mini-saas-api/
  go.mod                      # module mini-saas-api; replace github.com/birdordev/plumego => ../..
  main.go                     # load config, construct app, run with signal context
  README.md                   # purpose, run, curl walkthrough
  AGENTS.md                   # local operating rules (pattern: use-cases/workerfleet)
  ARCHITECTURE.md             # ownership + dependency direction
  env.example                 # every APP_* variable with safe defaults
  api/
    examples.http             # editor-runnable request file
    curl.sh                   # end-to-end curl walkthrough script
    postman_collection.json   # same flows for Postman
  internal/
    config/config.go          # env loading, defaults, validation (fails on empty APP_JWT_SECRET)
    app/app.go                # core.New, middleware order, graceful shutdown
    app/routes.go             # full visible route table + dependency wiring
    handler/                  # auth.go, me.go, tenant.go, members.go, audit.go, health.go (+ _test.go)
    domain/
      user/                   # user.go, store.go, service.go
      tenantspace/            # tenant + membership model, store, service
      project/                # project model, store, service (consumed by x/rest controller)
      access/                 # RBAC checker (role lattice, action checks)
      audit/                  # append-only audit recorder + query
```

Dependency direction (one way, as in standard-service):
`main.go → internal/config → internal/app → internal/handler → internal/domain/*`;
handlers never import `internal/app` or `internal/config`.

## 5. API List

All JSON; success via `contract.WriteResponse`, errors via `contract.WriteError`.
Mutating endpoints under `/api/v1` accept an `Idempotency-Key` header
(scoped per tenant) — replays return the stored first response.

Public:

| Method/Path | Purpose |
|---|---|
| `POST /api/v1/auth/signup` | Create user + workspace (tenant); returns user, tenant, token pair. 201 |
| `POST /api/v1/auth/login` | Email+password → access (15 min) + refresh token. 200 |
| `POST /api/v1/auth/refresh` | Rotate refresh token → new pair. 200 |
| `GET /healthz` | Liveness. 200 |
| `GET /readyz` | Readiness (store + key material checks). 200/503 |
| `GET /metrics` | Prometheus exposition; requires `APP_METRICS_TOKEN` bearer when set |

Authenticated (Bearer JWT; tenant resolved from claim; per-tenant rate limit applies):

| Method/Path | Min role | Purpose |
|---|---|---|
| `GET /api/v1/me` | member | Current principal + role + tenant |
| `GET /api/v1/tenant` | member | Workspace details + quota usage |
| `PATCH /api/v1/tenant` | admin | Rename workspace / change plan label |
| `GET /api/v1/tenant/members` | member | List memberships |
| `POST /api/v1/tenant/members` | admin | Add member by email + role. 201 |
| `PATCH /api/v1/tenant/members/{id}` | admin | Change role (cannot demote last owner) |
| `DELETE /api/v1/tenant/members/{id}` | admin | Remove member (cannot remove last owner). 204 |
| `GET /api/v1/tenant/audit` | admin | Audit entries, newest first, paginated |
| `GET /api/v1/projects` | member | List tenant projects (x/rest controller) |
| `POST /api/v1/projects` | member | Create project; quota-checked. 201 |
| `GET /api/v1/projects/{id}` | member | Fetch one |
| `PUT /api/v1/projects/{id}` | member | Update name/description/status |
| `DELETE /api/v1/projects/{id}` | admin | Delete. 204 |

Error envelope: one canonical shape from `contract.NewErrorBuilder()` —
401 unauthenticated, 403 role denied, 404 wrong-tenant or missing resource
(no cross-tenant existence leak), 409 duplicate email/slug, 422 validation,
429 rate/quota exceeded.

## 6. Data Model

All IDs are random 128-bit hex (`crypto/rand`); times UTC.

```text
User        { ID, Email (unique, lowercased), Name, PasswordHash, CreatedAt }
Tenant      { ID, Slug (unique), Name, Plan ("free"|"team" label), CreatedAt }
Membership  { ID, TenantID, UserID, Role (owner|admin|member), CreatedAt }
Project     { ID, TenantID, Name, Description, Status (active|paused|archived),
              CreatedBy, CreatedAt, UpdatedAt }
RefreshToken{ TokenHash (SHA-256, key), UserID, TenantID, ExpiresAt }   # store/kv, TTL
AuditEntry  { ID, TenantID, ActorID, Action, ResourceType, ResourceID,
              Detail (string, no secrets), At }
IdempotencyRecord — owned by store/idempotency, keyed tenantID+Idempotency-Key
```

Invariants: every `Membership`/`Project`/`AuditEntry` row carries `TenantID`;
every repository method takes `tenantID` as an explicit argument; a tenant has
≥1 `owner` at all times; project count per tenant ≤ quota for its plan label.

## 7. Security Model

- **Passwords:** `security/password.HashPassword` (bcrypt) +
  `ValidatePasswordStrength(DefaultPasswordStrengthConfig())` on signup;
  `CheckPassword` on login. Hashes never serialized into responses or logs.
- **Tokens:** access = JWT HS256 via `security/jwt.NewJWTManager`, secret from
  `APP_JWT_SECRET` (config validation refuses empty/short), 15-minute expiry,
  claims `{sub, tenant_id, role}`. Refresh = opaque 256-bit random value,
  stored as SHA-256 hash in `store/kv` with TTL, single-use rotation; reuse of
  a rotated token invalidates the family (fail closed).
- **Request auth:** explicit per-route wrapper in `routes.go` extracts the
  bearer token (`authn.ExtractBearerToken`), verifies via the JWT manager, and
  installs the principal with `authn.WithPrincipal`; handlers read it back with
  `PrincipalFromContext`. No service-locator, no hidden binding.
- **Tenancy isolation:** `x/tenant` resolution derives the tenant from the JWT
  claim (header spoofing impossible); repositories filter by explicit tenant
  ID; lookups in another tenant return 404, not 403, to avoid existence leaks.
- **RBAC:** `internal/domain/access` owns the role lattice
  (owner > admin > member) and per-action checks; handlers call it before the
  service layer. Middleware stays transport-only per the boundary rules.
- **Abuse controls:** `middleware/abuseguard` token bucket on `/api/v1/auth/*`
  (brute-force), `x/tenant/ratelimit` per tenant on the API surface,
  `x/tenant/quota` on project creation, `bodylimit` globally.
- **Transport hardening:** `securityheaders`, strict CORS from
  `APP_CORS_ALLOWED_ORIGINS`, request `timeout`, `recovery`.
- **Secrets discipline:** JWT secret, password hashes, refresh tokens, and
  `Idempotency-Key` values never appear in logs or audit detail; metrics token
  compared with `crypto/subtle.ConstantTimeCompare`. All auth/verification
  errors fail closed.
- **Audit:** every mutating authenticated action appends an `AuditEntry`
  (actor, action, resource) — content excludes credentials and payload bodies.

## 8. Test Strategy

- **Handler/API tests** (`internal/handler/*_test.go`, `httptest` through the
  fully wired app router): happy path + negative path per endpoint — missing/
  expired/garbage token (401), insufficient role (403), cross-tenant resource
  ID (404), duplicate email (409), weak password / bad payload (422),
  rate-limit and quota exhaustion (429).
- **Domain unit tests** next to each package: RBAC lattice truth table,
  last-owner protection, project status transitions, audit append/query order.
- **Auth-flow tests:** signup→login→refresh rotation; reuse of rotated refresh
  token rejected; tampered JWT signature rejected; tenant claim mismatch rejected.
- **Tenant-isolation test:** two tenants seeded; every list/get/update/delete
  asserted invisible across the boundary.
- **Idempotency tests:** same `Idempotency-Key` replays the stored response
  (one project created); different tenants with the same key do not collide.
- **Lifecycle test:** app starts, `/readyz` flips ready, context cancel drains
  in-flight requests (pattern from `reference/standard-service/app_test.go`).
- All tests stdlib `testing` + `httptest`, run with `-race`; no test
  frameworks. Concurrency-sensitive stores get parallel-access tests.

## 9. Validation Commands

In-module (per change, from `use-cases/mini-saas-api/`):

```bash
gofmt -l . && go vet ./... && go test -race -timeout 60s ./...
```

Repo root (boundary + workflow gates, and before PR):

```bash
go run ./internal/checks/dependency-rules
go run ./internal/checks/agent-workflow
go run ./internal/checks/module-manifests
go run ./internal/checks/reference-layout
go run ./internal/checks/public-entrypoints-sync
make validate-diff
```

`make gates` only if root packages are touched (they should not be).
`make website-sync` only if a generated doc source (root `README.md`,
`docs/modules/*`, `specs/task-routing.yaml`, roadmap) changes during
registration — then commit `website/src/generated/` together.

## 10. Step-by-Step Implementation Tasks

## Phase Map

- Phase 1: Orient — read context files, allocate real card IDs.
- Phase 2: Foundation — scaffold + domain (sequential).
- Phase 3: Capabilities — auth / tenancy / CRUD / observability (parallel).
- Phase 4: Surface — examples + docs registration.
- Phase 5: Validate and ship.

## Card Inventory

| Card | Goal | Primary Module | Owned Files | Depends On | Quick Gates |
|------|------|----------------|-------------|------------|-------------|
| 1525 | Scaffold: go.mod+replace, main.go, config, middleware chain, /healthz,/readyz, env.example, AGENTS/ARCHITECTURE stubs | use-cases/mini-saas-api | go.mod, main.go, internal/config, internal/app, internal/handler/health.go | — | in-module gate |
| 1526 | Domain: user/tenantspace/project/access/audit models, in-memory stores, services, unit tests | use-cases/mini-saas-api | internal/domain/** | 1525 | in-module gate |
| 1527 | Auth: signup/login/refresh handlers, JWT manager wiring, refresh rotation in store/kv, abuseguard on auth routes | use-cases/mini-saas-api | internal/handler/auth.go, me.go, routes.go (auth section) | 1526 | in-module gate |
| 1528 | Tenancy: x/tenant resolution from claim, per-tenant rate limit, quota, member CRUD, last-owner guard | use-cases/mini-saas-api | internal/handler/tenant.go, members.go, routes.go (tenant section) | 1527 | in-module gate |
| 1529 | Projects: x/rest controller over project service, store/idempotency on mutating routes, isolation tests | use-cases/mini-saas-api | internal/handler + routes.go (projects section) | 1527 | in-module gate |
| 1530 | Observability: Prometheus collector into httpmetrics, /metrics exporter with token guard, audit endpoint | use-cases/mini-saas-api | internal/app/app.go, internal/handler/audit.go, metrics route | 1527 | in-module gate |
| 1531 | Examples + docs: api/curl.sh, examples.http, postman_collection.json, README walkthrough, ARCHITECTURE final | use-cases/mini-saas-api | api/**, README.md, ARCHITECTURE.md | 2073, 2074, 2075 | curl.sh against `go run .` |
| 1532 | Register: use-cases/AGENTS.md table row, root AGENTS.md/CLAUDE.md app lists, website-sync if generated sources changed | docs | use-cases/AGENTS.md, AGENTS.md, CLAUDE.md | 1531 | agent-workflow check |

Card IDs `2070–2077` are provisional — confirm against `tasks/cards/` at
execution time and renumber if taken.

## Dependency Edges

- `1525 -> 1526 -> 1527`
- `1527 -> 1528`, `1527 -> 1529`, `1527 -> 1530` (parallel group)
- `{1528, 1529, 1530} -> 1531 -> 1532`

## Parallel Groups

- Group A: 1528 (tenancy)
- Group B: 1529 (projects CRUD)
- Group C: 1530 (observability/audit)

## Risk Register

- Risk: `x/tenant` is beta with **high** risk rating; API drift would break the app.
  Mitigation: use only documented entrypoints shown in `reference/with-tenant`;
  confine all `x/tenant` imports to `internal/app` wiring so a change is one-file.
- Risk: cross-tenant data leak through the `x/rest` controller path (controller
  abstracts the repository call).
  Mitigation: repository interface requires explicit `tenantID`; dedicated
  two-tenant isolation test suite is an acceptance gate.
- Risk: in-memory persistence undercuts the "real SaaS" claim.
  Mitigation: repositories are interfaces; document the deliberate trade-off in
  ARCHITECTURE.md; follow-up card may add `store/db`+SQLite **only after**
  dependency approval per use-case policy.
- Risk: scope creep — the feature list (auth, tenancy, RBAC, CRUD, idempotency,
  audit, ops) is wide for one milestone.
  Mitigation: §3 exclusions are hard stops; each card is single-concern;
  anything else becomes a follow-up card.
- Risk: refresh-token rotation is easy to get subtly wrong (replay window).
  Mitigation: single-use hashed tokens with family invalidation, covered by
  explicit reuse-rejection tests.
- Risk: docs/registration drift (`agent-workflow`, manifest checks fail late).
  Mitigation: card 2077 owns registration; root checks run in every card's gate.

## Verification Strategy

- Card-level checks: in-module `gofmt -l . && go vet ./... && go test -race
  -timeout 60s ./...`; plus the root boundary checks when wiring or layout changed.
- Milestone-level checks: full Acceptance Criteria block in `M-025.md`,
  `api/curl.sh` walkthrough against a running instance, `verify-M-025.md`
  filled before PR packaging.

## Checkpoints

| Phase | Checkpoint Gate | Status |
|-------|-----------------|--------|
| Phase 2 | `cd use-cases/mini-saas-api && go test -race -timeout 60s ./...` | pending |
| Phase 3 | in-module tests + `go run ./internal/checks/dependency-rules` | pending |
| Phase 4 | `api/curl.sh` end-to-end + `go run ./internal/checks/agent-workflow` | pending |
| Phase 5 | full M-025 Acceptance Criteria + `make validate-diff` | pending |
