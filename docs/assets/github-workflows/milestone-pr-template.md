<!--
  Milestone PR Body Template
  ──────────────────────────
  The agent fills this template when opening the milestone PR.
  Replace every <placeholder> with actual content.
  Delete comment blocks before submitting.

  Usage: paste this template as the PR body, fill each section,
  then delete this header comment.
-->

## milestone(<MILESTONE_ID>): <Title>

> **Spec:** `tasks/milestones/active/<MILESTONE_ID>.md`  
> **Branch:** `milestone/<MILESTONE_ID>-<slug>`  
> **Depends on:** <none | M-NNN, M-MMM>

---

### Goal

<!-- Copy verbatim from the spec's ## Goal section. -->
<paste goal here>

---

### Approach

<!--
  Agent-authored. 3–5 bullets describing WHAT was done and any non-obvious
  choices made during implementation. This is the "方案确认" surface for the
  human reviewer — they confirm the approach matches intent.
  Focus on decisions, not file lists.
-->

- <!-- e.g. "Chose interface embedding over wrapping to preserve http.Handler compatibility" -->
- <!-- e.g. "Placed ResourceRouter in a separate file to keep handler.go under 120 lines" -->
- <!-- e.g. "Used table-driven tests with httptest.NewRecorder for all error paths" -->

---

### Architecture Decisions — Implemented As Specified

<!--
  For each Architecture Decision in the spec, confirm it was followed.
  Format: "✅ <decision> — <how it was implemented>"
  If a decision could NOT be followed: "⚠️ <decision> — <deviation and reason>"
  Deviations must be surfaced here; do not silently deviate.
-->

| Decision | Status | Notes |
|----------|--------|-------|
| <!-- decision from spec --> | ✅ followed | |
| | | |

---

### Scope Boundary Check

<!--
  List every file changed. Mark each with its module and whether it was
  declared in the spec's Affected Modules.
-->

| File | Module | In Spec Scope? |
|------|--------|---------------|
| <!-- e.g. x/rest/handler.go --> | <!-- x/rest --> | ✅ yes |
| | | |

**Out-of-scope files touched:** (none) <!-- or list with justification -->

---

### Open Questions Resolved

<!--
  Copy from the spec's ## Open Questions section.
  If the section was empty, write "(none)".
  If the agent surfaced new questions and resolved them autonomously, list them.
  If any question required human input and is STILL OPEN, mark the PR as Draft.
-->

(none)

---

### Quality Gate Results

<!--
  Paste the actual terminal output of the acceptance criteria commands.
  All must show exit 0. Do not summarize; paste verbatim.
-->

<details>
<summary>Gate output (click to expand)</summary>

```
$ go test -race -timeout 60s ./<primary-module>/...
<output>

$ make gates
<output>
```

</details>

---

### Reviewer Checklist

<!-- Human fills this during review. -->

- [ ] Goal matches what was intended in the spec.
- [ ] Approach section confirms the method, not just the result.
- [ ] No Architecture Decision violations (all rows show ✅).
- [ ] Scope boundary: no unexpected files in the diff.
- [ ] CI gate table in the bot comment: all green.
- [ ] `go.mod` unchanged (no new external dependencies).
- [ ] Tests cover the negative path.
- [ ] Ready to merge.
