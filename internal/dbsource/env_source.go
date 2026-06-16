package dbsource

import "fmt"

// envSource resolves a connection from a postgres:// URL held in a local
// environment variable. This is the escape hatch for platforms without a
// native source (1Password-injected env, bastion tunnel, Neon, RDS, etc.).
type envSource struct {
	spec Spec
	deps Deps
}

func (s *envSource) Resolve() (*ConnInfo, error) {
	if s.spec.Var == "" {
		return nil, &SpecError{
			Env:     s.spec.Env,
			Message: "via: env requires a 'var' naming the local environment variable holding the postgres URL",
		}
	}
	raw := s.deps.Getenv(s.spec.Var)
	if raw == "" {
		return nil, fmt.Errorf("%w: environment variable %s is not set", ErrMissingURL, s.spec.Var)
	}
	return parsePostgresURL(raw, s.spec.SSLMode)
}
