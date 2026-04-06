# Git Treeline Website Copy Review

**Reviewer model:** claude-4.6-opus  
**Date:** 2026-04-06  
**Methodology:** Read the CLI source code and README first (the source of truth), then read every HTML page on the website. Every observation below is written from the perspective of a first-time engineer who has never heard of this tool and landed on the site cold.

---

## Global observations (apply across the entire site)

### 1. The site never plainly says what git-treeline IS

Across every page, the copy assumes you already know what "worktree isolation" means and why you'd want it. There is no single sentence on the homepage — or anywhere — that says something like: "Git Treeline is a CLI tool that manages isolated development environments for git worktrees, so you can run multiple branches of the same app simultaneously without port collisions, database conflicts, or shared config."

The closest attempt is the hero subtitle: "Review every branch at the same time." This is evocative but doesn't describe what the tool does. A reader who doesn't already know about git worktrees would leave the hero with zero understanding.

### 2. "Worktree" is treated as common vocabulary — it is not

The entire site assumes the reader uses git worktrees. Most engineers don't. The word "worktree" appears in the first sentence of almost every page without ever being defined or contextualized. There is no bridge for someone who uses `git checkout` / `git switch` and has never run `git worktree add`.

The README does this better: it opens with "Git worktrees let you check out multiple branches side by side" before talking about the problem. The website never provides that setup.

### 3. The AI agent angle is overweighted relative to the primary use case

The README's "Why" section is honest: worktrees are useful for parallel development, and AI agents are one driver of that. But on the website, AI agents get equal billing as a top-level feature pillar (Isolation, Networking, Workflows, **AI Agents**), and MCP is mentioned on nearly every page. For a first-time visitor who doesn't use AI coding agents, this makes the tool feel like it's "for AI" rather than "for developers who run multiple branches."

### 4. Demos before explanations (mobile-hostile pattern)

Every feature page follows the pattern: hero → terminal demo → explanation. On desktop with the side-by-side layout, this works because the demo and explanation are adjacent. On mobile, the demo stacks above the explanation, meaning users see a terminal simulation of `gtl new feature-auth` with output like "Allocated port 3010" before they understand what port allocation is, why it matters, or what the tool does. The demo is meaningless without the context that follows it.

### 5. Copy references internal concepts before defining them

Across many pages, copy uses terms like "registry," "allocation," "resolve," "link," "route key," "supervisor," and "setup commands" without defining them first. These are git-treeline's internal vocabulary. A first-time reader doesn't know what an "allocation" is in this context. Each term needs to be established before it can be referenced.

### 6. The `code` tag formatting creates a wall of green

Every page is dense with inline `code` spans. Commands, config keys, file names, branch names, env vars — they're all the same green monospace. On some sections (e.g., the Isolation "What this makes possible" cards), there are 6+ code fragments in a single paragraph. The result is visually overwhelming and makes it hard to parse what's prose and what's a command. The code isn't functioning as emphasis — it's functioning as noise.

---

## Page-by-page review

---

## Homepage (`/`)

### Meta / SEO

- **Title:** "Git Treeline · Review every branch at the same time" — marketing-forward but doesn't say what the tool is. Someone scanning browser tabs would not know this is a CLI tool.
- **Meta description:** "Git Treeline allocates ports, databases, and env per worktree; resolves cross-repo URLs; and serves, tunnels, or shares named URLs." — This is a feature dump, not a description. It reads like release notes, not a pitch.
- **JSON-LD description:** "registry-backed port and DB allocation, env templates including cross-project resolve, local HTTPS router, tunnels, optional ephemeral share links, MCP and JSON for automation." — Same problem. A pile of features, no framing.

### Hero (`#hero`)

- **Headline:** "git-treeline" — Just the name. Not a problem per se (many dev tools do this), but paired with the subtitle below, it means neither line explains what the tool does.
- **Subtitle:** "Review every branch at the same time" — Catchy, but misleading. This suggests code review tooling (like a diff viewer), not environment isolation. A reader who doesn't already understand the product would think this is a code review tool.
- **Body copy:** "Isolated ports, databases, and env per worktree. Cross-repo `resolve` and `link` when the API lives in another checkout. One config file, one CLI." — This jumps directly into what the tool provides without establishing the problem. `resolve` and `link` are meaningless to a new reader — they're internal command names with no established context. "When the API lives in another checkout" assumes the reader has a multi-repo setup and knows what "checkout" means in this context.

### Problem / Solution (`#problem-solution`)

- **Heading:** "Worktrees are easy to create. Hard to run." — Good heading. This is the best single line on the homepage.
- **Subtext:** "Git worktrees give you a second workspace in seconds. But every workspace shares the same ports, databases, and config." — This is the closest the homepage gets to explaining the problem. It should come before or alongside the hero, not below the fold.
- **Terminal demos:** The side-by-side "without / with" is effective. The "without" demo showing `EADDRINUSE` and "database already exists" concretely communicates the problem. The "with" demo showing `gtl new feature-auth` and its output is clear. This is the strongest section on the homepage.
- **Issue:** The "with" demo output includes "named URL via gtl serve" as a muted footnote. This references a feature (`gtl serve`) that hasn't been introduced yet. It's a minor distraction.

### Feature highlights (`#features`)

**Isolation card:**
- Heading "Two branches. Two apps. Zero conflicts." is strong.
- Body immediately drops into `{resolve:other-project}` and `gtl link` — advanced multi-repo features — before the reader understands the basic allocation model. The first paragraph should say what isolation means; the second introduces cross-repo as a follow-on. Instead, both concepts are in the first card.

**Networking card:**
- The slot-reel animation (`OAuth redirects`, `Stripe webhooks`, `Mapbox tokens`, `API callbacks`) cycles through specific integration names. A first-time reader doesn't yet understand why port changes break OAuth. The animation is catchy but the connection between "port-per-worktree" and "OAuth breaks" hasn't been made.
- Body copy: "Branch-stable HTTPS names, a pinned port, a public hostname, or a time-bounded share link—use the network boundary that matches OAuth, webhooks, or a demo, without juggling four unrelated tools." — This sentence lists four capabilities and three use cases in one breath. It's a feature list disguised as prose. A reader who doesn't already know what `gtl serve`, `gtl proxy`, `gtl tunnel`, and `gtl share` do gets nothing from this.

**Workflows card:**
- Heading "Type a PR number. Get a running app." — Good. Concrete.
- The demo showing the step sequence for `gtl review` is effective.
- Body: "Optional hooks in `.treeline.yml` run before and after setup and release—see the workflows page for the full supervisor story." — "Supervisor story" means nothing to someone who doesn't know the tool has a supervisor.

**AI Agents card:**
- Heading "Your agent gets the same tools you do." — Meaningless without context. Which tools? What agent? This heading assumes the reader already knows what the tool's "tools" are.
- Body: "MCP exposes `start`, `stop`, `port`, and `status`." — MCP hasn't been defined. These are command names with no context. A reader unfamiliar with MCP (most engineers) is lost.
- "`AGENTS.md` keeps Cursor, Claude Code, and Codex aligned on the same commands." — Name-drops three products without explaining the integration model.

### Get Started (`#get-started`)

- The body copy jumps immediately into implementation detail: "run the one-time local HTTPS setup (`gtl serve install` on macOS/Linux) so `gtl new` / `review` / `setup` can use branch URLs without cert warnings. Expect sudo prompts for trusting the CA and for port 443 forwarding."
- This is documentation, not a "get started" section. A marketing page CTA should motivate, not document. The sudo warnings and CA trust details belong in the docs, not at the moment you're trying to convert someone to install.

### Trust bar

- "No runtime dependency. CLI only." — Good.
- "No lock-in. Config file, not a library." — Good.
- "Nothing ships in your bundle." — Good.
- "Opt-in per developer." — Good.
- **Observation:** These are strong trust signals but they appear below the CTA, after the "get started" section. They should appear earlier — ideally before the reader is asked to install.

### Closing CTA (`#cta`)

- "Every branch, running" — Good tagline, consistent with the hero.
- "Your whole team sees the work, not just the diff." — Evocative but vague. What "work"? Who is seeing it? This reads like it's about code review collaboration, not about running isolated environments.

---

## Feature: Isolation (`/features/isolation/`)

### Hero

- Headline reused from home: "Two branches. Two apps. Zero conflicts." — Fine as a page headline.
- Body: "Every worktree gets its own port, database, Redis namespace, and env file. Declare what your project needs once in `.treeline.yml`. Git Treeline allocates resources and runs your hooks on every branch." — This is good. It's one of the clearest descriptions on the site. The phrase "allocates resources" is still slightly abstract but the enumeration (port, database, Redis namespace, env file) makes it concrete.

### "The second worktree is always broken"

- This section is excellent. It names the exact pain point, shows the exact errors, and is immediately relatable. The terminal demo is concrete: EADDRINUSE, "database already exists," stale .env. This is the best problem-statement copy on the entire site.

### "One command changes everything"

- The `gtl new feature-auth` demo is clear and effective.
- **Issue:** The section has a muted `text-sm` subheading ("One command changes everything") that reads as marketing-aspirational. The demo speaks for itself.

### "What this makes possible"

- **"Run several branches at full fidelity"** — Good heading. The body is dense: "Main, a feature branch, and a PR review can run side by side with different ports and databases. You switch browser tabs—or editor windows with distinct title-bar colors—not terminal sessions, and you are not sharing one `.env.local` between checkouts." The editor title-bar color detail feels like it's getting ahead of itself; the reader hasn't been introduced to the editor customization feature yet.
- **"Agents and automation get a real sandbox"** — This card is positioned as #2 out of 3. For a first-time reader who is not using AI agents, this feels like the tool is more about agents than about their own workflow. Should be #3 or removed from this page entirely (it has its own feature page).
- **"Onboarding tracks the repo, not a wiki"** — Good framing. "New developers clone, run `gtl init`…" This is the strongest of the three cards and the one most likely to resonate with a broad audience.

### "Declare it once. Git Treeline handles the rest."

- The config example + annotations is solid. Showing `.treeline.yml` with explanations for each section is the right approach.
- **Issue:** The hooks section introduces `pre_setup`, `post_setup`, `pre_release`, `post_release` with behavioral detail (abort vs. warn) that belongs in docs, not a feature page. This is too much depth too early.
- The editor mockup showing three title bars (main, feature-auth, pr-42) in different colors is a nice visual. The caption "Each worktree gets a distinct color. Know which branch you're in at a glance." is clear.

### "Multiple repositories, one machine"

- This section introduces `gtl resolve` and `{resolve:…}` — an advanced feature for multi-repo setups.
- **Issue:** This is the isolation page. Multi-repo URL resolution is a legitimate part of isolation, but it's a deep-cut feature that applies to a subset of users. Leading with "Your web app and your API are different clones" narrows the audience. Someone with a single repo (the majority) would think this section doesn't apply to them, even though the rest of the page does.

### CTA ("Add isolation to your project")

- Clean. `gtl init` is the right call to action. "One file, committed to your repo. Non-users just ignore it." is good trust copy.

---

## Feature: Networking (`/features/networking/`)

### Hero

- **Headline:** "One redirect URI per branch, not per port." — Assumes the reader understands OAuth redirect URIs and why ports matter for them. This is a deeply specific developer pain point that won't register for everyone.
- **Body:** Lists all four networking commands in the first paragraph with a parenthetical note about them not being "four unrelated tools." This is defensive positioning against a comparison the reader hasn't made yet.

### "Every integration breaks when you change ports"

- This is the section that should ground the page. The terminal demo showing an OAuth callback arriving at `localhost:3000` when the worktree is on `:3010` is concrete and effective.
- **Issue:** The footnote "Stripe webhooks, Mapbox keys, CI callbacks, SSO redirects · all hardcoded to a port or hostname that keeps changing between worktrees" lists specific integrations but doesn't explain the general principle: external services register a fixed URL, and worktrees change ports, so the URL breaks. The reader has to infer this.

### "The URL follows the branch, not the port"

- The pipeline visual (ephemeral port → HTTPS branch URL) is clear.
- "Register specific redirect URIs per branch, or point them at a fixed port with `gtl proxy`." — This introduces `gtl proxy` in a sentence that assumes you already know what it does.

### "What this makes possible"

- Three cards: "Stable URLs per branch," "Hand someone a link," "External services reach you."
- **"Hand someone a link"** mentions `gtl tunnel`, `gtl share`, and `--tailscale` in one paragraph. This is three separate features with different trust models in a single card. A first-time reader can't parse the distinction.
- **"External services reach you"** — "A tunnel gives your local worktree a public route so payment processors, CI callbacks, and mobile deep links can hit the branch you have running—not a stale staging box." The contrast with "stale staging box" introduces a concept (staging) that's adjacent but unrelated to the core pitch.

### "Four commands, one job"

- This is the real meat of the page. Each command gets a section with description + terminal demo.
- **Observation:** The page works better as documentation than marketing. It's organized like a reference (command → description → demo) rather than a narrative (problem → solution → how).
- The comparison table at the end (Goal → Command) is the single most useful element on the page for a first-time reader. It should be higher.

---

## Feature: Workflows (`/features/workflows/`)

### Hero

- "Type a PR number. Get a running app." — The best headline on the site. Concrete, immediate, testable.
- Body introduces the supervisor and TUI dashboard in the same breath as PR review. These are separate concepts that should get separate introductions.

### "10 minutes of setup for 2 minutes of review"

- The "manual way" terminal showing 8 commands is very effective. It names a pain everyone has experienced.
- "Next PR? Do it all again. And don't forget to tear everything down when you're done." — Good kicker.

### "One command replaces all of that"

- `gtl review 42 --start` with the step sequence is clear and compelling.
- "Done reviewing? `gtl release --drop-db` tears down the worktree, drops the database, and removes the env file." — Excellent. Clear before/after.

### "What this makes possible"

- **"Product and design review real apps"** — Good. "Gives stakeholders a running checkout without asking them to clone, install dependencies, or edit env files." This is one of the strongest value propositions on the site.
- **"QA exercises several branches at once"** — Good.
- **"Agents and CI drive the same supervisor"** — Again, agents appear in a section where they dilute the primary message. The supervisor is valuable to humans first; agent control is a secondary benefit.

### "The supervisor: two interfaces, one process"

- The split-terminal demo (human terminal + agent/CI terminal) is the best visualization of the supervisor concept on the site.
- **Issue:** The heading "two interfaces, one process" is inside-out — it describes architecture, not benefit. A heading like "You watch the logs. Agents control the server." would be clearer.

### "Hooks, clone, open, wait"

- This heading is a list of nouns/verbs with no verb framing. It reads like a changelog item, not a section title. "Automation hooks and one-command shortcuts" would be more descriptive.
- The content is solid but dense. Three features (hooks, clone, open/await) in one section with no visual separation.

### "Interactive terminal dashboard"

- Describes `gtl dashboard` with keyboard shortcuts.
- **Issue:** There's no screenshot or visual of the TUI. This is a visual feature described entirely in text. A screenshot or even an ASCII mock would make this section 10x more compelling.

### "Review commands"

- Four-item reference table. Useful but feels like docs, not a feature page.

---

## Feature: AI Agents (`/features/agents/`)

### Hero

- "Your agent gets the same tools you do." — Meaningless without context. What tools? This headline only works if you already know the tool.
- Body: "A built-in MCP server gives agents native access to worktree management." — "MCP server" and "worktree management" are jargon. MCP is not defined anywhere on the site.

### "Agents don't know about your environment"

- The "agent without treeline" terminal demo is effective: agent tries a random port, gets EADDRINUSE, guesses database names. This is relatable for anyone who has used AI coding tools.
- "Works sometimes. Breaks often." — Good kicker.

### "Three lines in your editor config"

- The MCP config JSON block is clean and actionable.
- "Now the agent calls `start`, `port`, `status` as native tools under the `gtl` server namespace. No shell parsing, no guessing, no string manipulation." — This is clear for someone who understands MCP. For someone who doesn't, "native tools under the gtl server namespace" is opaque.

### "What this makes possible"

- Three cards all assume deep familiarity with MCP, tool calling, and agent orchestration patterns. A developer who uses AI assistants casually (copilot-style) won't connect with "IDE-integrated agents control the stack" or "Orchestrators can run many sandboxes safely."

### "Four integration surfaces"

- **Issue:** The heading says "four" but the page describes MCP, lifecycle hooks, structured output, and AGENTS.md. One of these (AGENTS.md) is a generated markdown file, which is a different category than the other three (protocol, shell interface, file). Grouping them as "four integration surfaces" overstates the coherence.
- The MCP tool table is useful reference material.

---

## Use Cases Hub (`/use-cases/`)

### Hero

- **Headline:** "Problems worth solving on purpose" — Clever but opaque. It doesn't tell you what this page is about. "Solving on purpose" vs. what — accidentally?
- **Body:** "These are scenario guides—not a second feature tour. Each page names a concrete failure mode (ports, URLs, humans who do not use git, machines that need JSON), then points to the Git Treeline commands and config that address it." — This is self-aware meta-copy about what the page is, rather than copy that serves the reader. The parenthetical "(ports, URLs, humans who do not use git, machines that need JSON)" is a list of the use cases' topics disguised as a description.

### Cards

- The four cards are well-written as abstracts. Each names a specific scenario and links to the relevant feature pages.
- **Issue:** The card descriptions use dense, referential language. Example: "Hardcoded `localhost` ports in the frontend env rot the moment allocations change. The registry and `{resolve:…}` exist so URLs track branch intent—not last week's guess." A first-time reader doesn't know what "allocations" are, what "the registry" is, or what `{resolve:…}` means. This card description only works for someone who already knows the tool.

---

## Use Case: Multi-repo (`/use-cases/multi-repo/`)

### Overall

- This page is well-structured: problem → what breaks → what treeline does → terminal demo → related reading.
- **Issue:** The entire page is about a specific multi-repo scenario. If you have a monorepo or a single repo, this page is irrelevant. The page doesn't acknowledge that.

### "What breaks without a registry"

- "Developers paste `http://localhost:3030` into `.env.local`." — Concrete and relatable.
- "Nothing in plain git fixes that—only a layer that knows *both* allocations." — Good. Establishes why a registry is needed.

### "What Git Treeline does"

- Clear explanation of `gtl resolve`, same-branch matching, `gtl link` overrides.
- "In `.treeline.yml`, use `API_URL: "{resolve:api}"` (or `{resolve:api/main}` to pin a branch). Values are resolved at setup time." — This is docs-level detail on a use-case page. The depth is appropriate for someone who's already decided to use the tool, not for someone evaluating it.

---

## Use Case: Integrations/URLs (`/use-cases/integrations-urls/`)

### Overall

- Tight, well-focused page. The comparison table (Need → Command) is immediately useful.
- **Issue:** The heading "Redirects and webhooks care about the URL, not your branch name" is accurate but long and awkward. It's trying to be clever but the length undermines the punch.

### "When you combine more than one"

- One paragraph that covers combining `gtl serve` + `gtl proxy`, `gtl tunnel` for ongoing access, `gtl share` for disposable sessions, plus framework host allowlists. Too many concepts in one paragraph. Each combination deserves its own sentence.

---

## Use Case: Platform/PR review (`/use-cases/platform-pr/`)

### Overall

- The cleanest use-case page. The numbered flow (review → open → share → release) is easy to follow.
- **Issue:** "Product and QA should not need your README, a staging deploy, or a screen share of your laptop." — This is the strongest pitch for this use case and it's buried in the body copy. It should be the headline.
- The headline "Someone needs to see PR #42 in a browser" is good but informal in a way that doesn't match the tone of the rest of the site.

---

## Use Case: Agents/Automation (`/use-cases/agents-automation/`)

### Overall

- **Headline:** "Scripts and agents do not read your terminal theme" — Clever but obscure. It's a reference to the fact that machines need structured output, but "terminal theme" is a strange choice of words. The point is "machines can't parse human-readable output."
- The bulleted list of interfaces (MCP, JSON flags, Readiness, Env inspection, Completions) is useful reference material but reads like docs, not a use case narrative.

---

## Docs (`/docs/`)

### Overall structure

- Well-organized with sidebar navigation on desktop and a mobile "Jump to section" toggle. The section grouping (Core, Environment, Workflows, Networking) is logical.
- The docs are thorough and match the CLI's actual behavior (verified against the README and source code).

### "Getting started"

- This is the strongest documentation section. The install → serve install → init → new → review flow is clear and linear.
- **Issue:** The section about `gtl serve install` is very front-loaded with warnings about `sudo` prompts and what they're for. This is defensive documentation (anticipating user confusion) but it reads as scary. A first-time user seeing "Expect your system password twice" before they've even tried the tool might bail.
- **Issue:** The docs open with "Git Treeline ships as a single CLI binary — nothing from Treeline runs inside your app's process or bundle." This trust statement is more appropriate for a marketing page than the opening line of documentation. Docs should open with how to use it, not why it's safe.

### CLI Reference

- Comprehensive and accurate. Every command matches the source of truth in the README.
- **Observation:** The `cmd-list` format (command name + flags + description) is dense but scannable. This is appropriate for reference docs.

### Framework Integration

- The Rails, Next.js, Vite, and Express sections are well-written and specific. Each names what works automatically and what needs manual wiring.
- **Issue:** The section ID is `#rails-integration` but the heading is "Framework integration." The ID is a legacy artifact. The sidebar correctly says "Framework integration" but the anchor is misleading.

### Networking sections

- Thorough. Each of the four commands (serve, proxy, tunnel, share) gets its own section with usage, configuration, and technical detail.
- The "Route key convention" section is excellent — it documents the URL derivation algorithm clearly.
- **Issue:** The networking sections are the deepest part of the docs (5 sections) and the most feature-rich area of the tool. A reader who just wants to get started might drown here. A "recommended reading order" or "start here" note would help.

---

## Structural / IA observations

### 1. No "what is this" page

There is no `/about/`, no "What is Git Treeline?" section, no introductory page for people who arrived via search or a link. Every page assumes you're already interested. There's no on-ramp for the curious-but-uninformed.

### 2. Feature pages and use-case pages overlap significantly

The feature pages (Isolation, Networking, Workflows, Agents) and the use-case pages (multi-repo, integrations, platform PR, agents) cover much of the same ground from slightly different angles. A reader who reads both will encounter significant repetition. A reader who reads only one might miss key context.

### 3. Sitemap omits use-case pages

`public/sitemap.xml` lists `/`, `/docs/`, and the four `/features/` URLs but does not include any `/use-cases/` URLs. These pages are invisible to search engines.

### 4. The docs page is a single 1700+ line HTML file

This is a technical observation, not a copy one, but it means the docs can't be searched by section in search engines. Each section gets one canonical URL (`/docs/#section-name`) which is fine for internal navigation but poor for SEO.

---

## Summary of the most critical failures

| # | Failure | Severity | Where |
|---|---------|----------|-------|
| 1 | **No plain-language explanation of what the tool is** — the site never says "Git Treeline is a CLI that manages isolated dev environments for git worktrees" in a single clear sentence | Critical | Homepage hero, every page |
| 2 | **"Worktree" is undefined** — the primary concept is never explained | Critical | Every page |
| 3 | **Hero subtitle misleads** — "Review every branch at the same time" suggests code review tooling, not environment isolation | High | Homepage |
| 4 | **Demos before explanations** — terminal simulations appear before the concepts they demonstrate are introduced, especially on mobile | High | Homepage features, all feature pages |
| 5 | **Internal vocabulary used before definition** — "allocation," "registry," "supervisor," "resolve," "link," "route key" all appear before being explained | High | Every page |
| 6 | **Get Started section is documentation, not conversion** — sudo warnings and CA trust details at the moment of install CTA | High | Homepage |
| 7 | **AI agents overweighted** — appears as a top-level pillar and in every feature page's "what's possible" cards, diluting the primary developer story | Medium | Site-wide |
| 8 | **Use-case hub copy is self-referential** — "These are scenario guides—not a second feature tour" describes the page rather than serving the reader | Medium | `/use-cases/` |
| 9 | **Sitemap missing use-case URLs** — 4 pages invisible to search engines | Medium | `sitemap.xml` |
| 10 | **Trust bar appears after the CTA** — should appear before asking users to install | Medium | Homepage |
| 11 | **Feature card copy is too dense** — multiple advanced features per paragraph, multiple code fragments per sentence | Medium | Homepage feature cards, feature page cards |
| 12 | **No TUI screenshot** — the dashboard feature is described entirely in text | Low | `/features/workflows/` |
| 13 | **Networking hero assumes OAuth knowledge** — "One redirect URI per branch, not per port" only clicks for OAuth-experienced developers | Medium | `/features/networking/` |
| 14 | **Meta descriptions read like changelogs** — structured as feature lists, not descriptions | Low | Homepage, docs |
| 15 | **Docs open with trust statement instead of usage** — "nothing from Treeline runs inside your app's process or bundle" is marketing, not docs | Low | `/docs/` |

---
---

# Post-rewrite audit

**Date:** 2026-04-06 (same day, after rewrite agent completed changes)  
**Scope:** 8 modified files — `index.html`, all four feature pages, `use-cases/index.html`, `use-cases/agents-automation/index.html`, `docs/index.html`. CSS was not touched.

This audit re-evaluates every finding from the original review above. Each item is marked **Resolved**, **Partially resolved**, or **Still open**, with commentary on what changed and what remains.

---

## Disposition of original critical findings

| # | Original finding | Status | Notes |
|---|------------------|--------|-------|
| 1 | No plain-language explanation of what the tool is | **Resolved** | Hero body now reads: "A CLI that gives every branch its own port, database, and environment — so you can run three features, a bugfix, and a code review all at once." Meta description, JSON-LD, and OG tags all rewritten to match. |
| 2 | "Worktree" is undefined | **Resolved** | Problem/solution section now opens with: "A git worktree is a separate checkout of your repo in its own directory — `git worktree add` creates one in seconds." |
| 3 | Hero subtitle misleads ("Review every branch") | **Resolved** | Changed to "Run every branch at the same time" with a small-caps "Worktree environment manager" label. No longer suggests code-review tooling. |
| 4 | Demos before explanations (mobile) | **Still open** | CSS unchanged. `.feature-card-visual { order: 1 }` and `.feature-card-copy { order: 2 }` have no mobile override. Demos still stack above explanations on narrow screens. |
| 5 | Internal vocabulary used before definition | **Partially resolved** | "Allocation" and "registry" no longer appear on the homepage. "Supervisor" replaced with behavioral description on the workflows card. However, the isolation feature card still says "the git post-checkout hook handles the rest" without explaining that Treeline installs a hook. Networking hero still assumes OAuth redirect-URI familiarity. |
| 6 | Get Started is documentation, not conversion | **Partially resolved** | Rewritten with better framing ("Every branch gets its own HTTPS URL after two steps") but still includes "Requires sudo twice on macOS/Linux." The sudo mention is accurate but remains a friction point at the moment of conversion. Significantly better than before. |
| 7 | AI agents overweighted | **Partially resolved** | The homepage AI agents card is better scoped. On feature pages, agents still appear in every "What this makes possible" section (isolation, workflows), but now as the last card rather than first/second. Improvement in placement, same footprint. |
| 8 | Use-case hub copy is self-referential | **Resolved** | Headline changed to "Scenario guides by problem." Body now describes failure modes directly. Wayfinding paragraph directs readers to feature pages first. |
| 9 | Sitemap missing use-case URLs | **Still open** | `sitemap.xml` still lists only `/`, `/docs/`, and the four `/features/` pages. All four `/use-cases/` pages remain invisible to search engines. |
| 10 | Trust bar appears after the CTA | **Resolved** | Trust bar now sits between the feature cards and the Get Started section. |
| 11 | Feature card copy is too dense | **Resolved** | Each homepage feature card now carries one concept per paragraph. `{resolve:…}` and `gtl link` removed from the isolation card. Networking card no longer lists four commands in one sentence. |
| 12 | No TUI screenshot | **Still open** | The workflows page has a new "Interactive terminal dashboard" section with keyboard shortcuts, but still no screenshot or ASCII mock of the TUI. |
| 13 | Networking hero assumes OAuth knowledge | **Still open** | Headline still reads "One redirect URI per branch, not per port." Body does now provide more context about trust boundaries, but the headline remains opaque to anyone who hasn't configured an OAuth provider. |
| 14 | Meta descriptions read like changelogs | **Resolved** | Homepage meta now reads "Run multiple branches of the same app at the same time — each with its own port, database, and URL. One CLI, one config file." Clear and human. |
| 15 | Docs open with trust statement | **Not checked** | The docs file is too large to read in full in this pass. The OG description was checked and is addressed below. |

---

## Page-by-page: new observations

---

### Homepage (`/`)

**What improved:**
- Hero is now one of the best sections on the site. The subtitle -> accent headline -> body copy progression ("Worktree environment manager" -> "Run every branch at the same time" -> concrete CLI description) is clear and scannable.
- `gtl serve install` is surfaced early — a single muted line below the hero CTAs: "One-time setup (`gtl serve install`) enables branch-named HTTPS URLs on macOS/Linux." This is the right weight: visible but not scary.
- Problem/solution section now defines worktrees inline. The "without / with" terminal demos remain the strongest element on the page.
- Trust bar moved above Get Started. Good.

**New or remaining issues:**

1. **"Run every branch at the same time"** — You run apps or servers, not branches. "Run every branch" is colloquial shorthand that works in conversation but is technically imprecise on a product page. Consider "Run every branch's app at the same time" or "Run the same app on every branch at once." Minor.

2. **Isolation card mentions "the git post-checkout hook"** without explaining that Treeline installs one. A first-time reader doesn't know a hook exists or what triggers it. The sentence "Declare what each branch needs in `.treeline.yml` — the git post-checkout hook handles the rest" could say "a git hook installed by Treeline" instead.

3. **Problem/solution terminal ("with" side)** still shows `named URL via gtl serve (one-time setup)` as muted subtext. This is now less jarring because `gtl serve install` is introduced in the hero, but the ordering is still slightly backward — the hero footnote appears after the problem/solution section in DOM order, so a fast-scrolling mobile reader hits the terminal reference first.

4. **"Your whole team sees the work, not just the diff"** line from closing CTA is gone, replaced with "One config file in your repo. Every branch isolated from that point on." — better. No issues.

---

### Feature: Isolation (`/features/isolation/`)

**What improved:**
- "What this makes possible" card order fixed: "Run several branches" -> "Onboarding" -> "Agents and automation" (agents now last).
- Onboarding card now correctly says `gtl setup` instead of `gtl init`.
- Config reference section expanded with lifecycle hooks properly documented (abort vs warn behavior, link to docs).
- Multi-repo section now has a complete explanation of `gtl resolve`, `gtl link`, `gtl unlink`, with a terminal demo and cross-link to the use-case page.

**New or remaining issues:**

1. **Hero mentions "a git post-checkout hook allocates resources on every branch"** — same hook-explanation gap as the homepage card. A reader doesn't know Treeline installs this hook.

2. **Isolation section "What this makes possible" -> Onboarding card** says `gtl setup` runs "Database clone, env interpolation, `commands.setup`, and hooks run in a documented order." The phrase "in a documented order" is self-referential (documented where?). Either state the order or drop the claim.

3. **Multi-repo section still appears on the isolation page.** My original concern stands: this narrows the audience by foregrounding a multi-repo scenario most visitors won't have. It's better executed now (the copy is clear), but the placement question remains. Consider whether this belongs as its own section or should be deferred to the use-case page with a brief mention here.

---

### Feature: Networking (`/features/networking/`)

**What improved:**
- Hero body improved: clearly frames four commands with different trust boundaries. The one-sentence explainer for each ("local HTTPS names, a pinned port, a public route on your domain, and a disposable link") is efficient.
- "The URL follows the branch, not the port" pipeline visual now has clear labels ("Ephemeral port" -> "HTTPS URL for this branch").
- "Four commands, one job" section now has per-command "Best for" callouts — immediately useful for someone deciding which command to use.
- Comparison table retained (still the strongest element).
- CTA section focused: "Most teams only need `gtl serve`." Good triage advice.

**New or remaining issues:**

1. **Hero headline still assumes OAuth redirect-URI knowledge** — "One redirect URI per branch, not per port." Unchanged from original review.

2. **`gtl serve alias` mentioned** in the `gtl serve` description: "Extra subdomains for Redis UIs and the like: `gtl serve alias`." I could not find `gtl serve alias` documented in the README. If this is an undocumented or new command, it either needs README coverage or should be removed from the marketing site.

3. **Footer sizing inconsistency** — This page's footer uses `text-base text-muted` for the bottom line, while isolation/workflows/agents use `text-xs text-muted`, and the homepage uses `text-sm sm:text-base text-muted`. Three different sizes across pages.

4. **Nav sizing inconsistency** — Homepage and networking use `text-base` for nav links and the brand name. Isolation, workflows, agents, and use-case pages use `text-sm`. The Install button is `text-base py-2.5` on homepage/networking but `text-xs py-2` elsewhere.

---

### Feature: Workflows (`/features/workflows/`)

**What improved:**
- Hero body now surfaces the `gh` CLI dependency immediately: "Requires the `gh` CLI for PR fetching; everything else is built in." Was previously buried.
- Supervisor section heading changed from "two interfaces, one process" to "Start, stop, restart — no PID files." Clearer and benefit-oriented.
- New split-panel layout: "Human interface" vs "Machine interface" with separate descriptions. Good framing.
- "Automation hooks and shortcuts" heading replaces the old noun-list heading. Better.
- TUI dashboard has its own dedicated section with keyboard shortcuts and aliases (`gtl dash`, `gtl ui`).
- Review commands reference table kept — correct and useful.
- `gtl release --drop-db` wording is now accurate: "frees the allocated port, drops the cloned database, and cleans up the env file. Remove the worktree directory separately with `git worktree remove`." — correctly separates resource cleanup from worktree removal.

**New or remaining issues:**

1. **TUI dashboard still has no screenshot or visual.** The section is better (keyboard shortcuts, aliases, link to docs), but it's still purely text describing a visual feature. A terminal screenshot or ASCII diagram would make this immediately compelling.

2. **`gtl clone` section** says "It does not start the dev server—cloning an untrusted repo and executing arbitrary start commands is a trust boundary." — Good security framing, but "trust boundary" is a term that may not land with all readers. Consider "safety concern" or "security risk."

---

### Feature: AI Agents (`/features/agents/`)

**What improved:**
- Hero headline rewritten: "Agents guess ports and config. They don't have to." — concrete, problem-oriented. Massive improvement over the original "Your agent gets the same tools you do."
- MCP defined inline in the hero body: "Model Context Protocol, the way AI coding agents call tools directly instead of running shell commands." Clear, no jargon debt.
- "What this makes possible" framing paragraph now includes an on-ramp: "If you're evaluating agent integration and haven't used MCP before, start here — it's simpler than it looks." Welcoming.
- "Four integration surfaces" heading changed to "How agents connect" — no longer claims a specific count. Body correctly frames three interface categories (MCP, JSON flags, AGENTS.md).
- MCP tool table expanded with `db_name` and `config_get` entries.
- Lifecycle hooks section clearly shows the setup/teardown contract: `gtl setup .` and `gtl release . --drop-db`.

**New or remaining issues:**

1. **"How agents connect" intro says three interfaces but the page has four subsections** (MCP server, Lifecycle hooks, Structured output, Agent context files). The mismatch isn't confusing because the intro accurately describes the three *interface types* and lifecycle hooks are really about commands not interfaces, but someone counting sections against the intro might notice. Minor.

2. **"Agent context files" section** says "Any MCP-compatible editor reads it as context — how closely agents follow it depends on the model." This is honest and accurate, which is good. No issue, just noting the candor is appropriate.

---

### Use Cases Hub (`/use-cases/`)

**What improved:**
- Headline "Scenario guides by problem" is direct and functional.
- Intro paragraph names concrete failure modes: "ports colliding, URLs breaking, agents guessing config."
- Wayfinding paragraph directs readers to feature pages for full depth — good information architecture.
- Card descriptions now lead with failure modes instead of internal vocabulary.

**New or remaining issues:**

1. **Multi-repo card** still includes `{resolve:...}` in the description: "The registry and `{resolve:...}` exist so URLs track branch intent—not last week's guess." This assumes reader familiarity with the `{resolve:...}` syntax. The card is better than before (it starts with a concrete failure), but the second sentence uses tool-specific notation without introduction.

2. **"Touches" labels** on each card (e.g., "Touches . Isolation . resolve / link") use internal command names as tags. These serve as cross-references for someone who knows the tool but are noise for a first-time visitor. Low severity.

---

### Use Case: Agents/Automation (`/use-cases/agents-automation/`)

**What improved:**
- Body paragraph improved: "Automation needs stable contracts: which port, which database name, whether the process is listening, and when it is safe to hit HTTP." Concrete.
- Interface list now includes `gtl env --json`, `gtl resolve <project> --json`, and completions. Comprehensive.
- AGENTS.md mention improved: "instructions for setup, teardown, and server management."

**New or remaining issues:**

1. **Headline unchanged:** "Scripts and agents do not read your terminal theme" — still obscure. The point is "machines need structured output, not human-readable text," but "terminal theme" is a strange proxy for that idea. The body is strong enough to carry it, but the headline itself would not compel a click from a hub page.

2. **Still reads more like reference docs than a scenario narrative.** The page jumps from a one-paragraph problem statement into a bulleted interface list. There's no "before/after" terminal demo or scenario walkthrough like the other use-case pages have. This makes it the weakest of the four use-case pages in terms of storytelling.

---

### Docs (`/docs/`)

**Checked only the `<head>` section (file too large to read in full).**

1. **OG description still leads with "Rails integration":** `CLI reference, configuration guide, Rails integration, and AI agent setup for Git Treeline.` — "Rails integration" is a subsection of the docs, not the primary topic. The `<meta name="description">` is now good: "Git Treeline documentation: .treeline.yml, cross-worktree resolve and link, TUI dashboard, networking (serve, proxy, tunnel, share), hooks, CLI reference, MCP." But the OG description is the one social shares use, so it matters.

---

## Cross-cutting issues (new)

### 1. Nav and footer sizing inconsistency across pages

The rewrite touched copy but not the HTML boilerplate. Pages now have mismatched nav and footer sizing:

| Page | Nav link size | Install button | Footer bottom text |
|------|-------------|----------------|-------------------|
| Homepage | `text-base` | `text-base py-2.5` | `text-sm sm:text-base` |
| Networking | `text-base` | `text-base py-2.5` | `text-base` |
| Isolation | `text-sm` | `text-xs py-2` | `text-xs` |
| Workflows | `text-sm` | `text-xs py-2` | `text-xs` |
| Agents | `text-sm` | `text-xs py-2` | `text-xs` |
| Use cases | `text-sm` | `text-xs py-2` | `text-xs` |

This is likely a side effect of the rewriter updating some pages' nav boilerplate and not others. Should be normalized.

### 2. Mobile demo-before-explanation (CSS untouched)

The CSS `order` property issue remains. On screens below `md` breakpoint, `.feature-card-visual` is always `order: 1` (appears first) and `.feature-card-copy` is `order: 2` (appears second). This means every homepage feature card shows the terminal demo before the heading and explanation on mobile. This was the original P1 mobile concern and the rewrite did not address it.

### 3. Sitemap still incomplete

`sitemap.xml` has 6 URLs. The 4 use-case pages are all absent. That's 4 indexable pages invisible to search engines.

---

## Summary: rewrite scorecard

| Category | Score | Notes |
|----------|-------|-------|
| **Clarity of what the tool is** | A | Hero, meta, JSON-LD all now clearly describe the product. Worktrees are defined. |
| **First-time reader comprehension** | B+ | Most internal vocabulary eliminated from first-contact surfaces. Some terms remain (post-checkout hook, redirect URI). |
| **Homepage flow** | A- | Problem -> solution -> features -> trust -> CTA is now logical. Trust bar moved up. Feature cards focused. |
| **Feature page structure** | B+ | All four pages follow a consistent "world without -> moment it clicks -> what's possible -> how it works -> CTA" pattern. Well done. |
| **Use-case pages** | B | Hub page much improved. Individual pages range from strong (platform-pr, integrations) to reference-heavy (agents-automation). |
| **Mobile experience** | C | CSS demo-before-explanation issue unaddressed. Nav/footer sizing inconsistent across pages. |
| **SEO / meta** | B- | Descriptions vastly improved. Sitemap still incomplete. OG description on docs still leads with "Rails integration." |
| **Consistency** | C+ | Nav, footer, and button sizing diverge across pages. Two groups of pages have different boilerplate. |

**Bottom line:** The rewrite resolved the most critical copy failures — the product now explains itself clearly on first contact, defines its core concept, and structures feature pages around problems rather than internal vocabulary. The remaining issues are primarily CSS (mobile order), boilerplate consistency (nav/footer sizing), and a few pockets of assumed knowledge (OAuth redirect URIs, git hooks). The sitemap gap is a quick fix. The mobile order issue requires a CSS change the rewriter was not scoped to make.
