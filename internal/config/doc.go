// Package config handles user and project configuration for git-treeline.
//
// # Project Config Loading Strategy
//
// Project configuration (.treeline.yml) serves two distinct purposes with
// different loading requirements.
//
// ## Worktree-Scoped (runtime)
//
// Load from: current working directory (the worktree).
//
//	pc := config.LoadProjectConfig(absPath) // absPath = cwd
//
// Use when the command operates on this worktree's runtime. Branch-specific
// config overrides apply.
//
//   - start / stop / restart — needs worktree's commands.start
//   - doctor — diagnoses this worktree
//   - env — shows this worktree's resolved vars
//   - editor refresh — configures editor for this worktree
//   - link / unlink — modifies this worktree's env
//   - setup.RegenerateEnvFile() — re-resolves this worktree's env
//
// ## Repository-Scoped (provisioning)
//
// Load from: main repository root.
//
//	mainRepo := worktree.DetectMainRepo(absPath)
//	pc := config.LoadProjectConfig(mainRepo)
//
// Use when creating worktrees or operating on the project as a whole.
//
//   - new / review / clone — creates worktrees from canonical config
//   - prune — operates on registry
//   - refresh — re-runs full setup
//   - init — writes config to main repo
//   - setup.New() — copies files from main repo
//
// ## Why It Matters
//
// If .treeline.yml differs by branch:
//
//	# main branch
//	commands:
//	  start: "rails server -p {port}"
//
//	# feature-vite branch
//	commands:
//	  start: "bin/dev -p {port}"
//
// gtl start in a feature-vite worktree must load that worktree's config
// to get bin/dev, not the main repo's rails server command.
package config
