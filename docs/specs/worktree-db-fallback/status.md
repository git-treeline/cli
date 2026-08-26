# Status

## State
ready-for-review — implementation complete, all acceptance commands green, awaiting decider review

## Done
- Ladder in `internal/setup`: missing template → auto-provision (empty/hydrate;
  source only with `provision.database.auto: true`) → empty-DB fallback →
  warn-and-continue; strict restores hard fail
- `database.Adapter` gained `Create`; `LockTemplate` exported so
  auto-provision serializes with concurrent clones
- `gtl new --strict` flag (mutually exclusive with `--no-setup`)
- Degradation warning block at end of setup output; `database_state` +
  `database_warning` in MCP `new`/`setup` tool results
- `provision.database.auto` config key parsed
- Tests: 12-branch ladder table (`internal/setup`), SQLite fallback, warning
  message contents, config parsing, cmd wiring; full `make ci` green
- README: `--strict` in CLI table, `provision.database.auto` in config table,
  provision prose corrected
- Verified live: non-strict `gtl new` with missing SQLite template → worktree
  created, empty DB at the env-var path, warning block, exit 0; `--strict` →
  rollback, exit 1

## In progress
(none)

## Last green checkpoint
870ccd8 — `make ci` green; live gtl new degraded run exit 0, --strict exit 1

## Dead ends
(none)

## Corrections
- Empty-mode auto-provision initially reported a healthy clone; now records the empty degradation so MCP surfaces it — provable — implementer
