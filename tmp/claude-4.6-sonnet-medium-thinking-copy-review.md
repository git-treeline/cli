# Git Treeline Website — Copy Review
**Reviewer model:** claude-4.6-sonnet-medium-thinking  
**Date:** April 6, 2026  
**Lens:** First-time engineer, no prior context

---

## What the Tool Actually Does (Source of Truth: README + Code)

Before annotating the copy, here is what git-treeline actually is, grounded in the repository:

- **A CLI tool (`gtl`) that manages local development environments across multiple git worktrees.**
- Git worktrees let you check out multiple branches of a repo simultaneously in separate directories. git-treeline solves the resource collision problem: when you run three worktrees, they fight over port 3000, the same database, and the same env file.
- **Core mechanism:** A `.treeline.yml` config file (committed per project) declares port count, database adapter, env vars, and setup commands. A per-machine registry (`registry.json`) tracks allocations. `gtl setup` allocates a port block, clones the database, writes an env file with interpolated values, and runs setup commands. `gtl release` frees those resources.
- **Beyond port isolation:** Local HTTPS routing (`gtl serve` — installs a background router mapping `{project}-{branch}.localhost` to the worktree port, with a local CA), public tunnels (`gtl tunnel` — Cloudflare), port forwarding (`gtl proxy`), time-limited share links (`gtl share`).
- **Workflow shortcuts:** `gtl new <branch>`, `gtl review <PR#>` (requires `gh` CLI), `gtl switch`.
- **Process supervisor:** `gtl start` boots the app command, `gtl stop`/`gtl restart` control it via Unix socket — no PID files.
- **AI agent surfaces:** MCP server (`gtl mcp`), `--json` flags on most commands, `AGENTS.md` generation.
- **Multi-repo:** A registry-level `resolve` mechanism lets one project's env template reference another project's allocated port by name, with same-branch matching as default.
- **Setup is two-step:** Install the binary (Homebrew, `go install`, or release binary), then run `gtl serve install` once per machine (requires two sudo prompts: CA trust + port 443 forwarding).

---

## Global Issues (Appear Across All Pages)

### 1. "Worktree" Is Never Explained
Every page assumes the reader knows what a git worktree is. The concept (`git worktree add` lets you check out multiple branches of a repo simultaneously into different directories) appears once on the isolation page terminal demo and never again as prose. Anyone who hasn't used `git worktree` is immediately lost. The entire product pitch depends on understanding this primitive.

### 2. The Two-Step Install Is Buried
`gtl serve install` is not optional for the primary flows (`gtl new`, `gtl review`, `gtl setup`). It requires `sudo` twice and installs a background service, a CA certificate, and port forwarding rules. The homepage buries this in the "Get Started" section mid-page. Feature pages don't mention it at all. A user who installs the CLI and immediately tries `gtl new` will get an error or unexpected behavior. This is a significant first-run friction point with no upfront warning.

### 3. The Registry Is Referenced Before It's Explained
"The registry" (the machine-local ledger of port/database allocations) is mentioned on the homepage, the isolation page, the multi-repo use case, and in feature descriptions — but never introduced as a concept in any of these places. Readers encounter it as assumed vocabulary.

### 4. MCP Is Used Without Definition
On the homepage AI Agents card, on features/agents, and in the use-cases, "MCP" (Model Context Protocol) appears as an unexplained acronym. The only page that comes close to defining it (`features/agents`) says "a built-in MCP server gives agents native access" but never says what MCP is or why it matters.

### 5. Supervisor Is Referenced as Known Vocabulary Sitewide
"Supervisor," "supervisor socket," "supervisor-controlled start/stop," and "the supervisor story" appear on the homepage, isolation page, workflows page, and agents page — but the supervisor is only explained on the workflows page. Feature cards and isolation copy reference it before the reader has any context.

---

## Page-by-Page Review

---

## Homepage (`/`)

### Meta / Title Tag
**Copy:** "Git Treeline · Review every branch at the same time"  
**Issue:** The page title frames the entire product as a PR review tool. That's one of five major use cases. Someone searching for "parallel git worktree port isolation" or "git worktree database cloning" won't recognize this title as relevant. The meta description is dense and accurate (`"allocates ports, databases, and env per worktree; resolves cross-repo URLs..."`) but the title chosen for sharing and SERPs buries the lede.

---

### Hero Section

**H1 copy:** `git-treeline`  
**Issue:** The H1 is the brand name. It communicates nothing about what the product does. On a first visit with no context, the first heading a reader sees tells them literally nothing. This is not a known brand that earns a name-only hero.

**Subheading:** "Review every branch at the same time"  
**Issue:** This frames the product as a PR review tool and nothing else. A developer who's running into port conflicts while developing three features in parallel wouldn't recognize their own problem here. The actual product is broader: it's a parallel environment manager. Review is one workflow, not the core identity.

**Body copy:** "Isolated ports, databases, and env per worktree. Cross-repo `resolve` and `link` when the API lives in another checkout. One config file, one CLI."  
**Issue:** This is actually the most accurate description of the product on the entire homepage, but:
1. It's small print below the main headline.
2. "Worktree" still assumes knowledge.
3. "`resolve` and `link`" are command names dropped without any context — a first-time reader does not know what resolving or linking means in this system.
4. "When the API lives in another checkout" — "checkout" is used synonymously with "worktree" but hasn't been established as such.

**Install command in hero:**  
The Homebrew command appears prominently. No mention that a second setup step (`gtl serve install`) is required before most commands will work. The call to action is "install the CLI" but the product doesn't fully work until you've also installed the HTTPS router.

---

### Problem/Solution Section

**Heading:** "Worktrees are easy to create. Hard to run."  
**Issue:** This heading assumes the reader uses worktrees. It's true and it's the right message for an in-market reader, but it's cold to anyone unfamiliar with `git worktree`. The problem statement arrives before the product has been situated in any broader context.

**Subheading:** "Git worktrees give you a second workspace in seconds. But every workspace shares the same ports, databases, and config."  
**Issue:** This is the site's one attempt to define worktrees — but it doesn't explain what a worktree actually is ("a second workspace" is vague). It also introduces three separate problems (ports, databases, config) simultaneously, which makes it unclear whether the product solves one or all three.

**Terminal: "without git-treeline" side**  
The `EADDRINUSE` error and the `database already exists` error are concrete and well-chosen. However, the third block shows `cat .env.local` — this assumes the reader knows what an env file is and why seeing `PORT=3000` is a problem. It's a logical step 3 in a sequence that hasn't been fully established.

**Terminal: "with git-treeline" side**  
The output includes `==> https://feature-auth.localhost` with a note "named URL via gtl serve." This is a result of the `gtl serve install` step that hasn't been explained yet. A reader who just saw the Homebrew install command will wonder where this URL came from.

---

### Feature Cards Section

**Isolation card:**  
Body: "Use `{resolve:other-project}` in env templates so a web checkout targets the API on the same branch by default; use `gtl link` when you need an explicit override."  
**Issue:** This is an advanced multi-repo feature being described in the intro isolation card. The reader has not learned:
- What env templates are
- What "same branch by default" means (same-branch matching in the registry)
- What the registry is
- What a project name or project namespace means in this context

Dropping `{resolve:other-project}` in a token syntax without any of this foundation doesn't inform — it signals complexity without meaning.

**Networking card:**  
Headline: "OAuth redirects / Stripe webhooks / Mapbox tokens / API callbacks just work."  
**Issue:** The animated slot headline makes a promise without any mechanism. "Just work" is a pattern that substitutes for explanation. The body then says "Branch-stable HTTPS names, a pinned port, a public hostname, or a time-bounded share link—use the network boundary that matches..." — this lists four different modes with different use cases in one sentence, which is accurate but assumes the reader knows why they'd want each of those four things differently.

**Workflows card:**  
Body: "Optional hooks in `.treeline.yml` run before and after setup and release—see the workflows page for the full supervisor story."  
**Issue:** "The supervisor story" is a phrase that treats the supervisor as an established concept the reader will want elaboration on. This is mid-intro copy. "The supervisor story" is a light but real AI/marketing-style phrase — it doesn't tell the reader what the supervisor does or why they care.

**AI Agents card:**  
Body: "MCP exposes `start`, `stop`, `port`, and `status`. `gtl env --json` and `gtl resolve --json` cover shell-only runners."  
**Issue:** Both sentences are entirely opaque without knowledge of:
- What MCP is
- What "shell-only runners" means in an agent context
- Why a reader would want to expose `start`, `stop`, `port`, and `status` to anything

The visual demo shows a JSON snippet with `mcpServers` config — which tells an experienced Cursor/Claude Code user exactly what to do, but communicates nothing to anyone else. The card has no entry point for someone who's heard about AI coding agents but hasn't used MCP-integrated editors.

---

### Get Started Section

**Copy:** "Install the CLI, then run the one-time local HTTPS setup (`gtl serve install` on macOS/Linux) so `gtl new` / `review` / `setup` can use branch URLs without cert warnings. Expect sudo prompts for trusting the CA and for port 443 forwarding—the guide explains what you will see."

**Issues:**
1. This is the first mention on the homepage that there is a two-step install process. A reader who saw the Homebrew command in the hero and thought "that's all I need" has already mentally committed to a simpler path.
2. "Without cert warnings" undersells the requirement. `gtl serve install` is required for the primary workflows, not just to avoid annoying warnings.
3. "The guide explains what you will see" is reassuring boilerplate. It doesn't tell the reader what they're about to do or what will be installed on their machine.
4. The copy mentions "sudo prompts for trusting the CA and for port 443 forwarding" — this is a significant thing to install (a CA cert, a background service, port forwarding rules) that should be framed as a decision, not a footnote. Security-conscious engineers will want to know this upfront, not after they're about to click install.

---

### Trust Bar

**Copy:** "No runtime dependency. CLI only. / No lock-in. Config file, not a library. / Nothing ships in your bundle. / Opt-in per developer."  
**Issues:**
1. "Nothing ships in your bundle" addresses a concern relevant to npm packages. For a standalone CLI tool, this concern is not intuitive — readers won't be worried about bundle size. It could be phrased as "doesn't run inside your app — it only writes config at setup time."
2. "Opt-in per developer" is ambiguous. Does it mean: optional for teammates? Optional per machine? Optional for the project? The README clarifies this (the config is committed but `gtl serve install` is per-machine) but the trust bar doesn't.
3. "No lock-in. Config file, not a library." — this is accurate but "lock-in" doesn't name the fear precisely. What are you not locked into? The CLI? The service? The platform?

---

### Closing CTA

**Heading:** "Every branch, running"  
**Subtext:** "Your whole team sees the work, not just the diff."

**Issue:** "Your whole team sees the work, not just the diff" is the best value-proposition sentence on the entire homepage, and it appears in the closing CTA at the bottom, not in the hero. It makes the benefit tangible, human, and immediately understood. It should be much closer to the top of the page.

---

## `/features/isolation/`

### Hero

**Heading:** "Two branches. Two apps. Zero conflicts."  
**Issue:** Same as the homepage feature card. Repetition is okay but this heading doesn't deepen understanding at the start of a dedicated feature page.

**Body:** "...runs your hooks on every branch."  
**Issue:** Hooks are mentioned here as a known concept. This is the first content section of the page. "Hooks" haven't been introduced yet.

---

### "The second worktree is always broken" Section

**Heading:** Good — concrete and specific.  
**Terminal demo:** Effective. The `EADDRINUSE`, database already exists, and identical env file are exactly the right failure modes.  
**Footnote:** "Port collisions. Database conflicts. Shared env files." — Good summary.  
**Issue, minor:** The `cat .env.local` block has a comment `# Same as every other worktree.` — the word "every" implies there are many worktrees in play, but the demo only showed two. Small continuity issue.

---

### "One command changes everything" Section

**Terminal output includes:**
```
Extra port 3011
Assigned Redis namespace myapp:feature-auth
Copied config/master.key
```
**Issue:** These outputs represent features (multi-port allocation, Redis namespacing, file copying) that have not been introduced yet at this point in the page. A reader encountering `Assigned Redis namespace` before understanding that Redis namespacing is even a concept the product handles will either be confused or just skim past it. Showing all features simultaneously in a demo before explaining any of them is a structural problem.

**Footnote:** "The worktree has its own port, its own database (cloned with data), its own env file with interpolated values, and secrets copied from main."  
**Issue:** "Interpolated values" — this is the first mention of interpolation (the `{port}`, `{database}` token syntax). It's used as a description rather than an introduction.

---

### "What This Makes Possible" Cards

**"Agents and automation get a real sandbox" card:**  
"A worktree created for an agent or script gets the same allocation rules as yours: isolated DB, env, and supervisor-controlled start/stop."  
**Issue:** "Supervisor-controlled start/stop" references the supervisor before the supervisor is explained. This card links to the AI Agents feature page for explanation, but a reader reading isolation top-to-bottom encounters supervisor as jargon.

**"Onboarding tracks the repo, not a wiki" card:**  
"New developers clone, run `gtl init` if the project uses Git Treeline, then `gtl setup` in a worktree."  
**Issue:** The qualifier "if the project uses Git Treeline" is doing real work here. The product is opt-in per project; new developers don't run `gtl init` if someone already committed `.treeline.yml`. The correct flow is `gtl setup`, not `gtl init`. `gtl init` is a one-time project setup step. This is a meaningful distinction that the copy obscures.

---

### "Declare it once" Section

**Intro text:** "Everything is driven by `.treeline.yml`... The excerpt below is representative: ports and database, env seeding, `commands.setup` / `commands.start`, lifecycle hooks (the same four keys the CLI implements), and editor chrome."  
**Issue:** "Editor chrome" is a term of art (UI surrounding the editor window). It means the editor title bar color here. This is fine for technical readers but is unexplained inline.

**Config block explanation panel — "Lifecycle hooks" entry:**  
"Pre-hooks abort the operation on non-zero exit; post-hooks warn and continue—matching the CLI behavior in the repo."  
**Issue:** "Matching the CLI behavior in the repo" — this is a self-referential note that adds nothing for someone reading the website who doesn't have the source code open. It's reassurance copy for contributors, not first-time readers.

---

### "Multiple Repositories, One Machine" Section

This section is well-written and concrete. The actual problem (hardcoding localhost ports in env files), the mechanism (`gtl resolve`, `{resolve:api}` template token, `gtl link` override), and the failure behavior ("setup fails with a clear error if the target is not allocated") are all stated directly.

**Issue, minor:** "`gtl resolve api` prints the base URL for project `api` on the same branch name as your current worktree" — "same branch name" matching is the default behavior. It's accurately described here but a reader who hasn't been told about the registry model may not understand why "same branch name" is the default or what happens if the API is on a different branch name. The link override (`gtl link`) is explained, which helps.

---

### Get Started CTA

**Copy:** "One file, committed to your repo. Non-users just ignore it."  
**Issue:** "Non-users just ignore it" is trying to address the adoption concern (teammates without gtl won't be disrupted), but it's poorly framed. `.treeline.yml` is not invisible — it exists in the repo and a teammate who doesn't have gtl and runs into a missing env file or unexpected config won't "just ignore it." The actual claim is that the CLI is optional for individual developers, but the config file affects the project.

---

## `/features/networking/`

### Hero

**Heading:** "One redirect URI per branch, not per port."  
**Observation:** This is the strongest headline on the site. It precisely names the problem for anyone who's ever worked with OAuth. It works without any context.

**Body:** Lists all four commands with one-line descriptions in a single sentence.  
**Issue:** The sentence tries to convey four distinct tools in one breath: "stable local HTTPS names, a pinned port, a public route on your domain when the internet must reach you, and a disposable link when you only need a demo." This is accurate but frontloads all the complexity at once. A reader who doesn't yet understand why you'd need four different solutions to "port to URL" mapping won't absorb this.

---

### "The world without it" Section

**Terminal demo:** Shows `OAuth callback received at localhost:3000 / ERR: No server listening on port 3000` — excellent, concrete, immediately recognizable.

**Footnote paragraph:** "Stripe webhooks, Mapbox keys, CI callbacks, SSO redirects · all hardcoded to a port or hostname that keeps changing between worktrees."  
**Issue:** The `·` separator is stylistically unusual (it reads as a list but looks like bullet points mid-sentence). Minor.

---

### "The URL follows the branch" Section

**Intro label:** "The URL follows the branch, not the port"  
**Issue:** This is the clearest one-line summary of what `gtl serve` does. It should appear earlier — in the hero or in the problem statement — not as a small uppercase label above the pipeline diagram. It's the conceptual unlock and it's buried.

---

### "Four Commands" Section

**`gtl serve` description:** "Safari on macOS: `gtl serve hosts sync`. Extra subdomains for Redis UIs and the like: `gtl serve alias`. See docs: gtl serve."  
**Issue:** The Safari gotcha is injected mid-description without context. A reader who uses Safari doesn't know if this is optional or required. The consequence of skipping it (`.localhost` subdomains won't resolve in Safari without a hosts file entry) is not stated. This is an important friction point delivered as a drive-by note.

**`gtl proxy` description:** "You choose the target · run it from a worktree directory and it infers the port, or specify both explicitly."  
**Issue:** The `·` mid-sentence separator is awkward. More importantly, "run it from a worktree directory and it infers the port" — how? From `.env.local`? From the registry? The mechanism of inference is not stated. It's described as magic.

**`gtl tunnel` description:** "Multiple named tunnel configs are supported; set a default with `gtl tunnel default <name>` and pass `--tunnel <name>` to override."  
**Issue:** The named tunnel config system (setting up a Cloudflare tunnel with a custom domain) is referenced as a known feature without any description of what it entails. Creating a named tunnel requires Cloudflare account setup, DNS configuration, and running `gtl tunnel setup`. This is described as a detail, not as the prerequisite it actually is.

**`gtl share` description:** "Token-gated reverse proxy over a tunnel"  
**Issue:** This is technically correct but circular — the reader is now on the `share` section, and `tunnel` hasn't been defined clearly either. "Token-gated reverse proxy over a tunnel" gives zero intuitive meaning to someone who hasn't used it.

---

### Get Started CTA

**Copy:** "Most teams only need `gtl serve`. Install once, every branch gets a name."  
**Issue:** This is actually very good — a single clear recommendation reduces decision paralysis. But it's followed immediately by `gtl serve install` as the call to action, with no mention of what `gtl serve install` does (installs a background service, CA cert, port forwarding). Someone clicking through from this page may not realize they're about to install system-level components.

---

## `/features/workflows/`

### Hero

**Heading:** "Type a PR number. Get a running app."  
**Observation:** This is the best headline on the site — specific, concrete, makes a promise with a clear mechanism. Works without any prior context.

**Body:** "A built-in supervisor lets agents control it while you watch the logs."  
**Issue:** "Supervisor" still appears without definition. Also "lets agents control it" — what kind of agents? AI agents? CI agents? Shell scripts? This sentence addresses three very different audiences without specifying.

---

### "10 Minutes of Setup for 2 Minutes of Review" Section

**Terminal demo — 8-command manual flow:**  
This is the most effective piece of copy on the site. The 8 commands + comment `# 8 commands. 10 minutes. Hope you didn't miss a step.` is a visceral demonstration of the pain. The rails-specific commands (`gh pr checkout`, `createdb`, `rails db:schema:load`) are well chosen — they feel real, not synthetic.

**Issue:** The demo assumes a Rails app. But `features/workflows` is supposed to be about all workflows, not Rails-specific. A Next.js or Node developer reading `bundle install` might mentally check out.

---

### "What This Makes Possible" Cards

**"Product and design review real apps" card:**  
"`gtl review <PR> --start` gives stakeholders a running checkout without asking them to clone, install dependencies, or edit env files."  
**Issue:** "Stakeholders" is one of those corporate-neutral terms that obscures who actually benefits here. Are these product managers? Designers? QA engineers? The page elsewhere says "Product owner asks to see PR #42" — that's more concrete. "Stakeholders" reads as filler.

**"QA exercises several branches at once" card:**  
"...`gtl release --drop-db` (or `gtl prune --merged`) reclaims disk and registry entries explicitly."  
**Issue:** "Registry entries" again without definition.

**"Agents and CI drive the same supervisor" card:**  
"Humans use `gtl start` in a terminal; scripts and agents use `gtl restart` / `gtl stop` over the Unix socket—no PID files or signals to guess."  
**Issue:** The Unix socket detail ("over the Unix socket") is accurate but means nothing to readers who don't know how process supervision works. The benefit ("no PID files or signals to guess") is the correct thing to lead with — the mechanism is the footnote, not the other way around.

---

### The Supervisor Section

**Heading:** "The supervisor: two interfaces, one process"  
**Issue:** This heading introduces the supervisor concept but the word "supervisor" has been used 5+ times before this section. A reader encountering the term for the first time earlier in the page scroll is only now getting the definition.

**Terminal demo:**  
The two-section terminal showing both the human-facing log stream and the agent-facing restart/stop commands is well-structured. This is good.

---

### "Hooks, Clone, Open, Wait" Section

**Heading:** "Hooks, Clone, Open, Wait"  
**Issue:** This heading lists four separate topics with no connective tissue. It reads like a leftover section that bundled miscellaneous features. Each of these could be its own titled subsection.

**Lifecycle hooks ordering (inline):** "Setup: allocate → env → database → `pre_setup` → `commands.setup` → editor → `post_setup`. Release: confirm → `pre_release` → free/drop → `post_release`."  
**Issue:** This ordering information is valuable and accurate (matches the README). But it's buried in a paragraph as inline em-dashes. The lifecycle ordering deserves to be a visual diagram or a numbered list — it's sequential by nature. Presented as prose, the arrows (`→`) read like decorative typography.

---

### TUI Dashboard Section

**Copy:** "Run `gtl dashboard` (or `gtl dash` / `gtl ui`) for a Bubble Tea TUI"  
**Issue:** "Bubble Tea TUI" is a reference to the Go library Bubble Tea. The target reader — a developer evaluating the tool — does not know or care what UI framework was used to build the dashboard. "Bubble Tea TUI" communicates nothing useful and sounds like jargon. The README calls it "an interactive TUI" which is adequate. The feature page could say "a full-screen terminal dashboard" and be better.

**Issue:** There is no screenshot or visual representation of the dashboard anywhere. It's described entirely in text: "all projects and worktrees in one place, refreshed every couple of seconds. Move with j/k; s start/stop, o open browser, d release..." The keyboard shortcuts make sense once you have the thing, but presenting them without a visual creates a wall of abstract instructions.

---

### Review Commands List

The feature-strip list (4 commands with descriptions) is clean and accurate. The descriptions are concrete.  
**Issue:** `gtl release --drop-db` description: "Remove worktree, drop the cloned database, clean up env files." — `gtl release` doesn't remove the worktree directory itself. It frees the allocation (port, database, env file registration). `git worktree remove` removes the directory. The README correctly separates these steps. The website description is subtly wrong, or at least imprecise.

---

### Get Started CTA

**Copy:** "Requires `gh` CLI for PR fetching. Everything else is built in."  
**Issue:** This is accurate but the `gh` dependency is mentioned only in the small-print footnote of the CTA. For someone who doesn't have `gh` installed, `gtl review 42 --start` will fail. This is a prerequisite, not a footnote. It should be in the hero or problem statement for this page.

---

## `/features/agents/`

### Hero

**Heading:** "Your agent gets the same tools you do."  
**Issue:** The headline is clever but backward for a new reader. What tools? What agent? A developer who doesn't use AI coding agents yet has no entry point. A developer who does use them may immediately recognize it, but this framing excludes the reader who's evaluating whether agents + git-treeline is relevant to their work.

**Body:** "A built-in MCP server gives agents native access to worktree management."  
**Issue:** MCP is used in the first body sentence of the page with no definition. "Native access" is vague. "Worktree management" is the first place the word "management" is used — but git-treeline manages environments, not worktrees per se (git itself creates/removes worktrees; treeline allocates resources for them). This is a subtle but real distinction the copy blurs.

**Body:** "Lifecycle hooks, structured output, and auto-generated context files make every agent environment-aware."  
**Issue:** "Environment-aware" is a marketing-style conclusion without content. In what sense? Aware of what environment? This is the kind of sentence that sounds meaningful but doesn't communicate anything specific.

---

### "Agents don't know about your environment" Section

**Terminal demo:**  
Excellent — shows the failure chain precisely: agent guesses random port, gets a port collision, guesses a database name. The progression is logical and recognizable.

---

### "Three lines in your editor config" Section

**After showing the JSON:**  
"Now the agent calls `start`, `port`, `status` as native tools under the `gtl` server namespace."  
**Issue:** "Under the `gtl` server namespace" is not meaningful to someone who doesn't know how MCP works. The mechanism — that MCP tools are organized by server namespace, and the agent calls them like function calls — is assumed knowledge. A one-sentence explanation of what MCP actually does for the agent ("instead of running shell commands, the agent can call these operations as structured function calls") would close this gap.

---

### "What This Makes Possible" Cards

**"IDE-integrated agents control the stack" card:**  
"With `gtl mcp` registered in Cursor, Claude Code, or Codex..."  
**Issue:** Three editor names are listed without context. A reader using a different editor (VS Code, JetBrains, Zed) doesn't know if they're included. The README covers Cursor and Claude Code specifically. The website implies exclusivity to these three, which may be unintentional.

**"Orchestrators can run many sandboxes safely" card:**  
"An orchestrator that launches multiple agents still reads deterministic state via `status --json` or MCP `list`, instead of scraping stdout."  
**Issue:** "Instead of scraping stdout" is precise and meaningful for someone who has built automation. But "orchestrator" is a term that only means something to people already doing multi-agent automation. This card is written entirely for readers who are already sold on agents — it doesn't invite anyone in.

---

### "Four integration surfaces" Section

**"Agent context files" subsection:**  
"The agent reads it on startup and knows which commands to run for setup, teardown, and server management."  
**Issue:** This overstates what `AGENTS.md` does. The file contains static instructions in natural language. Agents read it as context — it doesn't make agents "know" anything automatically in a programmatic sense. Whether and how much an agent respects the contents depends on the agent, the model, and the context window. The README is appropriately precise ("AGENTS.md is read by Cursor, Claude Code, and Codex"). The website copy is more assertive than the evidence supports.

---

### Get Started CTA

**Copy:** "Add the MCP config to your editor. The agent discovers everything else automatically."  
**Issue:** "The agent discovers everything else automatically" is the vaguest sentence in the CTA section of any page. What does the agent discover? How? Via what mechanism? The implicit claim is that because MCP exposes `list`, `status`, `doctor`, the agent has everything it needs. But the reader doesn't know that. "Automatically" is a word that marketing copy uses when it can't explain the mechanism — here the mechanism is worth explaining.

---

## `/use-cases/` (Index)

### Hero

**Heading:** "Problems worth solving on purpose"  
**Issue:** This is the most opaque heading on the site. It's trying to be clever but communicates nothing. Translated to plain English it means "these are real use cases, not hypothetical ones." But a reader who just arrived here needs to know what kinds of problems this page covers — not a meta-comment on the nature of the problems.

**Intro text:** "These are scenario guides—not a second feature tour."  
**Issue:** This is the writing describing itself ("scenario guides, not a feature tour") rather than just being what it says it is. If the pages are scenario guides, they don't need to announce that they're scenario guides rather than a feature tour. This is throat-clearing.

---

### Card: "Frontend + API, different repos"

**Description:** "The registry and `{resolve:…}` exist so URLs track branch intent—not last week's guess."  
**Issue:** "Branch intent" is a created phrase with no prior definition. What does "branch intent" mean? It means: the API you're pointing at should match the feature branch you're working on, not a stale hardcoded port. That's the right idea but "branch intent" obscures it.

---

### Card: "Agents, scripts, CI"

**Description:** "Automation should not scrape `gtl status` output."  
**Issue:** This is accurate and direct, but it starts with a prohibition rather than a benefit. "Scraping `gtl status` output" is a niche problem (most users won't have tried this yet). The card could lead with the benefit: "MCP, `--json` flags, and `gtl start --await` give automation stable contracts for ports, readiness, and teardown."

---

## `/use-cases/agents-automation/`

### Page Headline

**Copy:** "Scripts and agents do not read your terminal theme"  
**Issue:** This is a joke. The "terminal theme" reference is a pun on terminal color output — scripts can't read colorized terminal output reliably, so you need JSON. This is a somewhat clever observation, but as a page headline it is actively confusing for anyone who doesn't immediately get the joke. It communicates nothing about what the page covers. The subtitle below (`"Automation needs stable contracts: which port, which database name..."`) is much better — it should be the headline or the subtitle should do more work.

### Structure and Depth

This page is extremely thin. It is:
- One paragraph of intro
- One unordered list of 5 bullet points (each a command name with one sentence)
- One paragraph about `AGENTS.md`
- "Read next" links

For a "use case scenario" page, this reads like a stub. The scenario itself (what does an agent workflow actually look like end to end?) is never shown. There's no terminal demo, no walkthrough, no failure scenario. Compare to the multi-repo page, which at least shows a terminal demo and walks through the failure mode. This page is notably thinner.

---

## `/use-cases/multi-repo/`

### Lead paragraph

**Copy:** "Your SPA and your API are separate git repos. Each gets its own Git Treeline project name and its own port allocation."  
**Issue:** "Each gets its own Git Treeline project name" — this is the first time "project name" (the `project:` field in `.treeline.yml`) is introduced as a concept, but it's presented as something the reader already knows. The concept isn't defined anywhere in the use case (or on the homepage). A reader who skipped the isolation page doesn't know that project names are how the registry distinguishes between different repos on the same machine.

**Copy:** "The browser needs a base URL for XHR"  
**Issue:** XHR (XMLHttpRequest) is a deprecated-feeling term. Most developers today use `fetch`, `axios`, `React Query`, or similar. "XHR" is technically accurate (it refers to browser-side HTTP requests generally) but reads as dated. "The frontend needs a base URL for API calls" would be clearer and more broadly understood.

---

## `/use-cases/platform-pr/`

### Lead paragraph

**Copy:** "Product and QA should not need your README, a staging deploy, or a screen share of your laptop. They need a URL that shows the branch under review."  
**Issue:** This is the best customer-empathy sentence on the site. It's on a use-case sub-page, not on the homepage. It names the audience (product and QA), the current workaround (staging deploys, screen shares, READMEs), and the desired outcome (a URL). This line would strengthen the homepage hero considerably.

### Content Depth

The page is appropriately focused — it's a short scenario that maps directly to `gtl review`. The numbered list of steps is concrete. The note about `gtl share` vs `gtl tunnel` distinction is accurate.

**Minor issue:** Step 3: "If they are off your machine, `gtl share` exposes a time-bounded public or tailnet URL—different tradeoffs than `gtl tunnel`"  
The phrase "time-bounded" is used for share but `gtl tunnel` could also be considered time-bounded (you stop the tunnel when done). The distinction between share and tunnel is: share is token-gated and URL is not guessable from branch name; tunnel is open to anyone who knows the URL but stable. The use case page doesn't state this clearly.

---

## `/use-cases/integrations-urls/`

### Overall structure

This page is well-organized: it leads immediately with the decision table (which command for which situation), then explains when to combine them. This is good — it's decisional, not explanatory.

### "When you combine more than one" section

**Copy:** "Frameworks still need host allowlists; the CLI prints concrete hints for Rails, Vite, and Django when you run tunnel."  
**Issue:** This is the only mention anywhere on the website that frameworks require host allowlist configuration when using `gtl tunnel`. If you run `gtl tunnel` with Next.js, the framework will reject requests because the request host doesn't match the configured allowed hosts. This is a gotcha that affects most users. It's mentioned in one sentence at the end of the last paragraph on the page. It should be noted on the tunnel documentation or the networking feature page.

---

## `/docs/`

**Note:** The docs page is extensive (~1735 lines). A full section-by-section review of the docs exceeds the scope of this marketing copy review. Key observations on the docs:

### Getting Started section

**Copy:** "Most flows that create or use worktrees (`gtl new`, `gtl review`, `gtl setup`, `gtl clone`) expect a local HTTPS router so URLs like `https://{project}-{branch}.localhost` resolve without a port number. That router is optional in principle but required for those commands until you run the one-time install below."  
**Issue:** "Optional in principle but required for those commands" is a contradiction that will confuse readers. Either the router is required or it isn't. The README says `GTL_HEADLESS=1` is for CI where you "intentionally skip that check" — the real answer is: it's required for human-facing workflows. The docs should say that clearly.

**Docs title meta:** "CLI reference, configuration guide, Rails integration, and AI agent setup for Git Treeline."  
**Issue:** "Rails integration" suggests git-treeline is Rails-specific. The product is explicitly framework-agnostic. Rails appears in a framework example but it's not the primary integration story.

---

## Cross-Cutting Copy Patterns

### 1. "Story" as a Noun for Features
The workflows page has "the full supervisor story." This pattern — using "story" to refer to a technical feature — reads as AI-generated or marketing copy. Technical readers don't think in "stories" about process supervisors. Use "explanation" or just link directly.

### 2. Overuse of Dashes (·) as Separators
The `·` centered dot appears as a list separator in several places (`Stripe webhooks, Mapbox keys, CI callbacks, SSO redirects · all hardcoded...`). It creates visual ambiguity — is this a list? A sentence? It reads as decorative typography over semantic punctuation.

### 3. "Environment-aware," "First-class," "Native"
These are filler adjectives. "Environment-aware" (agents page), "first-class callers" (agents page), "native access" (agents page) all appear in marketing positions without concrete meaning. What specifically makes something "first-class"? What specifically makes access "native"? These phrases generate the sensation of precision without providing any.

### 4. Depth-Before-Breadth on Multiple Pages
Feature pages consistently introduce detailed configuration syntax, command flags, or edge cases before establishing the conceptual foundation. The isolation page shows `{redis_url}`, `Assigned Redis namespace`, and cross-repo `{resolve:…}` before the reader understands that git-treeline even has a registry. The agents page drops the MCP config JSON before explaining what MCP is.

### 5. Missing: A Single "How This Works" Diagram
There is no high-level architecture diagram anywhere on the site showing: worktree → `.treeline.yml` → registry → env file → running server → optional router URL. This diagram would dramatically reduce the cognitive load of the first visit. Right now, a reader has to assemble this mental model from scattered terminal demos.

### 6. The gtl serve install Requirement Has No Designated Home
The two-step install process (binary + `gtl serve install`) is the single biggest friction point for new users. It is:
- Mentioned in passing in the homepage "Get Started" section
- Not mentioned on any feature page hero
- Referenced in the docs Getting Started
- Never given a dedicated explanation with a clear "what gets installed and why" breakdown visible before installation

A first-time reader who sees the homepage Install CTA (`brew install git-treeline/tap/git-treeline`) will not realize they need a second setup step that installs system-level components.

---

## Summary: Highest-Impact Issues

| Priority | Issue | Location |
|---|---|---|
| 1 | The H1 on the homepage (`git-treeline`) communicates nothing | Homepage hero |
| 2 | `gtl serve install` two-step setup invisible until mid-page | Homepage, all feature pages |
| 3 | "Git worktree" never defined for uninitiated readers | Sitewide |
| 4 | MCP never defined | Sitewide |
| 5 | Best value-prop line buried at bottom ("Your whole team sees the work, not just the diff.") | Homepage closing CTA |
| 6 | Hero tagline "Review every branch" misrepresents the product scope | Homepage |
| 7 | Advanced features (resolve, hooks, Redis namespacing) introduced before basic concepts established | Isolation, Homepage |
| 8 | Framework host allowlist requirement for `gtl tunnel` mentioned once, in small print | integrations-urls use case |
| 9 | `gtl release` described as "remove worktree" — it doesn't remove the directory | Workflows feature strip |
| 10 | Agents use case page is a stub with no scenario, no demo, no narrative | use-cases/agents-automation |

---
---
---

# POST-REWRITE AUDIT
**Date:** April 6, 2026  
**Audit basis:** Full site rewrite by agent, evaluated against same first-time engineer lens and README as source of truth.

---

## What the Rewrite Got Right

These are real, meaningful improvements — not marginal adjustments.

### Homepage — most impactful fixes landed

| Fix | Assessment |
|---|---|
| `<title>` changed to "Isolated dev environments for every git worktree" | Correct. Highest-leverage single fix on the site. |
| JSON-LD `operatingSystem` corrected to `"macOS, Linux"` | Factual error removed. |
| Hero descriptor line added: "Worktree environment manager" | Additive without adding noise. |
| Hero subtitle: "Run every branch at the same time" | "Review" → "Run" changes the implied use case correctly. |
| Hero body: no `resolve`/`link` jargon, plain English description | First time a cold reader gets an honest description in the hero. |
| Worktree defined in problem/solution: "a separate checkout of your repo in its own directory, created with `git worktree add`" | The canonical sentence, in the right place. |
| Hero footnote: two-step install visible before Get Started | Install friction is no longer a surprise mid-page. |
| URL format fixed to `myapp-feature-auth.localhost` sitewide | Matches README's `{project}-{branch}` pattern. |
| AI Agents card headline: "Agents guess ports and config. They don't have to." | Names the problem. Replaces an abstract promise. |
| AI Agents card body defines MCP inline | First time MCP is introduced before being used as a noun. |
| `gtl release` factual error corrected on workflows page | No longer claims it removes the worktree directory. Both instances fixed. |
| "Your whole team sees the work" moved to problem/solution section | Correct placement — reward after the problem is established. |
| Networking card headline: "The URL follows the branch, not the port." | Best line on the networking page, promoted to where it does most work. |

### Networking page — substantially rewritten and improved

The networking page is the most improved feature page. The hero was replaced entirely. Four commands are now given individual sections with terminal demos, "Best for" callouts, and a decision table placed after the command explanations. The `sudo` requirement for `gtl serve install` is disclosed in the Get Started section. `gtl serve alias` is mentioned. The page now teaches the decision process instead of listing all options at once.

### Agents feature page — substantially rewritten and improved

The dedicated agents page was rewritten (unlike in the partial rewrite caught earlier). Key gains:
- A "before" terminal demo showing an agent guessing wrong ports is now present — the failure mode is named.
- "AI coding agents are the reason you end up with nine worktrees" — correct, memorable framing.
- "If you're evaluating agent integration and haven't used MCP before, start here — it's simpler than it looks." — directly addresses the prior review's top complaint about assumed MCP vocabulary.
- MCP tools list (start, stop, restart, port, status, list, doctor, db_name, config_get) is comprehensive and specific.
- JSON flag list is exhaustive and accurate.
- AGENTS.md description is honest: "how closely agents follow it depends on the model." — removes the prior overclaim.

### Use case pages — all substantially improved

- **multi-repo:** Explains the registry model, same-branch matching, and `gtl link` override correctly. `gtl unlink` and failure behavior ("setup fails with a clear error") are documented. No significant remaining issues.
- **integrations-urls:** Decision table now leads the page. Framework host allowlist requirement is acknowledged. Trust boundary framing is clear.
- **platform-pr:** "Someone needs to see PR #42 in a browser" — best use-case headline on the site. Four-step flow is accurate. `gh` dependency is present in step 1.
- **agents-automation:** Was a stub. Now has real content: MCP interface, JSON flags, readiness via `--await`, env inspection, completions.

---

## Remaining Issues

### FACTUAL — verify before shipping

**Isolation card, homepage:** "Declare what each branch needs in `.treeline.yml` — the git post-checkout hook handles the rest."

This is newly introduced copy and is not supported by the README. Git-treeline allocates resources when you explicitly run `gtl new`, `gtl setup`, or `gtl review` — not automatically on git checkout. The README documents no automatic post-checkout hook. A reader who installs git-treeline, commits `.treeline.yml`, then runs `git checkout my-branch` expecting the hook to fire will be confused and submit a bug report. Either verify this feature exists and document it properly, or change to: "the CLI allocates resources the first time you run `gtl new` or `gtl setup`."

**Agents "what changes" section:** "The declarative config and git hooks do the heavy lifting." — Same "git hooks" claim appears again. Same accuracy concern.

**Networking, `gtl serve` description:** "Safari on macOS: `gtl serve hosts sync`" — this command is not in the README. Needs verification against the codebase.

**Networking, `gtl tunnel` terminal demo:** Shows `https://feature-auth.example.dev` — uses just `{branch}.domain`, not `{project}-{branch}.domain`. If the tunnel uses the same route key as `gtl serve` (as stated in the prose), the demo should show `myapp-feature-auth.example.dev`.

**Networking, `gtl share` description:** "recipient opens the link, gets a session cookie, then sees clean URLs." — "session cookie" is a specific mechanical claim not mentioned in the README. Verify or generalize.

---

### COPY — still failing a first-time reader

**Isolation page hero body:** "Git Treeline allocates resources and runs your hooks on every branch." — Hooks introduced before hooks are defined. A reader hitting this page first has no idea what "hooks" means in this context. This was flagged in the original review; it's unchanged.

**Isolation page, "Onboarding" benefit card:** "New developers clone, run `gtl init` if the project uses Git Treeline, then `gtl setup` in a worktree." — Still wrong. If the project already has `.treeline.yml` committed (the normal case once adopted), new devs never run `gtl init`. They run `gtl setup`. `gtl init` is for the first person adding Treeline to a project. The `"if the project uses Git Treeline"` qualifier doesn't fix the logic — it describes the common case while prescribing the wrong command for it.

**Workflows hero body:** "A built-in supervisor lets agents control it while you watch the logs." — Agents are mentioned before humans in the supervisor description. The human use case (`gtl start` in a terminal, watching logs) should lead; agent/script use (`gtl restart` over Unix socket) is the secondary surface.

**Workflows, `gh` CLI dependency:** Still only appears in the CTA footnote ("Requires `gh` CLI for PR fetching"). Not mentioned in the hero, not mentioned alongside the first `gtl review` reference in the problem section. A reader who tries `gtl review 42 --start` without `gh` installed will get an error, not a helpful message pointing back to the requirement stated on the page.

**Workflows, "Hooks, clone, open, wait" section heading:** Unchanged from original. Still a list of four nouns with no verb. It does not tell the reader what this section is about. A heading like "Automation, cloning, and browser shortcuts" or even just "Additional commands" is better than an asyndeton of bare nouns.

**Agents feature page hero:** "A built-in MCP server gives agents native access to worktree management. Lifecycle hooks, structured output, and auto-generated context files make every agent environment-aware." — "environment-aware" is the same hollow adjective from the original. The body of the page earns this claim; the hero should borrow from it. Replace "make every agent environment-aware" with something like "so agents query real ports and control the server without guessing."

**Agents feature page, CTA code block:** The MCP config JSON is minified to one unreadable line: `{ "mcpServers": { "gtl": { "command": "gtl", "args": ["mcp"] } } }`. The identical config is formatted correctly earlier on the same page. This should match.

**Agents-automation use case, headline:** "Scripts and agents do not read your terminal theme" — the joke still doesn't communicate the page topic on its own. The body explains it clearly (structured data vs. parsing human-formatted output), but the H1 is the first thing a search result or a skimmer reads. "Give automation stable contracts: ports, readiness, and teardown as data" would work. The current headline is clever; it is not informative.

**Use cases index, headline:** "Problems worth solving on purpose" — unchanged. Marginally witty, minimally informative. This is the nav destination for readers choosing which scenario to read. The headline should orient, not editorialize.

---

## Revised Issue Priority Table

| Priority | Issue | Status vs. original |
|---|---|---|
| 1 | `<title>` communicated nothing | **Fixed** |
| 2 | Two-step install invisible | **Fixed** — visible in hero |
| 3 | "Worktree" never defined | **Fixed** — defined in problem/solution |
| 4 | MCP never defined | **Fixed on homepage card and agents page** — still undefined in agents page hero |
| 5 | "git post-checkout hook handles the rest" — unverified factual claim | **New issue introduced** |
| 6 | Isolation onboarding card prescribes `gtl init` for existing projects | **Still present** |
| 7 | `gh` dependency buried in workflows CTA footnote | **Still present** |
| 8 | Agents-automation headline still opaque | **Still present** |
| 9 | Agents page CTA code block minified/unreadable | **New issue introduced** |
| 10 | `gtl tunnel` demo URL uses wrong format (`{branch}` not `{project}-{branch}`) | **New issue introduced** |
| 11 | `gtl serve hosts sync` command not verified against README | **New issue introduced** |
| 12 | Advanced concepts introduced before base concepts on isolation hero | **Still present** |
| 13 | Use cases index headline doesn't orient | **Still present** |
