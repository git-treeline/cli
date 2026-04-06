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
- **Worktree guard** — `gtl new` and `gtl review` now error if run from inside a worktree (which would create confusing sibling worktrees). Suggests `gtl switch` or navigating to the main repo instead.

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
