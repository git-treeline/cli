# Git Treeline Website Copy Review

**Reviewer**: Grok (based on full README + sampled site pages)
**Date**: 2026-04-06
**Lens**: First-time engineer who has never heard of git worktrees, Treeline, or the problem. Lands on site from Google or a tweet. Wants to know "what does this do?", "why should I care?", "is it for me?" in <30 seconds. No assumed context about worktrees, ports, DB cloning, local CA, MCP, etc.

**Grounding**: Full README read first (source of truth). It describes a sophisticated CLI for managing git worktrees with:
- Port allocation + env file generation per worktree (`.treeline.yml` committed config + user config.json)
- Optional DB cloning (Postgres/SQLite), Redis namespacing
- Supervisor for `gtl start/stop/restart` (terminal-owned, socket-controlled)
- Local HTTPS router (`gtl serve install` — sudo CA + port 443 forward for `*.localhost`)
- Networking: proxy, tunnel (Cloudflare), share
- AI/MCP integration, `gtl review`, `gtl dashboard`, resolve/link for multi-repo
- Many commands, hooks, config interpolation tokens like `{port}`, `{resolve:api}`

The site copy is slick but fails the first-time user in multiple systemic ways.

## 1. Home Page (index.html)

### Hero
- **Failures**:
  - Headline "Review every branch at the same time" is catchy but misleading/hollow. README makes clear the core problem is *running* branches (ports/DB conflicts), not just reviewing. "Review" is a specific command (`gtl review`), but headline implies simultaneous UI review without explaining the prerequisite isolation.
  - Subheadline "Isolated ports, databases, and env per worktree. Cross-repo `resolve` and `link`..." immediately drops advanced terms (`worktree`, `resolve`, `link`, cross-repo) before explaining what a worktree is or why ports matter. Assumes reader knows git worktrees and the pain.
  - "One config file, one CLI" sounds simple, but README shows 18+ commands, complex setup (`gtl serve install` with sudo twice), framework-specific wiring, config tokens. Misleading simplicity.
  - Install command shown before any explanation of what it does or the sudo step. First-time user sees `brew install` and CTA but no "what happens next?"

**Overall**: Fails to establish the core problem before solution/features. Referential and assumes knowledge.

### Problem/Solution Section
- **Failures**:
  - "Worktrees are easy to create. Hard to run." — good start, but "worktrees" not defined. First-timer doesn't know what they are or why they'd create one.
  - Terminal demos are good (EADDRINUSE, DB collision vs `gtl new`), but the "with" terminal immediately shows advanced output (cloned DB, named URL via gtl serve). Introduces too many concepts at once (worktree creation, port allocation, DB cloning, HTTPS router).
  - No explanation of *why* this matters for AI agents or parallel dev until later. The why from README (AI agents in worktrees needing to run apps) is buried.

### Feature Highlights (Isolation, Networking, Workflows, AI Agents cards)
- **Failures**:
  - **Structural/Mobile**: Feature cards alternate visual left/right. On mobile, visuals (terminals, diagrams) likely appear before copy or in awkward order. Demos precede explanations.
  - **Isolation card**: Jumps to `{resolve:other-project}` and `gtl link` in the description. These are advanced cross-repo features; not core to "zero conflicts". Vague on what "env file" means in practice.
  - **Networking card**: "OAuth redirects, Stripe webhooks..." slot animation is clever but assumes reader has those problems. Copy says "branch-stable HTTPS names" but doesn't explain the `gtl serve` setup cost (sudo, CA trust, browser differences for Safari).
  - **Workflows card**: "Type a PR number. Get a running app." Good, but `gtl review`, `gtl dashboard`, supervisor mentioned without context. "Hooks in .treeline.yml" assumes user knows YAML config.
  - **AI Agents card**: MCP JSON snippet shown before explaining what MCP is or why an agent would need `gtl start/stop`. Assumes familiarity with Cursor/Claude Code agents working in worktrees. "AGENTS.md" dropped without explanation.
  - All cards link to feature pages but the home copy already overloads with details.

### Get Started Section
- **Failures**:
  - Acknowledges the "one-time local HTTPS setup" and sudo prompts, which is honest, but frames it as expected without addressing the friction ("expect sudo prompts" feels defensive).
  - Sends to "/docs/#first-time-setup" — good, but the section is after features, so user has already seen advanced copy.
  - Trust bar is good ("No runtime dependency"), but comes late.

### Closing CTA & Footer
- "Every branch, running. Your whole team sees the work, not just the diff." — aspirational but hollow. Doesn't tie back to concrete value (e.g. "run 3 AI agent branches side-by-side without port hell").
- Footer links are fine.

**Home Overall**: Strong visuals and terminal demos, but copy starts too far down the learning curve. Assumes git worktree knowledge, introduces tokens/commands prematurely, mixes concrete (ports) with referential (resolve/link). Marketing tone in places ("Zero conflicts", "just work").

## 2. Features Pages

### /features/isolation/
- Hero good: "Two branches. Two apps. Zero conflicts."
- "The second worktree is always broken" section excellent concrete example.
- But quickly dives into `.treeline.yml` examples, interpolation tokens, DB cloning details before basic "what is a worktree?" or "how do I install?"
- Assumes reader understands port allocation policy from user config vs project config.
- Good use of diagrams/terminals, but copy is dense with specifics (pattern: "{template}_{worktree}").
- Fails first-timer by not having a "What is git worktree?" primer.

### /features/networking/
- Strong problem statement ("Every integration breaks when you change ports").
- Explains 4 tools (serve, proxy, tunnel, share) — but the page is long and technical.
- Copy uses phrases like "one redirect URI per branch, not per port" — good, but explains `gtl serve install` late.
- Safari note and Cloudflare dependency mentioned — honest but adds to perceived complexity.
- Assumes knowledge of OAuth, webhooks, Cloudflare tunnels.

### /features/workflows/ and /features/agents/
- Similar issues: jump into `gtl review`, supervisor, MCP without foundational explanation.
- Workflows page covers PR review flow well but buries the supervisor mechanics.
- Agents page focuses on MCP/JSON/AGENTS.md — valuable for target audience (AI users) but alienating for general engineers.

**Features Overall**: Each page assumes you've read the home or README. Good detail for users who "get it", poor onboarding for new users. Too much depth too early.

## 3. Docs (/docs/)
- Very long single page (~1700 lines in built version).
- Sidebar is comprehensive but the content is extremely technical — CLI reference, full config spec, many examples.
- Good as reference, poor as "first landing".
- Copy is mostly accurate to README but dry and assumes deep familiarity.
- Sections like "getting-started" likely repeat the sudo/CA story.
- **Failure**: No gentle intro or "if you're new, start here" that avoids the full spec.

## 4. Use Cases (/use-cases/)
- Scenarios for multi-repo, integrations, PR review, agents.
- Concrete and helpful, but again assumes the tool is understood.
- Subpages (integrations-urls, platform-pr, etc.) go deeper.
- Good for validation once user is interested, weak for initial acquisition.

## Systemic Issues Across Site

1. **Assumed Knowledge**:
   - Git worktrees never explained simply ("check out multiple branches side-by-side" from README is perfect but missing).
   - Terms: worktree, .treeline.yml, gtl serve, resolve, link, supervisor, MCP dropped without intro.
   - AI agent context assumed (the README positions this as key value prop).

2. **Order of Information**:
   - Features/demos before "why" or "what".
   - Advanced (cross-repo resolve, DB cloning, hooks) mixed with basics.
   - Setup friction (sudo, CA, browser) acknowledged but not mitigated in copy.

3. **Tone**:
   - Mix of direct ("port collision") and marketing ("Zero conflicts", "just work", slot reel for integrations).
   - Some hollow aspirational phrases ("Your whole team sees the work, not just the diff").

4. **Mobile/Structure**:
   - Feature cards with left/right layout likely break hierarchy on mobile (visuals first?).
   - Long pages without clear scannability for first-timers.
   - Code-heavy copy everywhere.

5. **Value Proposition Clarity**:
   - Core value (isolated runnable worktrees for parallel dev + AI agents) is there but diluted by breadth.
   - Doesn't clearly answer: "Do I need this if I don't use AI agents or multi-worktree?"
   - Doesn't contrast with alternatives (manual port changing, tmux, Docker, etc.).

6. **Hollow or Vague Spots**:
   - Many "named URL", "stable", "just works" without showing the before/after clearly in all sections.
   - Trust signals good but late.

## Recommendations (diagnostic only, no rewrites)
- Add a short "What is this?" section early that explains git worktree + the port/DB problem in plain English, citing the README's "AI coding agents work in worktrees" paragraph.
- Move advanced features (resolve/link, MCP) to later sections or dedicated deep-dive pages.
- Strengthen the "why" with specific pain examples from README.
- Consider a simpler landing or guided tour.
- Ensure mobile stacking puts explanations before or beside visuals.
- Docs could have a "New to Treeline?" onboarding path.

The site looks polished and the copy is accurate where it counts, but it speaks to the initiated rather than converting the curious first-time engineer. The README itself is clearer on value than the marketing site in many places.

**End of Review**
