// Package database provides adapters for database template cloning.
// Supported adapters: PostgreSQL (server-side createdb --template)
// and SQLite (file copy). The adapter interface abstracts clone, drop,
// and existence checks across database types.
package database

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Adapter defines the interface for database template cloning and cleanup.
// Implementations handle database-specific operations:
//   - Clone creates a new database from a template
//   - Create creates an empty database with no template
//   - Drop removes a database
//   - Exists checks if a database already exists
//   - Rename renames an existing database in place
//   - Restore loads a dump file into a database
type Adapter interface {
	Clone(template, target string) error
	Create(name string) error
	Drop(target string) error
	Exists(name string) (bool, error)
	Rename(oldName, newName string) error
	Restore(target, dumpFile string) error
}

// ForAdapter returns the adapter for the given name.
// Defaults to PostgreSQL for empty string (backward compatibility).
// connArgs are connection flags (e.g. ["-h", "localhost"]) prepended to every
// pg tool invocation; pass nil when no explicit connection is needed.
func ForAdapter(name string, connArgs []string) (Adapter, error) {
	switch name {
	case "postgresql", "":
		return &PostgreSQL{ConnArgs: connArgs}, nil
	case "sqlite":
		return &SQLite{}, nil
	default:
		return nil, fmt.Errorf("unsupported database adapter: %q", name)
	}
}

// ForCheckedCleanup accepts only explicit connection coordinates. Ambient libpq
// routing is refused before even a listing; credentials stay in the captured
// process environment and never enter the persisted release receipt.
func ForCheckedCleanup(name string, connArgs []string) (Adapter, error) {
	return checkedCleanupAdapter(name, connArgs, os.Environ())
}

func checkedCleanupAdapter(name string, connArgs, env []string) (Adapter, error) {
	if name == "sqlite" {
		return &SQLite{}, nil
	}
	refuse := func() (Adapter, error) { return nil, fmt.Errorf("database connection is not explicitly bound") }
	if name != "postgresql" && name != "" || len(connArgs) != 6 {
		return refuse()
	}
	values := map[string]string{}
	for i := 0; i < len(connArgs); i += 2 {
		key, value := connArgs[i], connArgs[i+1]
		if (key != "-h" && key != "-p" && key != "-U") || values[key] != "" || value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return refuse()
		}
		values[key] = value
	}
	port, err := strconv.Atoi(values["-p"])
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != values["-p"] || strings.Contains(values["-h"], ",") || strings.Contains(values["-U"], "=") {
		return refuse()
	}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "PG") && key != "PGPASSWORD" && key != "PGPASSFILE" {
			return refuse()
		}
	}
	return &PostgreSQL{ConnArgs: append([]string(nil), connArgs...), checkedEnv: append([]string{}, env...)}, nil
}
