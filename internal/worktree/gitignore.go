package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnsureGitignored checks whether a worktree path that lives inside the repo
// root is gitignored. If not, it appends the directory to .gitignore and
// returns the pattern that was added. Paths outside the repo root (the default
// sibling layout) are a no-op and return "".
func EnsureGitignored(mainRepo, wtPath string) (pattern string, err error) {
	absRepo, _ := filepath.Abs(mainRepo)
	absWT, _ := filepath.Abs(wtPath)

	rel, err := filepath.Rel(absRepo, absWT)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", nil
	}

	cmd := exec.Command("git", "check-ignore", "-q", absWT)
	cmd.Dir = mainRepo
	if cmd.Run() == nil {
		return "", nil
	}

	topLevel := strings.SplitN(rel, string(filepath.Separator), 2)[0]
	pat := "/" + topLevel + "/"

	gitignorePath := filepath.Join(absRepo, ".gitignore")
	existing, _ := os.ReadFile(gitignorePath)
	if strings.Contains(string(existing), pat) {
		return "", nil
	}

	entry := pat + "\n"
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		entry = "\n" + entry
	}
	if err := os.WriteFile(gitignorePath, append(existing, []byte(entry)...), 0o644); err != nil {
		return "", fmt.Errorf("updating .gitignore: %w", err)
	}
	return pat, nil
}
