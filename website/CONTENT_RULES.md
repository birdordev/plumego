# Website Content Rules

Rules for writing and reviewing website content. Apply to all pages under `src/pages/` and `src/content/docs/`.

---

## Rule 1 — Code before prose

Every feature or module page must show a runnable code example as its **first content block**. Do not open with a conceptual description. If the page cannot show a meaningful code example, it may open with a CLI command or terminal output instead.

**Applies to:** module primers, guides, getting-started.

---

## Rule 2 — One page, one next step

Each documentation page must end with exactly **one** clear "next step" link. Do not offer three parallel options at the end of a page. If branching is necessary, use a table labeled "leave this page when" with a question in each row and one corresponding link.

---

## Rule 3 — Stability badge required on every x/* module page

Every page under `docs/modules/x/**/*.mdx` must display its stability tier as the **first visible element** after the title. Use one of:

- `**Stable**` — long-term API target, part of v1 hardening scope
- `**Experimental**` — API not frozen, compatibility not guaranteed

Do not bury maturity information in body text.

---

## Rule 4 — No internal terminology without definition

The following terms must be **defined on first use** on any user-facing page:

| Term | One-line definition to include |
| --- | --- |
| stable root | A module with a long-term API compatibility promise, part of the v1 hardening scope |
| x/* family | An optional capability module under the `x/` directory; experimental unless labeled otherwise |
| control plane | The `docs/`, `specs/`, and `tasks/` directories that hold human-readable rules and machine-readable constraints |
| canonical path | The default service layout defined by `reference/standard-service` |

Do not use these terms as if they are universally understood jargon.

---

## Rule 5 — Competitor comparisons must include "when not to choose Plumego"

Any page that compares Plumego with Gin, Echo, Chi, or other toolkits must include a column or section explicitly stating **when to choose the alternative** over Plumego. A comparison that only argues in favor of Plumego is not acceptable.

---

## Rule 6 — Chinese content sync transparency

The site displays a notice on Chinese pages flagged by the source-hash check.
Review the English and Chinese pair before updating its baseline. Follow the
[translation review workflow](./CONTRIBUTING.md#translation-lag-detection).

---

## Rule 7 — Homepage must be scannable in under 10 seconds

The homepage hero section must convey, **without scrolling**:

1. What Plumego is (one sentence)
2. A runnable code example (≤ 20 lines)
3. The primary call to action ("Get Started → /docs/getting-started")

Do not add marketing sections between the hero and the first code example.

---

## Rule 8 — Agent-first content placement

Plumego's machine-readable control plane is a genuine differentiator from other Go HTTP toolkits.
Its placement follows a two-tier model:

**Tier 1 — Marketing summary (allowed on why-plumego only):**
A dedicated section on `/why-plumego` (EN and ZH) may describe the agent-first design at the
level of "teams using AI coding tools benefit from X." This section must:
- Name the three capabilities: task routing, dependency enforcement, change recipes.
- Show one inline code reference per capability (e.g. `specs/task-routing.yaml`).
- Link to `/docs/concepts/agent-first-workflow` for full detail.
- Stay under 300 words total.

**Tier 2 — Full technical depth (advanced docs only):**
Full coverage of `specs/task-routing.yaml`, `specs/change-recipes/`, and
`internal/checks/dependency-rules` belongs only in:
- `docs/concepts/repo-control-plane`
- `docs/concepts/agent-first-workflow`

The `<TaskRouter>` interactive component (`src/components/docs/TaskRouter.astro`) is a
Tier 2 component. It renders 10 routing scenarios from the generated data layer and must
only be embedded in Tier 2 pages. Do not embed it in marketing pages or module primers.

The Tier 1 marketing section on `/why-plumego` may include a static 4-row classification
table (HTML `div.comparison-table`) as a teaser, plus a CTA link to the interactive demo.
This table must not exceed 4 rows and must not reproduce the full routing table.

**Must not appear on:**
- Homepage (`src/pages/index.astro`)
- Getting Started (`src/content/docs/docs/start/getting-started.mdx`)
- Any module primer
- Architecture page (beyond a single card link to the concept page)

The intended audience for full agent-first content is contributors and teams integrating AI
tooling into their workflow. The Tier 1 marketing summary serves developers evaluating Plumego
who use AI coding tools and need to know the framework is designed with that in mind.

---

## Rule 9 — JourneyBar consistency

Every marketing page that renders a `<JourneyBar>` must use the same four-step sequence:

```js
// English
[
  { label: 'Why Plumego', href: '/why-plumego' },
  { label: 'Extensions', href: '/extensions' },
  { label: 'Examples', href: '/examples' },
  { label: 'Releases', href: '/releases' },
]

// Chinese
[
  { label: '为什么选择', href: '/zh/why-plumego' },
  { label: '扩展', href: '/zh/extensions' },
  { label: '示例', href: '/zh/examples' },
  { label: '发布', href: '/zh/releases' },
]
```

Do not add, remove, or reorder steps between pages. The JourneyBar must visually convey a single adoption path, not a sitemap. Architecture is not a step in the adoption path — it is a reference page.

**Enforcement:** `node scripts/check-content-contracts.mjs` validates the exact step sequence on every `.astro` page that renders `<JourneyBar>`.

---

## Rule 10 — Examples pages must open with a runnable command

Any page whose title includes "Examples" or whose primary purpose is to show code must present a runnable terminal command or code block as its **first content block** after the hero/JourneyBar, before any descriptive prose or recipe lists.

The command must be concise enough to run in under 30 seconds: typically `go run ./reference/standard-service` plus one verification `curl`.

---

## Rule 11 — Pages without a translation must say so

If an English documentation page has no corresponding `/zh/` page, the English page must display the following notice in a blockquote at the start of the page body (after frontmatter):

```md
> **Note:** This page is not available in Chinese. Refer to the English version.
```

For existing Chinese translations, follow Rule 6.

---

## Rule 12 — One primary responsibility per page

Each documentation or marketing page must have exactly one primary responsibility stated in its frontmatter `description`. If a page answers more than two unrelated question types, split it or redirect secondary questions to the page that owns them.

Responsibility overlap test: if removing a section from the page would not break the page's primary purpose, the section probably belongs elsewhere.

**Examples of violations:**
- The getting-started page explaining module ownership decisions (belongs in Modules Overview)
- The modules overview page explaining how to run an example (belongs in Getting Started)
- The FAQ repeating information that is already the primary content of a guide page (keep the FAQ answer short and link to the guide)

---

## Rule 14 — Examples page must list all reference apps

When a new subdirectory is added under `reference/` in the repository root, a corresponding entry must
be added to the examples page (`src/pages/examples.astro` and `src/pages/zh/examples.astro`) within
the same PR or the immediately following docs PR.

The examples page is the canonical discoverability surface for reference material. A reference app
that exists in the repository but is not listed on the examples page is effectively invisible to
evaluators.

**Required fields per entry:** app path (`reference/<name>`), owning `x/*` family (or `stable roots`
for production-service), one-sentence description, maturity label, and a link to the module primer
or reference-app doc page.

**Applies to:** `src/pages/examples.astro`, `src/pages/zh/examples.astro`, `src/data/site.ts`
(EXAMPLES_COPY referenceMatrix).

**Verification:** Run `node scripts/check-content-contracts.mjs`. Every subdirectory except
`standard-service` (covered by the canonical reference-app page) and `benchmark` (not a service
reference app) must appear in `referenceMatrix`.

---

## Rule 13 — API call patterns that appear on multiple pages must trace to one canonical definition

If an API call pattern (e.g. `app.Run()`, `app.Prepare()` + `app.Server()`, handler registration)
appears on two or more pages, all occurrences must:

1. Use **identical syntax** (same method name, same argument shape, same error-handling style).
2. Either be the same pattern, or explicitly name both patterns and explain the difference on each
   page where both appear.

**Canonical source of truth:** `docs/reference/canonical-style-guide.md` defines the authoritative form for
all public API patterns. Website pages are downstream — when in doubt, match the style guide.

**Examples of violations:**
- Getting Started shows `app.Prepare()` + `app.Server()` while Hero shows `app.Run()` with no
  explanation that both are valid entry points.
- A module primer uses `contract.WriteError(w, err)` while another primer uses
  `contract.WriteError(w, r, err)` — both must match the current stable signature.

**Resolution pattern:** Add a brief inline note wherever a secondary pattern appears:
> `app.Run()` is the combined path. The code above uses `app.Prepare()` + `app.Server()` to
> show the two lifecycle steps explicitly. See [core module primer](/docs/modules/core) for
> the full API.

---

## Rule 15 — Content contract checks are mandatory

Run `node scripts/check-content-contracts.mjs` before merging website content changes. The checker
owns three executable contracts:

- every `<JourneyBar>` uses the fixed Why Plumego → Extensions → Examples → Releases sequence
- every `reference/` service app is either listed in the examples reference matrix or explicitly
  declared as an exception
- subordinate extension primitives are displayed with their full owning path, such as
  `x/messaging/webhook`, not shortened as if they were top-level `x/*` families

Run `node scripts/check-doc-api-symbols.mjs` as the companion API-pattern check. It scans docs,
marketing pages, and `src/data/site.ts` for stale Plumego API examples.

---

## Rule 16 — Module documentation coverage is intentionally selective

The website presents **32 curated module pages** (9 stable roots + 14 x/* families + 9 selected subordinates). The repository contains **75 exhaustive module primers** under `docs/modules/`. This is intentional, not a gap.

**Design rationale:** Website pages are scoped to the primary entrypoint for each capability family. Sub-packages that are consumed indirectly (`x/ai/semanticcache/*`, `x/gateway/protocolmw`, etc.) are documented in `docs/modules/` for engineers working inside those packages, but are not surfaced as standalone website pages to avoid overwhelming evaluators.

**What belongs on the website:**
- Every stable root module (`core`, `router`, `contract`, etc.) — one page each
- Every top-level `x/*` family (`x/ai`, `x/gateway`, etc.) — one page each
- Sub-packages that users directly import as their primary entry point (`x/observability/ops`, `x/messaging/webhook`, etc.)

**What belongs only in `docs/modules/`:**
- Sub-packages that are consumed transitively or only by advanced users
- Internal implementation packages within a family

**Maintenance rule:** When a new module is added under `x/`, its `docs/modules/` primer is mandatory. A website page is only required if the module is a top-level family entrypoint or a sub-package that users will directly import as their primary entry. When in doubt, start with `docs/modules/` only and promote to a website page once adoption patterns confirm user need.

**Verification:** `docs/modules/INDEX.md` is the master list of all 75 module primers. `src/content/docs/docs/modules/` is the website subset. These are intentionally different sizes.
