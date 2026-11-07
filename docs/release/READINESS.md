# v1 Readiness Checklist

This checklist verifies that Plumego v1 is truly "done" before a release. Track progress as PRs merge.

**Last updated:** June 2026  
**Target release:** v1.1.0 (completed) → v1.2.0 or later for next iteration

---

## Positioning ✅

- [x] README starts with "Why Plumego?" and comparison table
- [x] POSITIONING.md exists and is linked from README
- [x] Positioning vs. stdlib, Chi, Gin, Echo, Fiber is clear
- [x] "Not for teams who want zero wiring code" explicitly stated
- [x] Chinese README (README_CN.md) mirrors English positioning

**Status:** COMPLETE

---

## Stability ✅

- [x] STABILITY.md lists all 9 stable roots with v1 guarantee
- [x] STABILITY.md lists all 7 beta extensions with release refs
- [x] STABILITY.md lists all experimental packages (8+ families)
- [x] COMPATIBILITY.md explains upgrade paths
- [x] No dangling package names in stability docs
- [x] Links between STABILITY.md, COMPATIBILITY.md, extension-stability-policy.md verified
- [x] SemVer expectations clearly documented

**Status:** COMPLETE

---

## Adoption Path ✅

- [x] examples/hello runs in 1 minute
- [x] examples/standard-api is runnable and tutorial-annotated
- [x] examples/error-handling demonstrates common patterns
- [x] docs/start/adoption-path.md flows linearly (5 min → 30 min → 1 day)
- [x] All links in adoption path point to existing files
- [x] docs/start/getting-started.md exists and is up to date
- [x] docs/start/production-checklist.md lists production requirements
- [x] docs/start/extension-guide.md has decision tree for 15 extensions
- [x] Migration guides exist for Gin, Echo, Chi, stdlib
- [x] docs/guides/migration/from-stdmux.md covers stdlib migration

**Status:** COMPLETE

---

## Boundaries ✅

- [x] docs/modules/INDEX.md maps all 9 stable roots
- [x] Each module README has "owns" and "doesn't own" sections
- [x] module.yaml is present in core/, router/, contract/, middleware/, security/, store/, health/, log/, metrics/
- [x] module.yaml is present in major x/* roots (ai, data, gateway, messaging, observability, rest, tenant, websocket)
- [x] module.yaml added to experimental x/* roots (openapi, resilience, rpc, validate, fileapi)
- [x] module.yaml fields are consistent across all files
- [x] specs/dependency-rules.yaml prevents stable→x/* imports
- [x] specs/task-routing.yaml maps modules to ownership

**Status:** COMPLETE (core packages + key experimental)

---

## Control Plane ✅

- [x] specs/task-routing.yaml is accurate
- [x] specs/dependency-rules.yaml enforces boundaries correctly
- [x] internal/checks/ has all baseline validation programs
- [x] AGENTS.md is comprehensive (maintainer-facing)
- [x] docs/operations/agent-external-reference.md guides external agents
- [x] docs/operations/agent-context-budget.md documents agent knowledge
- [x] CLAUDE.md summarizes codebase snapshot
- [x] Reference applications demonstrate canonical wiring
- [x] reference/standard-service is the single source of truth for structure

**Status:** COMPLETE

---

## API Surface ✅

- [x] docs/reference/api-surface.md inventories all stable roots
- [x] All 9 stable roots have exported symbol counts verified
- [x] Major extensions documented (rest, websocket, gateway, etc.)
- [x] Quick lookup table ("I want to..." → symbol/module) included
- [x] Public API is stratified (primary, secondary, advanced)
- [x] Breaking changes documented in COMPATIBILITY.md
- [x] Deprecated symbols documented in COMPATIBILITY.md

**Status:** COMPLETE

---

## Versioning & Roadmap ✅

- [x] docs/release/ROADMAP.md covers v1.x timeline
- [x] v2.0 planning documented (late 2027, not committed)
- [x] Support commitment table shows 18+ months per version
- [x] Extension maturity path documented
- [x] Feature request process explained
- [x] Deprecation policy clearly stated
- [x] Breaking change guarantees transparent

**Status:** COMPLETE

---

## Testing ✅

- [x] All examples compile (`go build ./examples/...`)
- [x] All module tests pass (`go test ./...`)
- [x] Race detector passes for concurrent tests
- [x] Code coverage ≥90% on stable roots (go test -cover)
- [x] All internal checks pass locally:
  - [x] `go run ./internal/checks/dependency-rules`
  - [x] `go run ./internal/checks/module-manifests`
  - [x] `go run ./internal/checks/reference-layout`
  - [x] `go run ./internal/checks/public-entrypoints-sync`

**Status:** COMPLETE

---

## Documentation Quality ✅

- [x] README.md is clear and well-structured
- [x] README_CN.md mirrors English (translated)
- [x] All .md files have correct relative links
- [x] Code examples are syntactically correct and runnable
- [x] All directories have comprehensive README or INDEX
- [x] All modules have docs/modules/<module>/README.md
- [x] Godoc comments on exported symbols are clear
- [x] No broken links in docs (spot-checked)
- [x] Search for "TODO", "FIXME", "XXX" yields no critical items

**Status:** COMPLETE

---

## Release Artifacts ✅

- [x] STABILITY.md is finalized and in sync with source
- [x] COMPATIBILITY.md is finalized with migration paths
- [x] READINESS.md (this file) is complete
- [x] docs/release/V1-RELEASE-NOTES.md template prepared
- [x] CHANGELOG.md is up to date
- [x] Version tag is prepared (v1.x.x)
- [x] GitHub release is drafted
- [x] Blog/announcement outline is sketched

**Status:** READY FOR RELEASE

---

## v1 Feature Completeness

### Stable Roots (All GA)

- [x] core — App, routes, middleware, lifecycle
- [x] router — Matching, params, groups, metadata
- [x] contract — Responses, errors, binding
- [x] middleware — Composition + standard packages
- [x] security — Auth, JWT, passwords
- [x] store — Contracts + in-memory impls
- [x] health — Status models
- [x] log — Interfaces + default logger
- [x] metrics — Contracts + collectors

**Status:** ALL COMPLETE

### Beta Extensions (7 families)

- [x] x/rest — CRUD conventions
- [x] x/websocket — Real-time
- [x] x/gateway — Proxy/rewrite
- [x] x/observability — Prometheus/OTel
- [x] x/tenant — Multi-tenancy
- [x] x/frontend — Asset serving
- [x] x/messaging — Queues/pub-sub

**Status:** ALL COMPLETE & STABLE

### Reference Applications (16 apps)

- [x] standard-service — Canonical minimal
- [x] production-service — Hardened prod
- [x] with-rest — REST CRUD
- [x] with-websocket — WebSocket
- [x] with-gateway — Gateway
- [x] with-observability — Prometheus/OTel
- [x] with-tenant — Multi-tenant
- [x] with-frontend — Asset serving
- [x] with-messaging — Messaging
- [x] with-tenant-admin — Admin UI
- [x] with-ai — AI provider
- [x] with-events — Event-driven
- [x] with-ops — Admin routes
- [x] with-rpc — gRPC
- [x] with-webhook — Webhooks
- [x] benchmark — Performance

**Status:** ALL PRESENT & DOCUMENTED

---

## CI/CD Ready

- [x] GitHub Actions workflows configured
- [x] All gates pass locally (`make gates`)
- [x] Code coverage targets met
- [x] Linter (golangci-lint) passes
- [x] Security scans configured
- [x] Dependency audit configured
- [x] Docs build script functional (`make website-sync`)
- [x] Release script prepared

**Status:** READY

---

## Community Communication

- [x] README.md is clear to new developers
- [x] Getting started path is under 30 minutes
- [x] Adoption path is linear and well-documented
- [x] Migration guides from competitors are present
- [x] Extension guide helps with technology choices
- [x] FAQ/troubleshooting is available
- [x] Governance model is transparent (AGENTS.md, CLAUDE.md)
- [x] Support/contribution guidelines are clear

**Status:** READY

---

## Final Verification (Before v1.1.0 Release Tag)

Run this before tagging:

```bash
# Full validation suite
make gates

# Check documentation
grep -r "TODO\|FIXME\|XXX" docs/ examples/ || echo "✓ No blockers"

# Verify modules
go run ./internal/checks/module-manifests
go run ./internal/checks/dependency-rules
go run ./internal/checks/reference-layout
go run ./internal/checks/public-entrypoints-sync

# Build examples
go build ./examples/hello
go build ./examples/standard-api
go build ./examples/error-handling

# Verify links (manual spot-check)
echo "Check: README links to POSITIONING.md" && grep -q "POSITIONING.md" README.md && echo "✓"
echo "Check: STABILITY.md references 9 roots" && grep -c "^| \`" STABILITY.md | grep 9 && echo "✓"
echo "Check: extension-guide.md references all extensions" && grep -q "x/rest\|x/websocket" docs/start/extension-guide.md && echo "✓"
```

**Status for release:** ✅ ALL GREEN

---

## Post-Release (for next iteration)

After v1.1.0 is tagged and released:

- [ ] Monitor GitHub issues for feedback
- [ ] Collect v1.2 feature requests
- [ ] Plan v1.2 extension promotions (ai/provider → beta?)
- [ ] Update roadmap with v1.2 focus
- [ ] Announce to Go community (blog, Twitter, etc.)
- [ ] Monitor adoption metrics

---

## Sign-Off

**Readiness review:** ✅ COMPLETE  
**All checklist items:** ✅ DONE  
**Release status:** ✅ READY

This document confirms that Plumego v1 is:
- ✅ Positioned clearly to the Go community
- ✅ Documented comprehensively for adoption
- ✅ Bounded explicitly for safe development
- ✅ Stable and production-ready
- ✅ Agent-friendly for maintainability
- ✅ Ready for widespread community adoption

---

**Next:** Create GitHub release with V1-RELEASE-NOTES.md, tag v1.1.0, announce.
