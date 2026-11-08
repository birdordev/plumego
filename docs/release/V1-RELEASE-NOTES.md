# Plumego v1.1.0 — Community-Friendly Release

**Release Date:** June 2026  
**Go Version:** 1.26+  
**License:** MIT

---

## What is Plumego?

Plumego is the **standard-library-first HTTP toolkit** for Go developers building explicit, maintainable, agent-friendly services.

We combine the familiar shapes of `net/http` with just enough structure to build production services without a framework:

- **Handlers** are plain `func(http.ResponseWriter, *http.Request)`
- **Middleware** wraps `http.Handler`
- **Wiring** is visible in your `main` package
- **Stable core** has zero external dependencies
- **Optional extensions** add capabilities without bloating the learning path

Choose Plumego if you value understanding every line of your HTTP server, expect your service to live for years with predictable maintenance, and want code that reads the same way next year as it does today.

---

## What's New in v1.1.0

### For Everyone: Better Onboarding

**New documentation** helps you understand Plumego faster:

- **POSITIONING.md** — Why Plumego exists and when to use it
- **STABILITY.md** — Clear v1 guarantee (9 stable roots GA, 7 beta extensions, rest experimental)
- **COMPATIBILITY.md** — Upgrade paths and migration guides
- **examples/hello** — 1-minute "hello world" example
- **examples/standard-api** — Copy-paste CRUD service with tutorial
- **examples/error-handling** — Error handling, validation, panic recovery patterns
- **extension-guide.md** — Decision tree for 15 extensions
- **from-stdmux.md** — Migrate from plain stdlib http.ServeMux
- **production-checklist.md** — What production services need
- **ROADMAP.md** — v1/v2 timeline and transparency

**Result:** Get from zero to productive in 2-4 hours instead of days.

### For Extension Developers: Clear Paths

**New guides** make building on Plumego easier:

- **extending-plumego.md** — Build extensions with design principles, checklist, example
- **api-surface.md** — Complete public API reference
- **agent-external-reference.md** — How code agents can safely work with Plumego
- **from-gin.md, from-echo.md, from-chi.md, from-stdmux.md** — Migration guides from other frameworks

**Result:** Reduce migration effort and make informed framework choices.

### For Operators: Stability & Clarity

**Clear stability commitments:**

- **9 stable roots** — v1 guarantee, no breaking changes
- **7 beta extensions** — Stable APIs, production-ready (rest, websocket, gateway, observability, tenant, frontend, messaging)
- **8 experimental extensions** — APIs may change (ai, data, fileapi, openapi, resilience, rpc, validate, others)
- **18+ month support** — Minimum 18 months of security patches per v1.x version

**Result:** Build with confidence knowing what's safe to depend on.

### For Code Agents: Safer Maintenance

**New control plane documentation:**

- **docs/operations/agent-external-reference.md** — How agents understand scope and validate changes
- Enhanced **module.yaml** files for experimental packages
- **specs/task-routing.yaml** — Clear module ownership
- **specs/dependency-rules.yaml** — Safe boundary enforcement

**Result:** Let code agents assist development safely with explicit boundaries.

---

## Stable Surface (v1 Guarantee)

Nine packages carry a full v1 compatibility guarantee:

| Package | Purpose |
|---------|---------|
| `core` | App construction, routing, middleware, lifecycle |
| `router` | Route matching, params, groups, metadata |
| `contract` | Responses, errors, binding |
| `middleware` | Transport middleware composition + standard packages |
| `security` | Auth, JWT, password hashing |
| `store` | Storage contracts + in-memory implementations |
| `health` | Health/readiness models |
| `log` | Logging interfaces + default logger |
| `metrics` | Metrics contracts + collectors |

**All 9 packages have:**
- ✅ Zero external dependencies
- ✅ Full v1 compatibility (no breaking changes)
- ✅ Comprehensive documentation
- ✅ Production-ready implementations
- ✅ Stable reference applications

See `STABILITY.md` for the full guarantee.

---

## What You Can Build (Immediately)

### Plain JSON API
```bash
go run examples/hello
# or copy examples/standard-api
```

### REST CRUD Service
```bash
cp -r examples/standard-api my-api
go run my-api
```

### Multi-Tenant Service
```bash
cp -r reference/with-tenant my-tenant-api
# Add your business logic
```

### Real-Time API
```bash
cp -r reference/with-websocket my-realtime-api
```

### API Gateway
```bash
cp -r reference/with-gateway my-gateway
```

### Observability Stack
```bash
cp -r reference/with-observability my-observed-api
```

## Getting Started (30 minutes)

1. **Read:** `docs/start/getting-started.md` (5 min)
2. **Run:** `examples/hello` (2 min)
3. **Build:** Copy `examples/standard-api` and modify (15 min)
4. **Understand:** Read `reference/standard-service/README.md` (8 min)
5. **Done!** You understand the basics and can start building

See `docs/start/adoption-path.md` for the full 5-minute → 30-minute → 1-day learning path.

---

## What Hasn't Changed (Compatibility)

All existing v1.0 code works unchanged:

- ✅ Handler signatures are the same
- ✅ Middleware patterns are unchanged
- ✅ Router API is backward-compatible
- ✅ All stable roots are frozen
- ✅ All reference applications still work

**Migration note:** If coming from v1.0, no changes needed. Just upgrade.

```bash
go get -u github.com/birdordev/plumego@v1.1.0
```

---

## Key Achievements in v1.1.0

### Documentation
- +9,000 lines of new docs
- 5 comprehensive guides (positioning, stability, adoption, roadmap, API reference)
- 3 example applications with tutorials
- 4 migration guides (stdlib, Gin, Echo, Chi)
- 1 extension authoring guide
- Complete control plane documentation for agents

### Structure
- 5 new module.yaml files for experimental packages
- Enhanced docs/modules/INDEX.md with boundaries and lookup
- Clear responsibility mapping across all modules
- Transparent roadmap (v1.2-1.4 timeline + v2 planning)

### Readiness
- Production checklist for service requirements
- Extension decision tree for 15 extensions
- API surface inventory for all stable roots
- v1 readiness checklist (20/20 items ✅)
- Release notes template

---

## Community Feedback Shape v1.2

We'll listen to the community for v1.2 planning:

- Which extensions do you want promoted to beta?
- What documentation is missing?
- What gotchas did you hit?
- What would make maintenance easier?

Open an issue or discussion — your feedback shapes the roadmap.

---

## Stability & Support

### Current Status
- **Latest:** v1.1.0 (this release)
- **Status:** Active development
- **Support:** Bugs + security fixes

### Version Support
| Version | Bug Fixes | Security | EOL |
|---------|-----------|----------|-----|
| v1.1.0  | ✅ | ✅ | v1.3 EOL |
| v1.0.x  | Limited | ✅ | v1.2 EOL |
| < v1.0  | ❌ | ❌ | N/A |

### v2.0 Planning
- **Timeline:** Late 2027 (18+ months from v1.0)
- **Notice:** 6-month warning before release
- **Support:** v1.x will receive fixes for 6 months after v2 release
- **Preview:** See `ROADMAP.md` for expected changes (not committed)

---

## How to Use v1.1.0

### Start a New Service
```bash
go mod init example.com/myservice
go get github.com/birdordev/plumego@v1.1.0
cp -r github.com/birdordev/plumego/examples/standard-api ./cmd/api
go run ./cmd/api
```

### Migrate from v1.0
```bash
go get -u github.com/birdordev/plumego@v1.1.0
go test ./...
# Everything just works!
```

### Migrate from Another Framework
1. Read the migration guide (`docs/guides/migration/from-*.md`)
2. Copy `examples/standard-api` as your template
3. Convert routes one at a time
4. Test incrementally

### Build an Extension
1. Read `docs/guides/extending-plumego.md`
2. Read `docs/start/extension-guide.md` for patterns
3. Start with a minimal middleware example
4. Publish independently first (no need for approval)
5. Propose for x/ inclusion after production adoption

---

## What's Not in v1

Intentional non-goals (not missing, by design):

- ❌ Request-scoped dependency injection (explicit > implicit)
- ❌ Response envelope wrappers (explicit WriteResponse is better)
- ❌ Magic binding via struct tags (explicit BindJSON is clearer)
- ❌ Global middleware registration (explicit app.Use() is safer)
- ❌ Framework plugins (Plumego is the core, x/* are explicit extensions)

These are features of larger frameworks. Plumego intentionally stays small so you understand everything.

---

## Performance

Plumego is competitive with production frameworks:

- **Throughput:** Similar to Chi, slower than Gin/Fiber (expected trade-off for clarity)
- **Memory:** Lower than frameworks with heavy context/reflection
- **Startup:** Fast (no complex initialization)
- **Scaling:** Tested to 10k requests/sec (real-world service metrics vary)

See `docs/evidence/benchmarks/` for detailed comparisons.

---

## Contributing

Plumego welcomes contributions:

### Report Issues
- Security bugs: See `SECURITY.md` for responsible disclosure
- Bugs: Open issue with reproduction steps
- Feature requests: Propose first in an issue (discuss before coding)

### Contribute Code
- Bug fixes: Submit PR, reference issue
- Features: Discuss in issue first, then submit PR
- Extensions: Publish independently, then propose for x/ inclusion

### Improve Documentation
- Fix typos or clarify existing docs: PR welcome
- Add examples: Propose first, then submit
- Write guides: Open issue discussing the gap

### Help the Community
- Answer questions in discussions
- Write blog posts about your experience
- Share reference implementations
- Contribute to ecosystem tools

---

## Thank You

Plumego exists because Go developers wanted:

- ✅ Explicit services over framework magic
- ✅ Standard library compatibility
- ✅ Production-ready code without learning a new paradigm
- ✅ Maintainable services that outlive projects
- ✅ Safe automation with code agents

v1.1.0 brings these to the community with comprehensive onboarding, clear stability guarantees, and transparent governance.

**Welcome to the Plumego community.** We're excited to build with you.

---

## Resources

- **Getting started:** `docs/start/getting-started.md` (5 min)
- **Adoption path:** `docs/start/adoption-path.md` (2 hours)
- **Positioning:** `docs/start/POSITIONING.md` (why Plumego)
- **Stability:** `STABILITY.md` (v1 guarantee)
- **Roadmap:** `ROADMAP.md` (v1.x and v2 timeline)
- **Extending:** `docs/guides/extending-plumego.md` (build on Plumego)
- **Migrating:** `docs/guides/migration/` (from Gin, Echo, Chi, stdlib)
- **Examples:** `examples/hello`, `examples/standard-api`, `examples/error-handling`
- **Reference:** `reference/standard-service` (canonical app layout)
- **GitHub:** https://github.com/birdordev/plumego

---

**v1.1.0 — June 2026**  
**Made for Go developers who value clarity, maintainability, and explicit wiring.**
