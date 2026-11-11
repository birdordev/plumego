# Contributing to the Plumego Website

This document explains how to add or edit documentation, run a local preview, and meet the standards enforced by the content checkers.

## Quick start

Requirements: Node.js 20+, pnpm 10+.

```bash
cd website
pnpm install
pnpm dev        # starts dev server on http://localhost:4321
```

`pnpm dev` runs `pnpm sync` first to pull generated facts from the repository (roadmap, modules, releases, translation lag). You need the full repository checked out — the sync scripts read files from the parent directory.

---

## Adding a new doc page

### 1. Choose the right location

| Content type | Directory | Examples |
| --- | --- | --- |
| Concept explanation | `src/content/docs/docs/concepts/` | `error-model.mdx` |
| How-to guide | `src/content/docs/docs/guides/` | `add-jwt-auth.mdx` |
| Module primer | `src/content/docs/docs/modules/` | `core.mdx` |
| API quick reference | `src/content/docs/docs/reference/` | `api-core.mdx` |
| Top-level info | `src/content/docs/docs/` | `faq.mdx` |

Create the Chinese equivalent in the matching path under `src/content/docs/zh/`.

### 2. Required frontmatter

Every `.mdx` file must include:

```yaml
---
title: Short title (used in sidebar and <title>)
description: One sentence — shown in search results and social previews.
translationKey: unique-slug-matching-the-en-counterpart
sidebar:
  order: <integer>   # controls position within the sidebar group
---
```

`translationKey` links the en and zh versions for translation-lag detection. It must be identical in both files and unique across the entire docs tree.

### 3. Content rules (short version)

1. **Code before prose** on every feature and module page (CONTENT_RULES.md Rule 1).
2. **Stability badge first** on every `x/*` module page — use the `> **Experimental** ...` blockquote pattern.
3. **No circular navigation** — end each page with a "Read next" list of 2–5 links; do not duplicate the AdoptionNav four-card block.
4. **No invented performance claims** — any latency or throughput number must link to a reproducible benchmark (CONTENT_RULES.md Rule 8).
5. **Guide pages must open with a runnable terminal command** (CONTENT_RULES.md Rule 10).

Full rules: [`CONTENT_RULES.md`](./CONTENT_RULES.md).

### 4. Register the page in the sidebar

Open `astro.config.mjs` and add an entry in the correct sidebar group:

```js
{ label: 'My New Page', slug: 'docs/guides/my-new-guide', translations: { 'zh-CN': '我的新指南' } },
```

The `slug` must match the file path relative to `src/content/docs/` and without the `.mdx` extension.

---

## Editing an existing page

Edit the English source and review the matching Chinese page in the same change.
For shared changes such as a repository rename, verify that both pages contain the
updated commands and links before recording the new baseline.

---

## Running the content checkers

```bash
pnpm check            # runs sync, content/API checks, translation-lag check, and Astro type-check
```

The `dev`, `build`, and `check` scripts run `pnpm sync` automatically. Run `pnpm sync` manually to regenerate facts after changing specs, README, or roadmap.

---

## Translation lag detection

From `website/`, run `pnpm translations:check` for a read-only list of pairs whose
English body has changed since the recorded review, or whose baseline is missing.
The check reports both file paths and fails immediately on a mismatch.

After comparing the reported pages and updating the Chinese content as needed,
record the baseline for each reviewed key. Repeat `--update-key` in one command
when reviewing several pairs:

```bash
node scripts/check-translation-lag.mjs --update-key getting-started --update-key modules-core
pnpm sync
pnpm translations:check
```

Include the content changes, `src/content/docs/translation-source-hashes.json`,
and any changes to `src/generated/translation-lag.ts` in the same commit. The
hash excludes frontmatter, so review translated titles and descriptions when
editing them. The site renders a notice automatically on Chinese pages flagged
by the generated map.

---

## Running a production build

```bash
pnpm build    # outputs to dist/
pnpm preview  # serves dist/ locally
```

The build target is Cloudflare Pages (static output). Do not add server-side rendering; the site is fully static.

---

## Generated files

Files in `src/generated/` are created by the sync scripts. Do not edit them by hand — they are overwritten on every `pnpm sync` run.

| Generated file | Source | Script |
| --- | --- | --- |
| `modules.ts` | `specs/repo.yaml`, `specs/task-routing.yaml` | `sync-modules.mjs` |
| `releases.ts` | `README.md` | `sync-release-meta.mjs` |
| `roadmap.ts` | `docs/release/roadmap.md` | `sync-roadmap.mjs` |
| `translation-lag.ts` | English MDX bodies and `src/content/docs/translation-source-hashes.json` | `check-translation-lag.mjs` |

---

## Editorial governance

Before adding new content, read [`EDITORIAL.md`](./EDITORIAL.md). The eight standing rules prevent content sprawl and keep each page answering one primary question.
