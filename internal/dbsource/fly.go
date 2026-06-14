package dbsource

import (
	"fmt"
	"strings"
)

// flySource resolves a connection by reading a Fly app's runtime environment
// via `fly ssh console -a <app> -C 'printenv'`. It prefers a single URL var
// (DATABASE_URL by default), falling back to discrete PG* variables.
type flySource struct {
	spec Spec
	deps Deps
}

func (s *flySource) Resolve() (*ConnInfo, error) {
	if s.spec.App == "" {
		return nil, &SpecError{Env: s.spec.Env, Message: "via: fly requires an 'app' naming the Fly app"}
	}
	if s.deps.LookPath != nil {
		if _, err := s.deps.LookPath("fly"); err != nil {
			return nil, ErrFlyNotInstalled
		}
	}

	out, err := s.deps.RunFly("ssh", "console", "-a", s.spec.App, "-C", "printenv")
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if looksUnauthenticated(detail) {
			return nil, fmt.Errorf("%w (app %s)", ErrFlyNotAuthed, s.spec.App)
		}
		if detail != "" {
			return nil, fmt.Errorf("fly ssh console -a %s failed: %s", s.spec.App, detail)
		}
		return nil, fmt.Errorf("fly ssh console -a %s failed: %w", s.spec.App, err)
	}

	env := parsePrintenv(string(out))
	varName := s.spec.Var
	if varName == "" {
		varName = "DATABASE_URL"
	}
	if raw := env[varName]; raw != "" {
		return parsePostgresURL(raw, s.spec.SSLMode)
	}
	if ci, ok := connFromPGStar(env, s.spec.SSLMode); ok {
		return ci, nil
	}
	return nil, &VarNotFoundError{Env: s.spec.Env, Var: varName, App: s.spec.App}
}

// parsePrintenv turns `printenv` output into a key→value map, splitting each
// line on its first '='. Lines without '=' (and blank lines) are ignored so a
// value that itself contains '=' is preserved intact.
func parsePrintenv(out string) map[string]string {
	env := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		i := strings.IndexByte(line, '=')
		if i <= 0 {
			continue
		}
		env[line[:i]] = line[i+1:]
	}
	return env
}

// connFromPGStar builds a ConnInfo from discrete PGHOST/PGPORT/... variables.
// It requires at least PGHOST and PGDATABASE to be present.
func connFromPGStar(env map[string]string, defaultSSLMode string) (*ConnInfo, bool) {
	host := env["PGHOST"]
	db := env["PGDATABASE"]
	if host == "" || db == "" {
		return nil, false
	}
	port := env["PGPORT"]
	if port == "" {
		port = "5432"
	}
	return &ConnInfo{
		Host:     host,
		Port:     port,
		User:     env["PGUSER"],
		Password: env["PGPASSWORD"],
		DBName:   db,
		SSLMode:  resolveSSLMode(defaultSSLMode, env["PGSSLMODE"]),
	}, true
}

func looksUnauthenticated(detail string) bool {
	d := strings.ToLower(detail)
	return strings.Contains(d, "not authenticated") ||
		strings.Contains(d, "no access token") ||
		strings.Contains(d, "must be logged in") ||
		strings.Contains(d, "please log in") ||
		strings.Contains(d, "401")
}
