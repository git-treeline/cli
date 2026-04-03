## [Unreleased]

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
