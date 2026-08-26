---
slug: worktree-db-fallback
type: feature
status: building
decider: jonathan
blast_radius: medium
size: small
created: 2026-08-26
stale_after: 2026-09-25
---

# Worktree creation degrades gracefully when the template database is missing

## Intent

Today `gtl new` fails hard when the worktree database cannot be cloned because
the template database doesn't exist. A worktree with a correctly named but
empty database is strictly more useful than no worktree: the user (or an AI
agent) can load a schema and keep coding. Database provisioning is a service
concern, not a git concern — it should not veto worktree creation.

The fix follows a precedent already in the codebase: `SetupCommandError`
exists so that user-command failures don't read as infrastructure failures.
The database clone moves to the same side of that line.

Appetite: one sitting. The change is confined to the `cloneDatabase` step of
worktree setup, one new flag, one new config key, and warning surfaces.

## Goal

`gtl new` in a repo with a configured but absent template database produces a
usable worktree (allocated ports, env file with correct `DATABASE_URL`, and a
database with the allocated name — hydrated when provisioning is configured
and cheap, empty otherwise) with a prominent recovery warning, exiting 0;
`gtl new --strict` preserves today's hard failure.

## Non-goals

- No CI end-to-end coverage against a real Postgres service (candidate
  follow-up spec; today's e2e config has no `database` section at all).
- No auto-heal: a worktree that received the empty-DB fallback is not
  retroactively re-cloned when the template later appears. Recovery stays
  explicit (`gtl db reset`).
- No changes to `gtl provision`'s own CLI/UX, to `gtl db reset`, or to
  `database.sync_on_create` semantics.
- No new degradation handling for the *target* database already existing —
  the existing skip behavior stands.

## Behavior

1. **Missing template, provision mode `empty` or `hydrate` (requires an
   explicit `provision:` section):** `gtl new` runs the provision database
   step automatically (the same idempotent step `gtl provision` runs), then
   clones. Progress is logged like any other setup step. Without a
   `provision:` section the template is never created — auto-creating it
   would make it clone cleanly (and silently) on every later run — and
   setup goes straight to the empty-DB fallback (Behavior 4). If the
   provision step fails after creating the template (e.g. a failed hydrate
   command or interrupted restore), the partial template is dropped so
   later runs degrade loudly instead of cloning broken state.
2. **Missing template, provision mode `source`, no opt-in:** the remote pull
   is NOT run. Setup falls through to the empty-DB fallback (Behavior 4)
   and the warning names `gtl provision` as the way to hydrate.
3. **Missing template, provision mode `source`, with `provision.database.auto:
   true` in `.treeline.yml`:** the source hydration runs during `gtl new`.
   On success, clone proceeds; on failure, fall through to Behavior 4.
4. **Empty-DB fallback:** setup creates an *empty* database with the
   allocated worktree name (Postgres: `createdb` with no template; SQLite:
   create an empty file at the target path), then continues setup normally.
   Env vars are unchanged — they already point at the allocated name.
5. **Fallback also fails (e.g. database server unreachable):** the worktree
   is still created, the env file is still written, remaining setup steps
   still run (their failures classify as `SetupCommandError`, as today), and
   the run ends with the degradation warning.
6. **The degradation warning** is a single prominent block at the end of
   `gtl new` output — after the success summary, and also printed when
   setup commands fail (an empty DB is the likely cause of that failure) —
   stating: what state the database is in (empty or absent), why (with the
   branch-specific remedy: `gtl provision`, a config key, or manual
   creation), and that once the template exists, `gtl db reset` in the
   worktree re-clones from it. It must be explicit that the database will
   NOT self-heal on later runs.
7. **`gtl new --strict`:** any degradation in 1–5 (including a provision
   attempt that fails) aborts with a non-zero exit and today's error
   behavior. For scripts that treat exit 0 as "environment fully ready."
   Strict never mutates the host on its way to failing: an empty-mode
   provision (which could only produce a degraded clone) is rejected
   before the template is created.
8. **MCP surface:** the worktree-creation MCP tool reports the same
   degradation warning in its result payload, so agents see it without
   parsing CLI output.
9. **Exit code:** a degraded non-strict run exits 0. The warning block, not
   the exit code, carries the signal (see open-questions for the recorded
   default on this).

## Business rules

- **Must:** worktree creation never fails because of database degradation,
  except under `--strict`.
- **Must:** remote source hydration never runs during `gtl new` without
  `provision.database.auto: true`. Pulling a remote dump is a surprising
  side effect of creating a worktree; it stays opt-in.
- **Must:** the fallback database uses the exact allocated name; env-file
  contents are identical to the healthy path.
- **Must:** every degraded run prints the warning block of Behavior 6 —
  silent degradation is worse than the current hard fail.
- **Must:** the provision step invoked from `gtl new` is the same idempotent
  code path as `gtl provision`'s database step (skips when the template
  exists), not a reimplementation.
- **Should:** distinguish "template missing" from "server unreachable" via
  the adapter's `Exists(template)` check before attempting provision — don't
  attempt provision against a server that is down.
- **May:** extend the `database.Adapter` interface with a `Create` method if
  that is the cleanest way to express the empty-DB fallback across adapters.

## Assumptions

- The provision database step (`internal/provision/execute.go`) is
  idempotent and safe to invoke mid-`gtl new`: it logs "already exists —
  skipping" when the template is present.
- `createdb` / client tools being on PATH is already a precondition of the
  healthy clone path; this spec does not change tooling requirements.
- The per-template flock in `internal/database/postgresql.go` continues to
  serialize concurrent `gtl new` runs; auto-provision must run inside or
  before that same serialization so two concurrent runs don't both create
  the template. **Contradiction rule:** if the provision step reports the
  template exists but the clone still fails, treat it as a generic clone
  failure and fall through the ladder once — do not retry/loop.
- `database.sync_on_create` (which runs migrate in the root repo before
  cloning) may mask a missing template when the migrate command creates the
  DB. This spec leaves that path untouched; the ladder engages only when the
  clone step itself finds the template absent.

## Critical files

- `internal/setup/setup.go` — `cloneDatabase` (the ladder lives here) and
  its call site in `Run`; `SetupCommandError` precedent at the top of file.
- `internal/provision/execute.go` — the idempotent database step and the
  deps struct (`CreateDB`, `HydrateFromSource`) to reuse.
- `cmd/provision.go` — how `HydrateFromSource` is wired for `gtl provision`;
  the same wiring is needed from the setup path.
- `internal/database/adapter.go`, `postgresql.go`, `sqlite.go` — adapter
  interface; template flock in `postgresql.go`.
- `internal/config/project.go` — `Provision()` parsing; new
  `provision.database.auto` key.
- `cmd/new.go` — `--strict` flag.
- `internal/mcp/tools_write.go` — MCP worktree-creation result payload.
- `README.md` — CLI reference row for `gtl new` (add `--strict`); project
  config fields table (add `provision.database.auto`).

## Acceptance checks

### agent-loopable

- Setup-package tests cover each rung of the ladder: missing template →
  provision step invoked; provision failure → empty-DB fallback; fallback
  failure → warn and continue; `source` mode without `auto` → no hydration
  attempted — run: `go test ./internal/setup/...`
- Command-level tests cover `--strict` (degradation → non-zero exit) and
  the config key parsing — run: `go test ./cmd/...`
- Full suite green — run: `go test ./...`
- Lint and vulnerability gate green — run: `make ci`

### judgeable

- The degradation warning satisfies Behavior 6 (state, cause, recovery
  sequence, no-self-heal notice) and reads well on both the CLI and MCP
  surfaces (§Behavior).
- The implementation reuses the provision database step rather than
  duplicating creation/hydration logic (§Business rules).
- README changes follow the repo's README update policy: `--strict` added
  to the `gtl new` row of the CLI reference table; `provision.database.auto`
  added to the project config fields table mirroring the Go struct
  (§Critical files).
- Concurrency: auto-provision participates in the existing per-template
  serialization; no new race between two `gtl new` runs (§Assumptions).

### human-gate

- Decider runs `gtl new` in a repo whose `.treeline.yml` configures a
  template that doesn't exist, and judges the degraded-run output and
  recovery instructions acceptable as the thing an agent or user first sees.

## Out of scope / deferred

- CI e2e lifecycle coverage with a real Postgres service and a `database`
  section (no slug allocated yet).
- Auto-heal of empty-fallback worktree databases when the template appears.
