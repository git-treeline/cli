package dbsource

import (
	"errors"
	"fmt"
	"testing"
)

func herokuDeps(out string, runErr error, lookErr error) Deps {
	return Deps{
		RunHeroku: func(args ...string) ([]byte, error) {
			return []byte(out), runErr
		},
		LookPath: func(string) (string, error) {
			if lookErr != nil {
				return "", lookErr
			}
			return "/usr/local/bin/heroku", nil
		},
	}
}

func TestHerokuSource_DatabaseURL(t *testing.T) {
	src := &herokuSource{
		spec: Spec{Env: "production", Via: "heroku", App: "my-app"},
		deps: herokuDeps("postgres://u:secret@db.heroku.com:5432/club\n", nil, nil),
	}
	ci, err := src.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if ci.Host != "db.heroku.com" || ci.DBName != "club" || ci.Password != "secret" {
		t.Errorf("got %+v", ci)
	}
}

func TestHerokuSource_CustomVar(t *testing.T) {
	src := &herokuSource{
		spec: Spec{Env: "production", App: "my-app", Var: "STAGING_DATABASE_URL"},
		deps: herokuDeps("postgres://u:p@staging.heroku.com:5432/club\n", nil, nil),
	}
	ci, err := src.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if ci.Host != "staging.heroku.com" {
		t.Errorf("Host = %q, want staging.heroku.com", ci.Host)
	}
}

func TestHerokuSource_NotInstalled(t *testing.T) {
	src := &herokuSource{
		spec: Spec{Env: "production", App: "my-app"},
		deps: herokuDeps("", nil, errors.New("not found")),
	}
	if _, err := src.Resolve(); !errors.Is(err, ErrHerokuNotInstalled) {
		t.Errorf("want ErrHerokuNotInstalled, got %v", err)
	}
}

func TestHerokuSource_NotAuthed(t *testing.T) {
	src := &herokuSource{
		spec: Spec{Env: "production", App: "my-app"},
		deps: herokuDeps("Error: Not logged in.", fmt.Errorf("exit 1"), nil),
	}
	if _, err := src.Resolve(); !errors.Is(err, ErrHerokuNotAuthed) {
		t.Errorf("want ErrHerokuNotAuthed, got %v", err)
	}
}

func TestHerokuSource_VarNotFound(t *testing.T) {
	src := &herokuSource{
		spec: Spec{Env: "production", App: "my-app"},
		deps: herokuDeps("", nil, nil),
	}
	var ve *VarNotFoundError
	if _, err := src.Resolve(); !errors.As(err, &ve) {
		t.Errorf("want *VarNotFoundError, got %v", err)
	}
}

func TestHerokuSource_MissingApp(t *testing.T) {
	src := &herokuSource{spec: Spec{Env: "production", Via: "heroku"}, deps: herokuDeps("", nil, nil)}
	var se *SpecError
	if _, err := src.Resolve(); !errors.As(err, &se) {
		t.Errorf("want *SpecError, got %v", err)
	}
}
