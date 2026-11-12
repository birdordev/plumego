<!--
  Milestone Spec Template — plumego
  Copy to tasks/milestones/active/M-NNN-short-name/M-NNN.md and fill every section.
  Lines starting with <!-- are comments; delete them in your spec.
  Keep the spec under ~100 lines. If longer, split into two milestones.
-->

# M-XXX: <Title>

<!--
  Frontmatter for tooling and roadmap tracking.
  depends_on: list of milestone IDs that must be merged before this runs.
  parallel_ok: true if this milestone's implementation phases can run alongside
               other active milestones that touch different modules.
-->
- **Branch:** `milestone/M-XXX-<slug>`
- **Depends on:** (none) <!-- or: M-001, M-002 -->
- **Parallel OK:** yes <!-- yes = independent of other active milestones -->

<!--
  Companion artifacts expected for this milestone:
  - Plan:   tasks/milestones/<state>/M-XXX-short-name/plan-M-XXX.md
  - Verify: tasks/milestones/<state>/M-XXX-short-name/verify-M-XXX.md
  Before `make milestone`, scaffold the plan with:
    make new-plan M=active/M-XXX-short-name
  Before PR packaging, fill the verify report with:
    make new-verify M=active/M-XXX-short-name
-->

---

## Goal

<!-- One sentence. Describes the observable end-state, not the steps. -->
<!-- Good: "x/rest exposes a canonical ResourceHandler that replaces the three -->
<!--       ad-hoc CRUD patterns in reference apps." -->
<!-- Bad:  "Add a ResourceHandler interface and update routes." -->

---

## Architecture Decisions

<!--
  Human-authored. These are FIXED constraints the agent must not override.
  List only non-obvious decisions — things the agent might reasonably choose differently.
  The agent reads these before touching any code.
-->

- <!-- e.g. "ResourceHandler must embed http.Handler, not wrap it." -->
- <!-- e.g. "Error shape must use contract.WriteError, not a new type." -->
- <!-- e.g. "Use Go 1.26-compatible standard-library APIs only." -->

---

## Context — Read Before Touching Code

<!-- Ordered. Specific file paths, not just module names. -->

1. `AGENTS.md`
2. `specs/dependency-rules.yaml`
3. `specs/task-routing.yaml`
4. `<primary-module>/module.yaml`
5. <!-- e.g. `specs/change-recipes/add-http-endpoint.yaml` -->
6. <!-- e.g. `reference/standard-service/internal/app/routes.go` -->

## Affected Modules

- **Primary:** `<primary-module>`
- **Secondary:** (none) <!-- only if truly unavoidable -->

---

## Tasks

<!--
  Phases are executed in order. Tasks within a PARALLEL phase are independent
  and can run concurrently. Tasks within a SEQUENTIAL phase must run in order.
  Keep each task atomic: one file or one testable unit.
-->

### Phase 1 — Orient (sequential)

1. Read every file in the **Context** section above.
2. Identify the exact change points; do not modify anything yet.

### Phase 2 — Implement (parallel)

<!-- Tasks in this phase are independent. Run them concurrently. -->
- [ ] <!-- e.g. "`x/rest/handler.go`: Add ResourceHandler interface" -->
- [ ] <!-- e.g. "`x/rest/router.go`: Add ResourceRouter type" -->
- [ ] <!-- e.g. "`<primary-module>/module.yaml`: Update doc_paths" -->

### Phase 3 — Test (sequential)

3. Add or update tests next to every changed file.
4. Confirm tests cover the negative path (invalid input, boundary conditions).

### Phase 4 — Validate and Ship (sequential)

5. Run `make fmt` before validation.
6. Run the **Acceptance Criteria** commands below; fix any failures.
7. Commit: `feat(<module>): <short description> [M-XXX]`
8. Final commit: `milestone(M-XXX): <Title>` with bullet list of completed tasks.
9. Push to `milestone/M-XXX-<slug>`.
10. Open PR: title `milestone(M-XXX): <Title>`, body from `docs/assets/github-workflows/milestone-pr-template.md`.

---

## Acceptance Criteria

<!-- Exact commands. All must exit 0. Do not abbreviate. -->

```bash
go test -race -timeout 60s ./<primary-module>/...
make gates
```

Expected: both commands exit 0.

---

## Out of Scope

<!-- Hard stops. The agent must not cross these even if it seems helpful. -->

- Do not change stable root public APIs unless this spec explicitly lists it.
- Do not add entries to `go.mod` (main module stays stdlib-only).
- Do not modify `reference/standard-service` unless listed in Phase 2 above.
- <!-- add milestone-specific stops -->

---

## Open Questions

<!--
  Questions the human could not answer upfront.
  The agent fills this section during execution if it hits a decision point
  that might deviate from the Architecture Decisions above.
  Format: "Q: <question> → Decision: <what the agent did and why>"
  If a question requires human input, stop, push the branch, and open a
  draft PR with this section filled in.
-->

(none at spec time)

---

## Done Definition

- [ ] All Acceptance Criteria commands exit 0.
- [ ] `plan-M-XXX.md` exists in this milestone directory and matches the milestone scope.
- [ ] `verify-M-XXX.md` is filled in this milestone directory before PR packaging.
- [ ] Branch `milestone/M-XXX-<slug>` pushed.
- [ ] PR open, title `milestone(M-XXX): <Title>`.
- [ ] PR body filled from milestone-pr-template.md with actual gate output.
- [ ] Architecture Decisions section: no violations logged.
