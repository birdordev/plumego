# Agent External Reference

This guide is for code agents (Claude, Copilot, etc.) using Plumego to understand scope, validate changes, and follow boundaries safely.

## Quick Start for Agents

You're working on a Plumego-based service. Before making changes:

1. **Check what's safe to change:**
   - Stable roots (9 packages): Full v1 guarantee, no breaking changes
   - Beta extensions: Stable APIs, deprecation notice required for removals
   - Experimental extensions: APIs may change, caution needed
   - See `STABILITY.md` for the full list

2. **Determine module ownership:**
   - Check `specs/task-routing.yaml` to find which module owns your change
   - Read that module's `module.yaml` for owner, risk, validation commands
   - Read `docs/modules/INDEX.md` for module boundaries

3. **Verify you're in scope:**
   - Check `specs/dependency-rules.yaml` to ensure you're not crossing boundaries
   - Stable roots can only import from stdlib and other stable roots (never `x/*`)
   - Extensions can import each other but not from stable roots

4. **Plan your approach:**
   - Minimal changes, scoped to one module if possible
   - No opportunistic refactors of unrelated code
   - No changes to public API unless explicitly required
   - Run the module's validation commands before handing off

---

## Understanding Repository Structure

### Control Plane Files

These files define scope, ownership, and validation for safe agent changes:

| File | Purpose | For agents |
|------|---------|-----------|
| `AGENTS.md` | Shared repository-wide agent entry point and operational guide | Follow the read path and authority order in §1; use §3 for repository navigation |
| `STABILITY.md` | v1 compatibility guarantee | Check before breaking any public APIs |
| `COMPATIBILITY.md` | Version upgrade paths | Understand deprecation policy if removing symbols |
| `specs/task-routing.yaml` | Module ownership map | Find which module owns your change (AUTHORITATIVE) |
| `specs/dependency-rules.yaml` | Boundary enforcement | Verify you're not crossing import boundaries (AUTHORITATIVE) |
| `specs/module-manifest.schema.yaml` | module.yaml field definitions | Understand module metadata format |
| `specs/extension-maturity.yaml` | Extension status dashboard | Check stability of x/* packages |
| `internal/checks/` | Validation checkers | Run before submitting changes |

### Key Directories

| Directory | Purpose | For agents |
|-----------|---------|-----------|
| `core/`, `router/`, `contract/`, `middleware/`, etc. | Stable roots | v1 guarantee applies; breaking changes forbidden |
| `x/` | Extensions | Check maturity in STABILITY.md; experimental may change |
| `reference/` | Reference applications | Canonical wiring examples; don't change unless fixing bugs |
| `docs/` | Documentation | Update only for behavior/API/security changes |
| `specs/` | Machine-readable policies | Authoritative for scope and boundaries |
| `internal/checks/` | Validation programs | Run these before submitting PRs |
| `tasks/` | Task cards and milestones | Reference for scope and sequencing |

---

## Workflow: Before Making Changes

### 1. Understand Module Ownership

**File to check:** `specs/task-routing.yaml`

Example entry:
```yaml
# tasks/cards/active/0001-add-request-logger.md
scope:
  primary_module: middleware
  context_package: middleware/accesslog
  start_with:
    - middleware/module.yaml
    - middleware/accesslog/README.md
  in_scope:
    - middleware/accesslog/
    - docs/modules/middleware/accesslog/
  out_of_scope:
    - core/
    - x/observability/  # Don't add observability features to middleware
```

**Your action:** Find your task/module in this file. If not found, determine the owning module by reading `docs/modules/INDEX.md` boundaries.

### 2. Read Module Metadata

**File to check:** `<module>/module.yaml`

Example:
```yaml
name: middleware/accesslog
status: stable
owner: transport
risk: low
responsibilities:
  - HTTP request logging
  - Log formatting
  - Request/response timing

does_not_own:
  - Log storage (owned by log module)
  - Business metrics (owned by metrics module)

public_entrypoints:
  - Handler

validation_commands:
  - go test -timeout 20s ./middleware/accesslog/...
  - go vet ./middleware/accesslog/...
```

**Your action:**
- Confirm the module status (stable/beta/experimental)
- Note the `does_not_own` section — don't add features listed there
- Copy the `validation_commands` to run before submitting

### 3. Check Dependency Boundaries

**File to check:** `specs/dependency-rules.yaml`

Example excerpt:
```yaml
stable_roots:
  - core
  - router
  - contract
  - middleware
  # ... etc

rules:
  - stable_roots_cannot_import: x/*  # Can't import extensions
  - stable_roots_can_only_import: stdlib  # stdlib + other stable roots
  - extensions_cannot_import_from_stable_roots: true  # x/* stays separate
  - middleware_cannot_import_from_service: "business logic, ORM, domain models"
```

**Your action:**
- Verify your change doesn't violate these rules
- If you need to cross a boundary, stop and ask a human (it's likely a design issue)

### 4. Check Public API Stability

**File to check:** `STABILITY.md`

Example:
```markdown
## Stable Roots (v1 Guarantee)
- core, router, contract, middleware, security, store, health, log, metrics
Breaking changes forbidden within v1.

## Beta Extensions (Stable with Deprecation)
- x/rest, x/websocket, x/gateway, ...
Breaking changes require deprecation notice.

## Experimental Extensions (APIs May Change)
- x/ai, x/data, x/fileapi, ...
No guarantee; may change in any minor version.
```

**Your action:**
- If you're changing a stable root's public API: STOP. Breaking changes forbidden.
- If you're changing a beta extension: document deprecation path
- If you're changing experimental: proceed (but verify API stability first)

### 5. Determine Scope

**Ask yourself:**

- **Is this in the module's ownership?** (check module.yaml `does_not_own`)
- **Does this cross boundaries?** (check dependency-rules.yaml)
- **Does this break public APIs?** (check STABILITY.md)
- **Are there validation commands?** (check module.yaml `validation_commands`)

---

## Making Changes Safely

### DO

- ✅ Read the entire module's `module.yaml` before editing
- ✅ Make minimal changes scoped to one module
- ✅ Run all validation commands in the module.yaml before submitting
- ✅ Check `specs/task-routing.yaml` to understand the full task scope
- ✅ Update tests next to changed behavior
- ✅ Update docs only for behavior/API/security changes
- ✅ Run `make validate-diff` as a quick gate
- ✅ Run `make gates` before final submission

### DON'T

- ❌ Change public APIs without checking STABILITY.md
- ❌ Cross module boundaries (verify specs/dependency-rules.yaml)
- ❌ Refactor unrelated files in the same PR
- ❌ Add features to modules that don't own them
- ❌ Skip validation commands
- ❌ Change behavior without tests
- ❌ Update documentation without code changes
- ❌ Remove symbols without deprecation (see COMPATIBILITY.md)

---

## Validation: Before Submitting

### Minimal gate (for draft PRs)

```bash
# Run the module-specific validation
go test -timeout 20s ./middleware/...
go vet ./middleware/...

# Quick repo-wide validation
make validate-diff
```

### Full gate (before merging)

```bash
# All tests and checks
make gates

# Verify manifests are consistent
go run ./internal/checks/module-manifests
go run ./internal/checks/dependency-rules

# Verify examples still compile
go build ./examples/...
```

### Spot-check for common mistakes

```bash
# Search for new public API (exported symbols)
git diff HEAD -- middleware/handlers.go | grep "^+func "

# If you found new exported functions:
# 1. Are they in module.yaml's public_entrypoints?
# 2. Are they documented in comments?
# 3. Did you add example_test.go coverage?
```

---

## Common Patterns

### Adding a Handler Function

**Pattern:**
```go
// Don't export internal details
func newXHandler() http.Handler { ... }  // unexported

// Do export clean, simple functions
func Handler(opts ...Option) http.Handler { ... }  // exported
```

**Validation:**
- Add entry to module.yaml's `public_entrypoints`
- Add godoc comment
- Add example_test.go showing usage
- Run `go doc` to verify signature looks clean

### Adding Middleware

**Pattern:**
```go
// Middleware returns a handler wrapper
func Handler(config Config) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Do middleware work
            next.ServeHTTP(w, r)
        })
    }
}
```

**Validation:**
- Works with `app.Use(middleware.Handler(cfg))`
- Works with any `http.Handler` (test with stdlib mux too)
- Doesn't inject services or build DTOs
- Doesn't access user-specific context (auth middleware OK, business logic NO)

### Removing or Renaming Symbols

**If removing a stable root symbol:**
1. ❌ Can't remove in v1 (breaking change)
2. Deprecate it: add `// Deprecated: use NewX instead` comment
3. Document migration in COMPATIBILITY.md
4. Remove in v2.0

**If removing a beta extension symbol:**
1. Add deprecation comment
2. Create a PR with deprecation notice (let maintainer decide merge timing)
3. Remove in a future minor

**If removing experimental symbol:**
1. Just remove it (no guarantee applies)
2. Note in release notes

---

## When to Ask a Human

Stop and ask a human maintainer if:

- ❌ You're changing a stable root's public API signature
- ❌ You're crossing module boundaries (import rules violated)
- ❌ You're removing a symbol from a stable root or beta extension (without deprecation)
- ❌ Module.yaml says `does_not_own` but your change adds it anyway
- ❌ You're writing code in a module that's not your primary scope
- ❌ The task description is unclear or conflicts with module boundaries
- ❌ Validation commands fail and you don't understand why

In all these cases, open an issue or comment explaining the blocker.

---

## Key Files to Understand

**Read these files first:**
1. `STABILITY.md` — v1 guarantee
2. `specs/task-routing.yaml` — your module's scope
3. `<module>/module.yaml` — what the module owns
4. `docs/modules/INDEX.md` — module boundaries
5. `specs/dependency-rules.yaml` — import boundaries

**Reference as needed:**
- `COMPATIBILITY.md` — if removing symbols or changing APIs
- `docs/reference/canonical-style-guide.md` — handler/middleware patterns
- `docs/guides/extending-plumego.md` — if building an extension

---

## Example Workflow

**Task:** Add request body size limiting to middleware

**Your steps:**

1. **Check ownership:**
   - Look in `specs/task-routing.yaml` → not listed
   - Look in `docs/modules/INDEX.md` → "body limiting" → middleware module
   - ✅ In scope: middleware

2. **Read metadata:**
   - Open `middleware/module.yaml`
   - Responsibilities: "Transport-layer middleware composition"
   - Does NOT own: "business logic injection, DTO assembly"
   - ✅ In scope: body limiting is transport-level

3. **Check boundaries:**
   - Open `specs/dependency-rules.yaml`
   - Middleware can import: stdlib, core, contract, other middleware
   - ✅ OK: no cross-boundary imports needed

4. **Check stability:**
   - Open `STABILITY.md`
   - Middleware is stable root (v1 guarantee)
   - ⚠️ Note: breaking changes forbidden

5. **Make changes:**
   - Edit `middleware/bodylimit/handler.go`
   - Add `func Handler(maxBytes int64) http.Handler { ... }`
   - Add to `middleware/module.yaml` public_entrypoints
   - Update `middleware/bodylimit/README.md`
   - Add `middleware/bodylimit/example_test.go`

6. **Validate:**
   - Run `go test -timeout 20s ./middleware/bodylimit/...`
   - Run `go vet ./middleware/bodylimit/...`
   - Run `make validate-diff`
   - ✅ All green

7. **Submit:**
   - Create PR
   - Reference the task
   - Describe what changed and why

---

## Resources

- **Understanding module structure:** `docs/modules/INDEX.md`
- **Boundary rules:** `specs/dependency-rules.yaml`
- **Task ownership:** `specs/task-routing.yaml`
- **Style guide:** `docs/reference/canonical-style-guide.md`
- **Stability policy:** `STABILITY.md`
- **Extension authoring:** `docs/guides/extending-plumego.md`
- **Agent workflow:** `AGENTS.md`

---

**Remember:** Explicit is better than implicit. When in doubt, read the module.yaml and ask questions rather than guessing.
