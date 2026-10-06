package devresources

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

func validSQLiteName(name string) bool {
	if name == "" || name == "." || name == ".." || filepath.IsAbs(name) || filepath.Clean(name) != name || strings.HasPrefix(name, "../") || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return false
	}
	for _, component := range strings.Split(name, string(filepath.Separator)) {
		if strings.EqualFold(component, ".git") {
			return false
		}
	}
	return true
}

func validSQLitePattern(pattern string) bool {
	if !validSQLiteName(pattern) {
		return false
	}
	rendered := strings.NewReplacer("{template}", "template", "{worktree}", "worktree", "{project}", "project").Replace(pattern)
	return !strings.ContainsAny(rendered, "{}") && validSQLiteName(rendered)
}

// ValidateSQLiteFile checks a managed-dev database and its SQLite companions.
// It permits absent paths but never follows symlinks or accepts shared inodes.
// Callers must recheck immediately before an effect and own lifecycle exclusion;
// this read-only check does not exclude concurrent external filesystem writers.
func ValidateSQLiteFile(root, name string) error {
	if !validSQLiteName(name) || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return refusal
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return refusal
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return refusal
	}
	workspace := filepath.Join(home, ".treeline-dev", "workspaces")
	if !strings.HasPrefix(root, workspace+string(filepath.Separator)) {
		return refusal
	}
	relative, err := filepath.Rel(home, root)
	if err != nil || !validSQLiteName(relative) {
		return refusal
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		parts := strings.Split(filepath.Join(relative, name+suffix), string(filepath.Separator))
		current := home
		for index := -1; index < len(parts); index++ {
			if index >= 0 {
				current = filepath.Join(current, parts[index])
			}
			info, err := os.Lstat(current)
			if os.IsNotExist(err) && index >= 2 {
				break
			}
			if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
				return refusal
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || stat.Uid != uint32(os.Getuid()) {
				return refusal
			}
			if index == len(parts)-1 {
				if !info.Mode().IsRegular() || stat.Nlink != 1 {
					return refusal
				}
			} else if !info.IsDir() {
				return refusal
			}
		}
	}
	return nil
}
