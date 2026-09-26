package dbsource

import (
	"errors"
	"fmt"
	"strings"
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

// A failed `fly ssh console -C printenv` can still have dumped the app's
// environment to stdout, so the error must never echo that output.
func TestFlySource_FailureRedactsOutput(t *testing.T) {
	out := "DATABASE_URL=postgres://app:s3cr3t-pw@db.internal:5432/club\n" +
		"SECRET_KEY=abc123\n" +
		"STRIPE_API_KEY=sk_live_deadbeef\n" +
		"Error: connection reset by peer\n"
	runErr := fmt.Errorf("exit status 1")
	src := &flySource{
		spec: Spec{Env: "production", App: "cv-prod"},
		deps: flyDeps(out, runErr, nil),
	}
	_, err := src.Resolve()
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrFlyNotAuthed) {
		t.Fatalf("generic failure misclassified as unauthenticated: %v", err)
	}
	msg := err.Error()
	for _, leak := range []string{"s3cr3t-pw", "abc123", "sk_live_deadbeef", "postgres://", "DATABASE_URL", "SECRET_KEY", "connection reset"} {
		if strings.Contains(msg, leak) {
			t.Errorf("error leaks %q from command output: %s", leak, msg)
		}
	}
	if !strings.Contains(msg, "cv-prod") || !strings.Contains(msg, "redacted") {
		t.Errorf("error should name the app and say output was redacted: %s", msg)
	}
	if !errors.Is(err, runErr) {
		t.Errorf("underlying exec error should be wrapped, got %v", err)
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
