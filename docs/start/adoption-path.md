# Adoption Path

This is the fastest onboarding flow for Plumego. Follow it linearly: 5 minutes → 30 minutes → 1 day.

---

## Before you start

**Are you coming from another framework?**

Read the migration guide for your current stack first, then come back here:

| From | Guide |
|------|-------|
| Gin | `docs/guides/migration/from-gin.md` |
| Echo | `docs/guides/migration/from-echo.md` |
| Chi | `docs/guides/migration/from-chi.md` |
| stdlib http.ServeMux | `docs/guides/migration/from-stdmux.md` |
| Other middleware stack | `docs/guides/migration/middleware-compat.md` |

Otherwise, continue below.

---

## Timeline: 5 minutes → 30 minutes → 1 day

This path takes you from "hello world" to understanding the control plane. Time estimates are realistic if you're familiar with Go and HTTP.

---

## ⏱️ 5 Minutes: Run Hello World

**Goal:** See Plumego work with one route.

**Steps:**

1. **Read the quick intro:** `docs/start/getting-started.md` (2 min read)
2. **Run the hello-world example:**
   ```bash
   cd examples/hello
   go run main.go
   ```
3. **Test it:**
   ```bash
   curl http://localhost:8080/ping
   ```

**What you're learning:**
- `plumego.New()` creates an app
- `app.Get()` registers a route
- `contract.WriteResponse()` sends JSON responses
- App is a standard `http.Handler`

**Key symbols:**
- `core.New()`
- `app.Get()`, `app.Post()`, `app.Put()`, `app.Delete()`
- `contract.WriteResponse()`
- `contract.WriteError()`

**Done when:** You see `{"message":"pong"}` from curl.

---

## ⏱️ 30 Minutes: Build a CRUD API

**Goal:** Understand routing, request binding, error handling, and app structure.

**Steps:**

1. **Run the standard service from the repository root:**
   ```bash
   go -C reference/standard-service run .
   ```

2. **Try the API:** Follow the [CRUD walkthrough](../../reference/standard-service/TUTORIAL.md#try-the-api) to create, list, read, update, and delete an item.

3. **Read the code:** Follow the [code walkthrough](../../reference/standard-service/TUTORIAL.md#read-the-code) from startup through route registration, handlers, and storage.

4. **Try modifying it:**
   - Add a new route
   - Change the response format
   - Add a middleware

**What you're learning:**
- App structure (`cmd/`, `internal/`, handlers, config)
- Route registration and grouping
- Request binding with `contract.BindJSON()`
- Error handling with `contract.WriteError()`
- Middleware composition
- Graceful shutdown

**Key symbols:**
- `app.Group(prefix string) *RouteGroup`
- `group.Use(middleware func(http.Handler) http.Handler)`
- `contract.BindJSON(r, &data)`
- `contract.Param(r, "id")`
- `app.Prepare()`, `app.Server()`, `app.Shutdown()`

**Done when:** You can modify the example and add your own endpoints.

---

## ⏱️ 1 Day: Understand the Full Picture

**Goal:** Know the complete architecture, boundaries, and how the control plane works.

**Reading (in order):**

### Part 1: Stable Roots (1 hour)
Read the READMEs for modules you'll use:

**Essential:**
- `docs/modules/core/README.md` — app construction
- `docs/modules/router/README.md` — routing and path params
- `docs/modules/contract/README.md` — responses and binding
- `docs/modules/middleware/README.md` — middleware composition

**Pick 2-3 of these depending on your needs:**
- `docs/modules/security/README.md` — auth and password hashing
- `docs/modules/store/README.md` — storage contracts
- `docs/modules/log/README.md` — structured logging
- `docs/modules/metrics/README.md` — metrics collection
- `docs/modules/health/README.md` — health checks

### Part 2: Extensions Overview (30 minutes)
- `docs/start/extension-guide.md` — decision tree for 15 extensions
- Pick ONE extension that matches your next feature
- Read that extension's README in `docs/modules/x/*/`

**Common first extensions:**
- `x/rest` — REST CRUD conventions
- `x/observability` — Prometheus metrics
- `x/websocket` — Real-time features
- `x/tenant` — Multi-tenancy

### Part 3: Design & Standards (30 minutes)
- `docs/reference/canonical-style-guide.md` — how to structure services
- `docs/modules/INDEX.md` — module boundaries and ownership
- `docs/reference/reference-apps.md` — overview of reference implementations

### Part 4: Control Plane (optional, for maintainers/agents)
If you're maintaining a service or using code agents:
- `AGENTS.md` — overview of agent workflow
- `docs/operations/agent-context-budget.md` — what agents need to know
- `specs/task-routing.yaml` — module ownership map
- `specs/dependency-rules.yaml` — API boundaries

**What you're learning:**
- Complete module API surface
- How to choose extensions
- Canonical app structure
- Boundaries between modules
- Agent-friendly workflow (if needed)

**Done when:** You can:
- ✅ Explain why Plumego exists and who should use it
- ✅ Name the 9 stable roots and what each does
- ✅ Understand module boundaries (what each owns)
- ✅ Choose an extension for a new feature
- ✅ Understand the difference between stable/beta/experimental
- ✅ Build a production service with proper structure

---

## Next Steps After Day 1

**Ready to build a real service?**

1. Use [`reference/standard-service`](../../reference/standard-service) as your template
2. Follow `docs/start/production-checklist.md` to add production capabilities
3. Choose 1-2 extensions from `docs/start/extension-guide.md`
4. Copy the matching reference app (e.g., `reference/with-rest/`)
5. Check `docs/reference/canonical-style-guide.md` for patterns

**Want to understand the codebase deeply?**

1. Read `docs/concepts/extension-maturity.md` for extension status
2. Read `docs/reference/api-surface.md` for complete symbol inventory
3. Read `STABILITY.md` and `COMPATIBILITY.md` for version stability

**Want to contribute or maintain with code agents?**

1. Read `docs/operations/agent-external-reference.md` for agent workflow
2. Read `AGENTS.md` for the full agent-first model
3. Familiarize yourself with `specs/task-routing.yaml`

**Want to build an extension?**

1. Read `docs/guides/extending-plumego.md`
2. Check `docs/start/extension-guide.md` for patterns
3. Copy a similar extension and adapt it

---

## Messaging Rule (for docs updates)

When writing docs or examples, present Plumego in this order:

1. Standard-library compatibility (stdlib shapes, no magic)
2. Small stable kernel (9 packages, v1 guarantee)
3. Explicit reference application path (`reference/standard-service`)
4. Optional capabilities (extensions are add-ons)
5. Agent-friendly control plane (specs, tasks, module.yaml)

---

## Common Questions

**Q: How long until I can build a real service?**  
A: By the end of Day 1, you'll understand enough to start. Most developers are productive after 2-4 hours of focused learning.

**Q: Do I need to learn all 9 stable roots?**  
A: No. Start with `core`, `router`, `contract`, and `middleware`. Add others as you need them.

**Q: What if I get stuck?**  
A: Check `docs/start/troubleshooting.md` for common issues, or open an issue on GitHub.

**Q: Should I read all the reference apps?**  
A: No. Read `reference/standard-service/` first (canonical), then pick 1-2 others that match your use case.

**Q: Is Plumego production-ready?**  
A: Yes. v1.0+ is GA and recommended for production. See `STABILITY.md` for guarantees.

---

**Time to get started?** Pick a timeline above and begin. Good luck!

