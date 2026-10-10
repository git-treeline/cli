---
slug: dashboard-recovery
type: feature
size: small
decider: jonathan
created: 2026-10-10
---

# dashboard-recovery

## Intent

Make the dashboard dependable for diagnosing and recovering one worktree.
Its existing controls have drifted from the CLI, and failures disappear before
the user can act on them. This increment closes that loop before adding more
workflows.

## Goal

A user can inspect a selected worktree's diagnosis, start or restart its server
or sync its environment, and inspect the operation's result inside the dashboard.

## Behavior

1. Start, stop, restart, environment sync, create, and release use the CLI's
   existing workflows in the selected worktree's context. Start works without an
   existing supervisor and waits for readiness.
2. Health shows the selected worktree's doctor diagnosis, including machine
   health, with an explicit recheck. Results remain associated with their target;
   operations affecting a checked worktree make its previous diagnosis stale.
3. Activity identifies the operation and target, indicates work in progress,
   and retains completion output and errors across dashboard polling. Only one
   operation runs at a time. Quitting cancels an unfinished command.
4. Selection and pending action targets survive list refreshes. Release explains
   that the directory and database are retained and requires confirmation.
5. Overview, Health, and Activity are keyboard accessible, their details scroll,
   and narrow terminals can switch between the worktree list and details.

## Non-goals

Unified application log streaming, new database or machine administration
controls, bulk actions, dependency editing, PR review, and an action palette.

## Business rules

- Must preserve canonical CLI validation and failure messages.
- Must not infer a healthy environment merely because doctor exited successfully.
- Must not run machine remediation automatically.
- Must keep command output bounded and prevent terminal control sequences in
  captured output from changing the dashboard.

## Assumptions

The running CLI supports `start --await` and retains its detached supervisor's
logs. Health is an explicit diagnostic snapshot, not a continuously running
machine probe. The dashboard lists allocated worktrees, as it does today.

## Critical files

- `internal/tui/model.go`
- `internal/tui/update.go`
- `internal/tui/view.go`
- `cmd/server.go`
- `cmd/doctor.go`

## Acceptance checks

### agent-loopable

- Action tests verify canonical arguments, selected-worktree context, cold-start
  handoff, error persistence, duplicate prevention, cancellation, and target
  identity across refreshes — run: `go test ./internal/tui`
- Command runner tests exercise real subprocess working directories, literal
  arguments, failure output, bounded capture, and cancellation.
- View tests verify readable diagnostic and failure content, detail scrolling,
  and terminal bounds at wide, narrow, and small sizes.
- Repository checks pass — run: `make ci`

### human-gate

- In a real development project, inspect Health, recover the server, and recheck
  Health; confirm the diagnostic/action/result loop is easy to follow.

## Out of scope / deferred

The broader route, dependency, and guided review additions remain separate work.
