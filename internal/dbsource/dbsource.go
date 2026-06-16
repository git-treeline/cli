// Package dbsource resolves remote PostgreSQL connection details for a
// configured database source (production, staging, ...).
//
// Each source's `via:` value maps to a Source implementation: `fly` reads
// connection info from a Fly app's environment, `url` reads a postgres:// URL
// from a local environment variable. Resolution produces a ConnInfo and never
// runs pg tooling itself — the Source interface is the seam for adding future
// reach strategies (proxy, remote-dump) without changing callers.
package dbsource

import (
	"os"
	"os/exec"
)

// ConnInfo is a fully-resolved remote PostgreSQL connection.
type ConnInfo struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// Spec describes one configured source, parsed from .treeline.yml's
// database.sources.<env> block.
type Spec struct {
	Env     string // logical env name, e.g. "production"
	Via     string // "fly" | "heroku" | "env"
	App     string // fly/heroku: the app name
	Var     string // fly/heroku: env var to read on the platform (default DATABASE_URL); env: local env var name
	SSLMode string // explicit config sslmode ("" when unset); see resolveSSLMode
}

// Source resolves a remote connection for one configured env.
type Source interface {
	Resolve() (*ConnInfo, error)
}

// Deps holds injectable seams so resolution can be tested without a real
// platform CLI or process environment.
type Deps struct {
	// RunFly runs the fly CLI with args and returns its combined output.
	RunFly func(args ...string) ([]byte, error)
	// RunHeroku runs the heroku CLI with args and returns its combined output.
	RunHeroku func(args ...string) ([]byte, error)
	// Getenv reads a local environment variable.
	Getenv func(string) string
	// LookPath reports whether a binary is on PATH.
	LookPath func(string) (string, error)
}

// DefaultDeps wires Deps to the real platform CLIs and process environment.
func DefaultDeps() Deps {
	return Deps{
		RunFly: func(args ...string) ([]byte, error) {
			return exec.Command("fly", args...).CombinedOutput()
		},
		RunHeroku: func(args ...string) ([]byte, error) {
			return exec.Command("heroku", args...).CombinedOutput()
		},
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
	}
}

// New builds the Source for a Spec, or an *UnknownViaError if via is unsupported.
func New(s Spec, deps Deps) (Source, error) {
	switch s.Via {
	case "fly":
		return &flySource{spec: s, deps: deps}, nil
	case "heroku":
		return &herokuSource{spec: s, deps: deps}, nil
	case "env":
		return &envSource{spec: s, deps: deps}, nil
	default:
		return nil, &UnknownViaError{Via: s.Via, Env: s.Env}
	}
}

// resolveSSLMode applies precedence: an explicit config value wins, then a
// value carried on the connection (URL query / PGSSLMODE), then "require".
// Cloud Postgres requires SSL, so "require" is the safe default.
func resolveSSLMode(explicit, fromConn string) string {
	if explicit != "" {
		return explicit
	}
	if fromConn != "" {
		return fromConn
	}
	return "require"
}
