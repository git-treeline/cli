# Git Treeline Website Copy Review (Diagnostic Only)

## Scope and method

- Ground truth used: `git-treeline` README and command behavior described there (allocation model, setup/release lifecycle, networking modes, supervisor semantics, MCP/JSON surfaces).
- Review target: every website HTML page and major copy section (hero, explainer blocks, feature cards, command tables, CTAs, docs sections, repeated footer/nav copy).
- Lens: first-time engineer with no prior context, trying to decide what the tool does and whether to adopt it.
- This document is intentionally **not** a rewrite pass. It catalogs failures in clarity, sequencing, assumptions, and trust.

## Product reality baseline (used to evaluate copy)

- Core promise is operational: deterministic per-worktree resources (ports, env, optional DB/Redis) + registry-backed commands (`new`, `setup`, `release`, `status`, `resolve`, `link`).
- Networking is four distinct boundaries with different trust models (`serve`, `proxy`, `tunnel`, `share`) and distinct prerequisites/tradeoffs.
- Workflows (`review`, `switch`, supervisor commands, dashboard) are orchestration convenience on top of that contract.
- Agent support is an interface story (MCP + `--json` + generated context), not a separate product.

---

## `/` (home)

### Hero

- **Vague/hollow:** “One config file, one CLI” is slogan-level and does not tell a first-time reader what concrete actions happen (allocate/write/setup/start/release).
- **Assumed knowledge:** Starts with `resolve` and `link` tokens in the first body paragraph before the reader knows what a “project”, “registry”, or “cross-repo” means.
- **Depth-before-concept:** Introduces advanced cross-repo interpolation in the opening frame instead of first establishing the simple core problem (port/env collisions).
- **AI/marketing tone:** “Review every branch at the same time” is catchy but abstracts away the actual mechanism.

### Problem/Solution block

- **Good concrete core**, but still has issues:
- **Assumed knowledge:** “named URL via gtl serve” appears without stating that route keys are usually `{project}-{branch}` and that `serve install` prerequisites exist.
- **Potential mismatch/confusion:** Example URL `https://feature-auth.localhost` can conflict with docs elsewhere that show `https://{project}-{branch}.localhost`.

### Feature highlight: Isolation card

- **Depth-before-concept:** Jumps to `{resolve:other-project}` before fully explaining baseline isolation in plain language.
- **Assumed knowledge:** Mentions Redis namespace and resolve semantics without defining when those are optional vs default.
- **Mobile structure risk:** Visual demo appears before explanatory copy in DOM order; on mobile this forces users through output snippets before conceptual framing.

### Feature highlight: Networking card

- **AI/marketing tone:** Rotating slot copy (“OAuth redirects / Stripe webhooks / Mapbox tokens”) is attention-grabbing but reads ad-like and obscures decision logic.
- **Vague/hollow:** “without juggling four unrelated tools” names pain but not selection criteria.
- **Depth-before-concept:** Four command names are dropped before establishing the simple mapping of “local name vs fixed local port vs public stable URL vs disposable share URL.”
- **Mobile structure risk:** Like other cards, demo-first order can degrade comprehension on narrow screens.

### Feature highlight: Workflows card

- **Potential factual ambiguity:** “Type a PR number. Get a running app.” underplays dependency (`gh` required) and repo-context requirement (run from main repo).
- **Hollow phrasing:** “full supervisor story” is referential language, not explanatory language.

### Feature highlight: AI Agents card

- **Assumed knowledge:** “MCP exposes start/stop/port/status” assumes reader knows MCP and why tool calls matter.
- **Depth-before-concept:** Jumps directly to protocol surface instead of first clarifying the baseline failure mode (agents guessing ports/env).
- **Hollow:** “keeps Cursor, Claude Code, and Codex aligned” states outcome without explaining practical behavior.

### Get started section

- **Strong trust signaling**, but:
- **Cognitive density:** Packs cert trust, port forwarding, sudo prompts, and command prerequisites into one paragraph; first-time users likely skim and miss critical conditions.
- **Terminology burden:** “local HTTPS setup” / “branch URLs” / “cert warnings” in one block before readers see the simpler install→init→new sequence.

### Trust bar

- **Hollow:** “No runtime dependency. CLI only.” and “No lock-in” are claims without immediate proof or examples.
- **Missed specificity:** Could anchor to concrete file artifacts (`.treeline.yml`, user config, registry) but does not.

### Closing CTA

- **Vague:** “Every branch, running” repeats brand line without adding decision-supporting detail.

### Footer

- **No major failure** beyond repetition fatigue; copy is functional.

---

## `/features/isolation/`

### Hero

- **Strong concept**, but:
- **Assumed knowledge:** “runs your hooks on every branch” appears before hook semantics are introduced.
- **Depth-before-concept:** Bundles port, DB, Redis, env, hooks in one sentence; too many abstractions for a first pass.

### “The second worktree is always broken”

- **Mostly effective**; concrete pain narrative lands.
- **Minor assumption:** Rails + npm mixed examples can feel stack-specific without explicit “examples across stacks” framing.

### “One command changes everything”

- **Potential overcompression:** Output list implies all features happen universally (extra port, DB clone, Redis namespace, copied secrets), but several are config-dependent/optional.
- **Assumed defaults:** “Copied config/master.key” reads like expected behavior, but only occurs with `copy_files`.

### “What this makes possible”

- **AI/marketing tone:** Benefit framing drifts into outcomes without enough mechanism detail.
- **Hollow moments:** “Onboarding tracks the repo, not a wiki” is high-level rhetoric that needs one concrete before/after.

### “Declare it once. handles the rest.”

- **Better than most sections**, but still:
- **Depth load:** Big config excerpt + six concept blocks in one scroll chunk can overwhelm first-time readers.
- **Assumed knowledge:** Editor support claims broaden to tools where behavior differences are not clearly bounded in this section.

### Multi-repo subsection

- **Strong practical value**, but:
- **Concept jump:** Introduces same-branch matching and link overrides quickly; needs one simple “default path first, override second” progression.
- **Failure mode underexplained:** “setup fails with a clear error” is good, but no concrete example of what that failure means for the user’s workflow.

### Get started CTA

- **Vague:** “Non-users just ignore it” is socially reassuring but not technically explanatory.

---

## `/features/networking/`

### Hero

- **Depth-before-concept:** Leads with four boundaries and four commands in one sentence before teaching how to pick.
- **Assumed knowledge:** “trust boundary” concept appears repeatedly but is not introduced in beginner terms.
- **AI/marketing tone:** “not four unrelated tools” sounds defensive/positioning-heavy.

### “Every integration breaks when you change ports”

- **Concrete and useful**, but:
- **Narrow first example:** OAuth-only framing may hide that many teams first hit this with webhook providers.

### “The URL follows the branch”

- **Potential mismatch/confusion:** Uses `feature-auth.localhost` while other docs emphasize `{project}-{branch}.localhost`.
- **Assumed knowledge:** “Register specific redirect URIs per branch” presumes readers know provider limits and policy constraints.

### “What this makes possible”

- **Partly hollow:** Benefit cards speak in outcomes (“hand someone a link,” “external services reach you”) with limited operational caveats.
- **Security framing under-specified:** Mentions trust boundary but not concrete implications (who can access, what auth applies, lifecycle risks).

### “Four commands, one job” intro

- **Good structure idea**, but:
- **Cognitive overload:** Too many command-level distinctions introduced in compact prose; beginner decision tree remains implicit.

### `gtl serve` subsection

- **Potential factual risk:** Mentions `gtl serve alias`, but this is not surfaced in top-level README command reference; may read as undocumented or unsupported from user perspective.
- **Assumed prerequisites:** “install once, forget it” softens operational impact (sudo prompts, cert trust, service install).

### `gtl proxy` subsection

- **Mostly clear**, but:
- **Assumed ecosystem:** “Optional TLS via mkcert” appears without warning about mkcert install/trust state and fallback behavior.

### `gtl tunnel` subsection

- **Useful framing**, but:
- **Understates requirements:** “public HTTPS on your domain” can imply trivial setup; domain ownership + Cloudflare setup complexity is downplayed.

### `gtl share` subsection

- **Potential ambiguity:** “token-gated reverse proxy” is security-relevant but lacks one clear sentence on recipient flow and risk limits.
- **Decision boundary still fuzzy:** Relationship between `share` and `tunnel` is explained, but not with a fast “if X then use Y” checkpoint.

### Comparison table + CTA

- **Helpful**, but:
- **Hollow CTA:** “Most teams only need serve” may overgeneralize and contradict earlier nuance.

---

## `/features/workflows/`

### Hero

- **Assumed knowledge:** “built-in supervisor lets agents control it while you watch logs” assumes user understands why remote control of foreground process matters.
- **Tone drift:** “Type a PR number. Get a running app.” is catchy but oversimplifies prerequisites and constraints.

### “10 minutes of setup for 2 minutes of review”

- **Effective narrative**, but:
- **Potential strawman risk:** The manual workflow may feel exaggerated to experienced teams with scripts.

### “One command replaces all of that”

- **Potential factual overreach:** Implies total replacement while still requiring `gh`, proper config, optional serve setup for named URLs.

### “What this makes possible”

- **Hollow phrasing pockets:** “first look” / “real apps” language is persuasive but not always tied to command-level constraints.
- **Assumption:** Stakeholder access story still conflates local URL and remote-sharing path unless user reads networking sections.

### Supervisor section

- **Strong mechanistic content** overall.
- **Complexity cliff:** Introduces Unix socket control with minimal newcomer framing.

### “Hooks, clone, open, wait”

- **Density issue:** Four distinct concepts in one compact section; scanning readers may miss critical trust-boundary nuance around `clone`.

### TUI dashboard section

- **Good utility detail**; minimal copy issues.

### Review commands table

- **Possible inaccuracy:** `gtl release --drop-db` is described as removing worktree + DB + env files in nearby copy, but release generally frees allocations/resources; worktree removal is a separate git action in main README flow.

### Final CTA

- **Too terse for first-time context:** “Requires gh” appears late; this caveat matters earlier.

---

## `/features/agents/`

### Hero

- **Assumed knowledge:** “built-in MCP server” and “environment-aware” are unexplained for readers new to agent tooling.
- **Depth-before-concept:** Jumps to interfaces before anchoring the basic problem in one plain sentence.

### “Agents don’t know your environment”

- **Strong concrete failure narrative**; this section is one of the clearest on site.

### “Three lines in your editor config”

- **Potential overpromise:** “No guessing” implies full elimination of failure modes, while misconfigured project/setup still possible.
- **Assumed knowledge:** “native tools under server namespace” is protocol language, not beginner language.

### “What this makes possible”

- **Mostly substantive**, but:
- **Terminology density:** Supervisor, shell runners, hooks, orchestrators all compressed in one visual block.
- **Hollow edge:** “first-class callers” reads slogan-y without user-task grounding.

### “Four integration surfaces”

- **Good taxonomy**, though:
- **Potential overload:** Surface-by-surface detail is strong but long; novice readers may lose hierarchy.
- **Assumed baseline:** Reader still needs pre-existing understanding of setup/release lifecycle.

### “Connect your agent” CTA

- **Over-simplified:** “discovers everything else automatically” can set unrealistic expectations around framework-specific setup.

---

## `/use-cases/` (hub)

### Hero and framing paragraphs

- **Meta-referential copy:** “not a second feature tour” and “problem you are solving” is internally coherent but abstract for someone still learning product basics.
- **Assumed knowledge:** Requires reader to already know which category their problem fits.

### Card: Frontend + API

- **Strong technical reality**, but:
- **Depth-before-concept:** Mentions `{resolve:...}` quickly for users who may not yet understand registry model.

### Card: Integrations/URLs

- **Command list overload:** four networking commands listed with minimal beginner triage.

### Card: Platform/PR

- **Potential ambiguity:** “stakeholders need a running app” can imply simple remote access path; details require another page.

### Card: Agents/automation

- **Good caution about parsing output**, but still assumes JSON-contract literacy.

---

## `/use-cases/multi-repo/`

### Hero and lead

- **Strong and concrete**.
- **Assumed knowledge:** “project name” and “allocation” terminology arrives before beginner definition.

### “What breaks without a registry”

- **Effective**, actionable failure framing.

### “What Git Treeline does”

- **Depth-before-concept:** Same-branch default + link overrides + setup-time token resolution in one block.
- **Potential complexity wall:** Too many branching rules without a simple default example first.

### Terminal snippet

- **Clear**, but:
- **Assumed setup state:** Doesn’t remind user both projects must already be allocated for resolve.

### “URLs vs localhost”

- **Conceptually useful**, but mixes browser origin strategy with server/tooling strategy quickly.

---

## `/use-cases/integrations-urls/`

### Hero and lead

- **Strong framing** of constraints and trust boundaries.
- **Potential jargon load:** “trust boundaries” repeated without plain-language bridge.

### “Pick a command” table

- **Helpful structure**, but:
- **Assumption risk:** “provider only allows localhost:3000” is one scenario; many providers allow only fixed public callback URLs, which could be elevated earlier.

### “Combine more than one”

- **Good real-world guidance**, but:
- **Dense sentence construction:** packs multiple command interactions + framework caveats in one paragraph.

### Read-next links

- **Functional**, no major issue.

---

## `/use-cases/platform-pr/`

### Hero and lead

- **Strong user-centered framing**.
- **Minor overreach:** “should not need your README” is rhetoric; practical access still depends on networking choice and environment readiness.

### “The flow”

- **Mostly concrete**, but:
- **Assumed knowledge:** distinction between local `open` and external `share` path can still be missed in quick scan.

### Hooks paragraph

- **Hollow/reference-heavy:** “without forking Treeline” is product-language more than task-language.

---

## `/use-cases/agents-automation/`

### Hero and lead

- **Strong premise**, though:
- **Assumed sophistication:** “stable contracts” language may be clear to platform engineers but not general app developers.

### “Interfaces that scale”

- **Dense and good**, but:
- **Order issue:** starts with MCP rather than first showing simplest shell `--json` path for broader audience.

### AGENTS.md paragraph

- **Useful but compressed:** too many agent products and behaviors in one sentence.

### Read next

- Functional.

---

## `/docs/`

## High-level docs copy diagnosis

- **Strength:** highly concrete, command-rich, and closer to source-of-truth than marketing pages.
- **Primary failure mode:** information architecture overload for first-time readers; too many advanced details before baseline mental model is stable.
- **Secondary issue:** occasional drift/ambiguity between docs wording and broader product wording on route shapes, release semantics, and tunnel config keys.

### Header + nav scaffolding

- **Vague top-line:** “Everything you need…” is generic and doesn’t orient by progression (install → init → setup → run → release).
- **Mobile structural risk:** large jump-menu with many deep anchors can be cognitively heavy on first visit.

### Getting started

- **Strong detail**, but:
- **Complex first paragraph:** introduces binary behavior, env write timing, command set, local HTTPS requirement, and optionality caveat all at once.
- **Potential contradiction feel:** “optional in principle but required for those commands” is accurate but cognitively awkward.

### Configuration + full reference

- **Strong reference utility**, weak onboarding utility.
- **Depth-before-concept:** long field catalog appears before readers likely understand minimal required config.

### Interpolation tokens

- **Useful table**, but assumes comfort with tokenized env templating and cross-project concepts.

### Lifecycle hooks

- **Clear and concrete**, minimal issues.

### Cross-worktree resolve

- **Good conceptual content**, but same-branch default/link overrides still likely too advanced for early docs flow position.

### CLI reference

- **Comprehensive but high cognitive load** in one giant block.
- **Potential trust issue:** new users cannot easily distinguish “start here” commands from advanced/rare commands.

### User config

- **Useful details**, but:
- **Depth-before-concept:** user-level global tuning appears before many users need it.

### Framework integration

- **Generally strong and concrete**.
- **Assumption:** framework-specific caveats are accurate but can imply broader “works automatically” expectations if skimmed.

### Setup commands

- **Very good practical guidance**.
- **Potential overload:** sequence + fixup pattern + re-setup behavior in one section can feel heavy without summary checkpoints.

### Database management

- **Clear, actionable**.
- **Minor assumption:** readers already know whether DB cloning was configured.

### Process supervisor

- **Strong mechanics**, but:
- **Assumed familiarity:** Unix socket details and control semantics may be too deep for first pass.

### TUI dashboard

- **Good command discoverability**, minimal issues.

### Editor customization

- **Useful**, though:
- **Peripheral for first-time value:** sits in main path with equal visual weight to core setup topics.

### AI agent setup + MCP server

- **Comprehensive and generally accurate**.
- **Depth-first issue:** this level of protocol detail can distract from core product understanding for non-agent users.

### Networking overview + `serve`/`proxy`/`tunnel`/`share`

- **Strong command-level content**, but:
- **Consistency risk:** some copy references old tunnel config shape (`tunnel.name` / `tunnel.domain`) while other source-of-truth descriptions emphasize named tunnel maps/default selection, which can create trust friction.
- **Potential confusion:** route examples and naming conventions vary across pages (`feature-auth.localhost` vs `{project}-{branch}.localhost` family).

### Route keys + networking config reference

- **Good explanation**, but:
- **Advanced positioning:** too deep for early understanding; better as secondary detail.

### Footer

- Functional.

---

## `/networking/` (redirect page)

### Redirect-only content

- **No material copy issue** (utility redirect).

---

## Cross-site systemic failures

### 1) Concept sequencing breaks for first-time readers

- Advanced concepts (`resolve`, `link`, MCP, hooks, trust boundaries) appear too early and too often before the simple core loop is locked in.
- Multiple pages introduce command names before users can answer: “what does the tool fundamentally do on my machine?”

### 2) Command-name density exceeds comprehension

- Home + feature pages frequently stack 3-6 command names in one paragraph.
- This reads as capability listing, not guidance.

### 3) Mobile narrative order often puts demos before explanation

- Repeated feature-card structure shows visual/terminal artifacts before plain-language conceptual framing.
- For narrow screens, this can make sections feel cryptic and “for insiders.”

### 4) Referential language weakens trust

- Phrases like “full story,” “in depth,” “works automatically,” “first-class callers,” and “without unrelated tools” often point at value without fully communicating mechanism.

### 5) Inconsistent specificity on naming and lifecycle

- URL shape messaging varies across pages (`branch.localhost` vs `project-branch.localhost` style).
- Release semantics are occasionally framed as if worktree removal is included, whereas main lifecycle is generally “release resources, then remove worktree separately.”

### 6) Security/trust model is present but fragmented

- Security-relevant behavior (token gates, public exposure, local CA trust, route stability, teardown behavior) is explained, but scattered and unevenly prioritized.
- First-time readers may not confidently know “what is publicly reachable, by whom, for how long.”

---

## Priority risk list (for product decision-making)

1. **Understanding risk:** New users fail to grasp core value quickly due to premature advanced terminology.
2. **Adoption risk:** Command overload and dense prose create “looks powerful but hard” perception.
3. **Trust risk:** Small inconsistencies in URL/config semantics can reduce confidence in operational docs.
4. **Mobile comprehension risk:** Demo-first section order degrades scanability and concept uptake.
5. **Security comprehension risk:** Networking trust boundaries are explained, but not in a consistently user-decision-first frame.

