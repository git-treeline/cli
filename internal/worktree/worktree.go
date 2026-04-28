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

// gitRun executes a git command in dir and returns trimmed stdout.
// On failure, the error includes the git subcommand and trimmed stderr.
func gitRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return "", fmt.Errorf("git %s: %s", args[0], detail)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitOutput executes a git command in dir and returns stdout only (no stderr).
// Returns "" and nil on failure for queries where absence is not an error.
func gitOutput(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitCheck runs a git command and returns true if it exits 0.
func gitCheck(dir string, args ...string) bool {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.Run() == nil
}

// DetectRepoRoot returns the top-level directory of the current git worktree.
// Falls back to the given path if detection fails.
func DetectRepoRoot(path string) string {
	root := gitOutput(path, "rev-parse", "--show-toplevel")
	if root == "" {
		return path
	}
	return root
}

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

	_, err := gitRun("", args...)
	return err
}

// BranchExists checks whether a branch exists locally or as a remote tracking ref.
func BranchExists(branch string) bool {
	return gitCheck("", "show-ref", "--verify", "--quiet", "refs/heads/"+branch) ||
		gitCheck("", "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch)
}

// Fetch fetches a branch from the given remote.
func Fetch(remote, branch string) error {
	_, err := gitRun("", "fetch", remote, branch)
	return err
}

// FindWorktreeForBranch returns the path of an existing worktree that has
// the given branch checked out, or empty string if none.
func FindWorktreeForBranch(branch string) string {
	out := gitOutput("", "worktree", "list", "--porcelain")
	if out == "" {
		return ""
	}

	var currentPath string
	for _, line := range strings.Split(out, "\n") {
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
	out, err := gitRun(repoPath, "branch", "--merged", defaultBranch)
	if err != nil {
		return nil, fmt.Errorf("%w\n\nSet merge_target in .treeline.yml if your integration branch is not main/master", err)
	}

	var branches []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
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

// CurrentBranch returns the currently checked-out branch in dir.
// Returns "" if dir is not a git repo or HEAD is detached.
func CurrentBranch(dir string) string {
	branch := gitOutput(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "HEAD" {
		return ""
	}
	return branch
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
	ref := gitOutput(repoPath, "symbolic-ref", "refs/remotes/origin/HEAD")
	if ref == "" {
		return ""
	}
	parts := strings.Split(ref, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

func detectBranchFromRemoteShow(repoPath string) string {
	out := gitOutput(repoPath, "remote", "show", "origin")
	if out == "" {
		return ""
	}
	return parseHeadBranch(out)
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
		if gitCheck(repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+candidate) {
			return candidate
		}
	}
	return ""
}

// WorktreeBranches returns a map of worktree absolute path → branch name
// by parsing `git worktree list --porcelain`. Paths are normalized via
// filepath.EvalSymlinks to match the paths stored in the registry.
func WorktreeBranches(repoPath string) map[string]string {
	out := gitOutput(repoPath, "worktree", "list", "--porcelain")
	if out == "" {
		return nil
	}

	result := make(map[string]string)
	var currentPath string
	for _, line := range strings.Split(out, "\n") {
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

// Checkout switches the current directory's worktree to a different branch.
func Checkout(branch string) error {
	_, err := gitRun("", "checkout", branch)
	return err
}

// ListBranches returns branch names matching the given prefix.
// Lists both local and remote branches, deduplicating origin/ variants.
func ListBranches(prefix string) []string {
	out := gitOutput("", "branch", "-a", "--format=%(refname:short)")
	if out == "" {
		return nil
	}

	seen := make(map[string]bool)
	var result []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimPrefix(line, "origin/")
		if name == "" || name == "HEAD" || seen[name] {
			continue
		}
		if prefix == "" || strings.HasPrefix(name, prefix) {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}

// Remove runs `git worktree remove` on the given path. If force is true,
// it passes --force to remove even with uncommitted changes.
// Returns nil if the path is the main worktree (cannot be removed).
func Remove(worktreePath string, force bool) error {
	mainRepo := DetectMainRepo(worktreePath)
	if mainRepo == worktreePath {
		return fmt.Errorf("cannot remove the main worktree")
	}

	args := []string{"worktree", "remove", worktreePath}
	if force {
		args = []string{"worktree", "remove", "--force", worktreePath}
	}
	_, err := gitRun(mainRepo, args...)
	return err
}

// HasUncommittedChanges returns true if the worktree has staged or unstaged changes.
func HasUncommittedChanges(worktreePath string) bool {
	return !gitCheck(worktreePath, "diff", "--quiet") ||
		!gitCheck(worktreePath, "diff", "--cached", "--quiet")
}

// UnpushedCommitCount returns the number of commits on the current branch that
// haven't been pushed to the remote. Returns 0 if the remote branch doesn't exist
// or if the check fails (e.g., offline).
func UnpushedCommitCount(worktreePath string) int {
	branch := CurrentBranch(worktreePath)
	if branch == "" || branch == "HEAD" {
		return 0
	}

	remote := gitOutput(worktreePath, "rev-parse", "--verify", "--quiet", "origin/"+branch)
	if remote == "" {
		// No remote branch — all local commits are "unpushed"
		out := gitOutput(worktreePath, "rev-list", "--count", branch)
		if out == "" {
			return 0
		}
		var count int
		if _, err := fmt.Sscanf(out, "%d", &count); err != nil {
			return 0
		}
		return count
	}

	out := gitOutput(worktreePath, "rev-list", "--count", "origin/"+branch+"..HEAD")
	if out == "" {
		return 0
	}
	var count int
	if _, err := fmt.Sscanf(out, "%d", &count); err != nil {
		return 0
	}
	return count
}

// DetectMainRepo returns the root worktree path (the main repo) by parsing
// `git worktree list --porcelain`. Falls back to the given path.
func DetectMainRepo(worktreePath string) string {
	out := gitOutput(worktreePath, "worktree", "list", "--porcelain")
	if out == "" {
		return worktreePath
	}
	lines := strings.Split(out, "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "worktree ") {
		return strings.TrimPrefix(lines[0], "worktree ")
	}
	return worktreePath
}
