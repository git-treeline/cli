package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func withTemplateCheckout(ctx context.Context, mainRepo, branch string, log io.Writer, run func(path string) error) (result error) {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
			"GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE",
			"GIT_INDEX_FILE", "GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX", "GIT_SHALLOW_FILE", "GIT_COMMON_DIR":
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "GTL_DEFER_SETUP=1", "LC_ALL=C")
	git := func(ctx context.Context, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = mainRepo
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			operation := args[0]
			if operation == "-c" {
				operation = args[2]
			}
			return "", fmt.Errorf("git %s: %w: %s", operation, err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	version, err := git(ctx, "--version")
	if err != nil {
		return fmt.Errorf("checking Git version for template database update: %w", err)
	}
	if err := checkTemplateCheckoutGitVersion(version); err != nil {
		return err
	}
	if branch == "" {
		ref, err := git(ctx, "symbolic-ref", "refs/remotes/origin/HEAD")
		if err == nil && strings.HasPrefix(ref, "refs/remotes/origin/") {
			branch = strings.TrimPrefix(ref, "refs/remotes/origin/")
		} else {
			branch = "main"
		}
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("invalid template branch %q", branch)
	}
	if _, err := git(ctx, "check-ref-format", "refs/heads/"+branch); err != nil {
		return fmt.Errorf("invalid template branch %q: %w", branch, err)
	}
	commonDir, err := git(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(mainRepo, commonDir)
	}
	commonDir, err = filepath.Abs(commonDir)
	if err != nil {
		return fmt.Errorf("resolving Git common directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(commonDir, "gtl-template-update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening template update lock: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("a template database update is already running for this repository")
		}
		return fmt.Errorf("locking template update: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	tempDir, err := os.MkdirTemp("", "gtl-template-update-")
	if err != nil {
		return fmt.Errorf("creating template checkout directory: %w", err)
	}
	checkout := filepath.Join(tempDir, "checkout")
	hooksDir := filepath.Join(tempDir, "hooks")
	tempRef := "refs/gtl/template-updates/" + filepath.Base(tempDir)
	added := false
	defer func() {
		cleanupCtx := context.WithoutCancel(ctx)
		keepDir := false
		if _, err := os.Lstat(filepath.Join(checkout, ".git")); added || err == nil {
			if _, err := git(cleanupCtx, "-c", "core.hooksPath="+hooksDir, "worktree", "remove", "--force", checkout); err != nil {
				result = errors.Join(result, fmt.Errorf("removing temporary template checkout (retained at %s): %w", checkout, err))
				keepDir = true
			}
		}
		if _, err := git(cleanupCtx, "-c", "core.hooksPath="+hooksDir, "update-ref", "-d", tempRef); err != nil {
			result = errors.Join(result, fmt.Errorf("removing temporary template ref %s: %w", tempRef, err))
		}
		if !keepDir {
			if err := os.RemoveAll(tempDir); err != nil {
				result = errors.Join(result, fmt.Errorf("removing template checkout directory %s: %w", tempDir, err))
			}
		}
	}()
	if err := os.Mkdir(hooksDir, 0o700); err != nil {
		return fmt.Errorf("creating empty hooks directory: %w", err)
	}
	if log != nil {
		_, _ = fmt.Fprintf(log, "Fetching origin/%s for template database update\n", branch)
	}
	if _, err := git(ctx, "-c", "core.hooksPath="+hooksDir, "fetch", "--no-write-fetch-head", "--no-auto-maintenance", "--no-tags", "--no-recurse-submodules", "--refmap=", "origin", "refs/heads/"+branch+":"+tempRef); err != nil {
		return fmt.Errorf("fetching template branch: %w", err)
	}
	commit, err := git(ctx, "rev-parse", "--verify", tempRef+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolving template commit: %w", err)
	}
	if _, err := git(ctx, "-c", "core.hooksPath="+hooksDir, "worktree", "add", "--detach", checkout, commit); err != nil {
		return fmt.Errorf("creating template checkout: %w", err)
	}
	added = true
	return run(checkout)
}

func checkTemplateCheckoutGitVersion(output string) error {
	fields := strings.Fields(output)
	if len(fields) < 3 || fields[0] != "git" || fields[1] != "version" {
		return fmt.Errorf("cannot determine Git version from %q; template database updates require Git 2.29 or newer", output)
	}
	parts := strings.Split(fields[2], ".")
	if len(parts) < 2 {
		return fmt.Errorf("cannot determine Git version from %q; template database updates require Git 2.29 or newer", output)
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 0 || minor < 0 {
		return fmt.Errorf("cannot determine Git version from %q; template database updates require Git 2.29 or newer", output)
	}
	if major < 2 || (major == 2 && minor < 29) {
		return fmt.Errorf("template database updates require Git 2.29 or newer (found %s); upgrade Git and try again", fields[2])
	}
	return nil
}
