package dbsource

import "fmt"

// urlSource resolves a connection from a postgres:// URL held in a local
// environment variable. This is the universal escape hatch for anything not
// covered by a platform preset (1Password-injected env, bastion tunnel, etc.).
type urlSource struct {
	spec Spec
	deps Deps
}

func (s *urlSource) Resolve() (*ConnInfo, error) {
	if s.spec.URLEnv == "" {
		return nil, &SpecError{
			Env:     s.spec.Env,
			Message: "via: url requires an 'env' naming the local environment variable holding the postgres URL",
		}
	}
	raw := s.deps.Getenv(s.spec.URLEnv)
	if raw == "" {
		return nil, fmt.Errorf("%w: environment variable %s is not set", ErrMissingURL, s.spec.URLEnv)
	}
	return parsePostgresURL(raw, s.spec.SSLMode)
}
