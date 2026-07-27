package database

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestFindInDirs(t *testing.T) {
	dirs := []string{"", "/opt/pg/bin", "/usr/local/bin"}

	// Skips the empty dir and the non-matching one, returns the first hit.
	got := findInDirs("psql", dirs, func(p string) bool {
		return p == filepath.Join("/usr/local/bin", "psql")
	})
	if want := "/usr/local/bin/psql"; got != want {
		t.Fatalf("findInDirs = %q, want %q", got, want)
	}

	// No dir matches -> empty string.
	if got := findInDirs("psql", dirs, func(string) bool { return false }); got != "" {
		t.Fatalf("findInDirs (no match) = %q, want empty", got)
	}

	// Earliest matching dir wins.
	got = findInDirs("psql", []string{"/a/bin", "/b/bin"}, func(string) bool { return true })
	if want := filepath.Join("/a/bin", "psql"); got != want {
		t.Fatalf("findInDirs (first wins) = %q, want %q", got, want)
	}
}

func TestPgToolDirs(t *testing.T) {
	dirs := pgToolDirs()

	if runtime.GOOS != "darwin" {
		if dirs != nil {
			t.Fatalf("pgToolDirs off darwin = %v, want nil", dirs)
		}
		return
	}

	// Postgres.app's `latest` symlink is the authoritative default and must
	// be searched first.
	if len(dirs) == 0 || dirs[0] != "/Applications/Postgres.app/Contents/Versions/latest/bin" {
		t.Fatalf("Postgres.app latest not searched first: %v", dirs)
	}

	// Homebrew's keg-only libpq must be among the fallbacks.
	if !contains(dirs, "/opt/homebrew/opt/libpq/bin") {
		t.Fatalf("homebrew libpq dir missing from %v", dirs)
	}
}

func TestPgToolAbsolutePathPassthrough(t *testing.T) {
	p := "/custom/pgroot/bin/psql"
	if got := pgTool(p); got != p {
		t.Fatalf("pgTool(%q) = %q, want passthrough", p, got)
	}
}

func TestPgToolUnresolvableFallsBackToBareName(t *testing.T) {
	// Neither on $PATH nor in any known dir: return the name unchanged so the
	// caller still gets the original "not found in $PATH" error.
	name := "gtl-nonexistent-pg-tool-xyz"
	if got := pgTool(name); got != name {
		t.Fatalf("pgTool(%q) = %q, want bare-name fallback", name, got)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
