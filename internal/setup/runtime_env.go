package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/interpolation"
	"github.com/git-treeline/cli/internal/platform"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/resolve"
	"github.com/git-treeline/cli/internal/worktree"
)

// ResolveRuntimeEnv returns the complete set of Treeline-managed variables
// for an allocated worktree. The returned map is suitable for replacing a
// supervisor's managed environment; callers must not merge it with an older
// result because a key may have been removed from the current env template.
// A worktree without an allocation returns nil, nil.
func ResolveRuntimeEnv(worktreePath string, uc *config.UserConfig) (map[string]string, error) {
	absPath, err := filepath.Abs(worktreePath)
	if err != nil {
		return nil, fmt.Errorf("resolving worktree path: %w", err)
	}

	reg := registry.New(RegistryPath)
	allocMap := reg.Find(absPath)
	if allocMap == nil {
		return nil, nil
	}

	pc := config.LoadProjectConfig(absPath)
	if err := pc.Validate(); err != nil {
		return nil, fmt.Errorf("validating project config: %w", err)
	}
	interpAlloc := interpolation.Allocation(allocMap)
	branch := worktree.CurrentBranch(absPath)
	InjectRouterTokens(interpAlloc, pc.Project(), branch, uc.RouterDomain(), uc.TunnelDomain(""))
	redisURL := interpolation.BuildRedisURL(uc.RedisURL(), interpAlloc)
	resolver := resolve.New(reg, absPath, branch)

	envVars, err := BuildEnvVarsWithResolver(pc, interpAlloc, redisURL, resolver.Resolve)
	if err != nil {
		return nil, fmt.Errorf("resolving env vars: %w", err)
	}
	return envVars, nil
}

// SyncRuntimeEnv writes the current managed environment into the worktree's
// env file and returns the same complete managed map for a supervised child.
// A worktree without an allocation returns nil, nil, matching the historical
// RegenerateEnvFile no-op behavior.
func SyncRuntimeEnv(worktreePath string, uc *config.UserConfig) (map[string]string, error) {
	envVars, err := ResolveRuntimeEnv(worktreePath, uc)
	if err != nil || envVars == nil {
		return envVars, err
	}

	absPath, err := filepath.Abs(worktreePath)
	if err != nil {
		return nil, fmt.Errorf("resolving worktree path: %w", err)
	}
	pc := config.LoadProjectConfig(absPath)
	if err := writeManagedEnv(filepath.Join(absPath, pc.EnvFileTarget()), envVars); err != nil {
		return nil, err
	}

	return envVars, nil
}

// managedEnvState records hashes of assignments written by Treeline. It is deliberately
// separate from the env file: a key present in an older installation has no
// ownership record and must never be removed just because it disappears from
// the current template.
type managedEnvState struct {
	Assignments map[string]string `json:"assignments"`
}

// writeManagedEnv applies vars to envPath and records the exact assignments it
// wrote. When a formerly managed key is absent from vars, its assignment is
// removed only if it still has Treeline's last-written value. This protects
// manual edits while allowing template removals to reach dotenv consumers.
func writeManagedEnv(envPath string, vars map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		return fmt.Errorf("creating env file directory: %w", err)
	}
	if info, err := os.Lstat(envPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write managed env through symlink: %s", envPath)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking env file: %w", err)
	}
	return withManagedEnvLock(envPath, func() error {
		return writeManagedEnvLocked(envPath, vars)
	})
}

func writeManagedEnvLocked(envPath string, vars map[string]string) error {
	state, known, err := loadManagedEnvState(envPath)
	if err != nil {
		return err
	}
	if vars == nil {
		vars = map[string]string{}
	}

	data, mode, fileExists, err := readManagedEnvFile(envPath)
	if err != nil {
		return fmt.Errorf("reading env file: %w", err)
	}
	content := string(data)
	if known {
		for key, assignmentHash := range state.Assignments {
			if _, stillManaged := vars[key]; !stillManaged {
				content = removeManagedAssignments(content, key, assignmentHash)
			}
		}
	}

	for _, key := range sortedEnvKeys(vars) {
		content = replaceOrAppendEnvAssignment(content, key, vars[key])
	}

	// A no-op must leave an already-private env file alone so file watchers do
	// not treat a refresh as an application change. A broader existing file is
	// atomically replaced even when its contents are unchanged, avoiding a
	// path-based chmod that could follow a swapped symlink.
	if content != string(data) || (!fileExists && content != "") || (fileExists && mode != platform.PrivateFileMode) {
		if err := platform.AtomicWriteFile(envPath, []byte(content), platform.PrivateFileMode); err != nil {
			return fmt.Errorf("writing env file: %w", err)
		}
	}
	if err := saveManagedEnvState(envPath, managedEnvState{Assignments: managedEnvAssignmentHashes(vars)}); err != nil {
		return err
	}
	return nil
}

// readManagedEnvFile verifies that the opened file is the regular file that
// was inspected. That avoids following a destination symlink inserted between
// the initial Lstat and the read, while still allowing symlinked parents.
func readManagedEnvFile(path string) ([]byte, os.FileMode, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, 0, false, fmt.Errorf("refusing to read managed env through symlink: %s", path)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("managed env is not a regular file: %s", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer func() { _ = file.Close() }()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, 0, false, err
	}
	if !os.SameFile(info, openedInfo) {
		return nil, 0, false, fmt.Errorf("managed env changed while opening: %s", path)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, false, err
	}
	return data, openedInfo.Mode(), true, nil
}

// withManagedEnvLock keeps the env file and its ownership state in sync when
// concurrent CLI commands update the same worktree.
func withManagedEnvLock(envPath string, fn func() error) error {
	statePath, err := managedEnvStatePath(envPath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(statePath)
	if err := os.MkdirAll(dir, platform.DirMode); err != nil {
		return fmt.Errorf("creating managed env state directory: %w", err)
	}
	if err := os.Chmod(dir, platform.DirMode); err != nil {
		return fmt.Errorf("securing managed env state directory: %w", err)
	}

	lockPath := statePath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, platform.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("opening managed env lock: %w", err)
	}
	defer func() { _ = lockFile.Close() }()
	if err := os.Chmod(lockPath, platform.PrivateFileMode); err != nil {
		return fmt.Errorf("securing managed env lock: %w", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		err = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for managed env lock: %s", lockPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func sortedEnvKeys(vars map[string]string) []string {
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func managedEnvAssignmentHashes(vars map[string]string) map[string]string {
	values := make(map[string]string, len(vars))
	for key, value := range vars {
		values[key] = hashEnvAssignment(formatEnvAssignment(key, value))
	}
	return values
}

func replaceOrAppendEnvAssignment(content, key, value string) string {
	line := formatEnvAssignment(key, value)
	re := regexpForEnvKey(key)
	if re.MatchString(content) {
		return re.ReplaceAllStringFunc(content, func(string) string { return line })
	}
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + line + "\n"
}

func removeManagedAssignments(content, key, assignmentHash string) string {
	lines := strings.SplitAfter(content, "\n")
	var kept strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimSuffix(line, "\n")
		if isEnvAssignmentForKey(trimmed, key) && hashEnvAssignment(trimmed) == assignmentHash {
			continue
		}
		kept.WriteString(line)
	}
	return kept.String()
}

func isEnvAssignmentForKey(line, key string) bool {
	return strings.HasPrefix(line, key+"=")
}

func formatEnvAssignment(key, value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	escaped = strings.ReplaceAll(escaped, "\r", `\r`)
	return fmt.Sprintf(`%s="%s"`, key, escaped)
}

func regexpForEnvKey(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=.*$`)
}

func hashEnvAssignment(assignment string) string {
	hash := sha256.Sum256([]byte(assignment))
	return hex.EncodeToString(hash[:])
}

func managedEnvStatePath(envPath string) (string, error) {
	absPath, err := filepath.Abs(envPath)
	if err != nil {
		return "", fmt.Errorf("resolving env file path: %w", err)
	}
	canonicalPath := filepath.Join(canonicalManagedEnvParent(filepath.Dir(absPath)), filepath.Base(absPath))
	hash := sha256.Sum256([]byte(canonicalPath))
	return filepath.Join(managedEnvStateDir(), hex.EncodeToString(hash[:])+".json"), nil
}

// canonicalManagedEnvParent resolves symlinks in the env file's containing
// directory while preserving a missing tail. The env file itself is left
// untouched so callers still write through their requested path.
func canonicalManagedEnvParent(dir string) string {
	original := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}

	tail := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return original
		}
		tail = append(tail, filepath.Base(dir))
		dir = parent
	}
}

func managedEnvStateDir() string {
	if RegistryPath != "" {
		return filepath.Join(filepath.Dir(RegistryPath), "managed-env")
	}
	return filepath.Join(platform.ConfigDir(), "managed-env")
}

func loadManagedEnvState(envPath string) (managedEnvState, bool, error) {
	statePath, err := managedEnvStatePath(envPath)
	if err != nil {
		return managedEnvState{}, false, err
	}
	data, err := os.ReadFile(statePath)
	if os.IsNotExist(err) {
		return managedEnvState{}, false, nil
	}
	if err != nil {
		return managedEnvState{}, false, fmt.Errorf("reading managed env state: %w", err)
	}
	var state managedEnvState
	if err := json.Unmarshal(data, &state); err != nil {
		return managedEnvState{}, false, fmt.Errorf("decoding managed env state: %w", err)
	}
	if state.Assignments == nil {
		state.Assignments = map[string]string{}
	}
	return state, true, nil
}

func saveManagedEnvState(envPath string, state managedEnvState) error {
	statePath, err := managedEnvStatePath(envPath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(statePath)
	if err := os.MkdirAll(dir, platform.DirMode); err != nil {
		return fmt.Errorf("creating managed env state directory: %w", err)
	}
	if err := os.Chmod(dir, platform.DirMode); err != nil {
		return fmt.Errorf("securing managed env state directory: %w", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encoding managed env state: %w", err)
	}
	if err := platform.AtomicWriteFile(statePath, data, platform.PrivateFileMode); err != nil {
		return fmt.Errorf("writing managed env state: %w", err)
	}
	return nil
}
