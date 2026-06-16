package dbsource

import (
	"fmt"
	"strings"
)

// herokuSource resolves a connection by reading a Heroku app's config var
// via `heroku config:get <var> -a <app>`. Defaults to DATABASE_URL.
type herokuSource struct {
	spec Spec
	deps Deps
}

func (s *herokuSource) Resolve() (*ConnInfo, error) {
	if s.spec.App == "" {
		return nil, &SpecError{Env: s.spec.Env, Message: "via: heroku requires an 'app' naming the Heroku app"}
	}
	if s.deps.LookPath != nil {
		if _, err := s.deps.LookPath("heroku"); err != nil {
			return nil, ErrHerokuNotInstalled
		}
	}

	varName := s.spec.Var
	if varName == "" {
		varName = "DATABASE_URL"
	}

	out, err := s.deps.RunHeroku("config:get", varName, "-a", s.spec.App)
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if looksHerokuUnauthenticated(detail) {
			return nil, fmt.Errorf("%w (app %s)", ErrHerokuNotAuthed, s.spec.App)
		}
		if detail != "" {
			return nil, fmt.Errorf("heroku config:get -a %s failed: %s", s.spec.App, detail)
		}
		return nil, fmt.Errorf("heroku config:get -a %s failed: %w", s.spec.App, err)
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, &VarNotFoundError{Env: s.spec.Env, Var: varName, App: s.spec.App}
	}
	return parsePostgresURL(raw, s.spec.SSLMode)
}

func looksHerokuUnauthenticated(detail string) bool {
	d := strings.ToLower(detail)
	return strings.Contains(d, "not logged in") ||
		strings.Contains(d, "authentication required") ||
		strings.Contains(d, "invalid credentials") ||
		strings.Contains(d, "ip allowed") ||
		strings.Contains(d, "401")
}
