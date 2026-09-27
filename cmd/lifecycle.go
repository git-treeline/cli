package cmd

import "github.com/git-treeline/cli/internal/registry"

// Tests replace acquisition to exercise command refusal boundaries without
// creating lock files in the host's lifecycle lock directory.
var acquireCommandLifecycleLocks = registry.AcquireLifecycleLocks
