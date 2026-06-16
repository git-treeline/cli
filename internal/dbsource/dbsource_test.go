package dbsource

import (
	"errors"
	"testing"
)

func TestNew_Dispatch(t *testing.T) {
	deps := Deps{}
	if s, err := New(Spec{Via: "fly"}, deps); err != nil {
		t.Fatalf("fly: %v", err)
	} else if _, ok := s.(*flySource); !ok {
		t.Errorf("fly → %T, want *flySource", s)
	}
	if s, err := New(Spec{Via: "heroku"}, deps); err != nil {
		t.Fatalf("heroku: %v", err)
	} else if _, ok := s.(*herokuSource); !ok {
		t.Errorf("heroku → %T, want *herokuSource", s)
	}
	if s, err := New(Spec{Via: "env"}, deps); err != nil {
		t.Fatalf("env: %v", err)
	} else if _, ok := s.(*envSource); !ok {
		t.Errorf("env → %T, want *envSource", s)
	}
	var ue *UnknownViaError
	if _, err := New(Spec{Via: "unknown", Env: "production"}, deps); !errors.As(err, &ue) {
		t.Errorf("unknown → %v, want *UnknownViaError", err)
	}
}

func TestEnvSource_Resolve(t *testing.T) {
	env := map[string]string{"STAGING_DATABASE_URL": "postgres://u:p@h/club"}
	src := &envSource{
		spec: Spec{Env: "staging", Via: "env", Var: "STAGING_DATABASE_URL"},
		deps: Deps{Getenv: func(k string) string { return env[k] }},
	}
	ci, err := src.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if ci.Host != "h" || ci.DBName != "club" {
		t.Errorf("got %+v", ci)
	}
}

func TestEnvSource_MissingVar(t *testing.T) {
	src := &envSource{
		spec: Spec{Env: "staging", Var: "STAGING_DATABASE_URL"},
		deps: Deps{Getenv: func(string) string { return "" }},
	}
	if _, err := src.Resolve(); !errors.Is(err, ErrMissingURL) {
		t.Errorf("want ErrMissingURL, got %v", err)
	}
}

func TestEnvSource_NoVarConfigured(t *testing.T) {
	src := &envSource{spec: Spec{Env: "staging", Via: "env"}, deps: Deps{}}
	var se *SpecError
	if _, err := src.Resolve(); !errors.As(err, &se) {
		t.Errorf("want *SpecError, got %v", err)
	}
}

func TestResolveSSLMode(t *testing.T) {
	cases := []struct{ explicit, fromConn, want string }{
		{"", "", "require"},
		{"", "disable", "disable"},
		{"verify-full", "disable", "verify-full"},
		{"require", "", "require"},
	}
	for _, c := range cases {
		if got := resolveSSLMode(c.explicit, c.fromConn); got != c.want {
			t.Errorf("resolveSSLMode(%q,%q) = %q, want %q", c.explicit, c.fromConn, got, c.want)
		}
	}
}
