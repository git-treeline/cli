# Worktree database degradation (worktree-db-fallback)

Worktree creation never fails because the database couldn't be cloned. A
worktree with a correctly named but empty database is more useful than no
worktree; database trouble is reported, not fatal — the same boundary
`SetupCommandError` draws for user commands.

## The ladder

`internal/setup` walks this ladder when a fresh allocation needs a database
(`cloneDatabaseWith`):

1. Clone from the template when it exists.
2. Template missing **and the repo has an explicit `provision:` section**:
   run the same idempotent provision database step as `gtl provision`, under
   the per-template lock, then clone. `source` hydration additionally
   requires `provision.database.auto: true` — pulling a remote dump is never
   an implicit side effect of creating a worktree.
3. Otherwise (no `provision:` section, provisioning failed, or the clone
   itself failed): create an *empty* database with the allocated name, so
   the env file points at something real.
4. If even that fails (server unreachable): keep the worktree, continue
   setup, and report.

Every degraded run records a `setup.DBDegradation` (state `empty` or
`absent` plus a branch-specific reason) and ends with a warning block —
after the success summary, and also on the `SetupCommandError` path, since
an empty database is usually why setup commands failed. MCP `gtl_new`/
`gtl_setup` results carry `database_state` and `database_warning`; MCP error
results append the same message.

`gtl new --strict` restores the hard failure (non-zero exit, worktree rolled
back) and never mutates the host on the way to failing.

## Business rules that shipped

- The template is **never created without a `provision:` opt-in**. An
  auto-created empty template clones cleanly on every later run, converting
  one loud degradation into permanent silent breakage.
- A **partially provisioned template is dropped** (under the template lock)
  when the provision step fails after creating it — a failed hydrate or an
  interrupted restore must not leave state that later runs clone as healthy.
- Degradation does not self-heal: `cloneDatabase` skips when the target
  exists. Recovery is explicit — make the template exist, then
  `gtl db reset` in the worktree.
- Provisioning output routes through the setup log / stderr, never
  `os.Stdout`: stdout is `gtl claim`'s capture stream and the MCP JSON-RPC
  transport.
- `gtl provision` itself holds the per-template lock while acting on the
  template, closing the race with a concurrent clone.

## Known limitations (accepted)

- `--strict` is a guarantee about fresh setup only; against an
  already-allocated worktree it exits 0 without re-verifying the database.
- A server outage during `gtl new` yields exit 0 with no database (state
  `absent`) — decider-ratified: warn-and-continue applies to infrastructure
  failure too.

## Corrections (build record)

- Empty-mode auto-provision initially reported a healthy clone; now records the empty degradation so MCP surfaces it — provable — implementer
- Template was auto-created even without a provision: section, silently poisoning every later run (clean clone of an empty template, no warning); now gated on an explicit provision: opt-in — provable — reviewer
- Failed hydrate/source provisioning left a partial template that later runs cloned as healthy; now dropped under the template lock — provable — reviewer
- Degradation warning was dropped on the SetupCommandError path (CLI and MCP) — the path where the empty DB likely caused the failure; now printed/attached there — provable — reviewer
- Warning printed before the "Done!" summary, so the last output asserted a healthy database; moved after the summary — judgeable — reviewer
- Recovery text said "run gtl provision" even when no provision: section exists (a no-op); recovery line is now branch-specific — judgeable — reviewer
- Hydrate/source output went to os.Stdout, corrupting gtl claim's captured stdout and the MCP stdio transport; routed to setup log / stderr — provable — reviewer
- gtl provision itself took no template lock while rewriting the template a concurrent gtl new could be cloning; now locks — provable — reviewer
- --strict with empty-mode provision created the template before failing; now rejected before host mutation — provable — reviewer
