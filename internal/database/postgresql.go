package database

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// dbIdentifierRe validates PostgreSQL identifiers to prevent SQL injection.
// Only alphanumeric characters and underscores are allowed, starting with
// a letter or underscore. This regex is checked before any identifier is
// used in SQL queries or shell commands.
var dbIdentifierRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Per-template lock for serializing concurrent database clones.
// createdb --template requires exclusive access to the template database.
var templateLocks sync.Map

// PostgreSQL implements the Adapter interface for PostgreSQL databases.
// Clone uses createdb --template, Drop uses dropdb --if-exists.
type PostgreSQL struct {
	execRun    func(name string, args ...string) error
	execOutput func(name string, args ...string) ([]byte, error)
}

func (pg *PostgreSQL) run(name string, args ...string) error {
	if pg.execRun != nil {
		return pg.execRun(name, args...)
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (pg *PostgreSQL) runSilent(name string, args ...string) error {
	if pg.execRun != nil {
		return pg.execRun(name, args...)
	}
	return exec.Command(name, args...).Run()
}

func (pg *PostgreSQL) output(name string, args ...string) ([]byte, error) {
	if pg.execOutput != nil {
		return pg.execOutput(name, args...)
	}
	return exec.Command(name, args...).Output()
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

	mu := getTemplateLock(template)
	mu.Lock()
	defer mu.Unlock()

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

func getTemplateLock(template string) *sync.Mutex {
	actual, _ := templateLocks.LoadOrStore(template, &sync.Mutex{})
	return actual.(*sync.Mutex)
}
