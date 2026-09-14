// Package config handles user and project configuration for git-treeline.
//
// # Project Config Loading Strategy
//
// Project configuration (.treeline.yml) is branch-specific — each worktree has
// its own copy matching its checked-out branch. The rule is simple:
//
// **Always load .treeline.yml from the worktree you're operating on.**
//
// Two exceptions:
//
//   - gtl new / gtl review — worktree doesn't exist yet, so initial config is
//     read from the main repo. After git worktree add, setup runs from the NEW
//     worktree's config. The --start flag also reloads from the worktree.
//   - gtl init / gtl prune — global operations with no specific worktree.
//
// ## Commands That Load From Worktree
//
// All of these read .treeline.yml from the current worktree directory:
//
//   - gtl start / stop / restart — start also syncs env file + editor config
//   - gtl setup (when run inside an existing worktree)
//   - gtl refresh — detection uses worktree's port_count, not main repo's
//   - gtl doctor
//   - gtl env show / gtl env sync
//   - gtl editor refresh
//   - gtl link / unlink
//   - gtl open, gtl tunnel, gtl db, gtl release
//   - gtl clone (operates on the cloned repo itself)
//
// ## Env Sync Lifecycle
//
// When does .treeline.yml env: get written to the env file (.env.local)?
//
//   - gtl start — syncs env file + editor settings before starting the server
//   - gtl restart — syncs the env file and atomically replaces the supervisor's
//     managed environment and port before restarting
//   - gtl env sync — explicit manual sync for users who don't use gtl start
//   - gtl setup — full provisioning: copies source template then writes managed keys
//
// Changes to commands.start require killing the supervisor (Ctrl+C) and
// running gtl start fresh. gtl stop only stops the child process — the
// supervisor stays alive and gtl start resumes with the original command.
// gtl restart warns if it detects a mismatch.
//
// ## Implementation
//
// Most code uses:
//
//	pc := config.LoadProjectConfig(absPath) // absPath = worktree directory
//
// The setup package provides these constructors:
//
//   - setup.New(worktreePath, mainRepo, uc) — loads config from worktree.
//     mainRepo is only used for copy_files source and SQLite template paths.
//     The cmd layer handles pre-creation config reads (e.g. gtl new reads
//     mainRepo for project name before the worktree exists).
//
//   - setup.NewWithOptions(worktreePath, mainRepo, uc, options) — use DryRun
//     before construction to keep legacy config migrations read-only.
//
// setup.RegenerateEnvFile synchronizes managed assignments without copying the
// source template. It removes obsolete, unchanged assignments with recorded
// ownership and preserves unrelated entries. No allocation is a no-op.
package config
