# Git Treeline website — critical copy review

**Reviewer lens:** First-time engineer, no prior context, landing from search or a link.  
**Source of truth consulted:** `git-treeline` repo `README.md` (worktree manager: `.treeline.yml` + machine-local `config.json` + `registry.json`; commands `gtl new` / `setup` / `release` / `serve install` / networking / MCP / etc.).  
**Scope:** All live marketing HTML under `website/` (`index.html`, `docs/index.html`, `features/*`, `use-cases/*`). `/networking/` is a redirect to `/features/networking/`.  
**Not in scope:** Rewrites or fixes; menulet app; built `dist/` output.

---

## Audit update — 2026-04-06

This review was **reconciled** against:

- **`tmp/copy-rewrite-plan.md`** — synthesis of five agent reviews (Composer, Grok, Claude 4.6 Opus, Claude 4.6 Sonnet Medium Thinking, GPT 5.3 Codex), including a **truth pass** on the Go codebase.
- **Peer reviews** in `tmp/` for gaps this draft under-emphasized.

**Note:** The original brief asked for six reviewers; only **five** `*-copy-review.md` files exist in `tmp/`. Consensus labels below use **[/5]** where the plan recorded them.

### Codebase truth pass (from synthesis; confirms or tightens earlier notes)

| Claim | Verdict |
| --- | --- |
| `redis.strategy` in project `.treeline.yml` docs | **Wrong per code + README** — parsed from user `config.json` (`internal/config/user.go`); not a project-config key in `project.go`. |
| `gtl release` removes worktree directory | **False** — registry cleanup only; batch mode can note worktree dir still exists (`registry.go` per plan). |
| `gtl serve install` on Windows | **Blocked** in `cmd/serve.go` (non-macOS/non-Linux); WSL called out in error hint. |
| `gtl serve alias` | **Real** (`cmd/serve.go`); networking page mention is justified. |

### What this audit adds to the original Composer review

- **Consensus themes** the first draft spread across sections but did not name as **[/5] universal**: (A) no plain “what is this” sentence, (B) “worktree” undefined, (C) advanced jargon before core loop, (D) two-step install invisible in hero, (E) demo-before-explanation on mobile stack order.
- **Page-level gaps** originally thin or missing: Isolation hero **hooks** before introduction; onboarding **`gtl init` vs `gtl setup`** sequence; Workflows **teardown** paragraph (same factual bug as the review table); Agents **`environment-aware` / MCP undefined** called **P0** in synthesis; use-case hub **opaque headline**; **agents-automation** page stub depth; docs **opening paragraph** order and **optional/required** contradiction for `serve`.
- **Synthesis rejected** one Composer nit: **Mapbox** in the networking integration list was flagged here as buzzword-y; the plan treats it as **defensible** (keys can be hostname-restricted). **Retracted as a copy failure** unless you want tighter domain examples only.

**Downstream deliverable:** `tmp/copy-rewrite-plan.md` turns these diagnostics into a phased rewrite plan (voice, homepage arc, layout track). This file stays **diagnostic-only**; it does not duplicate that plan section-by-section.

---

## Source-of-truth vs copy (cross-cutting)

Factual or model mismatches undermine trust even when the prose sounds confident.

1. **Branch URL shape** — README and CLI use `https://{project}-{branch}.localhost`. Many demos use `https://feature-auth.localhost`, omitting the **project** prefix. **[3/5]** in synthesis for fixing demos; same underlying issue as original review.

2. **`redis.strategy` placement** — README: Redis namespacing under **user** `config.json`. Docs **Configuration → Full reference** lists `redis.strategy` as a `.treeline.yml` field. **Verified:** user config only in code — **high-impact doc bug.**

3. **`gtl release` vs removing worktrees** — Release frees allocations in the registry; **directory removal** is separate (`git worktree remove`). The Workflows **Review commands** row and the **“Done reviewing?”** paragraph both overstate (“Remove worktree” / “tears down the worktree”). **Verified** in codebase per synthesis.

4. **Windows** — `index.html` JSON-LD lists `operatingSystem: macOS, Linux, Windows`. **`gtl serve`** path is not native Windows; CLI may still install. Schema oversells full product parity. **[1/5]** in plan but **P1** regardless of count.

5. **`{resolve:project/branch}` and link overrides** — Docs interpolation table vs README: explicit branch pin vs link override semantics remain easy to miss; site should state **one crisp sentence** (README: `{resolve:api/main}` ignores link overrides; `{resolve:api}` checks links first).

6. **“Use `--json` on any read command”** (`docs` AI agents) — Overbroad; not every subcommand supports `--json`.

7. **`gtl serve alias`** — **Confirmed real**; original review did not dispute networking copy here; noted for audit completeness.

---

## Page: `/` (`index.html`)

### Meta / SEO

- **Vague vs concrete:** `<title>` “Review every branch at the same time” does not say **git worktrees** or **parallel local environments**. **[4/5]** wanted a category-clear title; **P0** in synthesis: subtitle misreads as **code-review tooling** vs **running** multiple branches.
- **Assumed knowledge:** Meta description leads with ports / `resolve` / `link` before category sticks.
- **Hollow / structural:** JSON-LD is feature-dense while visible hero does not spell “worktree.” **P1:** align `operatingSystem` with serve support.

### Nav

- **Mobile / structure:** “Features” is **hover** dropdown — weak on touch (Design track; noted in synthesis as non-copy). **[5/5]** theme: jargon bucket labels without problem mapping.
- **Assumed knowledge:** Isolation / Networking / Workflows / AI Agents are IA labels, not pains.

### Hero

- **Vague / ordering:** H1 is brand-only; **synthesis: add descriptor line** (“worktree environment manager” class). Subhead “Review every branch…” mis-centers **review** vs **run**. **[5/5]**
- **Referential:** Body uses `resolve` / `link` before worktrees are explained. **Directive:** remove from hero; defer to feature pages. **[5/5]**
- **Two-step install:** Hero shows **brew only**; `gtl serve install` appears much lower. **[5/5]**
- **Tone:** Accurate nouns but outcome could be sharper (e.g. second dev server stops dying on :3000).

### Problem / solution

- **Strong:** “Worktrees are easy to create. Hard to run.” — **kept in rewrite plan**; best line **[5/5]**.
- **Accuracy / teaching:** Subtext should **define worktree** in plain language (README-style). **P0** per synthesis.
- **Accuracy friction:** “With” panel `https://feature-auth.localhost` — wrong shape; footnote should flag **one-time** `serve install` (not frictionless). **[3/5]**

### Feature cards (Isolation / Networking / Workflows / AI)

- **Isolation:** Multi-repo **`resolve` / `link` on homepage card** — **P0:** remove; belongs on isolation multi-repo section / subpages only. **[5/5]**
- **Networking:** Slot headline and body pack modes/commands — **P1** reduce; problem-first headline.
- **Workflows:** **`gh` prerequisite** buried; “supervisor story” insider phrasing. **[5/5]** on supervisor wording.
- **AI Agents:** Headline hollow; **MCP undefined** — synthesis **P0** on agents hero.
- **Mobile DOM:** **[/5]** — explanatory copy should lead demos at ~375px *(layout/CSS `order`, not necessarily DOM)*.

### Get started

- **Honest friction** good; synthesis says section reads **doc-heavy** — should be motivation → two numbered steps → link for CA/sudo detail **(P0)**.
- **Anchor:** `#first-time-setup` is empty `div` inside “Getting started” — scroll works; **label expectation** mismatch for “First-time setup guide” CTA.

### Trust bar

- **Position:** Synthesis: trust should sit **before** install ask *(layout)*. **[4/5]**
- **Wording:** “CLI only” vs background **router service** — **P2:** prefer “CLI-driven” / “no runtime in your app bundle.” **[2/5]**

### Closing CTA

- “Whole team sees the work…” — strong line **buried**; synthesis: move concept **up** near problem/solution; closing CTA more **action-oriented**. **[4/5]**
- Still risks sounding like hosted magic unless share/tunnel/review-on-your-machine is clear (original note stands).

### Footer

- Functional; menulet link good.

---

## Page: `/features/isolation/`

### Hero

- **Synthesis add:** “Runs your hooks on every branch” **before** hooks are introduced — **remove from hero (P1).**
- Assumes “worktree” — still needs one-line definition or link.

### “The second worktree is always broken”

- **Strong** — **[/5]** keep.

### “One command changes everything”

- **Synthesis:** Terminal shows **extra port, Redis, master.key** as if universal — optional/config-dependent; note or simplify **(P2).**

### “What this makes possible”

- “Isolation is the contract” — slightly manifesto; OK.
- **Registry** still undefined for casual readers (original note).
- **Card order:** agent/sandbox card before general audience — **P2** reorder.
- **Onboarding card:** “run `gtl init` if the project uses…” — **wrong for repos with committed `.treeline.yml`**; should emphasize **`gtl setup`** for new joiners **(P1, Sonnet).**

### “Declare it once…”

- **Strong** — **[/5]** keep YAML walkthrough.

### Lifecycle hooks (prose)

- **Synthesis:** “Matching the CLI behavior **in the repo**” — contributor-facing; remove **(P2).**

### Multi-repo

- Strong; **add one line** “skip if single repo” **(P2).**

### Get started CTA

- “Non-users just ignore it” — **glib**; rephrase to “unaffected at build/deploy time” **(P2).**

---

## Page: `/features/networking/`

### Hero

- Headline strong for OAuth-literate readers **[/3]**.
- **Synthesis:** Strip **four command names** from hero body; defer below **(P1).**

### Problem section

- OAuth demo strong **[/5]**.
- **Mapbox list item** — Composer originally flagged; **retracted** per synthesis (see Audit update).

### “Port → HTTPS branch URL” demo

- Shorthand hostname — same `{project}-{branch}` fix as homepage.

### Four commands

- Aligned with README; **mkcert** needs one clause of context **(P2).**
- **Safari:** synthesis — state **consequence** (no `*.localhost` without hosts) not only `hosts sync` **(P2).**
- “Install once, forget it” — synthesis: avoid **“forget”**; use **per-machine** + link **(P2).**

### Get started

- “Most teams only need `gtl serve`” — add **high-level what `serve install` installs** **(P1).**

---

## Page: `/features/workflows/`

### Hero

- PR headline **praised [/5]**; body mixes PR + supervisor + TUI — **split (P1).**

### Manual path terminal

- **Effective [/5]** keep.

### “Moment it clicks”

- **Teardown line:** “`gtl release --drop-db` tears down the worktree…” — **same factual bug** as table row; must match README + code **(P1).**
- `myapp-pr-42.localhost` — **good** (project prefix present).

### “What this makes possible”

- Stakeholder/machine boundary still thin (original note).

### Supervisor

- Strong content; synthesis: heading **architecture-first** (“two interfaces, one process”) — prefer **benefit-first (P2).**
- **Unix socket** before benefit — reorder **(P2).**

### “Hooks, clone, open, wait”

- Heading is **noun pile** — rename e.g. “Automation hooks and shortcuts” **(P2).**

### TUI section

- **“Bubble Tea”** — implementation noise; drop for readers **(P2).** Dense keyboard block on mobile OK for reference **(synthesis).**

### Review commands table

- **`gtl release --drop-db`:** “Remove worktree…” — **false**; **P1** accuracy failure (original + synthesis).

---

## Page: `/features/agents/`

### Hero

- **MCP undefined + worktree management abstract** — synthesis **P0** (stricter than original “slightly hollow”).
- “Environment-aware” — **banned filler** per target voice in plan.

### Problem terminal

- Clear **[/5].**

### MCP / tool lists

- “Native tools under `gtl` server namespace” — protocol jargon; plain-language parallel needed **(P1).**

### “What this makes possible”

- Cards assume MCP/orchestration depth — bridging for Copilot-only readers **(P1).**

### “Four integration surfaces”

- **False equivalence** (AGENTS.md vs protocol) — rehead without forced “four” **(P2, Opus).**

### Agent context files

- “Reads on startup and **knows** which commands” — overclaims determinism **(P2, Sonnet).**

### Get started

- “Discovers everything automatically” — **too absolute (P1)** (original + synthesis).

---

## Page: `/use-cases/`

### Hero

- “Problems worth solving on purpose” — **opaque (P1)** per synthesis.
- Meta “not a second feature tour” — **self-referential (P1)**; show, don’t announce.

### Cards

- Lead with plain pain; **registry / allocation / resolve** before reader has glossary **(P1).**

---

## Page: `/use-cases/multi-repo/`

- Strong flow **[/4]**.
- **XHR** → “API calls” / fetch **(P2).**
- **Project name** — tie to `project:` in yaml **(P2).**
- `api-feature-auth.localhost` — still ambiguous vs teaching `{project}-{branch}` (original note).

---

## Page: `/use-cases/integrations-urls/`

- Table **[/5]** keep.
- Combine-more-than-one paragraph — **split paragraphs** for scan **(P2).**

---

## Page: `/use-cases/platform-pr/`

- Empathy lead strong **[/3].**
- Verify **no** sentence implies `release` deletes worktree dir **(P1)** — synthesis spot-check.

---

## Page: `/use-cases/agents-automation/`

- Headline joke **opaque (P1)** — synthesis.
- **Stub depth** — add demo or numbered lifecycle **(P2, Sonnet).**
- Bullets still strong vs README.

---

## Page: `/docs/` (`docs/index.html`)

### Layout / mobile

- Mobile nav may **omit** `#mcp-server`, **Route keys** (original grep-based note) — verify after next site build.

### Header

- Generic line — **P2.**

### Getting started

- **Open with purpose/quick-start** before long trust paragraph **(P1).**
- **Optional + required** in same breath for serve — **contradiction to resolve (P1).**
- `GTL_HEADLESS` / CI — should appear where “optional” is discussed (per README).

### Configuration full reference

- **`redis.strategy` misplaced** — **P1** verified.

### CLI reference

- Discoverability / drowning risk for newcomers (original).

### Framework integration

- Solid; **OG meta** leads with “Rails” — **P2** reorder per synthesis.

### Setup / DB / supervisor / TUI / editor

- **TUI `s` vs `r`** — workflows vs docs: **verify single source** (original).

### AI agents / MCP

- Windsurf mention — **verify** official support before listing **(open question in plan).**

### Networking sections

- Same hostname / combination-tool risks as feature page.

### Concept ordering

- **Phase 3** restructuring: progressive disclosure **(P2 flag).**

### Inline code density

- Paragraphs with many `<code>` spans — tables/lists **(P2).**

---

## Structural / mobile themes (site-wide)

1. **Hover-only nav** — touch/accessibility; design track.
2. **Jargon before category** — `resolve`, `link`, `registry`, `supervisor`, `MCP` early **[/5].**
3. **Hostname demos** — systematic `{project}-{branch}` gap **[/3].**
4. **Newbie path unclear** — Isolation vs Install vs Docs CTAs compete.
5. **Brochure cadence** at closing vs sharp voice in Getting started / isolation depth.
6. **Concept dependency chain** (from synthesis): worktree → collision problem → `.treeline.yml` → `gtl setup` → registry → `resolve`.

---

## Summary judgment (post-audit)

The site still has **standout** engineering-forward sections (problem/solution terminals, isolation YAML, integrations table, candid serve discussion in docs). **Five independent reviews** converge on the same homepage failures: **no plain-language product definition**, **undefined “worktree”**, **hero/subtitle misframing review vs run**, **advanced features on the homepage before the core loop**, and **invisible second install step**. **Verified code/readme mismatches** (`redis.strategy` doc placement, `release` vs filesystem, Windows schema) remain the highest trust risks.

Composer’s earlier **Mapbox** objection is **withdrawn** after synthesis review. **Layout-dependent** items (trust bar order, feature-card mobile `order`) belong in a parallel UX track per `copy-rewrite-plan.md`.

**Related:** `tmp/copy-rewrite-plan.md` (v2) — phased rewrite, voice guardrails, and rejected findings.

---

*Diagnostic only. Rewrites are intentionally out of scope for this document.*
