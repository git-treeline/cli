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
type Adapter interface {
	Clone(template, target string) error
	Drop(target string) error
	Exists(name string) (bool, error)
}

// ForAdapter returns the adapter for the given name.
// Defaults to PostgreSQL for empty string (backward compatibility).
func ForAdapter(name string) (Adapter, error) {
	switch name {
	case "postgresql", "":
		return &PostgreSQL{}, nil
	case "sqlite":
		return &SQLite{}, nil
	default:
		return nil, fmt.Errorf("unsupported database adapter: %q", name)
	}
}
