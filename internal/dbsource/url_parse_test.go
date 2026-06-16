package dbsource

import (
	"errors"
	"testing"
)

func TestParsePostgresURL(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		cfgSSL      string
		wantHost    string
		wantPort    string
		wantUser    string
		wantPass    string
		wantDB      string
		wantSSL     string
	}{
		{
			name:     "full url",
			raw:      "postgres://u:p@db.example.com:6432/club?sslmode=require",
			wantHost: "db.example.com", wantPort: "6432", wantUser: "u", wantPass: "p",
			wantDB: "club", wantSSL: "require",
		},
		{
			name:     "no port defaults 5432",
			raw:      "postgres://u:p@db.example.com/club",
			wantHost: "db.example.com", wantPort: "5432", wantUser: "u", wantPass: "p",
			wantDB: "club", wantSSL: "require",
		},
		{
			name:     "percent-encoded password decoded once",
			raw:      "postgres://u:p%40ss%2Fword@h/club",
			wantHost: "h", wantPort: "5432", wantUser: "u", wantPass: "p@ss/word",
			wantDB: "club", wantSSL: "require",
		},
		{
			name:     "url sslmode honored when no config override",
			raw:      "postgres://u:p@h/club?sslmode=disable",
			wantHost: "h", wantPort: "5432", wantUser: "u", wantPass: "p",
			wantDB: "club", wantSSL: "disable",
		},
		{
			name:     "config sslmode overrides url",
			raw:      "postgres://u:p@h/club?sslmode=disable",
			cfgSSL:   "verify-full",
			wantHost: "h", wantPort: "5432", wantUser: "u", wantPass: "p",
			wantDB: "club", wantSSL: "verify-full",
		},
		{
			name:     "ipv6 host",
			raw:      "postgres://u:p@[::1]:5433/club",
			wantHost: "::1", wantPort: "5433", wantUser: "u", wantPass: "p",
			wantDB: "club", wantSSL: "require",
		},
		{
			name:     "postgresql scheme accepted",
			raw:      "postgresql://u:p@h/club",
			wantHost: "h", wantPort: "5432", wantUser: "u", wantPass: "p",
			wantDB: "club", wantSSL: "require",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ci, err := parsePostgresURL(tt.raw, tt.cfgSSL)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ci.Host != tt.wantHost {
				t.Errorf("Host = %q, want %q", ci.Host, tt.wantHost)
			}
			if ci.Port != tt.wantPort {
				t.Errorf("Port = %q, want %q", ci.Port, tt.wantPort)
			}
			if ci.User != tt.wantUser {
				t.Errorf("User = %q, want %q", ci.User, tt.wantUser)
			}
			if ci.Password != tt.wantPass {
				t.Errorf("Password = %q, want %q", ci.Password, tt.wantPass)
			}
			if ci.DBName != tt.wantDB {
				t.Errorf("DBName = %q, want %q", ci.DBName, tt.wantDB)
			}
			if ci.SSLMode != tt.wantSSL {
				t.Errorf("SSLMode = %q, want %q", ci.SSLMode, tt.wantSSL)
			}
		})
	}
}

func TestParsePostgresURL_Errors(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if _, err := parsePostgresURL("  ", ""); !errors.Is(err, ErrMissingURL) {
			t.Errorf("want ErrMissingURL, got %v", err)
		}
	})

	var pe *URLParseError
	for _, tt := range []struct {
		name string
		raw  string
	}{
		{"wrong scheme", "mysql://u:p@h/db"},
		{"no database", "postgres://u:p@h"},
		{"no host", "postgres:///db"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parsePostgresURL(tt.raw, "")
			if !errors.As(err, &pe) {
				t.Fatalf("want *URLParseError, got %T: %v", err, err)
			}
		})
	}
}
