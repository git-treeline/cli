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
type PostgreSQL struct{}

func (pg *PostgreSQL) Exists(name string) (bool, error) {
	if !dbIdentifierRe.MatchString(name) {
		return false, fmt.Errorf("invalid database identifier: %q", name)
	}

	out, err := exec.Command("psql", "-lqt").Output()
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
	_ = exec.Command("psql", "-d", "postgres", "-c", terminateSQL).Run()

	cmd := exec.Command("createdb", target, "--template", template)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to clone database %s -> %s: %w", template, target, err)
	}

	return nil
}

func (pg *PostgreSQL) Drop(target string) error {
	return exec.Command("dropdb", "--if-exists", target).Run()
}

func getTemplateLock(template string) *sync.Mutex {
	actual, _ := templateLocks.LoadOrStore(template, &sync.Mutex{})
	return actual.(*sync.Mutex)
}
