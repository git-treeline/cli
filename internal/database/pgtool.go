package database

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// pgTool resolves the absolute path of a PostgreSQL client binary
// (psql, pg_dump, pg_restore, createdb, dropdb) for exec.
//
// gtl forks these tools by bare name. From a normal shell that resolves
// fine against $PATH. But gtl is frequently invoked as a child of a
// GUI-launched macOS app (Treeline), which inherits the sandbox-default
// launchd PATH — /usr/bin:/bin:/usr/sbin:/sbin — with none of the Homebrew
// or Postgres.app directories a developer's interactive shell carries. The
// pg client tools are then not found, and a DB-backed `gtl setup` / `db
// reset` fails with `exec: "psql": executable file not found in $PATH`
// even though Postgres is installed.
//
// This is the binary-resolution sibling of the ConnArgs work (see the
// PostgreSQL struct): that made the *connection* GUI-app-safe (forcing TCP
// to dodge Postgres.app's socket-authorization dialog); this makes *binary
// lookup* GUI-app-safe. $PATH stays authoritative — LookPath still wins, so
// terminal behavior is unchanged — and we only fall back to well-known
// install locations when it misses. If nothing resolves we return the bare
// name, preserving the original "not found in $PATH" error rather than
// masking it behind a different failure.
func pgTool(name string) string {
	// An explicit path (or an already-resolved name) is used verbatim.
	if strings.ContainsRune(name, os.PathSeparator) {
		return name
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if p := findInDirs(name, pgToolDirs(), isExecutableFile); p != "" {
		return p
	}
	return name
}

// pgToolDirs returns directories to search for pg client binaries beyond
// $PATH. Only macOS needs this: Postgres.app (the dominant macOS install)
// keeps its binaries inside the app bundle, entirely off any default PATH,
// and Homebrew's keg-only libpq is likewise off the launchd PATH. On Linux
// the tools install into standard bin dirs already on PATH, and a GUI
// launchd context isn't gtl's use case there.
func pgToolDirs() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	dirs := []string{
		// Postgres.app: `latest` is the symlink it points at the user's
		// active version — the authoritative default.
		"/Applications/Postgres.app/Contents/Versions/latest/bin",
	}
	// Explicitly installed Postgres.app versions, newest first, as a fallback
	// when `latest` is absent (some setups pin a version). Reverse-lexical is
	// good enough for modern (>= 10) two-digit majors.
	versioned, _ := filepath.Glob("/Applications/Postgres.app/Contents/Versions/*/bin")
	sort.Sort(sort.Reverse(sort.StringSlice(versioned)))
	dirs = append(dirs, versioned...)
	// Homebrew (Apple silicon then Intel), including the keg-only libpq that
	// ships the client tools without a full server.
	dirs = append(dirs,
		"/opt/homebrew/opt/libpq/bin",
		"/opt/homebrew/bin",
		"/usr/local/opt/libpq/bin",
		"/usr/local/bin",
	)
	// EnterpriseDB / postgresql.org graphical installer.
	edb, _ := filepath.Glob("/Library/PostgreSQL/*/bin")
	sort.Sort(sort.Reverse(sort.StringSlice(edb)))
	return append(dirs, edb...)
}

// findInDirs returns the path of the first dir/name that isExec reports as an
// executable file, or "" if none match. Pure but for the injected predicate,
// so it is unit-testable without touching the filesystem.
func findInDirs(name string, dirs []string, isExec func(string) bool) string {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if isExec(candidate) {
			return candidate
		}
	}
	return ""
}

// isExecutableFile reports whether path is a regular file (symlinks followed,
// so Postgres.app's `latest` resolves) with an executable bit set.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}
