---
type: bug
size: small
---

# deferred-worktree-hook-setup

## Goal

Worktree creation with `--no-setup` returns without running setup, allowing
Treeline to open the workspace and perform setup in the background.

## Intent

The installed post-checkout hook runs setup inside `git worktree add`, before
the CLI checks `--no-setup`. In the affected stable app, worktree files existed
about 13 seconds before selection. Treeline already schedules a separate setup
phase with progress, failure reporting, and retry.

## Behavior

1. `gtl new --no-setup` skips allocation, environment generation, and setup
   commands, including when an existing post-checkout hook invokes setup or
   the requested branch already has a worktree.
2. Unrelated Git hook commands still run. Ordinary Git operations retain their
   automatic setup behavior, and a subsequent explicit `gtl setup` runs normally.
3. Existing Treeline hooks require no reinstall or `.treeline.yml` changes.
   A different, older `gtl` on PATH cannot defeat the creating CLI's deferral.

## Non-goals

Changing the app's modal, replacing installed apps or CLI binaries, changing
project hooks on disk, or redesigning setup and its error handling.

## Acceptance checks

### agent-loopable

- In an isolated repository with a legacy Treeline post-checkout hook and an
  older `gtl` on PATH, `new --no-setup` creates the requested new or existing
  branch worktree without setup artifacts. Unrelated hook commands still run.
- A later explicit setup creates the configured setup marker; ordinary Git
  worktree creation still runs automatic setup. Retrying `new --no-setup` for
  an already checked-out branch does not allocate resources.
- Generated hook variants honor deferral and retain their normal behavior.

### human-gate

- After releasing and adopting the fixed CLI, create a worktree in Treeline
  with a slow setup command. Verify the modal closes before setup completes,
  the sidebar reports setup progress, and failure can be retried.

## Assumptions

Treeline continues to run its independent setup phase after creation. The app
must adopt a released CLI containing this fix before its installed build gains
the corrected behavior.
