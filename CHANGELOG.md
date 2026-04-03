## [Unreleased]

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
