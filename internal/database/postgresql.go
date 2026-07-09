package database

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/git-treeline/cli/internal/platform"
)

// dbIdentifierRe validates PostgreSQL identifiers to prevent SQL injection.
// Only alphanumeric characters and underscores are allowed, starting with
// a letter or underscore. This regex is checked before any identifier is
// used in SQL queries or shell commands.
var dbIdentifierRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// PostgreSQL implements the Adapter interface for PostgreSQL databases.
// Clone uses createdb --template, Drop uses dropdb --if-exists.
type PostgreSQL struct {
	// ConnArgs are prepended to every pg tool invocation (e.g. ["-h", "localhost", "-p", "5432"]).
	// Setting -h forces TCP instead of Unix socket, which avoids Postgres.app's
	// socket authorization dialog when gtl is invoked from a native macOS app.
	ConnArgs   []string
	execRun    func(name string, args ...string) error
	execOutput func(name string, args ...string) ([]byte, error)
}

func (pg *PostgreSQL) run(name string, args ...string) error {
	all := append(pg.ConnArgs, args...)
	if pg.execRun != nil {
		return pg.execRun(name, all...)
	}
	cmd := exec.Command(name, all...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (pg *PostgreSQL) runSilent(name string, args ...string) error {
	all := append(pg.ConnArgs, args...)
	if pg.execRun != nil {
		return pg.execRun(name, all...)
	}
	return exec.Command(name, all...).Run()
}

func (pg *PostgreSQL) output(name string, args ...string) ([]byte, error) {
	all := append(pg.ConnArgs, args...)
	if pg.execOutput != nil {
		return pg.execOutput(name, all...)
	}
	return exec.Command(name, all...).Output()
}

func (pg *PostgreSQL) Exists(name string) (bool, error) {
	if !dbIdentifierRe.MatchString(name) {
		return false, fmt.Errorf("invalid database identifier: %q", name)
	}

	out, err := pg.output("psql", "-lqt")
	if err != nil {
		return false, fmt.Errorf("failed to list databases: %w", err)
	}
	return parsePsqlListContains(string(out), name), nil
}

// ParsePsqlListContains checks psql -lqt output for a database name.
// Exported for testing.
func parsePsqlListContains(output, name string) bool {
	if name == "" {
		return false
	}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "|")
		if len(parts) > 0 && strings.TrimSpace(parts[0]) == name {
			return true
		}
	}
	return false
}

func (pg *PostgreSQL) Clone(template, target string) error {
	if !dbIdentifierRe.MatchString(target) {
		return fmt.Errorf("invalid database identifier: %q", target)
	}
	if !dbIdentifierRe.MatchString(template) {
		return fmt.Errorf("invalid database identifier: %q", template)
	}

	// Serialize clones of this template across every gtl process on the host.
	// createdb --template requires the template to have no other sessions, and
	// two concurrent `gtl new` runs cloning the same template would otherwise
	// race — each terminating the other's connections mid-clone. A flock on a
	// per-template lock file (advisory, released on unlock or process exit)
	// provides cross-process mutual exclusion; an in-process sync.Mutex could
	// not, since separate gtl invocations don't share memory.
	unlock, err := lockTemplate(template)
	if err != nil {
		return fmt.Errorf("locking template %s: %w", template, err)
	}
	defer unlock()

	// Terminate sessions still connected to the template (e.g. a lingering app
	// or an interrupted earlier clone) so createdb isn't rejected. Scoped by
	// datname = template, this only touches sessions on THIS template; because
	// the flock above serializes same-template clones, it can never kill a
	// concurrent clone's own createdb connection.
	// SAFETY: template is validated by dbIdentifierRe above, which only allows
	// [a-zA-Z_][a-zA-Z0-9_]* — no quotes, semicolons, or special characters.
	// This prevents SQL injection in the pg_terminate_backend query.
	terminateSQL := fmt.Sprintf(
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid();",
		template,
	)
	_ = pg.runSilent("psql", "-d", "postgres", "-c", terminateSQL)

	if err := pg.run("createdb", target, "--template", template); err != nil {
		return fmt.Errorf("failed to clone database %s -> %s: %w", template, target, err)
	}

	return nil
}

func (pg *PostgreSQL) Drop(target string) error {
	if !dbIdentifierRe.MatchString(target) {
		return fmt.Errorf("invalid database identifier: %q", target)
	}
	return pg.runSilent("dropdb", "--if-exists", target)
}

func (pg *PostgreSQL) Rename(oldName, newName string) error {
	if !dbIdentifierRe.MatchString(oldName) {
		return fmt.Errorf("invalid database identifier: %q", oldName)
	}
	if !dbIdentifierRe.MatchString(newName) {
		return fmt.Errorf("invalid database identifier: %q", newName)
	}
	// Terminate active connections before renaming — ALTER DATABASE RENAME TO
	// requires no other sessions connected to the target database.
	// SAFETY: oldName is validated by dbIdentifierRe above.
	terminateSQL := fmt.Sprintf(
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid();",
		oldName,
	)
	_ = pg.runSilent("psql", "-d", "postgres", "-c", terminateSQL)
	// SAFETY: both identifiers are validated by dbIdentifierRe above.
	renameSQL := fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", oldName, newName)
	return pg.runSilent("psql", "-d", "postgres", "-c", renameSQL)
}

func (pg *PostgreSQL) Restore(target, dumpFile string) error {
	if !dbIdentifierRe.MatchString(target) {
		return fmt.Errorf("invalid database identifier: %q", target)
	}

	if err := pg.run("createdb", target); err != nil {
		return fmt.Errorf("creating database %s: %w", target, err)
	}

	var err error
	if isCustomFormat(dumpFile) {
		err = pg.run("pg_restore", "--no-owner", "--no-acl", "-d", target, dumpFile)
	} else {
		err = pg.run("psql", "-d", target, "-f", dumpFile)
	}
	if err != nil {
		return fmt.Errorf("restoring %s into %s: %w", dumpFile, target, err)
	}
	return nil
}

func isCustomFormat(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	// pg_dump custom format starts with "PGDMP"
	header := make([]byte, 5)
	n, _ := f.Read(header)
	return n == 5 && string(header) == "PGDMP"
}

// lockTemplate acquires an exclusive, cross-process advisory lock keyed on the
// template name and returns a release function. The lock file lives under the
// gtl config dir so every gtl invocation on the host agrees on the same path.
// template is pre-validated by dbIdentifierRe, so it is safe as a filename
// component (no path separators or traversal).
func lockTemplate(template string) (func(), error) {
	dir := filepath.Join(platform.ConfigDir(), "locks")
	if err := os.MkdirAll(dir, platform.DirMode); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, "template-"+template+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, platform.PrivateFileMode)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
