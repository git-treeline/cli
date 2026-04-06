# Git Treeline Website — Copy Rewrite Plan

**Synthesized from:** 5 independent agent reviews (Composer, Grok, Claude 4.6 Opus, Claude 4.6 Sonnet Medium Thinking, GPT 5.3 Codex). The prompt specified six; only five `*-copy-review.md` files were found in `tmp/`. If a sixth exists elsewhere, it should be incorporated and consensus scores adjusted.
**Source of truth:** CLI README at `git-treeline/README.md`
**Reviewed by:** 4 agents (Grok, Codex, Sonnet, Opus) provided feedback on v1 of this plan. This is v2, revised to address their critiques.
**Date:** 2026-04-06

---

## 0. Prerequisites — Answer Before Writing

These questions were deferred in v1. Reviewers correctly identified them as **blockers**, not appendix items. The rewrite cannot start until each has an answer.

### Q1: Target audience — worktree beginners or existing users?

**Recommendation: worktree beginners.** The README's "Why" section opens with "Git worktrees let you check out multiple branches side by side. That's always been possible." — it teaches before it sells. The website should do the same. AI coding agents are driving adoption among developers who have never manually run `git worktree add`; they encounter worktrees as a side-effect of agent tooling. If the answer is "existing worktree users," the worktree-definition directives below should be softened to tooltips. **This plan proceeds assuming beginners.**

### Q2: AI agents — top-level pillar or secondary feature?

**Answer (from owner): Pillar. Agents are the cause of the journey and along for it.**

The three-part story:
1. **Agents create the demand.** You're running 3 agents across 3 repos, 3 branches each — that's 9 simultaneous dev environments. Agent-based coding is the scale driver.
2. **GTL solves the complexity that scale creates.** Port isolation, DB cloning, env allocation. The declarative config (`.treeline.yml`) + git post-checkout hook does the heavy lifting.
3. **MCP closes the loop.** Agents stay aware of allocations, can start/stop/restart, check status — no guessing.

The agent page stays as a nav pillar. The copy should earn that billing by making the page intelligible to someone who's used Copilot but never configured MCP. The framing is not "agents are the tool" but "agents are why you need 9 worktrees, and here's how they stay informed."

**MCP is editor-agnostic.** Any MCP-compatible editor works. The Go code provides setup examples for Cursor and Claude Code, but the protocol is not editor-specific. The site should say "works with any MCP-compatible editor" and show setup examples for the two documented editors.

**Follow-up (not website copy):** For cloud/CI agents that can't install `gtl`, the declarative `.treeline.yml` and `--json` flags provide the same information. This needs to be defined for agent consumption more than user comprehension. File as a CLI agent task.

### Q3: Windows support

**Answer (from truth pass + owner):** `gtl serve install` explicitly blocks on Windows (`cmd/serve.go:76`). The CLI itself runs on Windows, but `serve` (HTTPS, subdomain routing) does not. Windows users fall back to `localhost:port` instead of `{project}-{branch}.localhost`. WSL2 support is planned per the CLI error hint.

**Action:** Remove "Windows" from JSON-LD `operatingSystem`. Where platform is mentioned, say "macOS and Linux" for the full experience. Note that the CLI installs on Windows but local HTTPS routing requires macOS or Linux (WSL2 planned).

### Q4: Truth pass results (verified in codebase)

- [x] `gtl serve alias` — **Real command.** `cmd/serve.go:293`. Manages static alias routes in user config. Networking page copy is correct; README CLI table should add it.
- [x] `redis.strategy` — **User config only.** Parsed from `config.json` (`internal/config/user.go`). Not in project config known keys (`project.go:526`). Docs page listing it under `.treeline.yml` is **wrong** — fix it.
- [x] MCP editors — **MCP is editor-agnostic** (protocol, not editor-specific). Go code has setup examples for Cursor and Claude Code. Site should say "any MCP-compatible editor" + show those two examples.
- [x] `gtl release` — **Does NOT remove the worktree directory** (confirmed). `registry.go:141` only filters `registry.json`. Batch mode even prints `"(worktree dir still exists)"`. A `--remove` flag is being added to the CLI; write copy for shipped behavior now, note the flag when it lands.
- [x] Windows — **`serve install` explicitly blocked** on non-macOS/non-Linux (`cmd/serve.go:76`). Hint says "Windows support via WSL2 is planned." Windows users fall back to `localhost:port`.

---

## 0b. Priority Definitions

| Tier | Meaning | Sequencing |
|---|---|---|
| **P0** | Blocks comprehension. A first-time visitor cannot understand what the tool does or is actively misled. | Must ship in Phase 1. |
| **P1** | Creates confusion or erodes trust. Friction around setup, core value, or accuracy. | Ship in Phase 1 or Phase 2. |
| **P2** | Suboptimal but functional. Tone, scannability, polish, edge-case accuracy. | Phase 3 or backlog. |

**Note on consensus scores:** The `[N/5]` strength scores indicate how many agents independently flagged the issue. They are a signal of how obvious the problem is, not a measure of importance. Factual errors are P1 minimum regardless of how many agents noticed them.

---

## 0c. Target Voice

Write as a senior engineer explaining the tool to a colleague over Slack — direct, specific, zero filler. Not a marketer writing a landing page. Not dry reference docs either.

Characteristics:
- **Concrete over abstract.** "Each worktree gets its own port" over "seamless isolation."
- **Mechanism over promise.** "A background router maps `{project}-{branch}.localhost` to the right port" over "URLs just work."
- **Honest about friction.** "`gtl serve install` requires sudo twice — once for the CA cert, once for port forwarding" over "quick one-time setup."
- **No words that communicate nothing.** Ban list: seamless, powerful, next-level, first-class, environment-aware, full story, just works (without showing why), automatically (without naming what does the automation).

---

## 0d. Homepage Narrative Arc

The homepage's individual sections are mostly good; the problem is their *sequence*. A first-time visitor should accumulate understanding in this order:

1. **What is this?** (Hero) — One sentence: what the tool is and the job it does. Not the brand name alone, not a workflow-specific tagline.
2. **Why does it exist?** (Problem/Solution) — The port collision / DB conflict problem. Define "worktree" here. The "without/with" terminals.
3. **What changes when you use it?** (Feature cards) — Four pillars, each one sentence of problem + one sentence of mechanism. No advanced concepts. No command-name dumps.
4. **Can I trust it?** (Trust bar) — Before being asked to install. "No runtime dependency in your app. Config file, not a library. Opt-in per developer."
5. **How do I start?** (Get Started) — Two steps, clearly numbered: install CLI, run one-time setup. Motivation first, details in the linked guide.
6. **What's the bigger picture?** (Closing CTA) — The team-level outcome. "Your whole team sees the work, not just the diff."

The current site jumbles steps 4 and 5 (trust bar after CTA) and fails step 1 entirely (hero communicates nothing). The feature cards (step 3) front-load advanced concepts that belong in step 6 or on subpages.

---

## 0e. Implementation Phasing

### Phase 1: Homepage + truth fixes (highest leverage)
- All homepage directives marked P0 and P1
- All factual fixes site-wide (`gtl release` wording, URL pattern, JSON-LD platform)
- Truth pass verification (§0.Q4)
- Estimated scope: 1 page rewrite + find-and-replace sweep

### Phase 2: Feature pages
- `/features/isolation/`, `/features/networking/`, `/features/workflows/`, `/features/agents/`
- Apply revised voice, concept ordering, mobile DOM fixes
- Estimated scope: 4 page revisions

### Phase 3: Use cases + docs consistency
- `/use-cases/*` pages — terminology alignment, thin-page expansion
- `/docs/` — flagged issues only (redis.strategy, getting-started opening, serve-install contradiction)
- Estimated scope: 5 page revisions + targeted docs edits

### Separation: Copy Track vs. Layout/UX Track

Some directives require CSS or structural HTML changes (mobile DOM reorder, trust bar repositioning). These should be filed as a parallel Layout/UX track so copy work doesn't stall on engineering dependencies. Directives that cross tracks are marked with *(Layout)* below.

---

## 1. Diagnosis Summary

Five problems account for the majority of first-visit comprehension failures across the site.

**A. The site never says what the tool is in plain language [5/5].** No page contains a single sentence like "Git Treeline is a CLI that manages isolated development environments for git worktrees so you can run multiple branches of the same app simultaneously without port collisions, database conflicts, or shared config." The homepage H1 is the brand name; the subtitle ("Review every branch at the same time") reads as code-review tooling, not environment isolation. The closest attempt — the problem/solution subheading — is below the fold. Affects: homepage hero, every page's opening copy.

**B. "Worktree" is treated as common vocabulary — it is not [5/5].** The entire product pitch depends on the reader understanding git worktrees. The word appears in the first sentence of nearly every page without ever being defined. The README does this correctly: "Git worktrees let you check out multiple branches side by side." The website never provides that sentence. Affects: every page.

**C. Advanced concepts surface before the baseline is established [5/5].** `resolve`, `link`, `registry`, `supervisor`, `MCP`, `allocation`, and `route key` all appear on the homepage and feature page heroes before the reader understands the simple core loop (install → init → new → run). The isolation card on the homepage jumps to `{resolve:other-project}` — a multi-repo cross-project feature — before explaining single-repo port isolation. Affects: homepage feature cards, all feature page heroes, use-case hub cards.

**D. The two-step install is invisible until mid-page [5/5].** `gtl serve install` is required for primary workflows (`gtl new`, `gtl review`, `gtl setup` on macOS/Linux). It requires `sudo` twice and installs a background service, a CA certificate, and port forwarding rules. The homepage hero shows only `brew install`; the `serve install` requirement appears for the first time in the Get Started section, which reads like documentation (sudo warnings, CA trust details) rather than conversion copy. Affects: homepage hero, homepage Get Started, all feature page CTAs.

**E. Terminal demos precede their explanations, especially on mobile [5/5].** Every feature page follows hero → demo → explanation. On desktop the side-by-side layout mitigates this. On mobile, demos stack above explanations: users see `gtl new feature-auth` output before understanding what port allocation is or why it matters. Affects: homepage feature cards, all feature pages.

---

## 2. Rewrite Plan, Page by Page

### Agent labels
- **Composer** = Agent 1
- **Grok** = Agent 2
- **Opus** = Agent 3
- **Sonnet** = Agent 4
- **Codex** = Agent 5

---

### `index.html` (Homepage)

#### Meta / SEO

| Element | Directive | Strength | Priority |
|---|---|---|---|
| `<title>` | **Revise.** Replace "Review every branch at the same time" with a title that says what the tool is and names the category (worktree environment manager, parallel dev environments, or similar). The title must be intelligible to someone who has never heard of git worktrees. | 4/5 | P0 |
| `<meta name="description">` | **Revise.** Current copy is a feature dump ("allocates ports, databases, and env per worktree; resolves cross-repo URLs…"). Rewrite as a one-sentence pitch followed by a concrete outcome. Do not lead with product-internal nouns (`resolve`, `link`). | 3/5 | P1 |
| OG title/description | **Keep as-is.** The OG tags ("Isolated dev environments for every git worktree") are actually better than the page title. Consider promoting the OG title to the `<title>` tag. | — | — |
| JSON-LD `operatingSystem` | **Revise (factual fix).** Currently lists "macOS, Linux, Windows". README is explicit that `gtl serve` is not supported on native Windows (WSL only). Either remove Windows or add a qualifier. **Acceptance:** JSON-LD `operatingSystem` matches README platform support. | 1/5 | P1 |
| JSON-LD `description` | **Revise.** Same problem as meta description — feature dump with internal vocabulary. Rewrite to match the revised meta description. | 1/5 | P2 |

#### Hero (`#hero`)

| Element | Directive | Strength | Priority |
|---|---|---|---|
| H1 (`git-treeline`) | **Revise.** The H1 is the brand name alone. Add a descriptor line below it (e.g., "Worktree environment manager") so the reader knows the category before reading further. The brand name can remain as the primary visual element. **Acceptance:** A first-time reader can state what category of tool this is after reading only the H1 and its descriptor. | 5/5 | P1 |
| Subtitle ("Review every branch at the same time") | **Revise. This is the single highest-leverage change on the site.** "Review" suggests code-review tooling. The core value is *running* branches in parallel. The subtitle should describe the outcome (run multiple branches locally without port or database conflicts), not one specific workflow. **Acceptance:** The subtitle does not mislead a reader into thinking this is a diff/review tool. | 5/5 | P0 |
| Body copy (resolve/link sentence) | **Revise.** Remove `resolve` and `link` command names from the hero body. These are advanced multi-repo features. Replace with one sentence establishing the core mechanism (allocates ports, databases, and env per worktree) and one naming the outcome (run N branches simultaneously). Cross-repo can be mentioned in feature cards or subpages. **Acceptance:** No internal command names appear in the hero body text that haven't been explained. | 5/5 | P1 |
| Install command | **Revise.** Add a brief note or footnote near the install command indicating that a one-time setup step (`gtl serve install`) follows. Do not bury this in the Get Started section below. **Acceptance:** A reader who copies the brew command knows there is a second step before they can use `gtl new`. | 5/5 | P1 |

#### Problem / Solution (`#problem-solution`)

| Element | Directive | Strength | Priority |
|---|---|---|---|
| Heading ("Worktrees are easy to create. Hard to run.") | **Keep as-is.** All 5 agents identify this as the strongest line on the homepage. | 5/5 | — |
| Subtext | **Revise.** "Git worktrees give you a second workspace in seconds" is the site's one attempt to explain worktrees but it's vague. Add one plain-language sentence defining what a git worktree actually is ("a separate checkout of your repo in its own directory") before describing the problem. **Acceptance:** A reader who has never used `git worktree add` understands the concept after reading this paragraph. | 5/5 | P0 |
| "Without" terminal | **Keep as-is.** The EADDRINUSE / database-already-exists demos are concrete and effective. All agents agree. | 5/5 | — |
| "With" terminal — URL line | **Revise.** The output shows `https://feature-auth.localhost`. README consistently uses `{project}-{branch}.localhost`. Change to `https://myapp-feature-auth.localhost` to match actual behavior. **Acceptance:** Demo URL matches the `{project}-{branch}` pattern documented in the README. | 3/5 | P1 |
| "With" terminal — footnote "named URL via gtl serve" | **Revise.** Add "(one-time setup)" or similar qualifier so the reader knows this URL doesn't appear for free after `brew install`. **Acceptance:** Footnote acknowledges that serve requires setup. | 3/5 | P2 |

#### Feature Cards (`#features`)

| Card | Directive | Strength | Priority |
|---|---|---|---|
| **Isolation** — heading | **Keep as-is.** "Two branches. Two apps. Zero conflicts." is strong. | 5/5 | — |
| **Isolation** — body | **Revise.** Remove `{resolve:other-project}` and `gtl link` from this card entirely. The isolation card should explain single-repo isolation only: port, database, env file. Cross-repo is a separate feature and belongs in the multi-repo section or a later card. **Acceptance:** The isolation card body contains zero references to multi-repo features. | 5/5 | P0 |
| **Networking** — slot-reel headline | **Revise.** The animated "OAuth redirects / Stripe webhooks / …" promises these things "just work" without explaining the mechanism. Replace with a headline that names the problem (integrations break when ports change) rather than listing integrations by name. **Acceptance:** Headline communicates the problem, not a list of integrations. | 4/5 | P1 |
| **Networking** — body | **Revise.** Current body packs four networking modes into one sentence. Reduce to one sentence naming the core benefit (stable URLs per branch) and one sentence pointing to the networking page for the full comparison. Do not list all four commands here. **Acceptance:** Body contains at most one command name. | 5/5 | P1 |
| **Workflows** — heading | **Keep as-is.** "Type a PR number. Get a running app." is the best headline on the site per 3/5 agents. | 5/5 | — |
| **Workflows** — body | **Revise.** Remove "the full supervisor story" phrasing — it treats the supervisor as an established concept. Replace with a concrete description of what the supervisor does (controls start/stop/restart so agents don't take over your terminal). **Acceptance:** The word "story" does not appear; the supervisor is described by function. | 5/5 | P1 |
| **AI Agents** — heading | **Revise.** "Your agent gets the same tools you do" is meaningless without context. Replace with a heading that names the problem: agents guess ports and break. The failure mode is more compelling than the fix. **Acceptance:** Heading is intelligible to someone who has never configured MCP. | 4/5 | P1 |
| **AI Agents** — body | **Revise.** Remove "MCP exposes start, stop, port, and status" — MCP hasn't been defined. Replace with one sentence explaining what MCP is (a protocol that lets AI coding agents call tools directly) and one sentence naming what agents can do (query ports, control servers, inspect config). **Acceptance:** MCP is defined before it is referenced. | 5/5 | P1 |
| **All cards** — mobile DOM order | **Revise.** *(Layout)* On mobile, ensure the explanatory copy appears before or alongside the terminal demo, not after it. Use CSS `order` on flex children for small screens rather than changing DOM order (to preserve desktop layout). **Acceptance:** On a 375px viewport, no terminal demo appears before its explanatory heading and first paragraph. | 5/5 | P1 |

#### Get Started (`#get-started`)

| Element | Directive | Strength | Priority |
|---|---|---|---|
| Body copy | **Revise.** Current copy reads like documentation (sudo warnings, CA trust, port 443 forwarding details). Rewrite as motivation → action → reassurance. Lead with what the reader will get (named HTTPS URLs for every branch). State the two steps clearly (1. install CLI, 2. run one-time setup). Move sudo/CA details to the linked docs guide. Be honest about what `serve install` does ("installs a local CA and port forwarding — requires sudo twice") without burying the reader in implementation. **Acceptance:** The Get Started section contains no more than 2 sentences of implementation detail; the rest is motivation and a clear CTA. | 5/5 | P0 |

#### Trust Bar

| Element | Directive | Strength | Priority |
|---|---|---|---|
| Position | **Restructure.** *(Layout)* Move the trust bar above the Get Started section, not below it. Trust signals should appear before asking the user to install, not after. **Acceptance:** Trust bar appears in the DOM before the Get Started section. | 4/5 | P1 |
| "CLI only" claim | **Revise.** "CLI only" understates the local HTTPS stack surface area (`gtl serve install` installs a background service, CA cert, and port forwarding). Rephrase to "CLI-driven" or "No runtime dependency in your app" to remain accurate. **Acceptance:** Trust claim does not contradict the existence of a background service. | 2/5 | P2 |

#### Closing CTA (`#cta`)

| Element | Directive | Strength | Priority |
|---|---|---|---|
| Subtext ("Your whole team sees the work, not just the diff") | **Restructure.** Multiple agents identify this as the strongest value-prop sentence on the homepage, but it's buried at the bottom. Promote this line (or a variant) to the **problem/solution section** — it works as the team-level consequence of solving port collisions, not as a first-sentence pitch. The closing CTA subtext should be replaced with something action-oriented tied to the next step (docs, install). **Acceptance:** The "sees the work, not just the diff" concept appears in or adjacent to the problem/solution section. | 4/5 | P1 |

---

### `/features/isolation/`

| Section | Directive | Strength | Priority |
|---|---|---|---|
| Hero body | **Revise.** "Runs your hooks on every branch" appears before hooks are introduced. Remove hooks from the hero; they belong in the config walkthrough section below. Add a one-line worktree definition or link to one. **Acceptance:** Hero body does not reference hooks. | 4/5 | P1 |
| "The second worktree is always broken" | **Keep as-is.** All agents agree this is the best problem-statement copy on the site. | 5/5 | — |
| "One command changes everything" — terminal output | **Revise.** The output shows Extra port, Redis namespace, and Copied config/master.key. These are config-dependent/optional features shown as if universal. Add a brief note ("output depends on your .treeline.yml") or simplify the demo to port + database + env only. **Acceptance:** Demo output does not present optional features as default behavior. | 3/5 | P2 |
| "What this makes possible" — card ordering | **Revise.** "Agents and automation get a real sandbox" is card #2 of 3. For a first-time reader not using AI agents, this dilutes the primary message. Move it to #3 or restructure so the agent card comes last. **Acceptance:** Agent/automation card is not positioned before the general-audience cards. | 2/5 | P2 |
| "What this makes possible" — "Onboarding" card | **Revise.** "New developers clone, run `gtl init` if the project uses Git Treeline, then `gtl setup`" is subtly wrong. If `.treeline.yml` is already committed, new devs run `gtl setup`, not `gtl init`. Correct the sequence. **Acceptance:** Onboarding card matches README's flow for repos with existing config. | 1/5 (Sonnet) | P1 |
| "Declare it once" config walkthrough | **Keep as-is.** All agents agree this is one of the strongest sections on the site. The YAML + annotation format is clear. | 5/5 | — |
| Lifecycle hooks subsection | **Revise.** "Matching the CLI behavior in the repo" is self-referential contributor language. Remove that clause. **Acceptance:** No copy references "the repo" as if the reader has the source code open. | 1/5 (Sonnet) | P2 |
| Multi-repo section | **Keep as-is with minor revision.** The `resolve`/`link` explanation is strong. Add a one-line qualifier at the top: "This section applies if your frontend and API are in separate repos. If you have one repo, skip ahead." **Acceptance:** A single-repo reader knows this section is optional. | 2/5 | P2 |
| CTA ("Non-users just ignore it") | **Revise.** "Non-users just ignore it" is glib. `.treeline.yml` exists in the repo; teammates will see it. Rephrase to describe what actually happens: "Teammates without gtl installed are unaffected — the config file has no effect on builds or deployments." **Acceptance:** CTA does not dismiss teammate concerns with "just ignore it." | 2/5 | P2 |

---

### `/features/networking/`

| Section | Directive | Strength | Priority |
|---|---|---|---|
| Hero headline | **Keep as-is.** "One redirect URI per branch, not per port" is identified as one of the strongest headlines by 2/5 agents. It does assume OAuth knowledge, but this is an acceptable narrowing for a dedicated networking page. | 3/5 | — |
| Hero body | **Revise.** Lists all four commands in one sentence. Reduce to: name the problem (integrations break when ports change), name the solution category (four networking commands with different trust boundaries), and defer the command names to the sections below. **Acceptance:** Hero body contains zero command names. | 4/5 | P1 |
| Problem section ("Every integration breaks") | **Keep as-is.** Terminal demo is concrete. Footnote listing integrations is fine. | 5/5 | — |
| "The URL follows the branch" — pipeline demo URL | **Revise.** Change `feature-auth.localhost` to `myapp-feature-auth.localhost` to match README's `{project}-{branch}` pattern. Same fix as homepage. **Acceptance:** Pipeline URL matches documented route key format. | 3/5 | P1 |
| "What this makes possible" — "Hand someone a link" card | **Revise.** Packs `gtl tunnel`, `gtl share`, and `--tailscale` into one paragraph — three features with different trust models. Split into two sentences: one for stable public routes (tunnel), one for disposable links (share). **Acceptance:** Tunnel and share are described in separate sentences with distinct use cases named. | 2/5 | P2 |
| "Four commands, one job" section | **Keep table position.** v1 proposed moving the comparison table above the per-command details. On review, this is circular: the table references four command names the reader hasn't seen yet. The table works as a summary *after* the commands are introduced. **Instead:** add a brief intro sentence at the top of the section framing the four commands as different trust boundaries for the same goal (stable URLs). | 2/5 (Opus) | P2 |
| `gtl serve` subsection — Safari note | **Revise.** The Safari gotcha is buried mid-paragraph without explaining the consequence. Add one sentence: "Safari on macOS does not resolve `*.localhost` without a hosts entry; run `gtl serve hosts sync` to fix this." **Acceptance:** Safari note states the consequence, not just the fix. | 2/5 | P2 |
| `gtl serve` subsection — "install once, forget it" | **Revise.** This softens the operational impact. Replace with "install once per machine" and link to the docs for what gets installed. **Acceptance:** Copy does not use the word "forget." | 3/5 | P2 |
| `gtl proxy` subsection — "mkcert" | **Revise.** "Optional TLS via mkcert" drops a tool name without context. Replace with "Optional TLS (requires a local cert tool like mkcert)." **Acceptance:** mkcert is described, not just named. | 2/5 | P2 |
| CTA ("Most teams only need gtl serve") | **Revise.** Add one sentence noting that `gtl serve install` installs system-level components (CA, service, port forwarding) so the reader knows what they're about to do. **Acceptance:** CTA mentions what serve install does at a high level. | 3/5 | P1 |

---

### `/features/workflows/`

| Section | Directive | Strength | Priority |
|---|---|---|---|
| Hero headline | **Keep as-is.** "Type a PR number. Get a running app." — universally praised. | 5/5 | — |
| Hero body | **Revise.** Mixes PR review, supervisor, and TUI dashboard in one paragraph. These are three separate concepts. Keep the hero body to one sentence about the PR review workflow. Move supervisor and dashboard to their respective sections below. **Acceptance:** Hero body describes only one workflow. | 3/5 | P1 |
| "10 minutes of setup" terminal demo | **Keep as-is.** The 8-command manual flow is the most effective piece of pain-point copy on the site. | 5/5 | — |
| "One command replaces all of that" — teardown line | **Revise (factual fix).** "gtl release --drop-db tears down the worktree, drops the database, and removes the env file." Per README, `gtl release` frees the allocation (port, database registration, env file). It does **not** remove the worktree directory — that requires `git worktree remove`. Change to: "frees the allocated port, drops the database, and cleans up the env file. Remove the worktree directory separately with `git worktree remove`." **Acceptance:** Copy does not claim `gtl release` removes the worktree directory. | 3/5 | P1 |
| "What this makes possible" — "Agents and CI" card | **Revise.** "Over the Unix socket" is an implementation detail that means nothing to readers who don't know process supervision. Lead with the benefit ("no PID files or signal guessing"), make the mechanism a parenthetical. **Acceptance:** Benefit comes before mechanism. | 2/5 | P2 |
| Supervisor heading | **Revise.** "The supervisor: two interfaces, one process" describes architecture, not benefit. Replace with a heading that names what the supervisor gives the user: start/stop/restart without PID files. Avoid anything cute. **Acceptance:** Heading names a user benefit, not an architecture pattern. | 2/5 (Opus) | P2 |
| "Hooks, clone, open, wait" heading | **Revise.** This heading is a list of nouns with no verb framing. Replace with something descriptive: "Automation hooks and shortcuts." **Acceptance:** Heading is a phrase, not a comma-separated list. | 2/5 | P2 |
| TUI dashboard section | **Revise.** Remove "Bubble Tea TUI" — naming the Go UI library communicates nothing to the reader. Replace with "full-screen terminal dashboard." Also: the dashboard is described entirely in text with no visual. Add a screenshot or ASCII mockup if one exists. **Acceptance:** The implementation library name does not appear; the dashboard has a visual or the absence is noted with a TODO. | 2/5 | P2 |
| Review commands — `gtl release --drop-db` row | **Revise (factual fix).** Description says "Remove worktree, drop the cloned database, clean up env files." This is wrong per README. Change to "Free allocated resources, drop the cloned database, clean up env files." **Acceptance:** The word "Remove worktree" does not appear in this row. | 3/5 | P1 |
| CTA footnote | **Revise.** "Requires `gh` CLI for PR fetching" appears only in small-print at the bottom. Add this prerequisite to the hero body or the first mention of `gtl review` on the page. **Acceptance:** `gh` CLI requirement appears before or alongside the first `gtl review` mention. | 3/5 | P1 |

---

### `/features/agents/`

| Section | Directive | Strength | Priority |
|---|---|---|---|
| Hero headline | **Revise.** "Your agent gets the same tools you do" is meaningless without context. Replace with a headline that names the failure mode: agents guess ports and break, or hardcode values that change per branch. **Acceptance:** Headline is intelligible without knowing what the tool's "tools" are. | 4/5 | P1 |
| Hero body | **Revise.** "A built-in MCP server gives agents native access to worktree management" uses two undefined terms (MCP, worktree management). Add a one-line definition of MCP: "MCP (Model Context Protocol) lets AI coding agents call tools directly instead of running shell commands." Replace "worktree management" with specific capabilities. Remove "environment-aware" — it's a filler adjective. **Acceptance:** MCP is defined in the hero. "Environment-aware" does not appear. | 5/5 | P0 |
| "Agents don't know about your environment" demo | **Keep as-is.** All agents agree this is one of the clearest problem demos on the site. | 5/5 | — |
| "Three lines in your editor config" body | **Revise.** "Native tools under the `gtl` server namespace" is MCP protocol language, not beginner language. Replace with a plain description: "The agent can call start, stop, port, and status as structured tool calls — no shell commands, no output parsing." **Acceptance:** No MCP protocol terminology appears without a plain-language equivalent. | 3/5 | P1 |
| "What this makes possible" — all three cards | **Revise.** All three cards assume deep familiarity with MCP, orchestration, and multi-agent patterns. Add a bridging sentence at the start of the section for readers who are evaluating AI agent integration, not already implementing it. Rewrite card headlines to name benefits rather than architecture ("Agents query real ports" over "IDE-integrated agents control the stack"). **Acceptance:** A developer who has used Copilot but not MCP can understand all three cards. | 3/5 | P1 |
| "Four integration surfaces" heading | **Revise.** The heading says "four" but one surface (AGENTS.md) is a generated markdown file — a different category than the other three (protocol, shell interface, hooks). Change heading to "How agents connect" or "Integration surfaces" without the count. **Acceptance:** Heading does not promise a count that creates a false equivalence. | 1/5 (Opus) | P2 |
| Agent context files — "knows which commands to run" | **Revise.** "The agent reads it on startup and knows which commands to run" overstates what AGENTS.md does. Agents read it as context; whether they follow it depends on the model. Rephrase to "provides agents with instructions for setup, teardown, and server management." **Acceptance:** Copy does not claim AGENTS.md gives agents deterministic knowledge. | 1/5 (Sonnet) | P2 |
| CTA ("discovers everything else automatically") | **Revise.** "The agent discovers everything else automatically" is the vaguest CTA on the site. Replace with a specific statement: "With MCP configured, agents can query ports, control the dev server, and inspect allocations — no shell parsing required." **Acceptance:** CTA names at least two concrete capabilities. | 3/5 | P1 |

---

### `/use-cases/` (Hub)

| Section | Directive | Strength | Priority |
|---|---|---|---|
| Hero headline | **Revise.** "Problems worth solving on purpose" is opaque. Replace with a headline that describes what this page is: scenario guides organized by the problem you're facing. **Acceptance:** A reader knows this is a use-case index after reading only the headline. | 4/5 | P1 |
| Intro paragraph | **Revise.** "These are scenario guides—not a second feature tour" is self-referential meta-copy. The page should just *be* scenario guides without announcing what it isn't. Remove the self-description; keep the cross-links to feature pages. **Acceptance:** Intro paragraph does not describe itself. | 3/5 | P1 |
| Card descriptions | **Revise.** Card descriptions use internal vocabulary ("allocations," "registry," `{resolve:…}`, "branch intent"). Rewrite each card description to lead with the pain point in plain language, then name the solution briefly. **Acceptance:** No card description uses a term that hasn't been defined on a page the reader has likely already visited (homepage or a feature page). | 3/5 | P1 |

---

### `/use-cases/multi-repo/`

| Section | Directive | Strength | Priority |
|---|---|---|---|
| Lead paragraph — "XHR" | **Revise.** "The browser needs a base URL for XHR" — XHR is a dated term. Replace with "API calls" or "fetch requests." **Acceptance:** "XHR" does not appear. | 1/5 (Sonnet) | P2 |
| Lead paragraph — "project name" | **Revise.** "Each gets its own Git Treeline project name" introduces "project name" as assumed vocabulary. Add a brief parenthetical: "(the `project:` field in `.treeline.yml`)." **Acceptance:** "Project name" is explained inline. | 2/5 | P2 |
| Overall | **Keep as-is** with the above minor fixes. The page is well-structured: problem → what breaks → what treeline does → terminal demo → related reading. | 4/5 | — |

---

### `/use-cases/integrations-urls/`

| Section | Directive | Strength | Priority |
|---|---|---|---|
| Decision table | **Keep as-is.** All agents agree this is one of the most useful elements on the site. | 5/5 | — |
| "When you combine more than one" | **Revise.** One paragraph packs multiple command combinations + framework host allowlist caveats. Split into separate short paragraphs: one for serve + proxy combinations, one for tunnel for webhooks, one for share for demos. The framework allowlist note (Rails, Vite, Django) should be its own sentence, not buried at the end. **Acceptance:** Each combination is its own paragraph; framework allowlist note is visually distinct. | 2/5 | P2 |
| Headline | **Keep as-is.** Accurate if long. | — | — |

---

### `/use-cases/platform-pr/`

| Section | Directive | Strength | Priority |
|---|---|---|---|
| Lead paragraph | **Keep as-is.** "Product and QA should not need your README, a staging deploy, or a screen share of your laptop" is the strongest customer-empathy sentence on the site. | 3/5 | — |
| Step 4 — release language | **Revise.** "`gtl release` frees the allocation" — make sure this does not imply worktree directory removal. Current wording ("frees the allocation; add `--drop-db` when the cloned database should go too") is acceptable. Verify no other sentence claims directory removal. **Acceptance:** No sentence on this page claims `gtl release` removes the worktree directory. | 2/5 | P1 |
| Overall | **Keep as-is.** Cleanest use-case page per multiple agents. | 4/5 | — |

---

### `/use-cases/agents-automation/`

| Section | Directive | Strength | Priority |
|---|---|---|---|
| Headline | **Revise.** "Scripts and agents do not read your terminal theme" is a joke that communicates nothing about the page's content. Replace with a headline that names the problem: machines need structured data, not human-readable output. **Acceptance:** Headline is intelligible without getting the joke. | 2/5 | P1 |
| Page depth | **Revise.** This page is a stub: one paragraph, one bullet list, one paragraph, and links. It is notably thinner than the other use-case pages. Add a terminal demo showing the failure mode (agent parses `gtl status` text output and gets wrong results) vs. the structured path (`gtl status --json`). Or add a short walkthrough of an agent's lifecycle (setup → query port → start → await → work → release). **Acceptance:** Page contains at least one terminal demo or numbered walkthrough. | 1/5 (Sonnet) | P2 |

---

### `/docs/index.html`

v1 under-covered docs by self-imposing a constraint the prompt didn't set. This section is expanded. The docs page is ~1700 lines with real copy problems that compound the marketing page issues. Full section-by-section treatment is deferred to Phase 3, but the following issues are actionable now.

| Section | Directive | Strength | Priority |
|---|---|---|---|
| **Opening paragraph** (`#getting-started`) | **Revise.** The page opens with a trust statement ("Git Treeline ships as a single CLI binary — nothing from Treeline runs inside your app's process or bundle") rather than telling the reader how to use the tool. Docs should open with purpose and quick-start; move the trust claim to a "Philosophy" or "How it works" section, or keep it as a second paragraph. **Acceptance:** First paragraph tells the reader what they'll learn on this page. | 2/5 | P1 |
| **`gtl serve install` description** | **Revise.** "Optional in principle but required for those commands" is a contradiction. State plainly: required for local HTTPS workflows on macOS/Linux. Skippable in CI/headless with `GTL_HEADLESS=1`. List what it installs (CA cert, port forwarding, background service) so the reader can make an informed decision. **Acceptance:** No sentence contains both "optional" and "required." | 3/5 | P1 |
| **`redis.strategy` placement** (`#configuration` full reference, line ~361) | **Revise.** `redis.strategy` appears under the `.treeline.yml` field list. README documents Redis config under **user** `config.json`, not project config. Either move it to the user config reference section only, or clarify that it can appear in both (and update the README if so). **Verify in codebase.** **Acceptance:** `redis.strategy` location matches README or README is updated to match. | 1/5 | P1 |
| **OG meta description** | **Revise.** "CLI reference, configuration guide, Rails integration, and AI agent setup" leads with "Rails integration," suggesting the tool is Rails-specific. Reorder to lead with general usage: "CLI reference, configuration guide, AI agent setup, and framework-specific examples." **Acceptance:** OG description does not lead with a single framework. | 1/5 | P2 |
| **`#rails-integration` anchor** | **Flag.** Confirm the anchor ID matches internal and external links. If the section is renamed (e.g., to "Framework setup"), update all references. | 1/5 | P2 |
| **Concept ordering** | **Revise.** The docs follow the same pattern as the marketing pages: advanced concepts (lifecycle hooks, resolve/link, MCP) before the baseline (install → init → setup → new). The getting-started section should present the core loop first, then progressively layer in advanced features. This is a Phase 3 restructure, but flag it now so the writer knows the section sequence will change. | 3/5 | P2 |
| **Code density** | **Flag for Phase 3.** Some paragraphs contain 6+ inline `<code>` spans. When a paragraph has more than 3, consider whether the information should be in a table, bulleted list, or terminal demo. Not urgent, but improves scannability. | 3/5 | P2 |

---

### Global elements (nav, footer, redirect stubs)

Not covered page-by-page because they repeat across all pages. Flagged items:

| Element | Directive | Priority |
|---|---|---|
| Nav "Install" button | Currently points to `#get-started` on the homepage. After the Get Started section is revised, confirm the anchor still resolves and the landing spot matches the reader's expectation ("install" → they see install instructions, not a pitch). | P1 |
| Footer | No copy issues identified by any reviewer. **Keep as-is.** | — |
| `/features/networking/index.html` redirect stub | If a standalone redirect page exists at `/networking/`, confirm it 301s to `/features/networking/`. Not a copy issue but verify it doesn't show stale or duplicate content. | P2 |

---

## 3. Site-Wide Directives

These apply across all pages and should be treated as standing rules for future edits.

### Define worktree strongly once; link back from subpages [5/5]
The homepage problem/solution section must include the canonical definition: "A git worktree is a separate checkout of your repo in its own directory, created with `git worktree add`." The docs getting-started section should repeat it. Feature and use-case pages should not repeat the full definition — by the time a reader clicks through from the homepage, they've seen it. Instead, on subpages, the first mention of "worktree" should link back to the homepage definition or use a brief parenthetical ("worktrees — separate directory checkouts of the same repo"). Do not define worktree in every hero on every page; that becomes patronizing.

### Define MCP before first use on any page that mentions it [5/5]
MCP (Model Context Protocol) is not common vocabulary. Every page that references MCP must include at least one sentence explaining what it is: a protocol that lets AI coding agents call tools as structured function calls instead of running shell commands.

### Use `{project}-{branch}.localhost` in all URL demos [3/5]
The README documents the route key as `{project}-{branch}`. Multiple pages use shorthand like `feature-auth.localhost`, omitting the project prefix. All demos should use the full form (e.g., `myapp-feature-auth.localhost`) to match actual behavior.

### Do not introduce a concept before establishing the concept it depends on [5/5]
`resolve` requires understanding the registry; the registry requires understanding allocations; allocations require understanding the core `gtl setup` loop. Enforce a concept dependency chain: worktree → port/DB collision problem → `.treeline.yml` → `gtl setup` allocates resources → registry tracks allocations → `resolve` looks up other projects in the registry.

### Reduce inline code density on feature and use-case pages [3/5]
Some paragraphs contain 6+ `<code>` fragments. When a paragraph has more than 3 inline code spans, consider whether the information should be in a table, a bulleted list, or a terminal demo instead.

### Every factual claim must be verifiable against README behavior [5/5]
Before publishing, verify: `gtl release` does not remove worktree directories. `redis.strategy` is user config, not project config (or update README). `gtl serve` requires macOS/Linux. Windows support is WSL-only for networking features. `AGENTS.md` provides context, not deterministic instructions.

### Guardrails for future edits
- Keep claims concrete and verifiable against README behavior.
- Never introduce an advanced concept before the baseline concept is established.
- Avoid referential/marketing phrasing that communicates no mechanism ("seamless," "powerful," "next-level," "full story," "first-class," "environment-aware").
- Preserve technical accuracy vs. source-of-truth behavior.
- Do not use "automatically" without naming the mechanism.
- Do not use "just works" without showing the mechanism that makes it work.

---

## 4. Risks and Tradeoffs

**1. Clarity vs. length.** Adding worktree definitions, MCP definitions, and bridge sentences to every page will make pages longer. The homepage hero will grow. Mitigation: offset by removing the advanced concepts currently front-loaded (resolve/link from hero, hooks from isolation hero, all four networking commands from networking hero). Net length should be similar or shorter.

**2. Beginner clarity vs. expert scanning.** Defining "worktree" strongly on the homepage + docs and using lightweight links/parentheticals on subpages is the balance. If §0.Q1 answer changes to "existing worktree users," the homepage definition can move from a full sentence to a tooltip.

**3. Agent pillar positioning.** Per §0.Q2, agents are the cause of the journey (they create the scale that demands worktree management) and stay as a nav pillar. The homepage card order keeps agents last because isolation/networking/workflows establish the concepts agents depend on. The agent page must earn its billing by being intelligible to someone who's used Copilot but never configured MCP — framing MCP as "how agents stay informed" rather than "the point of the tool."

**4. Mobile DOM reordering may break desktop layout.** Moving `feature-card-copy` before `feature-card-visual` in the DOM for mobile readability could affect the desktop side-by-side layout. Mitigation: use CSS `order` on the flex children rather than changing DOM order. Test both breakpoints.

**5. Weakening the "without/with" terminal contrast on the homepage.** Adding a `serve install` qualifier to the "with" demo output may make the solution feel less clean. This is an honest tradeoff: the current version is misleading in its simplicity; the revised version is accurate but slightly less punchy. Accuracy wins.

---

## 5. Out of Scope / Rejected

| Finding | Source | Reason for exclusion |
|---|---|---|
| **Sitemap missing use-case URLs** | Opus | Technical/SEO fix, not a copy change. File separately. |
| **Hover-only nav dropdown for Features** | Composer | Design/UX issue. Cannot be fixed with copy. File separately. |
| **Docs page is a single 1700-line HTML file (bad for SEO)** | Opus | Information architecture, not copy. File separately. |
| **Add a "What is this?" / About page** | Opus | This plan fixes the homepage hero to serve that function first. If the hero revision proves insufficient (3–4 lines may not be enough to orient a cold visitor), revisit as a homepage "What is Git Treeline?" section below the hero. Not rejected outright — deferred pending hero results. |
| **Add a high-level architecture diagram** | Sonnet | Visual/design deliverable, not copy. Note: this would be valuable but is out of scope for a copy plan. |
| **Contrast with alternatives (Docker, tmux, manual port changing)** | Grok | Only 1 agent raised this. The site's approach of naming the problem rather than competitors is a legitimate choice. |
| **Feature pages and use-case pages overlap significantly** | Opus | Only 1 agent raised this. The intentional distinction (feature = how it works, use case = when you'd use it) is defensible. Cross-linking already exists. |
| **Mapbox in the networking integration list is not hostname-bound** | Composer | Pedantic. Mapbox API keys can be hostname-restricted. Not clearly wrong. |
| **"Bubble Tea" is a Go library name** dropped on workflows page | Sonnet | Included in the plan above (P2 revision to TUI section). Not rejected. |
| **TUI keyboard shortcuts are dense on mobile** | Composer | Design concern; the information is reference material and density is appropriate for that purpose. |

---

## 6. Open Questions

Questions 1–3 from v1 (target audience, agent positioning, Windows support) and the `gtl serve alias` / `redis.strategy` verification items have been promoted to §0 Prerequisites. The remaining questions:

1. **Is there a TUI dashboard screenshot or recording available?** The workflows page describes the dashboard in text only. If a screenshot exists, it should be added. If not, consider creating one before Phase 2.

2. **What editors does MCP integration officially support?** The agents page lists Cursor, Claude Code, and Codex. The README mentions Cursor and Claude Code. Does Codex officially work? Does Windsurf (mentioned in docs)? The site should list only verified editors.

3. **Should the homepage feature cards link directly to subpages or to anchor sections within subpages?** Current behavior varies. Consistency matters for the reader's expectation of what a click does.
