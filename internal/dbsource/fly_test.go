package dbsource

import (
	"errors"
	"fmt"
	"testing"
)

// flyDeps builds Deps with an injected fly runner. lookErr forces fly-not-found.
func flyDeps(out string, runErr error, lookErr error) Deps {
	return Deps{
		RunFly: func(args ...string) ([]byte, error) {
			return []byte(out), runErr
		},
		LookPath: func(string) (string, error) {
			if lookErr != nil {
				return "", lookErr
			}
			return "/usr/local/bin/fly", nil
		},
	}
}

func TestParsePrintenv(t *testing.T) {
	out := "DATABASE_URL=postgres://u:p@h/db?opt=1\n\nPGHOST=h\nMALFORMED\n=NOKEY\nFOO=a=b\n"
	env := parsePrintenv(out)
	if env["DATABASE_URL"] != "postgres://u:p@h/db?opt=1" {
		t.Errorf("DATABASE_URL = %q", env["DATABASE_URL"])
	}
	if env["FOO"] != "a=b" {
		t.Errorf("FOO = %q, want a=b (split on first =)", env["FOO"])
	}
	if _, ok := env["MALFORMED"]; ok {
		t.Error("line without = should be ignored")
	}
	if _, ok := env[""]; ok {
		t.Error("line beginning with = should be ignored")
	}
}

func TestFlySource_DatabaseURL(t *testing.T) {
	src := &flySource{
		spec: Spec{Env: "production", Via: "fly", App: "cv-prod"},
		deps: flyDeps("PORT=8080\nDATABASE_URL=postgres://u:secret@db.crunchy.com:5432/club\n", nil, nil),
	}
	ci, err := src.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if ci.Host != "db.crunchy.com" || ci.DBName != "club" || ci.Password != "secret" {
		t.Errorf("got %+v", ci)
	}
}

func TestFlySource_DiscretePGStar(t *testing.T) {
	out := "PGHOST=db.internal\nPGPORT=5433\nPGUSER=app\nPGPASSWORD=pw\nPGDATABASE=club\n"
	src := &flySource{
		spec: Spec{Env: "production", App: "cv-prod"},
		deps: flyDeps(out, nil, nil),
	}
	ci, err := src.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if ci.Host != "db.internal" || ci.Port != "5433" || ci.User != "app" ||
		ci.Password != "pw" || ci.DBName != "club" {
		t.Errorf("got %+v", ci)
	}
}

func TestFlySource_CustomVar(t *testing.T) {
	src := &flySource{
		spec: Spec{Env: "production", App: "cv-prod", Var: "APP_DATABASE_URL"},
		deps: flyDeps("DATABASE_URL=postgres://x:y@wrong/db\nAPP_DATABASE_URL=postgres://u:p@right/club\n", nil, nil),
	}
	ci, err := src.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if ci.Host != "right" {
		t.Errorf("Host = %q, want right (custom var honored)", ci.Host)
	}
}

func TestFlySource_NotInstalled(t *testing.T) {
	src := &flySource{
		spec: Spec{Env: "production", App: "cv-prod"},
		deps: flyDeps("", nil, errors.New("not found")),
	}
	if _, err := src.Resolve(); !errors.Is(err, ErrFlyNotInstalled) {
		t.Errorf("want ErrFlyNotInstalled, got %v", err)
	}
}

func TestFlySource_NotAuthed(t *testing.T) {
	src := &flySource{
		spec: Spec{Env: "production", App: "cv-prod"},
		deps: flyDeps("Error: You must be logged in to run this command.", fmt.Errorf("exit 1"), nil),
	}
	if _, err := src.Resolve(); !errors.Is(err, ErrFlyNotAuthed) {
		t.Errorf("want ErrFlyNotAuthed, got %v", err)
	}
}

func TestFlySource_VarNotFound(t *testing.T) {
	src := &flySource{
		spec: Spec{Env: "production", App: "cv-prod"},
		deps: flyDeps("PORT=8080\nRAILS_ENV=production\n", nil, nil),
	}
	var ve *VarNotFoundError
	if _, err := src.Resolve(); !errors.As(err, &ve) {
		t.Errorf("want *VarNotFoundError, got %v", err)
	}
}

func TestFlySource_MissingApp(t *testing.T) {
	src := &flySource{spec: Spec{Env: "production", Via: "fly"}, deps: flyDeps("", nil, nil)}
	var se *SpecError
	if _, err := src.Resolve(); !errors.As(err, &se) {
		t.Errorf("want *SpecError, got %v", err)
	}
}
