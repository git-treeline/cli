## [Unreleased]

- **`gtl install` command** — single command to set up git-treeline for a project and machine. Creates `.treeline.yml` if missing, creates user config, installs the post-checkout hook, allocates ports, and optionally enables the local HTTPS router (prompted with a docs link). Safe to run on first clone or any time after — every step is idempotent. Replaces the fragmented `gtl init` → `gtl setup` onboarding flow and works for both first-project setup and later joiners.
- **Core worktree commands no longer require HTTPS router** — previously `gtl setup`, `gtl new`, `gtl clone`, and `gtl review` would hard-fail if `gtl serve install` hadn't been run. Now they print a warning and proceed, so developers can use localhost-only workflows without the HTTPS stack. The warning points to `gtl install` or `gtl serve install`.
- **Explicit router intent** — `router.mode` now records whether HTTPS routing should be prompted, disabled, or repaired automatically. `warnings.router: false` still suppresses reminders, but no longer changes env interpolation or disables router URLs by itself.
- **Cleaner start URLs** — `gtl start` now prints the localhost URL alongside the router URL and no longer prints the tunnel command hint in start output. Use `gtl routes` or `gtl tunnel` when you need tunnel URLs.

## [0.38.1]

- **`gtl serve alias` auto-detects port** — running `gtl serve alias <name>` from a worktree directory now looks up the allocated port automatically. If the allocation has multiple ports, an interactive selector is shown. Explicit `gtl serve alias <name> <port>` still works as before.

## [0.38.0]

- **Project name drift detection** — `gtl start`, `gtl setup`, and `gtl env sync` now detect when the `project` field in `.treeline.yml` doesn't match the registry allocation. On drift, the user is prompted to revert `.treeline.yml` to the registry name (default: yes). Declining aborts with a hint to release all worktrees first, then re-setup. `gtl doctor` reports drift diagnostically (text and `--json`).
- **Worktree-aware `review` and `new`** — `gtl review` from inside a worktree now prompts to switch the current worktree to the PR branch instead of blocking. `gtl new` from a worktree warns about the target path and asks for confirmation; `--force`/`-f` skips the prompt. The old `worktreeGuard` hard error is removed.
- **TUI dashboard expansion** — the interactive dashboard now shows router URLs, tunnel URLs, and supports env sync, worktree creation, and release actions directly from the TUI.
- **`gtl stop --kill`** — shuts down the supervisor entirely instead of keeping it alive for resume. Also exposed as `kill: true` in the MCP `stop` tool.
- **Fix WebSocket forwarding** — the router and proxy now forward the original `Host` as `X-Forwarded-Host` instead of rewriting `Host`, fixing WebSocket upgrades and static asset serving behind the HTTPS router.
- **`{tunnel_host}` env token** — the `env:` block now supports `{tunnel_host}`, which resolves to the bare tunnel domain (e.g. `gtltunnel.dev`). Use it for cookie domains, CORS wildcard patterns, or any config that needs the tunnel base domain without the project-branch subdomain.
- **Cleaner CLI errors** — domain and state errors no longer print the full usage block. Only flag/argument errors show usage.
- **Fix router service surviving Homebrew upgrades** — `gtl serve install` now stores the stable symlink path (e.g. `/opt/homebrew/bin/gtl`) in the launchd plist instead of the versioned Cellar path. Previously, `brew upgrade` would remove the old version directory and break the router on reboot. The health check binary comparison also resolves symlinks before comparing.

## [0.37.0]

- **`gtl routes` command** — shows routing URLs for the current worktree. Prints the HTTPS router URL (or localhost fallback) for every allocated port, plus tunnel URL when configured. Supports `--json` for scripting and MCP consumption.
- **MCP write operations** — the MCP server now supports `setup`, `new`, `link`, `unlink`, `config_set`, and `env_sync` tools. Read tools expanded with `resolve`, `env`, `where`, and `routes`. Tool descriptions improved for AI agent consumption.
- **Shared `proxy.BuildRouterURL`** — router URL construction extracted from three call sites into a single testable function. Eliminates duplication across `open`, `resolve`, `routes`, and MCP tools.
- **`internal/envparse` package** — env file parsing extracted from `cmd/env.go` into a shared package with its own test suite. Used by both the CLI and MCP tools.
- **Hardened test coverage** — added tests for database adapters (PostgreSQL, SQLite), service health checks, setup orchestration, worktree operations, tunnel cert management, doctor checks, release safety, and framework detection. +3,500 lines of test code.

## [0.36.0]

- **Start hooks** — `.treeline.yml` now supports named hooks with `pre_start` and `post_stop` commands, activated via `gtl start --with <name>`. Hooks with `auto: true` run on every fresh `gtl start` without needing `--with`. Both `pre_start` and `post_stop` accept a single string or an array of strings for multi-step hooks. Hooks are for quick environment setup/teardown (registering aliases, seeding data, setting debug flags), not for managing long-running processes. `pre_start` hooks abort on failure. `post_stop` hooks run in reverse order on supervisor exit (Ctrl+C) and log errors without blocking. Hooks support `{port}` interpolation and are not re-fired on `gtl restart` or resume.

## [0.35.6]

- **Fix pfctl reload on reinstall** — `gtl serve install` no longer fails with "pfctl reload failed: exit status 1" when pf is already enabled. The reload now separates `-f` (load rules) from `-e` (enable pf) so the "pf already enabled" exit code doesn't abort the install.

## [0.35.5]

- **Router version mismatch warning** — after upgrading the CLI, `gtl serve status` and `gtl start` now warn if the running router is an older version and prompt to run `gtl serve install`. The router records its version on startup; the CLI compares on each invocation.
- **`gtl serve install` is now an upgrade command** — running `install` when a router is already running restarts it with the new binary. No need to `uninstall` first. All fix hints throughout the CLI now recommend `gtl serve install` instead of `uninstall && install`.
- **Hot-reload user aliases** — `gtl serve alias` changes are now picked up by the running router within 5 seconds. Previously the router used a stale in-memory config and required a full reinstall to see new aliases.

## [0.35.4]

- **Set `X-Forwarded-Proto` and `X-Forwarded-For` on proxied requests** — the `gtl serve` router and `gtl proxy` now set standard forwarding headers when proxying to backends. Fixes CSRF origin mismatches in Rails and other frameworks that compare the `Origin` header against `request.base_url` — previously the backend saw `http://` while the browser sent `https://`.

## [0.35.2]

- **`{router_url}` and `{router_domain}` env tokens** — the `env:` block in `.treeline.yml` now supports `{router_url}` (e.g. `https://salt-feature.prt.dev`) and `{router_domain}` (e.g. `prt.dev`). Use `{router_domain}` for composable values like Rails `config.hosts << ".{router_domain}"` or cookie domains. Use `localhost:{port}` for your app's canonical host — see README for guidance.

## [0.35.1]

- **Fix pfctl anchor install ordering** — `gtl serve install` wrote the pf anchor file *after* the `pfctl -n` dry-run validation, so the dry-run failed with "No such file or directory" and the entire install was skipped. The anchor is now written before validation.

## [0.35.0]

- **Complete worktree config loading** — all commands now load `.treeline.yml` from the worktree you're in: `gtl open`, `gtl tunnel`, `gtl db`, `gtl release`, `gtl refresh`, plus `--start` in `gtl new`/`gtl review`. Branch-specific config overrides work everywhere. Commands that create worktrees (`gtl new`, `gtl review`) read from the main repo before creation, then switch to the new worktree's config for setup and `--start`.
- **Env sync on `gtl start`** — fresh `gtl start` (no supervisor running) now re-reads `env:` from `.treeline.yml` and updates the env file (e.g. `.env.local`) before starting the server. Editor settings are also synced. No more stale env files after editing `.treeline.yml`.
- **Env sync on `gtl restart`** — `gtl restart` syncs the env file and pushes updated environment variables to the supervisor's in-memory env map before restarting. Both the env file and the child process env are up to date.
- **Stale command detection** — `gtl restart` now warns if `commands.start` in `.treeline.yml` differs from what the supervisor is running, with a hint to Ctrl+C and `gtl start` fresh.
- **`gtl env sync` command** — new subcommand that re-syncs the env file and editor settings from `.treeline.yml` without starting a server. For users who start their server outside of `gtl`.
- **`gtl env show` subcommand** — `gtl env` is now a parent command. The previous `gtl env` behavior is available as `gtl env show` (or just `gtl env` with no subcommand, which defaults to show). `gtl env --json` and `gtl env --template` continue to work.
- **`gtl worktree` command** — prints the worktree path for the current directory. Useful for scripting and agent tooling.
- **Default domain `prt.dev` for new installs** — new installs default to `prt.dev` instead of `localhost`. Existing installs are not affected — the CLI detects pre-existing configs and preserves `localhost`. `gtl serve install` now persists `router.domain` to `config.json`. See `docs/DOMAIN_MIGRATION.md` for details.
- **pfctl anchor recreation fix** — `gtl serve install` now checks that the pf anchor file exists, not just the pf.conf marker. Fixes "port forwarding skipped" errors after running `gtl serve uninstall` then `gtl serve install`.
- **`gtl refresh` uses worktree `port_count`** — `gtl refresh` detection now reads `port_count` from the worktree's `.treeline.yml` instead of the main repo's. Branch-specific port count changes are now detected correctly.

## [0.33.0]

- **`gtl worktrees`** — interactive TUI picker for worktrees, grouped by project. Arrow keys to navigate, Enter to print path. Alias: `gtl wt`.
- **`gtl where <branch>`** — print the path to a worktree by branch name. Searches across all projects in the registry. Use `project/branch` to disambiguate when multiple projects have the same branch.
- **Zero-config worktrees** — `gtl new` now works without a `.treeline.yml`. Detects the framework; if it's a server framework (Rails, Node, etc.), prompts to run init. For non-server projects (Go CLI, Rust library), creates a plain worktree with no allocation.
- **Branch protection** — `gtl release` warns if the worktree has unpushed commits before releasing, reducing accidental data loss.
- **Dynamic resolve links** — `gtl link` and `gtl unlink` now immediately regenerate the env file and restart the supervised server. Previously, links only updated the registry — the env file stayed stale until a manual `gtl refresh` or restart. The `--restart` flag is deprecated (restart is now automatic).
- **Safari warnings configurable** — removed Safari/hosts sync warnings from `gtl new`/`gtl setup` output (noise for non-Safari users). Warnings still appear in `gtl serve status`. Disable entirely with `gtl config set warnings.safari false`.
- **Editor title URL tokens** — `editor.title` template now supports `{url}` (e.g., `http://localhost:3010`) and `{router_url}` (e.g., `https://salt-feature.localhost`).

## [0.32.0]

- **Multi-domain tunnel support**— `gtl tunnel setup` now supports multiple Cloudflare domains. When the requested domain differs from your current credentials, gtl prompts to authenticate with the correct zone and stores per-domain certificates (`cert-{domain}.pem`). DNS routing uses `--origincert` to target the correct zone. After routing, gtl verifies the DNS record was created in the intended zone; if not, it provides clear manual CNAME instructions with the tunnel UUID.
- **Worktree removal safety** — `gtl release --remove-worktree` and `gtl prune --remove-worktree` now detect if you're inside the worktree being removed. Warns about directory invalidation, prompts for confirmation, and prints `cd` instructions for your terminal. IDE users are advised to close/switch workspaces.

## [0.31.0]

- **Port preservation** — default `port.base` changed from 3000 to 3002; default `port.increment` changed from 10 to 2. Common framework ports (3000, 4000, 5000, 8000, 8080, etc.) are now skipped by the allocator, keeping them free for the proxy to claim for origin-preserving routing. Third-party services (OAuth, Mapbox, Stripe) whitelisted for `localhost:3000` now work transparently across branches.
- **Router port conflict detection** — the allocator hard-fails if `port.base` equals `router.port`, preventing silent conflicts where worktree allocations block the proxy listener.
- **Port config warnings** — `gtl doctor` and `gtl init` warn when `port.base` is a common framework default or conflicts with the router port, with fix commands and a link to docs.
- **File permission hardening** — the config directory is created with mode `0700` (owner-only) and tightened on every CLI invocation. `config.json` and `registry.json` are written with mode `0600`. Lock files changed from `0644` to `0600`. Prevents other users on shared machines from reading credentials or manipulating the registry that drives proxy routing.
- **`gtl start` port wiring warning** — warns at start time (not just doctor/setup) when the start command is missing `{port}` for frameworks that ignore the `PORT` env var (Vite, Next.js, Django).
- **Resumable `gtl review` / `gtl new`** — when the branch already has a worktree, both commands now run setup if no allocation exists, print allocation info, and respect `--open`/`--start` flags instead of printing a dead-end message.

## [0.30.0]

- **Configurable worktree path** — set `worktree.path` in user config to control where `gtl new` and `gtl review` create worktrees. Template supports `{project}` and `{branch}` interpolation. Example: `"worktree": {"path": ".worktrees/{branch}"}` creates worktrees inside the repo instead of as siblings. Relative paths resolve from the repo root; absolute paths are used as-is. Default behavior (sibling directories) is unchanged.
- **Automatic `.gitignore` protection** — when worktree paths resolve inside the repo root, Treeline verifies the directory is gitignored before creating the worktree. If not, it appends the pattern to `.gitignore` automatically. Prevents accidentally committing worktree contents. Sibling paths (the default layout) skip this check.
- **Hop-counted loop detection** — the HTTPS router replaces the binary `X-Gtl-Proxy` header with a counted `X-Gtl-Hops` header, allowing up to 5 legitimate proxy hops before returning 508. Fixes false positives in multi-service setups where requests pass through the router more than once.
- **DNS label truncation** — route keys for long project/branch combinations are truncated to 63 characters (the DNS label limit) with a deterministic hash suffix, preventing routing failures for long branch names.
- **HTML error pages** — the router's 404 (route not found) and 508 (loop detected) responses are now styled HTML pages with dark mode support, available route listings, and framework-specific fix suggestions.
- **Atomic registry writes** — `registry.json` is now written via temp file + rename, preventing corruption from interrupted writes. Corrupt JSON is detected and reported as an error instead of being silently replaced.
- **CA auto-renewal** — `gtl serve install` and `EnsureCA` now regenerate the local CA certificate when it's within 7 days of expiry, preventing surprise HTTPS failures.
- **Browser blocked port avoidance** — the port allocator now skips ports on the WHATWG "bad port" list (e.g. 6000, 6665–6669) that browsers silently refuse to connect to.
- **Stale supervisor detection** — `gtl start` detects when a supervisor socket already exists and reports the conflict instead of silently failing.
- **Security hardening** — privileged operations (`pfctl`, `iptables`, `cp`) now use full binary paths to prevent PATH injection in sudo contexts. `SECURITY.md` rewritten to document the trust model and privileged operations.
- **Structured CLI errors** — all user-facing errors now use a `CliError` type with message, remediation hint, and optional docs link. Centralized formatting in the root command ensures consistent output across every command.
- **Config validation** — `WarnUnknownKeys` detects unrecognized keys in both user config and `.treeline.yml`, with Levenshtein-distance "did you mean?" suggestions for likely typos.
- **Guard tests** — static analysis tests using `go/parser` enforce architectural rules: no `os.Exit` in `internal/` or `cmd/RunE`, no `fmt.Print` in `internal/`. Catches violations at test time.
- **Git exec abstraction** — `gitRun`, `gitOutput`, `gitCheck` helpers in `internal/worktree` replace scattered `exec.Command("git", ...)` calls with consistent error handling.
- **Framework-aware port interpolation** — `{port}`, `{port_2}`, `{port_3}` tokens in start commands resolve to the allocated port block, supporting multi-port frameworks out of the box.
- **Alias routing** — the HTTPS router accepts alias sources for non-registry routes (e.g. Redis UI, Mailhog). Registry routes override aliases on collision.
- **`/etc/hosts` sync** — `gtl serve hosts sync` writes entries for all active routes to `/etc/hosts`, enabling Safari compatibility (Safari doesn't resolve `*.localhost` to loopback).
- **Configurable router domain** — `router.domain` in user config changes the base domain from `localhost` to a custom value (e.g. `dev.local`).
- **macOS CI** — platform-specific packages (proxy, service, supervisor) now run on macOS in CI.

## [0.29.0]

- **`gtl dashboard`** — interactive TUI for monitoring and managing all worktrees in real time. Two-panel layout with project-grouped worktree list and live detail view. Keyboard-driven: start/stop/restart supervisors, open in browser, release worktrees with confirmation, filter search, and help overlay. Mouse support included. Accessible via `gtl dashboard`, `gtl dash`, or `gtl ui`. Built on Bubble Tea v2.

## [0.28.1]

- **`gtl tunnel remove <name>`** — remove a named tunnel from local config. If the removed tunnel was the default and other tunnels remain, another is promoted automatically. If it was the last tunnel, gtl falls back to quick tunnels (random URLs). Does not delete the Cloudflare tunnel itself.

## [0.28.0]

- **`gtl env`** — print the current worktree's environment. Default output shows every key from the env file, with Treeline-managed keys annotated `[treeline]`. `--json` for structured output. `--template` shows unresolved interpolation tokens instead of final values.
- **Port conflict detection on reuse** — `gtl setup` now checks `IsPortFree` when reusing an existing allocation. If any port is occupied (e.g. another process grabbed it since last allocation), Treeline automatically re-allocates to a free block, updates the registry, and prints a warning. Treeline never hands back a port it cannot actually use.
- **Shell completions** — `gtl completion bash|zsh|fish|powershell` outputs completion scripts. Homebrew installs completions automatically. Dynamic completions added for `gtl config get/set` (common config keys) and `gtl status --project` (project names).
- **`gtl start --await`** — blocks until the server is accepting TCP connections on its allocated port, then exits 0. Designed for agents and CI scripts that need to wait for readiness before hitting the server. `--await-timeout` sets the deadline in seconds (default 60). Works on both fresh start and resume (when the supervisor is already running).
- **`gtl open`** — opens the current worktree in the browser. Prefers `https://{project}-{branch}.localhost` when `gtl serve` is running; falls back to `http://localhost:{port}`. Always opens the primary port.
- **`gtl clone`** — clone a repo and set up Treeline in one step. Passes all flags through to `git clone`, detects the framework, generates `.treeline.yml` if absent, and runs `gtl setup`. Deliberately does not auto-start — cloning a foreign repo and running arbitrary shell commands is a trust boundary.
- **Lifecycle hooks** — `.treeline.yml` now supports `pre_setup`, `post_setup`, `pre_release`, and `post_release` hooks. Pre-hooks abort the operation on failure; post-hooks warn and continue. Hook ordering: allocate → env → DB → `pre_setup` → `commands.setup` → editor → `post_setup`. Release: confirm → `pre_release` → free/drop → `post_release`.
- **`gtl resolve`** — look up another worktree's URL by project name. Uses same-branch matching by default: if your frontend and API repos both have `feature-auth` checked out, `{resolve:api}` in an env template resolves to `http://127.0.0.1:{api-port}` automatically. Override with `gtl resolve api staging` or `{resolve:api/staging}` in templates. Supports `--json` for scripting.
- **`gtl link` / `gtl unlink`** — runtime resolve overrides stored in the registry. `gtl link api staging` redirects all `{resolve:api}` lookups to the `staging` branch instead of the same-branch default. Survives restarts and `gtl refresh`. Visible in `gtl status` and `gtl doctor`. Use `--restart` to bounce the supervised server after linking. `gtl unlink api` reverts to the default.
- **`{resolve:project}` interpolation** — new env template token. Resolved at setup time using the registry. Supports `{resolve:project}` (same-branch default) and `{resolve:project/branch}` (explicit branch). Fails setup with a clear error if the target is not allocated.
- **Links visibility** — `gtl status` and `gtl doctor` now display active link overrides for each worktree.

## [0.27.0]

- **`gtl share`** — private branch sharing via token-gated URLs. Creates a Cloudflare tunnel fronted by an auth proxy: the recipient opens the link, gets a session cookie, and sees clean URLs from there. Tokens are ephemeral — new token and tunnel hostname on every run, everything destroyed on Ctrl+C. Uses your configured domain when a named tunnel is available; falls back to `*.trycloudflare.com` otherwise.
- **`gtl share --tailscale`** — alternative Tailscale Serve backend for tailnet-only sharing. No tokens needed — Tailscale handles identity-based auth with WireGuard encryption. Only people on your tailnet can reach the URL. Detects Tailscale from PATH or the macOS app bundle. Mutually exclusive with `--tunnel`.
- **Multi-tunnel config** — store multiple named tunnel configurations with a default, like rbenv. `gtl tunnel setup` now adds to your tunnel list; `gtl tunnel default <name>` switches the active config. Both `gtl tunnel` and `gtl share` accept `--tunnel <name>` to override the default. Old single-tunnel configs (`tunnel.name`/`tunnel.domain`) are auto-migrated.

## [0.26.0]

- **`--json` everywhere** — `gtl doctor`, `gtl port`, and `gtl db name` now accept `--json` for structured output. `gtl status --json` auto-probes port listening and supervisor state without requiring `--check`.
- **`gtl new` shows serve URL** — after creating a worktree, `gtl new` prints the HTTPS router URL when `gtl serve` is running, matching the behavior of `gtl setup`.
- **Tunnel host hints** — `gtl tunnel` detects the project framework and prints the exact config change needed to whitelist the tunnel domain. Covers Rails (`config.hosts`), Vite (`server.allowedHosts`), and Django (`ALLOWED_HOSTS`). Named tunnels show the wildcard for your domain; quick tunnels suggest `.trycloudflare.com`.

## [0.25.0]

- **AI agent integration** — git-treeline now speaks MCP (Model Context Protocol). Add it to your editor's MCP config and agents can query allocations, check health, read config, get database names, and control the dev server — all via structured JSON. Exposes 9 tools and 2 resources.
- **Config rename** — `ports_needed` renamed to `port_count`. Existing configs are auto-migrated on load with a deprecation warning.

## [0.21.0]

- **Editor auto-detection** — `gtl init` detects which editor is running (Cursor, VS Code, Zed, JetBrains products) via terminal env vars or PATH probing, and stores `editor.name` in user config. Used by the menulet for "Open in Editor" labels. Falls back gracefully — if detection fails, no name is stored and the menulet hides the link.
- **Editor customization** — new `editor.title`, `editor.color`, and `editor.theme` config in `.treeline.yml` replace the old `editor.vscode_title`. Auto-migrated on first load.
  - `title`: window title template with `{project}`, `{port}`, `{branch}` interpolation
  - `color`: title/status/activity bar color — `"auto"` generates a deterministic color from the branch name, or set an explicit hex value. User config overrides via `editor.colors` in `config.json`.
  - `theme`: full IDE theme override (e.g. `"Monokai"`). User config overrides via `editor.themes` in `config.json`.
- **Workspace file detection** — when a `.code-workspace` file references the worktree, editor settings are written there (required for multi-root workspaces in VS Code/Cursor). Falls back to `.vscode/settings.json` for single-folder projects.
- **JetBrains support** — if `.idea/` exists, `editor.color` sets the project header color in `workspace.xml` (JetBrains 2023.2+).
- **User-level editor overrides** — `config.json` supports `editor.themes` and `editor.colors` maps keyed by `project` or `project/branch` for per-repo or per-branch IDE customization.
- **Port reservations** — pin stable ports to projects or specific branches via `port.reservations` in user config. Project-level keys (`salt: 3000`) apply to the main repo; `project/branch` keys (`salt/staging: 3020`) pin a specific branch and take priority. Reserved ports block the full `port.increment` range so dynamic allocations never collide.
- **`gtl refresh`** — re-allocate all registered worktrees with current config and reservations in one shot. Supervised servers are restarted automatically; manually-started servers are flagged for manual restart. Supports `--dry-run` and `--force`.
- **`gtl port`** — prints the allocated port for the current worktree. Designed for agents and scripts that need the port without parsing status output.
- **`AGENTS.md` integration** — `gtl init` now writes a treeline section to `AGENTS.md` (works with Cursor, Claude Code, and Codex) instead of `.cursor/rules/treeline.mdc`. Appends to existing `AGENTS.md` or `CLAUDE.md`, or creates `AGENTS.md` if neither exists. Includes `gtl port` as the primary port discovery instruction.
- **Reservation-aware reuse** — `gtl setup` now detects when an existing allocation's port doesn't match a reservation (or conflicts with another project's reservation) and automatically re-allocates instead of reusing stale ports.
- **Fix: stale port reuse** — re-running `gtl setup` after changing `port_count` in config now correctly re-allocates instead of reusing the old port count.
- **Fix: `ProjectDefaults` env_file** — defaults now use the string shorthand form, matching the canonical config shape.
- **Self-documenting templates** — `gtl init` generates `.treeline.yml` with commented-out optional config (port_count, Redis, editor, etc.) so available features are discoverable without reading docs. `port_count: 2` is never auto-emitted as active config.

## [0.19.0]

### New

- **`gtl switch`** — switch a worktree to a different branch or PR in one step. Accepts branch names or PR numbers (resolved via `gh`). Fetches from origin, checks out the branch, updates the registry, and refreshes the env file. Use `--setup` to re-run `commands.setup` after switching.
- **`gtl doctor`** — check project config, allocation, runtime, and diagnostics in one view. Reports on `.treeline.yml` presence, env file status, port allocation, supervisor state, and framework-specific guidance.
- **Tab completion** — `gtl new`, `gtl review`, and `gtl switch` now provide shell completions for branch names and PR numbers.
- **`gtl release` confirmation** — single-worktree releases now show what will be released and prompt for confirmation. Use `--force` to skip.
- **Worktree guard** — `gtl new` and `gtl review` now error if run from inside a worktree (which would create confusing sibling worktrees). Suggests `gtl switch` or navigating to the main repo instead. _(Superseded in 0.37.0 — both commands now work from worktrees with interactive prompts.)_

### Changed

- **Simplified `env_file` config** — `env_file: .env.local` now works as a string shorthand (replaces the old `target:`/`source:` map). For cases where the written file differs from the seed, use `path:`/`seed_from:`. Old configs are auto-migrated on first load.
- Templates now emit the simplified `env_file` string form.

## [0.18.0]

### New

- **`gtl start` injects env vars** — the supervisor now reads the worktree's allocation from the registry, resolves env templates from `.treeline.yml`, and passes them into the child process environment. `PORT`, `DATABASE_URL`, etc. are available as real env vars without requiring the app to read `.env` files.
- **Vite detection** — `gtl init` recognizes Vite projects (`vite.config.js/ts/mjs`) and generates a tailored `.treeline.yml` with `npx vite` start command and `.env.local` wiring
- **Post-init/setup diagnostics** — `gtl init` and `gtl setup` now print actionable warnings:
  - Vite: explains `vite.config.js` + `loadEnv` port wiring
  - Node without dotenv: warns that `.env` won't be auto-read, suggests install
  - Python without python-dotenv: same pattern
  - Go/Rust: suggests sourcing env file in start command
  - Missing `env_file` block when `env` vars are configured
- **Smarter env_file emission** — templates now emit `env_file` for frameworks that natively load env files (Next.js, Vite, Rails) even when no `.env` file exists yet on disk
- **dotenv detection** — detects `dotenv`, `dotenv-cli`, `python-dotenv`, `django-environ` in dependency files

## [0.17.0]

### Breaking

- `setup_commands` → `commands.setup` and `start_command` → `commands.start` in `.treeline.yml`
- Auto-migration: existing configs with old keys are rewritten on first load — no manual cleanup needed
- Generated templates now include `commands.start` per framework (Next.js, Rails, Node, Python)

## [0.16.0]

- `gtl start` / `gtl stop` / `gtl restart` — supervised dev server
  - `start` runs `start_command` from `.treeline.yml` under a Unix socket supervisor
  - `stop` pauses the server; supervisor stays alive for resume
  - `start` (again) resumes the server in the original terminal
  - `restart` atomic bounce — stop + start in one step
  - Ctrl+C in the terminal fully exits the supervisor
- Hardened supervisor: socket permissions (0600), read deadlines, `sync.Once` on shutdown, 30s client timeout

## [0.15.0]

- `gtl db` command group for worktree database management:
  - `db name` — print the worktree's database name
  - `db reset` — drop and re-clone from template
  - `db reset --from <db>` — clone from a different local database
  - `db restore <file>` — restore from pg_dump (auto-detects custom format vs plain SQL)
  - `db drop` — drop without re-cloning
- Document port-dependent data pattern (setup_commands for OAuth/webhook fixups)

## [0.14.1]

- Homebrew: `gtl` alias available via `brew install git-treeline`

## [0.14.0]

- `gtl config` CLI: `list`, `get`, `set`, `path`, `edit` subcommands for user-level config
- Rails template: `ports_needed: 2` and `ESBUILD_PORT` only emitted when JS bundler detected
- Fix incorrect Next.js PORT documentation
- Drop git-treeline-rails gem reference

## [0.13.0]

### Breaking

- Rename `default_branch` → `merge_target` in `.treeline.yml`
- Auto-migration: existing configs with `default_branch` are rewritten to `merge_target` on first load — no manual cleanup needed
- If both keys exist, `merge_target` wins and `default_branch` is silently removed

## [0.12.0]

- Smarter env file detection: `init` finds which env file actually exists (`.env.local`, `.env.development`, `.env`, etc.) instead of hardcoding per framework
- Interactive env file selection: confirms single match, prompts to choose when multiple found
- Framework-specific port wiring hints printed after `init` (Next.js, Node, Python)
- Port guidance included in generated agent context files (`treeline.mdc`, `CLAUDE.md`)

## [0.11.0]

- Store `branch` name in allocation registry — enables external consumers (menulets, dashboards) to display the actual branch instead of the worktree directory name
- `gtl status` syncs branches in parallel on every call, keeping the registry fresh without git hooks
- Add `format.DisplayName()`: prefers `branch`, falls back to `worktree_name` — used across `status`, `release`, and `prune`
- Add `registry.UpdateField()` for lock-safe single-field updates
- Reuse detected branch in editor title config (eliminates redundant git call)

## [0.10.0]

- Add merge target config field: `prune --merged` now works with any target branch (develop, staging, trunk, etc.)
- Improve default branch detection: tries `origin/HEAD`, then `git remote show origin`, then local candidates — works with any git host
- Smarter `init`: only emits `env_file` and `env` sections when env files are detected on disk
- Fix path resolution in registry lookups (macOS symlink normalization)
- Fix errcheck lint violations on defer Close

## [0.9.1]

- Harden codebase: fix potential panic in port allocation, consolidate helper functions, add godoc

## [0.9.0]

- Add `prune --merged`: detect and release allocations for worktrees on branches merged to main
- Add `release --project`: batch release all allocations for a given project
- Add `release --all`: release all allocations across all projects
- Add `--force` flag to skip confirmation prompts on batch operations
- Add `--dry-run` flag to `release --project` and `release --all`
- Support `--drop-db` on `prune --merged` for database cleanup
- Fix path normalization for macOS symlinks in worktree-to-branch mapping

## [0.8.0]

- Smarter `init`: auto-detects framework (Next.js, Rails, Node, Python, Rust, Go) and generates tailored .treeline.yml
- Auto-generates agent context files (.cursor/rules/treeline.mdc or CLAUDE.md section) during init
- Add --skip-agent-config flag to opt out of agent context generation
- Detection signals: package.json, Gemfile, next.config.*, prisma/schema.prisma, config/database.yml, and more

## [0.7.0]

- Add database adapter interface with pluggable clone/drop/exists
- Add SQLite database adapter: clones via file copy, drops via file removal
- Store database adapter in registry for correct cleanup on release
- Fix empty database name matching in PostgreSQL existence check
- Backward compatible with existing PostgreSQL-only registries

## [0.6.0]

- Add `gtl` as a short alias for `git-treeline` (installed via Homebrew symlink)
- Add test coverage for internal/setup pipeline

## [0.5.2] - 2026-04-03

- Fix: main worktree allocation now scans for free ports instead of blindly assigning base ports

## [0.5.1] - 2026-03-31

- Fix: root repo setup now uses base port and template database directly instead of treating it as a worktree

## [0.5.0] - 2026-03-31

- Add `new` command: create worktree + allocate resources + run setup in one step
- Add `review` command: check out a GitHub PR into a worktree with full setup (requires `gh` CLI)
- Add `--watch` flag to `status`: auto-refresh with port health checks on a loop
- Add `--interval` flag to `status --watch` for configurable refresh rate
- Add `start_command` config field in `.treeline.yml` for optional app startup
- Add `--start` flag on `new` and `review` to run `start_command` after setup
- Add `--dry-run` flag on `new` to preview without side effects
- Extract shared `internal/worktree` package for git worktree operations
- Extract `internal/github` package for `gh` CLI integration
- Refactor `detectMainRepo` from setup into shared worktree package

## [0.4.0] - 2026-03-31

- Add CI with golangci-lint, govulncheck, and go vet
- Add Dependabot for Go modules and GitHub Actions (monthly)
- Add Makefile with ci, test, lint, vulncheck, and build targets
- Add Homebrew tap support via GoReleaser
- Add community health files (CONTRIBUTING, CODE_OF_CONDUCT, SECURITY)
- Add issue and PR templates
- Bump Go to 1.24.12 to fix stdlib vulnerabilities

## [0.3.0] - 2026-03-31

- Rewrite CLI in Go (Cobra), replacing Ruby implementation
- Add reliability hardening: file locking, idempotent setup, atomic registry writes
- Add `refresh` command for re-interpolating env files without re-cloning
- Add `prune --stale` to clean up allocations not in `git worktree list`
- Add `status --check` to probe allocated ports
- Add `status --json` for machine-readable output
- Add `--dry-run` flag on setup
- Add PostgreSQL database cloning via `createdb --template`
- Add Redis namespacing (prefixed and database strategies)
- Add VS Code window title configuration
- Cross-platform support (macOS, Linux, Windows) via platform-specific config paths

## [0.2.0] - 2026-03-31

- Add multi-port allocation (`ports_needed` config)
- Extract Railtie into separate `git-treeline-rails` gem
- Fix gemspec metadata warnings

## [0.1.0] - 2026-03-31

- Initial release
