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
- Template was auto-created even without a provision: section, silently poisoning every later run (clean clone of an empty template, no warning); now gated on an explicit provision: opt-in — provable — reviewer
- Failed hydrate/source provisioning left a partial template that later runs cloned as healthy; now dropped under the template lock — provable — reviewer
- Degradation warning was dropped on the SetupCommandError path (CLI and MCP) — the path where the empty DB likely caused the failure; now printed/attached there — provable — reviewer
- Warning printed before the "Done!" summary, so the last output asserted a healthy database; moved after the summary — judgeable — reviewer
- Recovery text said "run gtl provision" even when no provision: section exists (a no-op); recovery line is now branch-specific — judgeable — reviewer
- Hydrate/source output went to os.Stdout, corrupting gtl claim's captured stdout and the MCP stdio transport; routed to setup log / stderr — provable — reviewer
- gtl provision itself took no template lock while rewriting the template a concurrent gtl new could be cloning; now locks — provable — reviewer
- --strict with empty-mode provision created the template before failing; now rejected before host mutation — provable — reviewer
