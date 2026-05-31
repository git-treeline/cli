// Package database provides adapters for database template cloning.
// Supported adapters: PostgreSQL (server-side createdb --template)
// and SQLite (file copy). The adapter interface abstracts clone, drop,
// and existence checks across database types.
package database

import "fmt"

// Adapter defines the interface for database template cloning and cleanup.
// Implementations handle database-specific operations:
//   - Clone creates a new database from a template
//   - Drop removes a database
//   - Exists checks if a database already exists
//   - Restore loads a dump file into a database
type Adapter interface {
	Clone(template, target string) error
	Drop(target string) error
	Exists(name string) (bool, error)
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
