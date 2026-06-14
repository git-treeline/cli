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
	if s, err := New(Spec{Via: "url"}, deps); err != nil {
		t.Fatalf("url: %v", err)
	} else if _, ok := s.(*urlSource); !ok {
		t.Errorf("url → %T, want *urlSource", s)
	}
	var ue *UnknownViaError
	if _, err := New(Spec{Via: "heroku", Env: "production"}, deps); !errors.As(err, &ue) {
		t.Errorf("heroku → %v, want *UnknownViaError", err)
	}
}

func TestUrlSource_Resolve(t *testing.T) {
	env := map[string]string{"STAGING_DATABASE_URL": "postgres://u:p@h/club"}
	src := &urlSource{
		spec: Spec{Env: "staging", Via: "url", URLEnv: "STAGING_DATABASE_URL"},
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

func TestUrlSource_MissingEnv(t *testing.T) {
	src := &urlSource{
		spec: Spec{Env: "staging", URLEnv: "STAGING_DATABASE_URL"},
		deps: Deps{Getenv: func(string) string { return "" }},
	}
	if _, err := src.Resolve(); !errors.Is(err, ErrMissingURL) {
		t.Errorf("want ErrMissingURL, got %v", err)
	}
}

func TestUrlSource_NoEnvConfigured(t *testing.T) {
	src := &urlSource{spec: Spec{Env: "staging", Via: "url"}, deps: Deps{}}
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
