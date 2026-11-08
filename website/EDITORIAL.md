# Website Editorial Guidelines

This file records the standing content rules for the Plumego website.
It complements `README.md` (technical and deployment rules) by governing
messaging, page structure, and editorial decisions.

Use this file when:
- deciding how to frame a new page or section
- resolving a conflict between two ways to present the same idea
- reviewing a marketing or docs PR for content consistency

---

## Standing Rules

These rules are fixed. They apply to every page, every locale, every PR.

### Rule 1 — Every page serves exactly one primary question

Each published page must answer one question and redirect readers elsewhere
for everything else. A page that tries to serve two primary questions should
be split or merged.

| Page | Primary question |
| --- | --- |
| `/` | What is Plumego and is it worth a second look? |
| `/why-plumego` | Does Plumego fit my team, service shape, and review expectations? |
| `/architecture` | How are modules organized and where does a change belong? |
| `/examples` | What can I run and what does the canonical code look like? |
| `/releases` | Which surfaces are stable enough to adopt right now? |
| `/use-cases` | Is Plumego a quick fit for my situation? (short gateway only) |
| `/roadmap` | What is the current direction and what stays explicitly out of scope? |
| `/docs` | Which implementation page should I open first? |
| `/docs/getting-started` | How do I run the smallest canonical path? |
| `/docs/reference-app` | Which files should I copy from? |

When a page drifts toward a second primary question, record the conflict
here and resolve it before merging.

### Rule 2 — JourneyBar is the cross-page navigation; local footer links are narrow

The `JourneyBar` component (Why Plumego → Examples → Releases)
is the canonical cross-page orientation signal for marketing pages. It must
appear on every marketing page. Architecture is a reference page — it is not a
step in the adoption journey and is therefore not a JourneyBar step (see
CONTENT_RULES Rule 9).

This is enforced by `node scripts/check-content-contracts.mjs`; do not maintain
alternate per-page JourneyBar sequences by hand.

End-of-page navigation must not repeat the full four-card AdoptionNav block
on every page. Instead, link only to the **single most appropriate next step**
from the current page's primary question. Repeating all four choices at the
bottom of every page creates circular navigation.

Acceptable end-of-page patterns:

- one `text-link` to the next page in the journey
- two `text-link` entries at most when the reader's next question genuinely
  branches (e.g. "ready to start" vs "need to check maturity first")

The four-card AdoptionNav block belongs on the home page only.

### Rule 3 — The home page must contain a directly executable entry point

The first code a developer can run must be visible on the home page without
navigating away. This means at minimum one copy-paste block showing:

```
go run ./reference/standard-service
```

or the equivalent `go get` + minimal handler pattern from Path A.

A hero with only CTA buttons that redirect to another page before showing
any code does not satisfy this rule.

### Rule 4 — x/* capabilities must be discoverable from at least one marketing page

The architecture or examples page must contain a scenario-based entry point
for the primary x/* families. Each entry must name the capability, show a
concrete use case, and link to the corresponding module primer.

Minimum required scenarios:

| Scenario | x/* family | Link target |
| --- | --- | --- |
| AI streaming API | x/ai | /docs/modules/x/ai |
| Multi-tenant SaaS | x/tenant | /docs/modules/x/tenant |
| API gateway / proxy | x/gateway | /docs/modules/x/gateway |
| WebSocket real-time | x/websocket | /docs/modules/x/websocket |

A list of module names without scenario framing does not satisfy this rule.

### Rule 5 — Agent-first content is a first-class differentiator

The machine-readable control plane (`specs/task-routing.yaml`,
`specs/change-recipes/`, `specs/dependency-rules.yaml`) is Plumego's
strongest differentiator from other Go HTTP toolkits. It must be presented
as a distinct, named capability — not buried as a bullet point inside another
section.

Required: a dedicated page or a dedicated full section on an existing page
(minimum 300 words) that explains:

1. how `specs/task-routing.yaml` routes work to the right module
2. how `specs/change-recipes/` reduces agent hallucination surface
3. how `internal/checks/dependency-rules` enforces import boundaries
   mechanically instead of relying on reviewer memory

The page `/docs/concepts/agent-first-workflow` is the intended target.
Until it exists as a published page, the architecture page must carry this
content inline.

### Rule 6 — Every module name mentioned in marketing or docs must be linked

When a module name appears in prose or a list (`x/ai`, `x/tenant`, `core`,
`router`, etc.), it must be an anchor link pointing to the corresponding
module primer or a stable-roots / x-family overview page.

A static list of module names that are not clickable does not satisfy this rule.

This applies to:
- the architecture page module lists
- the home page module map
- the examples page import-path snippet labels
- any inline `code` span that is a Go import path

### Rule 7 — Stability signals must appear before the fourth click

A developer evaluating adoption must be able to determine which surfaces
are stable versus experimental within three clicks from the home page.

Required: at minimum one of the following on the home page or the
architecture page (not only on the releases page):

- a one-line label per module group: "Stable roots — long-term API target"
  vs "x/* families — experimental unless labeled otherwise"
- a prominent link labeled "Check module maturity" in the hero or
  immediately below the module map

The releases page carries the full support matrix. The earlier pages must
create a clear enough signal that readers know to check it before assuming
compatibility.

---

## Known Problems and Resolution Plans

These are open issues identified during the May 2026 site review. Each entry
records the problem, its evidence, and the agreed resolution. Close an entry
by implementing the resolution and updating the status.

---

### Problem 1 — `/use-cases` page has an ambiguous identity

**Status:** Closed — 2026-05-08

**Evidence:**
The `/use-cases` URL implies scenario demos ("use case examples"), but
the page's actual content is an adoption gate ("quick fit check"). The
page's own prose acknowledges this: `"This page is now the short adoption
gateway"`. The content heavily overlaps with `/why-plumego` (fit checks,
slow down signals, four-link navigation footer). Developers arriving from
search engines expecting runnable use case demos will be confused.

**Root cause:**
The page was repurposed from use-case demos to adoption fit-check without
updating its URL or aligning its scope with `/why-plumego`.

**Resolution:**
Choose one of the following and close this entry after implementing:

Option A — Merge into `/why-plumego`:
Move the "quick fit check" content into a collapsible or condensed section
at the top of `/why-plumego`. Redirect `/use-cases` → `/why-plumego`.
Update sitemap and footer links.

Option B — Repurpose as genuine use-case scenarios:
Replace the current content with four concrete scenario cards:
"Internal HTTP API", "Shared platform service", "Multi-tenant SaaS", and
"AI-enabled service". Each card links to the corresponding entry in
`/examples` or the relevant module primer. Rename the page eyebrow to
"Use Case Scenarios" and update the description accordingly.

Option B aligns better with Rule 4 (x/* discoverability) and removes the
overlap with `/why-plumego` entirely.

---

### Problem 2 — End-of-page navigation is circular on every marketing page

**Status:** Closed — 2026-05-08

**Evidence:**
The home page, `/why-plumego`, `/use-cases`, and `/examples` all end with
a near-identical four-card block (Why Plumego / Architecture / Examples /
Releases). A reader who finishes `/why-plumego` and clicks through to
`/architecture` sees the same four choices again at the bottom of that page.
There is no forward momentum signal — only a loop.

**Root cause:**
The `AdoptionNav` component was built for the home page and then reused
on interior pages without adjusting the link set.

**Resolution:**
- Keep `AdoptionNav` (four cards) on the home page only.
- On every other marketing page, replace the bottom navigation block with
  at most two `text-link` entries pointing to the single most logical next
  step from that page's primary question. See Rule 2 for the allowed pattern.
- Verify `JourneyBar` is present on every marketing page to provide the
  orientation context that the repeated four-card block was trying to supply.

---

### Problem 3 — x/* capability families are not discoverable from marketing pages

**Status:** Closed — 2026-05-08

**Evidence:**
The architecture page lists x/* module names (`x/ai`, `x/tenant`,
`x/gateway`, `x/websocket`, etc.) as a plain text list with no scenario
framing and no links to module primers. A developer who wants to know
"can Plumego handle AI streaming?" or "does it support multi-tenancy?"
must navigate from the marketing surface into `docs/modules/` manually.

**Root cause:**
The marketing surface was designed around stable-root clarity (which it
achieves) but did not apply the same treatment to extension families.

**Resolution:**
Add a "What you can build with x/*" section to the architecture page or
the examples page. The section must include at minimum the four scenarios
defined in Rule 4, each with a module name, a one-sentence description,
and a link to the module primer. Module names in the existing plain list
must also be converted to links per Rule 6.

---

### Problem 4 — Agent-first differentiator is buried as a bullet point

**Status:** Closed — 2026-05-08

**Evidence:**
The `/why-plumego` page has a section titled "Repository shape that AI
coding assistants can work with reliably" with three `signal-list__item`
cards covering `specs/task-routing.yaml`, `specs/dependency-rules.yaml`,
and `specs/change-recipes/`. This content is accurate but presented at the
same visual weight as other sections on the page, with no dedicated page
behind it. The link `href: '/docs/concepts/agent-first-workflow'` on
the architecture page references a page that may not yet be published.

**Root cause:**
The agent-first design was established early in the repo architecture but
not yet elevated to a first-class marketing page.

**Resolution implemented:**
1. `/docs/concepts/agent-first-workflow` already exists as a full-depth
   concept page (322 lines) covering task-routing, dependency enforcement,
   change recipes, gate execution, and cross-boundary agent workflow.
2. The architecture page `repositorySignals` array already contains a
   dedicated "control plane" card linking to this page with CTA
   "Read agent-first workflow".
3. The link resolves to the published page confirmed during May 2026 review.

The zh equivalent `/zh/docs/concepts/agent-first-workflow` remains pending
as a separate task if Chinese translation coverage needs to be extended.

---

### Problem 5 — Home page requires navigation before showing executable code

**Status:** Closed — 2026-05-08

**Evidence:**
The home page hero provides two CTAs ("Get Started" → `/docs/getting-started`
and "See Architecture" → `/architecture`) but no inline runnable code. The
`MinimalExample` component appears further down the page, but it is separated
from the hero by the `WiringContrast` section. A developer who lands on the
home page and wants the first `go run` command must either scroll past two
full sections or navigate to another page.

**Root cause:**
The hero was designed to lead with positioning and audience fit. The code
example was placed in a secondary section as supporting evidence.

**Resolution:**
Add an inline code block to the hero section (or immediately below the hero
summary) that shows the first runnable command without navigation:

```
go run ./reference/standard-service
# or: go get github.com/birdordev/plumego
```

This satisfies Rule 3 and reduces the click cost for developers who want
to evaluate the framework by running it rather than reading about it.

---

### Problem 6 — Stability signals require four clicks from the home page

**Status:** Closed — 2026-05-08

**Evidence:**
A developer who wants to know whether `x/tenant` is production-safe must:
home page → why-plumego or architecture → see that x/* is "experimental" in
passing text → releases page → read the support matrix. The first clear
signal that x/* families are experimental (not just different from stable
roots) does not appear until the releases page, which is the last item in
the `JourneyBar`.

The home page `heroStatus` section mentions "Extension families carry
explicit maturity labels" but does not show what those labels are or
link to the releases page from that statement.

**Root cause:**
Stability framing was concentrated in the releases page rather than
surfaced earlier in the evaluation path.

**Resolution:**
Add one of the following to the home page or architecture page per Rule 7:

- A two-line label block below the module map:
  `"Stable roots — long-term API target"` /
  `"x/* families — experimental unless labeled otherwise — check releases"`
- A `text-link` labeled "Check module maturity" directly from the `heroStatus`
  statement about maturity labels, pointing to `/releases`.

The releases page already has the full support matrix. The earlier pages
only need to route readers there with enough context to know why they
should check.

---

### Rule 8 — Performance claims must be backed by reproducible evidence

Any page that makes a performance claim about Plumego (throughput, latency, overhead relative to
`net/http`, or comparison benchmarks) must satisfy one of the following:

1. Link to a reproducible benchmark in the repository (`bench/` or a tagged release artifact).
2. Qualify the claim explicitly: "expected to be comparable to net/http — no benchmark available yet."

A claim such as "runs at net/http-equivalent throughput" or "no overhead beyond net/http" without
an evidence link is not acceptable. The claim must either be supported or removed until evidence
exists.

**Applies to:** `/why-plumego`, any module primer, any page that mentions performance in a
marketing context.

---

### Rule 11 — "Agent-first" must never be ambiguous between AI coding assistants and AI services

The word "agent" in Plumego has two distinct meanings that must not be conflated:

1. **AI coding assistants** — tools like Copilot, Claude, and Cursor that write or review code.
   Plumego's control plane (`specs/task-routing.yaml`, `specs/change-recipes/`,
   `internal/checks/dependency-rules`) is designed to give these tools the same routing signals
   as a senior human reviewer. This is Plumego's repository design philosophy.

2. **AI agent services** — application-layer AI systems built using `x/ai` (provider adapters,
   session management, streaming, tool invocation). This is a capability family, not a repository
   design philosophy.

**Required:** Any section or page that uses the term "agent-first" or discusses AI coding
assistant support must include a one-sentence disambiguation within the first paragraph of that
section. The sentence must make clear that the context is AI coding assistants (not AI agent
services), or vice versa.

**Acceptable disambiguation form** (on the coding-assistant side):
> "This section is about AI **coding assistants** (Copilot, Claude, Cursor) — tools that write
> or review code. It is not about building AI agent services; that is the scope of `x/ai`."

**Acceptable disambiguation form** (on the AI services side):
> "`x/ai` is for building services that call AI providers. For information about how AI coding
> assistants navigate this repository, see [Agent-first Workflow](/docs/concepts/agent-first-workflow)."

**Must apply to:** `/why-plumego` (EN + ZH) agent-first section, any module primer for `x/ai`,
any marketing or docs page that uses the phrase "agent-first" in a context where the reader might
confuse the two meanings.

---

## Revision Log

| Date | Change |
| --- | --- |
| 2026-05-08 | Initial version. Six problems recorded, seven rules established, based on full site review. |
| 2026-05-08 | All six problems implemented. P4 closed (page pre-existed). P1 use-cases rewritten as genuine scenarios. P2 circular nav fixed in why-plumego EN+ZH. P3 x/* scenarios and module links added to architecture EN+ZH. P5 run command added to Hero. P6 stability strip added to home page EN+ZH. |
| 2026-05-09 | Closed status fields for P1–P3, P5–P6 (were left Open in body text despite revision log recording them as done). Added Rule 8 (performance claims require evidence). CONTENT_RULES Rule 8 expanded to allow agent-first differentiator section on why-plumego marketing page. CONTENT_RULES Rule 13 added (multi-page API patterns must trace to canonical definition). EN why-plumego parity section added for AI coding differentiator (matching ZH). Advanced reference apps section added to EN+ZH examples pages. Getting Started lifecycle clarification added. |
| 2026-05-10 | Rule 8 violation fixed: why-plumego EN+ZH performance claim now qualified with "expected to be comparable — no benchmark yet." README Quick Start unified with Hero (both use app.Run() combined path; explicit lifecycle moved to a note). Architecture and examples page footer links reduced from 4 to 2 per Rule 2. docs/index.mdx EN+ZH: stage-based branching nav added ("Where are you?"). Rule 2 JourneyBar description corrected from 4-step to 3-step (Architecture is a reference page, not a journey step). why-plumego EN+ZH: inline terminology table added for stable root, x/* family, control plane, canonical path per CONTENT_RULES Rule 4. ZH JourneyBar unified to 3-step across all five zh marketing pages (was 4-step in all zh pages). ZH agent-first-workflow confirmed as full translation — no placeholder content. Roadmap EN+ZH: JourneyBar added (was missing, violating Rule 2). Releases EN+ZH: footer links reduced from 4 to 2. agent-first-workflow EN+ZH: "Routing in practice" worked example added — shows actual task-routing.yaml snippets for the rate-limiting classification scenario, making the agent-first differentiator experiential rather than only explanatory. TaskRouter interactive component added (src/components/docs/TaskRouter.astro): chip selector for 10 routing scenarios, result panel showing routing_rule/destination/startWith/avoid/classificationReason, vanilla JS, locale prop (en/zh), embedded in agent-first-workflow EN+ZH. Data layer: scripts/sync-routing.mjs generates src/generated/routing.ts from specs/task-routing.yaml + manual annotations. CONTENT_RULES Rule 8 updated to classify TaskRouter as Tier 2 component; Tier 1 static 4-row teaser table added to why-plumego EN+ZH with CTA to interactive demo. |
| 2026-05-10 | Site analysis and systematic improvements. (1) getting-started EN+ZH: main lifecycle example changed from app.Prepare()+app.Server() to app.Run() combined path, matching Hero and README; explicit lifecycle pattern moved to a callout note per CONTENT_RULES Rule 13. (2) why-plumego EN+ZH: one-sentence disambiguation added to agent-first section clarifying "agent" means AI coding assistant (not AI agent service built with x/ai), per new Rule 11. (3) examples EN+ZH + site.ts: full reference apps matrix added (7 capability apps: with-gateway, with-messaging, with-websocket, with-webhook, with-rest, with-ops, production-service); workerfleet elevated to dedicated "Production-scale reference" section with full detail list, per new CONTENT_RULES Rule 14. (4) docs/index.mdx EN+ZH: Architecture page added to "Pick by question" table so it is discoverable from within the docs portal. (5) CONTENT_RULES: Rule 14 added (examples page must list all reference apps, with verification instruction). (6) EDITORIAL: Rule 11 added (agent-first disambiguation required; defines acceptable disambiguation forms for both AI coding assistant and AI service contexts). |
