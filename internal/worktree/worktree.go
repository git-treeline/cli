// Package worktree provides git worktree operations including creation,
// branch detection, and repository inspection. It wraps git CLI commands
// for worktree management and merged branch detection.
package worktree

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Create adds a git worktree at path. If newBranch is true, it creates a new
// branch from base. Otherwise it checks out an existing branch.
func Create(path, branch string, newBranch bool, base string) error {
	args := []string{"worktree", "add"}
	if newBranch {
		args = append(args, path, "-b", branch)
		if base != "" {
			args = append(args, base)
		}
	} else {
		args = append(args, path, branch)
	}

	cmd := exec.Command("git", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// BranchExists checks whether a branch exists locally or as a remote tracking ref.
func BranchExists(branch string) bool {
	if localBranchExists(branch) {
		return true
	}
	return remoteBranchExists(branch)
}

func localBranchExists(branch string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

func remoteBranchExists(branch string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	return cmd.Run() == nil
}

// Fetch fetches a branch from the given remote.
func Fetch(remote, branch string) error {
	cmd := exec.Command("git", "fetch", remote, branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch %s %s failed: %s", remote, branch, strings.TrimSpace(string(out)))
	}
	return nil
}

// FindWorktreeForBranch returns the path of an existing worktree that has
// the given branch checked out, or empty string if none.
func FindWorktreeForBranch(branch string) string {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	var currentPath string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			currentPath = strings.TrimPrefix(line, "worktree ")
		}
		if strings.HasPrefix(line, "branch refs/heads/"+branch) {
			return currentPath
		}
	}
	return ""
}

// MergedBranches returns branch names that have been merged into the default
// branch. If defaultBranchOverride is non-empty it is used directly; otherwise
// the default branch is detected via symbolic-ref, falling back to "main"
// then "master".
func MergedBranches(repoPath, defaultBranchOverride string) ([]string, error) {
	defaultBranch := defaultBranchOverride
	if defaultBranch == "" {
		defaultBranch = DetectDefaultBranch(repoPath)
	}
	cmd := exec.Command("git", "branch", "--merged", defaultBranch)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git branch --merged %s: %w\n\nSet merge_target in .treeline.yml if your integration branch is not main/master", defaultBranch, err)
	}

	var branches []string
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		// Strip "* " (current branch) and "+ " (checked out in another worktree)
		name = strings.TrimPrefix(name, "* ")
		name = strings.TrimPrefix(name, "+ ")
		name = strings.TrimSpace(name)
		if name == "" || name == defaultBranch {
			continue
		}
		branches = append(branches, name)
	}
	return branches, nil
}

// DetectDefaultBranch resolves the default branch for the repo at repoPath.
// It tries (in order): local symbolic-ref, `git remote show origin` (network),
// then common local branch names. Returns branch name and true if found,
// or "main" and false if detection failed entirely.
func DetectDefaultBranch(repoPath string) string {
	if b := detectBranchFromSymbolicRef(repoPath); b != "" {
		return b
	}
	if b := detectBranchFromRemoteShow(repoPath); b != "" {
		return b
	}
	if b := detectBranchFromLocalCandidates(repoPath); b != "" {
		return b
	}
	return "main"
}

func detectBranchFromSymbolicRef(repoPath string) string {
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	ref := strings.TrimSpace(string(out))
	parts := strings.Split(ref, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

func detectBranchFromRemoteShow(repoPath string) string {
	cmd := exec.Command("git", "remote", "show", "origin")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return parseHeadBranch(string(out))
}

func parseHeadBranch(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "HEAD branch:") {
			branch := strings.TrimSpace(strings.TrimPrefix(line, "HEAD branch:"))
			if branch != "" && branch != "(unknown)" {
				return branch
			}
		}
	}
	return ""
}

func detectBranchFromLocalCandidates(repoPath string) string {
	for _, candidate := range []string{"main", "master", "develop", "trunk"} {
		cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+candidate)
		cmd.Dir = repoPath
		if cmd.Run() == nil {
			return candidate
		}
	}
	return ""
}

// WorktreeBranches returns a map of worktree absolute path → branch name
// by parsing `git worktree list --porcelain`. Paths are normalized via
// filepath.EvalSymlinks to match the paths stored in the registry.
func WorktreeBranches(repoPath string) map[string]string {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	result := make(map[string]string)
	var currentPath string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			p := strings.TrimPrefix(line, "worktree ")
			if resolved, err := filepath.EvalSymlinks(p); err == nil {
				p = resolved
			}
			currentPath = p
		}
		if strings.HasPrefix(line, "branch refs/heads/") {
			branch := strings.TrimPrefix(line, "branch refs/heads/")
			result[currentPath] = branch
		}
	}
	return result
}

// DetectMainRepo returns the root worktree path (the main repo) by parsing
// `git worktree list --porcelain`. Falls back to the given path.
func DetectMainRepo(worktreePath string) string {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return worktreePath
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "worktree ") {
		return strings.TrimPrefix(lines[0], "worktree ")
	}
	return worktreePath
}
